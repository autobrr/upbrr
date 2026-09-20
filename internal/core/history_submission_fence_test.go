// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHistoryDeletionRemovesAssociatedSubmissionExclusions(t *testing.T) {
	t.Parallel()

	repo, err := db.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	contents := []byte("verified release bytes")
	if err := os.WriteFile(source, contents, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	contentDigest := sha256.Sum256(contents)
	subject := api.UploadSubject{SourcePath: source, SourceIdentity: api.SourceContentIdentity{
		Version: api.SourceContentIdentityVersion,
		Digest:  strings.Repeat("a", 64),
		Files: []api.VerifiedSourceFile{{
			LocalPath: source,
			Size:      int64(len(contents)),
			SHA256:    hex.EncodeToString(contentDigest[:]),
		}},
	}}
	identity, err := workflowSubmissionContentIdentity(workflowSubmissionTorrentSubject(subject), subject.SourceIdentity)
	if err != nil {
		t.Fatalf("derive submission identity: %v", err)
	}
	ptpSite, err := trackers.CanonicalSubmissionTrackerSite("PTP", "https://ptp.example.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	btnSite, err := trackers.CanonicalSubmissionTrackerSite("BTN", "https://btn.example.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	workflow := historySubmissionWorkflowState(t, source, now)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
		t.Fatalf("create history workflow: %v", err)
	}
	insertHistorySubmissionFence(t, repo, workflow, identity, ptpSite, api.WorkflowEffectStatusSucceeded, now)
	insertHistorySubmissionFence(t, repo, workflow, identity, btnSite, api.WorkflowEffectStatusUnknown, now)
	if err := repo.DeleteReleaseWorkflowState(ctx, workflow.OwnerID, workflow.WorkflowID); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
		t.Fatalf("direct deletion of fenced workflow = %v", err)
	}
	deleted, err := repo.DeleteTerminalReleaseWorkflowStatesBefore(ctx, now.Add(time.Hour))
	if err != nil || deleted != 0 {
		t.Fatalf("retain fenced workflow source association: deleted=%d err=%v", deleted, err)
	}
	if _, err := repo.LoadReleaseWorkflowState(ctx, workflow.OwnerID, workflow.WorkflowID); err != nil {
		t.Fatalf("fenced workflow lost before history deletion: %v", err)
	}
	for _, site := range []string{ptpSite, btnSite} {
		if _, err := repo.LoadSubmissionFence(ctx, identity, site); err != nil {
			t.Fatalf("fence %q lost before history deletion: %v", site, err)
		}
	}
	if err := repo.Save(ctx, db.FileMetadata{
		Path:      source,
		InfoHash:  "history-source",
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save display history: %v", err)
	}
	history := newHistoryModule(repo, repo.DBPath(), api.NopLogger{})
	if err := history.Delete(ctx, source); err != nil {
		t.Fatalf("delete history: %v", err)
	}
	if _, err := repo.LoadReleaseWorkflowState(ctx, workflow.OwnerID, workflow.WorkflowID); !errors.Is(err, api.ErrReleaseWorkflowStateNotFound) {
		t.Fatalf("workflow after history deletion = %v", err)
	}
	for _, site := range []string{ptpSite, btnSite} {
		if _, err := repo.LoadSubmissionFence(ctx, identity, site); !errors.Is(err, api.ErrSubmissionFenceNotFound) {
			t.Fatalf("fence %q after history deletion = %v", site, err)
		}
	}
	filter := workflowSubmissionHistoryFilter{
		fences: repo,
		registry: workflowSubmissionTrackerRegistryFake{
			"PTP": {Name: "PTP", BaseURL: "https://ptp.example.invalid/"},
		},
	}
	remaining, exclusions, err := filter.FilterConfirmedSubmissions(ctx, subject, []api.TrackerID{"PTP"})
	if err != nil || len(remaining) != 1 || remaining[0] != "PTP" || len(exclusions) != 0 {
		t.Fatalf("fresh same-byte attempt after deletion: remaining=%#v exclusions=%#v err=%v", remaining, exclusions, err)
	}
}

func historySubmissionWorkflowState(t *testing.T, sourcePath string, now time.Time) api.ReleaseWorkflowStateRecord {
	t.Helper()
	payload, err := json.Marshal(struct {
		PreparationInput *api.PrepareInput
		Releases         map[api.ReleaseSnapshotID]api.ReleaseSnapshot
	}{
		PreparationInput: &api.PrepareInput{SourcePath: sourcePath},
		Releases: map[api.ReleaseSnapshotID]api.ReleaseSnapshot{
			"release": {Release: api.PreparedRelease{Source: api.SourceManifest{SourcePath: sourcePath}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return api.ReleaseWorkflowStateRecord{
		OwnerID:             "history-owner",
		WorkflowID:          "history-workflow",
		Revision:            1,
		Status:              api.WorkflowStatusCompleted,
		CreationKey:         "history-create",
		CreationFingerprint: "history-fingerprint",
		Payload:             payload,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func insertHistorySubmissionFence(
	t *testing.T,
	repo *db.SQLiteRepository,
	workflow api.ReleaseWorkflowStateRecord,
	identity api.SubmissionContentIdentity,
	site string,
	status api.WorkflowEffectStatus,
	now time.Time,
) {
	t.Helper()
	payload, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	confirmedAt := any(nil)
	if status == api.WorkflowEffectStatusSucceeded {
		confirmedAt = now.Format(time.RFC3339Nano)
	}
	if _, err := repo.RawDB().ExecContext(context.Background(), `
		INSERT INTO submission_fences (
			content_version, content_digest, content_scope, content_json, tracker_site,
			owner_id, workflow_id, operation_id, effect_id, status, started_at, updated_at, confirmed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, identity.Version, identity.Digest, identity.Scope, payload, site, workflow.OwnerID, workflow.WorkflowID, "operation", string(status), status,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), confirmedAt); err != nil {
		t.Fatalf("insert %s fence: %v", status, err)
	}
}
