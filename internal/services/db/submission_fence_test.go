// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSubmissionFencePersistsSuccessAndFencesUnknownAttempts(t *testing.T) {
	t.Parallel()

	repo, err := Open(filepath.Join(t.TempDir(), "submission-fence.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	workflow := workflowStateRecordForTest("submission-fence", api.WorkflowStatusActive, now, `{"revision":1}`)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	if err := activateSubmissionFenceTestInput(ctx, repo, workflow, now); err != nil {
		t.Fatal(err)
	}
	ctx = api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: "submission-fence-test", Fence: 1})
	identity := submissionFenceTestIdentity(t, "a")
	effect := submissionFenceTestEffect(workflow, "effect-success", identity, now)
	started, idempotent, err := repo.BeginReleaseWorkflowEffect(ctx, effect)
	if err != nil || idempotent || started.Status != api.WorkflowEffectStatusStarted {
		t.Fatalf("begin = %#v, idempotent=%v, err=%v", started, idempotent, err)
	}
	fence, err := repo.LoadSubmissionFence(ctx, identity, effect.Submission.TrackerSite)
	if err != nil || fence.Status != api.WorkflowEffectStatusStarted || fence.ConfirmedAt != nil {
		t.Fatalf("started fence = %#v, err=%v", fence, err)
	}

	completedAt := now.Add(time.Second)
	started.UpdatedAt, started.CompletedAt = completedAt, &completedAt
	if err := repo.CompleteReleaseWorkflowEffect(ctx, api.WorkflowEffectStatusSucceeded, started); err != nil {
		t.Fatal(err)
	}
	fence, err = repo.LoadSubmissionFence(ctx, identity, effect.Submission.TrackerSite)
	if err != nil || fence.Status != api.WorkflowEffectStatusSucceeded || fence.ConfirmedAt == nil || !fence.ConfirmedAt.Equal(completedAt) {
		t.Fatalf("confirmed fence = %#v, err=%v", fence, err)
	}
	second := submissionFenceTestEffect(workflow, "effect-duplicate", identity, completedAt.Add(time.Second))
	prior, idempotent, err := repo.BeginReleaseWorkflowEffect(ctx, second)
	if err != nil || !idempotent || prior.Status != api.WorkflowEffectStatusSucceeded || prior.EffectID != effect.EffectID {
		t.Fatalf("succeeded exclusion = %#v, idempotent=%v, err=%v", prior, idempotent, err)
	}

	unknownIdentity := submissionFenceTestIdentity(t, "b")
	unknown := submissionFenceTestEffect(workflow, "effect-unknown", unknownIdentity, completedAt.Add(2*time.Second))
	if _, _, err := repo.BeginReleaseWorkflowEffect(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(ctx, workflow.OwnerID, workflow.WorkflowID, unknown.OperationID, completedAt.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	fence, err = repo.LoadSubmissionFence(ctx, unknownIdentity, unknown.Submission.TrackerSite)
	if err != nil || fence.Status != api.WorkflowEffectStatusUnknown {
		t.Fatalf("unknown fence = %#v, err=%v", fence, err)
	}
	retry := submissionFenceTestEffect(workflow, "effect-retry", unknownIdentity, completedAt.Add(4*time.Second))
	if _, _, err := repo.BeginReleaseWorkflowEffect(ctx, retry); !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
		t.Fatalf("unknown retry = %v", err)
	}
	if err := repo.ResolveReleaseWorkflowEffectUnknown(ctx, workflow.OwnerID, workflow.WorkflowID,
		api.WorkflowExternalEffectTrackerSubmission, unknown.ScopeID, completedAt.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.LoadSubmissionFence(ctx, unknownIdentity, unknown.Submission.TrackerSite); !errors.Is(err, api.ErrSubmissionFenceNotFound) {
		t.Fatalf("resolved fence = %v", err)
	}
	if started, idempotent, err := repo.BeginReleaseWorkflowEffect(ctx, retry); err != nil || idempotent || started.Status != api.WorkflowEffectStatusStarted {
		t.Fatalf("reconciled retry = %#v, idempotent=%v, err=%v", started, idempotent, err)
	}
}

func TestPurgeContentDataPreservesSubmissionFences(t *testing.T) {
	t.Parallel()

	repo, err := Open(filepath.Join(t.TempDir(), "submission-fence-history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	now := time.Now().UTC().Truncate(time.Second)
	if err := repo.Save(ctx, FileMetadata{
Path: sourcePath,
 InfoHash: "history-target",
 UpdatedAt: now,
}); err != nil {
		t.Fatalf("save display history: %v", err)
	}
	for _, test := range []struct {
		identity api.SubmissionContentIdentity
		status   api.WorkflowEffectStatus
	}{
		{identity: submissionFenceTestIdentity(t, "c"), status: api.WorkflowEffectStatusSucceeded},
		{identity: submissionFenceTestIdentity(t, "d"), status: api.WorkflowEffectStatusUnknown},
	} {
		payload, err := json.Marshal(test.identity)
		if err != nil {
			t.Fatal(err)
		}
		confirmedAt := any(nil)
		if test.status == api.WorkflowEffectStatusSucceeded {
			confirmedAt = formatWorkflowStateTime(now)
		}
		if _, err := repo.RawDB().ExecContext(ctx, `
			INSERT INTO submission_fences (
				content_version, content_digest, content_scope, content_json, tracker_site,
				owner_id, workflow_id, operation_id, effect_id, status, started_at, updated_at, confirmed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, test.identity.Version, test.identity.Digest, test.identity.Scope, payload, "PTP|https://ptp.example.invalid/",
			"owner", "workflow", "operation", string(test.status), test.status,
			formatWorkflowStateTime(now), formatWorkflowStateTime(now), confirmedAt); err != nil {
			t.Fatalf("insert %s fence: %v", test.status, err)
		}
	}
	if err := repo.PurgeContentData(ctx, sourcePath); err != nil {
		t.Fatalf("purge display history: %v", err)
	}
	for _, test := range []struct {
		identity api.SubmissionContentIdentity
		status   api.WorkflowEffectStatus
	}{
		{identity: submissionFenceTestIdentity(t, "c"), status: api.WorkflowEffectStatusSucceeded},
		{identity: submissionFenceTestIdentity(t, "d"), status: api.WorkflowEffectStatusUnknown},
	} {
		fence, err := repo.LoadSubmissionFence(ctx, test.identity, "PTP|https://ptp.example.invalid/")
		if err != nil || fence.Status != test.status {
			t.Fatalf("fence after purge = %#v, err=%v", fence, err)
		}
	}
}

func TestPurgeContentDataRejectsActiveInput(t *testing.T) {
	t.Parallel()

	repo, err := Open(filepath.Join(t.TempDir(), "active-history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	now := time.Now().UTC()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	workflow := workflowStateRecordForTest("active-history", api.WorkflowStatusActive, now, `{"revision":1}`)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveInputRecord(ctx, api.InputRecord{
		ID: "active-history-input",
 CanonicalPath: sourcePath,
 SourceVersion: strings.Repeat("a", 64),
 Manifest: []byte(`{}`),
 UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save active input: %v", err)
	}
	empty, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opening := api.ActiveInputRecord{
		State: api.ActiveInputOpening,
 Revision: empty.Revision + 1,
 Fence: empty.Fence + 1,
		OwnerID: workflow.OwnerID,
 CoordinatorID: "active-history",
 LeaseExpiresAt: now.Add(time.Hour),
		ReservationID: "active-history-reservation",
 RequestedPath: sourcePath,
 IdempotencyKey: "active-history-open",
	}
	if err := repo.CompareAndSwapActiveInput(ctx, empty, opening, now); err != nil {
		t.Fatalf("open active input: %v", err)
	}
	active := opening
	active.State, active.Revision = api.ActiveInputActive, opening.Revision+1
	active.InputID, active.SourceVersion, active.WorkflowID = "active-history-input", strings.Repeat("a", 64), workflow.WorkflowID
	active.ReservationID, active.RequestedPath = "", ""
	if err := repo.CompareAndSwapActiveInput(ctx, opening, active, now); err != nil {
		t.Fatalf("activate input: %v", err)
	}
	if err := repo.PurgeContentData(ctx, sourcePath); !errors.Is(err, api.ErrActiveInputBusy) {
		t.Fatalf("purge active input history = %v", err)
	}
	effect := submissionFenceTestEffect(workflow, "active-history-unknown", submissionFenceTestIdentity(t, "e"), now)
	effect.Submission.CoordinatorID, effect.Submission.Fence = active.CoordinatorID, active.Fence
	ctx = api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: active.CoordinatorID, Fence: active.Fence})
	if _, _, err := repo.BeginReleaseWorkflowEffect(ctx, effect); err != nil {
		t.Fatalf("begin unresolved submission: %v", err)
	}
	if err := repo.PurgeContentData(ctx, sourcePath); !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
		t.Fatalf("purge unresolved input history = %v", err)
	}
}

func activateSubmissionFenceTestInput(
	ctx context.Context,
	repo *SQLiteRepository,
	workflow api.ReleaseWorkflowStateRecord,
	now time.Time,
) error {
	empty, err := repo.LoadActiveInput(ctx)
	if err != nil {
		return err
	}
	opening := api.ActiveInputRecord{
		State: api.ActiveInputOpening,
 Revision: empty.Revision + 1,
 Fence: empty.Fence + 1,
		OwnerID: workflow.OwnerID,
 CoordinatorID: "submission-fence-test",
 LeaseExpiresAt: now.Add(time.Hour),
		ReservationID: "submission-fence-reservation",
 RequestedPath: "C:/synthetic/source.mkv",
 IdempotencyKey: "submission-fence-open",
	}
	if err := repo.CompareAndSwapActiveInput(ctx, empty, opening, now); err != nil {
		return err
	}
	active := opening
	active.State, active.Revision = api.ActiveInputActive, opening.Revision+1
	active.InputID, active.SourceVersion, active.WorkflowID = "input", "source-version", workflow.WorkflowID
	active.ReservationID, active.RequestedPath = "", ""
	return repo.CompareAndSwapActiveInput(ctx, opening, active, now)
}

func submissionFenceTestIdentity(t *testing.T, value string) api.SubmissionContentIdentity {
	t.Helper()
	identity, err := api.NewSubmissionContentIdentity(api.SubmissionContentScopeSingleFile, []api.SubmissionContentFile{{
		Size: 1, SHA256: strings.Repeat(value, 64),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func submissionFenceTestEffect(
	workflow api.ReleaseWorkflowStateRecord,
	effectID string,
	identity api.SubmissionContentIdentity,
	now time.Time,
) api.ReleaseWorkflowEffectRecord {
	return api.ReleaseWorkflowEffectRecord{
		OwnerID: workflow.OwnerID,
 WorkflowID: workflow.WorkflowID,
 OperationID: "submission-operation",
 EffectID: effectID,
		Kind: string(api.WorkflowExternalEffectTrackerSubmission),
 ScopeID: "ALPHA",
 SemanticFingerprint: api.WorkflowFingerprint(effectID),
		Status: api.WorkflowEffectStatusStarted,
 StartedAt: now,
 UpdatedAt: now,
		Submission: &api.SubmissionFenceAuthority{
			ContentIdentity: identity,
 TrackerSite: "ALPHA|https://alpha.example/",
 CoordinatorID: "submission-fence-test",
 Fence: 1,
		},
	}
}
