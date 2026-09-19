// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestReleaseWorkflowStatePersistenceAndRetention(t *testing.T) {
	t.Parallel()

	repo := openMigratedTestRepo(t)

	ctx := context.Background()
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	active := workflowStateRecordForTest("workflow-active", api.WorkflowStatusActive, now, `{"source":"same","generation":1}`)
	created, idempotent, err := repo.CreateReleaseWorkflowState(ctx, active)
	if err != nil || idempotent || created.WorkflowID != active.WorkflowID {
		t.Fatalf("create workflow state = %#v, %v, %v", created, idempotent, err)
	}
	prior, idempotent, err := repo.CreateReleaseWorkflowState(ctx, active)
	if err != nil || !idempotent || string(prior.Payload) != string(active.Payload) {
		t.Fatalf("idempotent create = %#v, %v, %v", prior, idempotent, err)
	}
	conflict := active
	conflict.WorkflowID = "workflow-conflict"
	conflict.CreationFingerprint = "different"
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, conflict); !errors.Is(err, api.ErrReleaseWorkflowIdempotencyConflict) {
		t.Fatalf("creation conflict error = %v", err)
	}
	if _, err := repo.LoadReleaseWorkflowState(ctx, "owner-2", active.WorkflowID); !errors.Is(err, api.ErrReleaseWorkflowStateNotFound) {
		t.Fatalf("foreign-owner load error = %v", err)
	}

	updated := active
	updated.Revision = 2
	updated.Payload = []byte(`{"source":"same","generation":2}`)
	updated.UpdatedAt = now.Add(time.Minute)
	if err := repo.SaveReleaseWorkflowState(ctx, 1, updated); err != nil {
		t.Fatalf("save workflow state: %v", err)
	}
	if err := repo.SaveReleaseWorkflowState(ctx, 1, updated); !errors.Is(err, api.ErrReleaseWorkflowRevisionConflict) {
		t.Fatalf("stale save error = %v", err)
	}

	terminal := workflowStateRecordForTest("workflow-terminal", api.WorkflowStatusCompleted, now, `{"source":"same","generation":3}`)
	terminal.CreationKey = "terminal-key"
	terminal.CreationFingerprint = "terminal-fingerprint"
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, terminal); err != nil {
		t.Fatalf("create terminal workflow: %v", err)
	}
	deleted, err := repo.DeleteTerminalReleaseWorkflowStatesBefore(ctx, now.Add(time.Hour))
	if err != nil || deleted != 1 {
		t.Fatalf("delete terminal workflows = %d, %v", deleted, err)
	}
	if _, err := repo.LoadReleaseWorkflowState(ctx, active.OwnerID, active.WorkflowID); err != nil {
		t.Fatalf("active workflow removed by retention: %v", err)
	}
	if _, err := repo.LoadReleaseWorkflowState(ctx, terminal.OwnerID, terminal.WorkflowID); !errors.Is(err, api.ErrReleaseWorkflowStateNotFound) {
		t.Fatalf("terminal workflow retained: %v", err)
	}
}

func TestReleaseWorkflowStateConcurrentRevisionCAS(t *testing.T) {
	t.Parallel()

	repo := openMigratedTestRepo(t)

	ctx := context.Background()
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	record := workflowStateRecordForTest("workflow-cas", api.WorkflowStatusActive, now, `{"revision":1}`)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, record); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	var wait sync.WaitGroup
	errs := make(chan error, 2)
	for value := 2; value <= 3; value++ {
		wait.Add(1)
		go func(value int) {
			defer wait.Done()
			candidate := record
			candidate.Revision = 2
			candidate.UpdatedAt = now.Add(time.Duration(value) * time.Minute)
			candidate.Payload = []byte{byte(value)}
			errs <- repo.SaveReleaseWorkflowState(ctx, 1, candidate)
		}(value)
	}
	wait.Wait()
	close(errs)
	succeeded := 0
	conflicted := 0
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, api.ErrReleaseWorkflowRevisionConflict):
			conflicted++
		default:
			t.Fatalf("concurrent save error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent CAS outcomes: succeeded=%d conflicted=%d", succeeded, conflicted)
	}
}

func TestReleaseWorkflowStateSaveRollsBackReusableDescriptionWithState(t *testing.T) {
	t.Parallel()

	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	state := workflowStateRecordForTest("workflow-description-cache", api.WorkflowStatusActive, now, `{"revision":1}`)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, state); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	updated := state
	updated.Revision = 2
	updated.UpdatedAt = now.Add(time.Minute)
	updated.Payload = []byte(`{"revision":2}`)
	updated.DescriptionReuse = &api.ReusableDescriptionRecord{
		SourcePath:  sourcePath,
		Description: reusableDescriptionForTest("a", "first"),
	}
	if _, err := repo.RawDB().ExecContext(ctx, `
		CREATE TRIGGER fail_reusable_description_insert
		BEFORE INSERT ON description_reusable
		BEGIN SELECT RAISE(ABORT, 'forced reusable description failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveReleaseWorkflowState(ctx, 1, updated); err == nil {
		t.Fatal("expected reusable description failure")
	}
	stored, err := repo.LoadReleaseWorkflowState(ctx, state.OwnerID, state.WorkflowID)
	if err != nil || stored.Revision != state.Revision || string(stored.Payload) != string(state.Payload) || stored.DescriptionReuse != nil {
		t.Fatalf("failed save changed workflow state: %#v, err=%v", stored, err)
	}
	if _, found, err := repo.LoadReusableDescription(ctx, sourcePath); err != nil || found {
		t.Fatalf("failed save persisted reusable description found=%t err=%v", found, err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, `DROP TRIGGER fail_reusable_description_insert`); err != nil {
		t.Fatal(err)
	}
	updated.DescriptionReuse.Description.Descriptions[0].Rendered = "retry"
	if err := repo.SaveReleaseWorkflowState(ctx, 1, updated); err != nil {
		t.Fatalf("retry same expected revision: %v", err)
	}
	stored, err = repo.LoadReleaseWorkflowState(ctx, state.OwnerID, state.WorkflowID)
	if err != nil || stored.Revision != updated.Revision || string(stored.Payload) != string(updated.Payload) || stored.DescriptionReuse != nil {
		t.Fatalf("retry workflow state = %#v, err=%v", stored, err)
	}
	cache, found, err := repo.LoadReusableDescription(ctx, sourcePath)
	if err != nil || !found || cache.Descriptions[0].Rendered != "retry" {
		t.Fatalf("retry reusable description = %#v found=%t err=%v", cache, found, err)
	}
}

func TestReleaseWorkflowStateSaveRejectsUnauthorizedReusableDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		descriptionPath func(string) string
		context         func(context.Context, api.ActiveInputRecord) context.Context
		wantErr         error
	}{
		{
			name:            "mismatched source",
			descriptionPath: func(sourcePath string) string { return sourcePath + ".other" },
			context: func(ctx context.Context, active api.ActiveInputRecord) context.Context {
				return api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: active.CoordinatorID, Fence: active.Fence})
			},
			wantErr: api.ErrActiveInputChanged,
		},
		{
			name:            "stale coordinator",
			descriptionPath: func(sourcePath string) string { return sourcePath },
			context: func(ctx context.Context, _ api.ActiveInputRecord) context.Context {
				return api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: "stale", Fence: 1})
			},
			wantErr: api.ErrActiveInputLeaseLost,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := openMigratedTestRepo(t)
			ctx := t.Context()
			now := time.Now().UTC()
			state := workflowStateRecordForTest(api.WorkflowID("workflow-"+test.name), api.WorkflowStatusActive, now, `{"revision":1}`)
			if _, _, err := repo.CreateReleaseWorkflowState(ctx, state); err != nil {
				t.Fatal(err)
			}
			sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
			active := activateReleaseWorkflowStateTestInput(t, repo, state, sourcePath, now)
			updated := state
			updated.Revision = 2
			updated.UpdatedAt = now.Add(time.Minute)
			updated.Payload = []byte(`{"revision":2}`)
			updated.DescriptionReuse = &api.ReusableDescriptionRecord{
				SourcePath:  test.descriptionPath(sourcePath),
				Description: reusableDescriptionForTest("a", "rendered"),
			}
			if err := repo.SaveReleaseWorkflowState(test.context(ctx, active), 1, updated); !errors.Is(err, test.wantErr) {
				t.Fatalf("save unauthorized reusable description = %v, want %v", err, test.wantErr)
			}
			stored, err := repo.LoadReleaseWorkflowState(ctx, state.OwnerID, state.WorkflowID)
			if err != nil || stored.Revision != state.Revision || string(stored.Payload) != string(state.Payload) {
				t.Fatalf("unauthorized save changed workflow state: %#v, err=%v", stored, err)
			}
			if _, found, err := repo.LoadReusableDescription(ctx, test.descriptionPath(sourcePath)); err != nil || found {
				t.Fatalf("unauthorized save persisted reusable description found=%t err=%v", found, err)
			}
		})
	}
}

func TestReleaseWorkflowStateDeletionPreservesActiveSlotWorkflow(t *testing.T) {
	t.Parallel()

	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	now := time.Now().UTC()
	workflow := workflowStateRecordForTest("workflow-active-retention", api.WorkflowStatusCompleted, now, `{"revision":1}`)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	if err := activateSubmissionFenceTestInput(ctx, repo, workflow, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteReleaseWorkflowState(ctx, workflow.OwnerID, workflow.WorkflowID); !errors.Is(err, api.ErrActiveInputBusy) {
		t.Fatalf("delete active workflow = %v", err)
	}
	deleted, err := repo.DeleteTerminalReleaseWorkflowStatesBefore(ctx, now.Add(time.Hour))
	if err != nil || deleted != 0 {
		t.Fatalf("retain active terminal workflow: deleted=%d err=%v", deleted, err)
	}
	if _, err := repo.LoadReleaseWorkflowState(ctx, workflow.OwnerID, workflow.WorkflowID); err != nil {
		t.Fatalf("active workflow was removed by retention: %v", err)
	}
}

func workflowStateRecordForTest(
	workflowID api.WorkflowID,
	status api.WorkflowStatus,
	now time.Time,
	payload string,
) api.ReleaseWorkflowStateRecord {
	return api.ReleaseWorkflowStateRecord{
		OwnerID:             "owner-1",
		WorkflowID:          workflowID,
		Revision:            1,
		Status:              status,
		CreationKey:         "create-" + string(workflowID),
		CreationFingerprint: api.WorkflowFingerprint("fingerprint-" + string(workflowID)),
		Payload:             []byte(payload),
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func activateReleaseWorkflowStateTestInput(
	t *testing.T,
	repo *SQLiteRepository,
	state api.ReleaseWorkflowStateRecord,
	sourcePath string,
	now time.Time,
) api.ActiveInputRecord {
	t.Helper()
	input, err := repo.SaveInputRecord(t.Context(), api.InputRecord{
		ID:            "input-" + string(state.WorkflowID),
		CanonicalPath: sourcePath,
		SourceVersion: "version",
		Manifest:      []byte(`{}`),
		UpdatedAt:     now,
	})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := repo.LoadActiveInput(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	opening := api.ActiveInputRecord{
		State:          api.ActiveInputOpening,
		Revision:       empty.Revision + 1,
		Fence:          empty.Fence + 1,
		OwnerID:        state.OwnerID,
		CoordinatorID:  "workflow-state-test",
		LeaseExpiresAt: now.Add(time.Minute),
		ReservationID:  "open",
		RequestedPath:  sourcePath,
		IdempotencyKey: "open",
	}
	if err := repo.CompareAndSwapActiveInput(t.Context(), empty, opening, now); err != nil {
		t.Fatal(err)
	}
	active := opening
	active.State = api.ActiveInputActive
	active.Revision++
	active.InputID = input.ID
	active.SourceVersion = input.SourceVersion
	active.WorkflowID = state.WorkflowID
	active.ReservationID = ""
	active.RequestedPath = ""
	if err := repo.CompareAndSwapActiveInput(t.Context(), opening, active, now); err != nil {
		t.Fatal(err)
	}
	return active
}
