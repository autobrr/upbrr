// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

type sqliteSubmissionReporter struct {
	repo        *db.SQLiteRepository
	record      api.ReleaseWorkflowEffectRecord
	next        int
	failReceipt bool
}

func (r *sqliteSubmissionReporter) Begin(ctx context.Context, effect api.WorkflowExternalEffect) (api.WorkflowExternalEffectReceipt, error) {
	r.next++
	now := time.Now().UTC()
	record, already, err := r.repo.BeginReleaseWorkflowEffect(ctx, api.ReleaseWorkflowEffectRecord{
		OwnerID:             "owner",
		WorkflowID:          "workflow",
		OperationID:         "operation",
		EffectID:            fmt.Sprintf("effect-%d", r.next),
		Kind:                string(effect.Kind),
		ScopeID:             effect.ScopeID,
		SemanticFingerprint: effect.SemanticFingerprint,
		Status:              api.WorkflowEffectStatusStarted,
		StartedAt:           now,
		UpdatedAt:           now,
		Submission:          effect.Submission,
	})
	if err != nil {
		return api.WorkflowExternalEffectReceipt{}, fmt.Errorf("begin synthetic submission effect: %w", err)
	}
	r.record = record
	return api.WorkflowExternalEffectReceipt{
		EffectID:         record.EffectID,
		OperationID:      record.OperationID,
		AlreadySucceeded: already,
	}, nil
}

func (r *sqliteSubmissionReporter) Complete(ctx context.Context, _ api.WorkflowExternalEffectReceipt, succeeded bool) error {
	if r.failReceipt {
		return errors.New("synthetic receipt write failure")
	}
	now := time.Now().UTC()
	r.record.UpdatedAt, r.record.CompletedAt = now, &now
	status := api.WorkflowEffectStatusFailed
	if succeeded {
		status = api.WorkflowEffectStatusSucceeded
	}
	if err := r.repo.CompleteReleaseWorkflowEffect(context.WithoutCancel(ctx), status, r.record); err != nil {
		return fmt.Errorf("retain synthetic submission receipt: %w", err)
	}
	return nil
}

func TestSubmissionOutcomePersistsAndControlsRetryAfterReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "outcomes.sqlite")
	open := func() *db.SQLiteRepository {
		repo, err := db.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		return repo
	}
	repo := open()
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ctx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "coordinator", Fence: 1})
	_, _, err := repo.CreateReleaseWorkflowState(ctx, api.ReleaseWorkflowStateRecord{
		OwnerID:             "owner",
		WorkflowID:          "workflow",
		Revision:            1,
		Status:              api.WorkflowStatusActive,
		CreationKey:         "create",
		CreationFingerprint: "fingerprint",
		Payload:             []byte(`{"revision":1}`),
		CreatedAt:           now,
		UpdatedAt:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	opening := api.ActiveInputRecord{
		State:          api.ActiveInputOpening,
		Revision:       1,
		Fence:          1,
		OwnerID:        "owner",
		CoordinatorID:  "coordinator",
		LeaseExpiresAt: now.Add(time.Hour),
		ReservationID:  "reservation",
		RequestedPath:  sourcePath,
		IdempotencyKey: "open",
	}
	if err := repo.CompareAndSwapActiveInput(ctx, empty, opening, now); err != nil {
		t.Fatal(err)
	}
	active := opening
	active.State, active.Revision = api.ActiveInputActive, 2
	active.InputID, active.SourceVersion, active.WorkflowID = "input", "version", "workflow"
	active.ReservationID, active.RequestedPath = "", ""
	if err := repo.CompareAndSwapActiveInput(ctx, opening, active, now); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := registry.Register(stubDefinition{name: "ANT"}); err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithRegistry(config.Config{}, api.NopLogger{}, repo, registry)
	reporter := &sqliteSubmissionReporter{repo: repo}
	ctx = api.WithWorkflowExternalEffectReporter(ctx, reporter)
	rejection := fmt.Errorf("trackers: ANT upload failed status=%d: %s", http.StatusBadRequest, "A file with the same infohash already exists on the site!")
	cases := []struct {
		name        string
		err         error
		status      api.WorkflowEffectStatus
		history     string
		failReceipt bool
	}{
		{"rejected", submission.NewError(submission.Rejected, rejection), api.WorkflowEffectStatusFailed, "failed", false},
		{"not sent", submission.NewError(submission.NotSent, errors.New("synthetic request construction failure")), api.WorkflowEffectStatusFailed, "failed", false},
		{"unknown", io.ErrUnexpectedEOF, api.WorkflowEffectStatusUnknown, "unknown_outcome", false},
		{"unclassified duplicate text", rejection, api.WorkflowEffectStatusUnknown, "unknown_outcome", false},
		{"accepted", nil, api.WorkflowEffectStatusSucceeded, "uploaded", false},
		{"accepted receipt pending", nil, api.WorkflowEffectStatusUnknown, "uploaded", true},
		{"rejected receipt pending", submission.NewError(submission.Rejected, rejection), api.WorkflowEffectStatusUnknown, "failed", true},
	}
	identities := make([]api.SubmissionContentIdentity, len(cases))
	sequence := 0
	execute := func(identity api.SubmissionContentIdentity, err error) (trackerPlanSlot, int) {
		sequence++
		if err := repo.CreateUploadRecord(ctx, api.UploadRecord{
			Tracker:    "ANT",
			SourcePath: sourcePath,
			Status:     "pending",
			CreatedAt:  now.Add(time.Duration(sequence) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
		calls := 0
		slots := []trackerPlanSlot{{tracker: "ANT", plan: NewUploadPlan("ANT", api.TrackerDryRunEntry{Tracker: "ANT", Status: "ready"}, func(context.Context) (api.UploadSummary, error) {
			calls++
			if err != nil {
				return api.UploadSummary{}, err
			}
			return api.UploadSummary{Uploaded: 1}, nil
		}, nil)}}
		svc.submitTrackerPlans(ctx, api.UploadSubject{SourcePath: sourcePath, SubmissionContentIdentity: identity}, slots)
		return slots[0], calls
	}
	for i, test := range cases {
		identity, err := api.NewSubmissionContentIdentity(api.SubmissionContentScopeSingleFile, []api.SubmissionContentFile{{Size: 4, SHA256: fmt.Sprintf("%064x", i+1)}})
		if err != nil {
			t.Fatal(err)
		}
		identities[i] = identity
		reporter.failReceipt = test.failReceipt
		slot, calls := execute(identity, test.err)
		if calls != 1 {
			t.Fatalf("%s: submission callbacks=%d", test.name, calls)
		}
		if test.failReceipt && (slot.receiptFailure == nil || slot.submissionReceiptID != reporter.record.EffectID || slot.submissionOperationID != reporter.record.OperationID || slot.submissionOutcome != submission.OutcomeOf(test.err)) {
			t.Fatalf("%s: receipt failure lost exact remote outcome: %#v", test.name, slot)
		}
	}
	reporter.failReceipt = false
	if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(ctx, "owner", "workflow", "operation", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo = open()
	svc.repo, reporter.repo = repo, repo
	history, err := repo.ListUploadHistoryByPath(ctx, sourcePath)
	if err != nil || len(history) != len(cases) {
		t.Fatalf("history=%v error=%v", history, err)
	}
	for i, test := range cases {
		var status string
		if err := repo.RawDB().QueryRowContext(ctx, `SELECT status FROM release_workflow_effects WHERE owner_id=? AND workflow_id=? AND effect_id=?`, "owner", "workflow", fmt.Sprintf("effect-%d", i+1)).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != string(test.status) || history[len(cases)-1-i].Status != test.history {
			t.Fatalf("%s: persisted effect=%s history=%s", test.name, status, history[len(cases)-1-i].Status)
		}
		slot, calls := execute(identities[i], nil)
		switch test.status {
		case api.WorkflowEffectStatusFailed:
			if calls != 1 || slot.failure != nil {
				t.Fatalf("%s: reviewed retry calls=%d failure=%v", test.name, calls, slot.failure)
			}
		case api.WorkflowEffectStatusSucceeded:
			if calls != 0 || !slot.alreadySucceeded {
				t.Fatalf("accepted retry calls=%d alreadySucceeded=%v", calls, slot.alreadySucceeded)
			}
		case api.WorkflowEffectStatusStarted:
			t.Fatal("restart retained a started effect instead of marking it unknown")
		case api.WorkflowEffectStatusUnknown:
			if calls != 0 || slot.failure == nil || slot.failure.Code != "unknown_outcome" {
				t.Fatalf("unknown retry calls=%d failure=%v", calls, slot.failure)
			}
		}
	}
}
