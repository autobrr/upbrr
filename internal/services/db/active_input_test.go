// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestActiveInputCoordinatorTakeover(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	other, err := Open(repo.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	ctx := t.Context()
	now := time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC)
	empty, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opening := api.ActiveInputRecord{
		State:          api.ActiveInputOpening,
		Revision:       1,
		Fence:          1,
		OwnerID:        "owner",
		CoordinatorID:  "first",
		LeaseExpiresAt: now.Add(time.Minute),
		ReservationID:  "reservation",
		RequestedPath:  filepath.Join(t.TempDir(), "source.mkv"),
		IdempotencyKey: "open",
	}
	if err := repo.CompareAndSwapActiveInput(ctx, empty, opening, now); err != nil {
		t.Fatal(err)
	}
	competitor := opening
	competitor.CoordinatorID = "second"
	if err := other.CompareAndSwapActiveInput(ctx, empty, competitor, now); !errors.Is(err, api.ErrActiveInputChanged) {
		t.Fatalf("concurrent acquire = %v", err)
	}
	active := opening
	active.State, active.Revision = api.ActiveInputActive, 2
	active.InputID, active.SourceVersion, active.WorkflowID = "input", "verified", "workflow"
	active.ReservationID, active.RequestedPath = "", ""
	if err := repo.CompareAndSwapActiveInput(ctx, opening, active, now); err != nil {
		t.Fatal(err)
	}
	// A live owner cannot be replaced even by a caller that has read its current token.
	recovering := active
	recovering.State, recovering.Revision, recovering.Fence = api.ActiveInputRecovering, 3, 2
	recovering.CoordinatorID, recovering.LeaseExpiresAt = "second", now.Add(3*time.Minute)
	if err := other.CompareAndSwapActiveInput(ctx, active, recovering, now); !errors.Is(err, api.ErrActiveInputBusy) {
		t.Fatalf("live takeover = %v", err)
	}
	later := now.Add(2 * time.Minute)
	if err := other.CompareAndSwapActiveInput(ctx, active, recovering, later); err != nil {
		t.Fatal(err)
	}
	if err := repo.RenewActiveInput(ctx, "first", 1, later, later.Add(time.Minute)); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("late heartbeat = %v", err)
	}
	late := active
	late.Revision++
	if err := repo.CompareAndSwapActiveInput(ctx, active, late, later); !errors.Is(err, api.ErrActiveInputChanged) {
		t.Fatalf("late publish = %v", err)
	}
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	persisted, err := other.LoadActiveInput(ctx)
	if err != nil || persisted != recovering {
		t.Fatalf("migration lost slot: %#v, %v", persisted, err)
	}
}

func TestRelinquishActiveInputPreservesCommittedSlotAndRequiresExactFence(t *testing.T) {
	t.Parallel()

	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	now := time.Now().UTC()
	empty, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opening := api.ActiveInputRecord{
		State:          api.ActiveInputOpening,
		Revision:       1,
		Fence:          1,
		OwnerID:        "owner",
		CoordinatorID:  "coordinator",
		LeaseExpiresAt: now.Add(time.Minute),
		ReservationID:  "reservation",
		RequestedPath:  filepath.Join(t.TempDir(), "source.mkv"),
		IdempotencyKey: "open",
	}
	if err := repo.CompareAndSwapActiveInput(ctx, empty, opening, now); err != nil {
		t.Fatal(err)
	}
	active := opening
	active.State, active.Revision = api.ActiveInputActive, 2
	active.InputID, active.SourceVersion, active.WorkflowID = "input", "verified", "workflow"
	active.ReservationID, active.RequestedPath = "", ""
	if err := repo.CompareAndSwapActiveInput(ctx, opening, active, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.RelinquishActiveInput(ctx, "other", active.Fence, now); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("relinquish wrong coordinator = %v", err)
	}
	if err := repo.RelinquishActiveInput(ctx, active.CoordinatorID, active.Fence, now); err != nil {
		t.Fatalf("relinquish active input: %v", err)
	}
	relinquished, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if relinquished.InputID != active.InputID || relinquished.WorkflowID != active.WorkflowID ||
		relinquished.Fence != active.Fence || relinquished.LeaseExpiresAt.After(now) {
		t.Fatalf("relinquished active input = %#v", relinquished)
	}
}

func TestCloseIdleActiveInputClearsExpiredForeignCoordinatorAndFencesStaleClose(t *testing.T) {
	t.Parallel()

	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	now := time.Now().UTC()
	active := createIdleActiveInputForTest(ctx, t, repo, now, "owner", "workflow", "first-coordinator")
	if err := repo.CloseIdleActiveInput(ctx, active, active.LeaseExpiresAt.Add(time.Second)); err != nil {
		t.Fatalf("close expired input from prior coordinator: %v", err)
	}
	closed, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if closed.State != api.ActiveInputEmpty || closed.Revision != active.Revision+1 || closed.Fence != active.Fence {
		t.Fatalf("closed input = %#v", closed)
	}
	if err := repo.RenewActiveInput(ctx, active.CoordinatorID, active.Fence, now, now.Add(time.Minute)); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("previous coordinator renewed closed input: %v", err)
	}

	newer := createIdleActiveInputForTest(ctx, t, repo, now.Add(2*time.Minute), "owner", "workflow", "second-coordinator")
	if err := repo.CloseIdleActiveInput(ctx, active, now.Add(2*time.Minute)); !errors.Is(err, api.ErrActiveInputChanged) {
		t.Fatalf("stale close = %v, want %v", err, api.ErrActiveInputChanged)
	}
	persisted, err := repo.LoadActiveInput(ctx)
	if err != nil || persisted != newer {
		t.Fatalf("stale close changed new input = %#v, err=%v", persisted, err)
	}
}

func TestCloseIdleActiveInputPreservesProtectedWork(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		setup func(*testing.T, *SQLiteRepository, context.Context, time.Time, api.ActiveInputRecord)
		want  error
	}{
		{
			name: "queued operation",
			setup: func(t *testing.T, repo *SQLiteRepository, ctx context.Context, now time.Time, active api.ActiveInputRecord) {
				t.Helper()
				operation := workflowOperationRecordForTest(t, active.OwnerID, active.WorkflowID, "queued-operation", now)
				if _, _, err := repo.CreateReleaseWorkflowOperation(
					api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: active.CoordinatorID, Fence: active.Fence}), operation,
				); err != nil {
					t.Fatalf("create queued operation: %v", err)
				}
			},
			want: api.ErrActiveInputBusy,
		},
		{
			name: "live work lease",
			setup: func(t *testing.T, repo *SQLiteRepository, ctx context.Context, now time.Time, active api.ActiveInputRecord) {
				t.Helper()
				operation := workflowOperationRecordForTest(t, active.OwnerID, active.WorkflowID, "leased-operation", now)
				activeCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: active.CoordinatorID, Fence: active.Fence})
				created, _, err := repo.CreateReleaseWorkflowOperation(activeCtx, operation)
				if err != nil {
					t.Fatalf("create leased operation: %v", err)
				}
				if err := repo.ClaimReleaseWorkflowWork(activeCtx, api.ReleaseWorkflowWorkRecord{
					OwnerID:        active.OwnerID,
					WorkflowID:     active.WorkflowID,
					OperationID:    operation.OperationID,
					LeaseOwner:     "previous-process",
					LeaseExpiresAt: now.Add(time.Minute),
					Checkpoint:     []byte(`{}`),
					UpdatedAt:      now,
				}); err != nil {
					t.Fatalf("claim live work lease: %v", err)
				}
				completedAt := now.Add(time.Second)
				created.Status.Sequence++
				created.Status.Status = api.StageStatusCompleted
				created.Status.UpdatedAt, created.Status.CompletedAt = completedAt, &completedAt
				if err := repo.SaveReleaseWorkflowOperation(activeCtx, created.Status.Sequence-1, created); err != nil {
					t.Fatalf("complete leased operation: %v", err)
				}
			},
			want: api.ErrActiveInputBusy,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := openMigratedTestRepo(t)
			ctx := t.Context()
			now := time.Now().UTC()
			workflow := workflowStateRecordForTest("workflow", api.WorkflowStatusActive, now, `{}`)
			if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
				t.Fatalf("create workflow: %v", err)
			}
			active := createIdleActiveInputForTest(ctx, t, repo, now, workflow.OwnerID, workflow.WorkflowID, "first-coordinator")
			test.setup(t, repo, ctx, now, active)

			if err := repo.CloseIdleActiveInput(ctx, active, now); !errors.Is(err, test.want) {
				t.Fatalf("close protected input = %v, want %v", err, test.want)
			}
			persisted, err := repo.LoadActiveInput(ctx)
			if err != nil || persisted != active {
				t.Fatalf("protected close changed input = %#v, err=%v", persisted, err)
			}
		})
	}
}

func TestCloseIdleActiveInputDetachesUnresolvedEffects(t *testing.T) {
	t.Parallel()

	for _, status := range []api.WorkflowEffectStatus{api.WorkflowEffectStatusStarted, api.WorkflowEffectStatusUnknown} {
		t.Run(string(status), func(t *testing.T) {
			repo := openMigratedTestRepo(t)
			ctx := t.Context()
			now := time.Now().UTC()
			workflow := workflowStateRecordForTest("workflow", api.WorkflowStatusActive, now, `{}`)
			if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
				t.Fatalf("create workflow: %v", err)
			}
			active := createIdleActiveInputForTest(ctx, t, repo, now, workflow.OwnerID, workflow.WorkflowID, "first-coordinator")
			activeCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: active.CoordinatorID, Fence: active.Fence})
			effect, _, err := repo.BeginReleaseWorkflowEffect(activeCtx, workflowEffectForTest(workflow, "operation", "effect", "PTP", now))
			if err != nil {
				t.Fatalf("begin effect: %v", err)
			}
			if status == api.WorkflowEffectStatusUnknown {
				if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(activeCtx, workflow.OwnerID, workflow.WorkflowID, effect.OperationID, now.Add(time.Second)); err != nil {
					t.Fatalf("mark effect unknown: %v", err)
				}
			}
			if err := repo.CloseIdleActiveInput(ctx, active, now.Add(2*time.Second)); err != nil {
				t.Fatalf("close input with %s effect: %v", status, err)
			}
			closed, err := repo.LoadActiveInput(ctx)
			if err != nil || closed.State != api.ActiveInputEmpty {
				t.Fatalf("input after unresolved effect close = %#v, err=%v", closed, err)
			}
			var persistedStatus api.WorkflowEffectStatus
			if err := repo.RawDB().QueryRowContext(ctx, `SELECT status FROM release_workflow_effects WHERE owner_id = ? AND workflow_id = ? AND effect_id = ?`,
				workflow.OwnerID, workflow.WorkflowID, effect.EffectID).Scan(&persistedStatus); err != nil {
				t.Fatalf("read detached effect: %v", err)
			}
			if persistedStatus != status {
				t.Fatalf("detached effect status = %s, want %s", persistedStatus, status)
			}
			workflowIDs, err := repo.ListLegacyReleaseWorkflowRecoveryWorkflowIDs(ctx, workflow.OwnerID)
			if err != nil || len(workflowIDs) != 1 || workflowIDs[0] != workflow.WorkflowID {
				t.Fatalf("legacy recovery workflows = %#v, err=%v", workflowIDs, err)
			}
			opening := api.ActiveInputRecord{
				State:          api.ActiveInputOpening,
				Revision:       closed.Revision + 1,
				Fence:          closed.Fence + 1,
				OwnerID:        workflow.OwnerID,
				CoordinatorID:  "next-coordinator",
				LeaseExpiresAt: now.Add(time.Minute),
				ReservationID:  "open",
				RequestedPath:  filepath.Join(t.TempDir(), "source.mkv"),
				IdempotencyKey: "open",
			}
			if err := repo.CompareAndSwapActiveInput(ctx, closed, opening, now.Add(3*time.Second)); !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
				t.Fatalf("open after detached %s effect = %v, want %v", status, err, api.ErrReleaseWorkflowEffectOutcomeUnknown)
			}
		})
	}
}

func TestCloseIdleActiveInputAllowsSucceededEffects(t *testing.T) {
	t.Parallel()

	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	now := time.Now().UTC()
	workflow := workflowStateRecordForTest("workflow", api.WorkflowStatusActive, now, `{}`)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	active := createIdleActiveInputForTest(ctx, t, repo, now, workflow.OwnerID, workflow.WorkflowID, "first-coordinator")
	activeCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: active.CoordinatorID, Fence: active.Fence})
	started, _, err := repo.BeginReleaseWorkflowEffect(
		activeCtx,
		workflowEffectForTest(workflow, "operation", "effect", "PTP", now),
	)
	if err != nil {
		t.Fatalf("begin effect: %v", err)
	}
	completedAt := now.Add(time.Second)
	started.UpdatedAt, started.CompletedAt = completedAt, &completedAt
	if err := repo.CompleteReleaseWorkflowEffect(activeCtx, api.WorkflowEffectStatusSucceeded, started); err != nil {
		t.Fatalf("complete effect: %v", err)
	}
	if err := repo.CloseIdleActiveInput(ctx, active, completedAt); err != nil {
		t.Fatalf("close input with succeeded effect: %v", err)
	}
	closed, err := repo.LoadActiveInput(ctx)
	if err != nil || closed.State != api.ActiveInputEmpty {
		t.Fatalf("input after succeeded effect close = %#v, err=%v", closed, err)
	}
}

func createIdleActiveInputForTest(
	ctx context.Context,
	t *testing.T,
	repo *SQLiteRepository,
	now time.Time,
	ownerID string,
	workflowID api.WorkflowID,
	coordinator string,
) api.ActiveInputRecord {
	t.Helper()
	empty, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opening := api.ActiveInputRecord{
		State:          api.ActiveInputOpening,
		Revision:       empty.Revision + 1,
		Fence:          empty.Fence + 1,
		OwnerID:        ownerID,
		CoordinatorID:  coordinator,
		LeaseExpiresAt: now.Add(time.Minute),
		ReservationID:  "open",
		RequestedPath:  filepath.Join(t.TempDir(), "source.mkv"),
		IdempotencyKey: "open",
	}
	if err := repo.CompareAndSwapActiveInput(ctx, empty, opening, now); err != nil {
		t.Fatal(err)
	}
	active := opening
	active.State, active.Revision = api.ActiveInputActive, opening.Revision+1
	active.InputID, active.SourceVersion, active.WorkflowID = "input", "verified", workflowID
	active.ReservationID, active.RequestedPath = "", ""
	if err := repo.CompareAndSwapActiveInput(ctx, opening, active, now); err != nil {
		t.Fatal(err)
	}
	return active
}

func TestInputRecordPreservesIdentityAcrossVerification(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	record := api.InputRecord{
		ID:            "first",
		CanonicalPath: filepath.Join(t.TempDir(), "source.mkv"),
		SourceVersion: "old",
		Manifest:      []byte(`{"version":1}`),
		UpdatedAt:     time.Now().UTC(),
	}
	if _, err := repo.SaveInputRecord(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	record.ID = "new"
	updated, err := repo.SaveInputRecord(t.Context(), record)
	if err != nil || updated.ID != "first" {
		t.Fatalf("save identity = %#v, %v", updated, err)
	}
	loaded, err := repo.LoadInputRecord(t.Context(), record.CanonicalPath)
	if err != nil || loaded.ID != "first" || loaded.SourceVersion != "old" {
		t.Fatalf("load identity = %#v, %v", loaded, err)
	}
	record.SourceVersion = "changed"
	if _, err := repo.SaveInputRecord(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	loaded, err = repo.LoadInputRecord(t.Context(), record.CanonicalPath)
	if err != nil || loaded.SourceVersion != "changed" {
		t.Fatalf("changed source record = %#v, %v", loaded, err)
	}
	loaded, err = repo.LoadInputRecordByID(t.Context(), updated.ID)
	if err != nil || loaded.ID != updated.ID || loaded.CanonicalPath != record.CanonicalPath {
		t.Fatalf("load by ID = %#v, %v", loaded, err)
	}
	if _, err := repo.LoadInputRecordByID(t.Context(), "missing"); !errors.Is(err, api.ErrInputRecordNotFound) {
		t.Fatalf("load missing ID = %v", err)
	}
}

func TestInputWorkflowMigrationFindsRetainedAudioBeforeEmptyReload(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	now := time.Now().UTC()
	for _, item := range []struct {
		id      api.WorkflowID
		owner   string
		audio   map[string]any
		updated time.Time
	}{
		{
			id:      "workflow-audio",
			owner:   "owner-1",
			audio:   map[string]any{"result": map[string]any{"createdAt": now.Add(time.Second).Format(time.RFC3339Nano)}},
			updated: now,
		},
		{
			id:      "workflow-empty-reload",
			owner:   "owner-1",
			audio:   map[string]any{},
			updated: now.Add(time.Minute),
		},
		{
			id:      "workflow-other-owner",
			owner:   "owner-2",
			audio:   map[string]any{"result": map[string]any{"createdAt": now.Add(3 * time.Second).Format(time.RFC3339Nano)}},
			updated: now.Add(2 * time.Minute),
		},
	} {
		payload, err := json.Marshal(map[string]any{"SourcePath": source, "AudioAnalyses": item.audio})
		if err != nil {
			t.Fatal(err)
		}
		state := workflowStateRecordForTest(item.id, api.WorkflowStatusDraft, item.updated, string(payload))
		state.OwnerID = item.owner
		if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.SaveInputRecord(t.Context(), api.InputRecord{
		ID:            "input",
		CanonicalPath: source,
		SourceVersion: "verified",
		Manifest:      []byte(`{}`),
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := migrateRetainInputWorkflow(t.Context(), repo.RawDB()); err != nil {
		t.Fatal(err)
	}
	for owner, want := range map[string]api.WorkflowID{"owner-1": "workflow-audio", "owner-2": "workflow-other-owner"} {
		workflowID, audioID, err := repo.LoadInputWorkflowAssociation(t.Context(), source, owner, "verified")
		if err != nil || workflowID != want || audioID != "result" {
			t.Fatalf("backfilled owner %s workflow = %q audio = %q, %v", owner, workflowID, audioID, err)
		}
	}
	changedSource := filepath.Join(t.TempDir(), "Changed.Release.2026.mkv")
	changedPayload, err := json.Marshal(map[string]any{
		"SourcePath":    changedSource,
		"AudioAnalyses": map[string]any{"old": map[string]any{"createdAt": now.Add(time.Second).Format(time.RFC3339Nano)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	changedState := workflowStateRecordForTest("workflow-before-change", api.WorkflowStatusDraft, now, string(changedPayload))
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), changedState); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveInputRecord(t.Context(), api.InputRecord{
		ID:            "changed-input",
		CanonicalPath: changedSource,
		SourceVersion: "new-verified-version",
		Manifest:      []byte(`{}`),
		UpdatedAt:     now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := migrateRetainInputWorkflow(t.Context(), repo.RawDB()); err != nil {
		t.Fatal(err)
	}
	workflowID, audioID, err := repo.LoadInputWorkflowAssociation(t.Context(), changedSource, "owner-1", "new-verified-version")
	if err != nil || workflowID != "" || audioID != "" {
		t.Fatalf("changed bytes backfill = %q audio = %q, %v", workflowID, audioID, err)
	}
	subsecondSource := filepath.Join(t.TempDir(), "Subsecond.Release.2026.mkv")
	subsecondPayload, err := json.Marshal(map[string]any{
		"SourcePath":    subsecondSource,
		"AudioAnalyses": map[string]any{"old": map[string]any{"createdAt": "2026-09-25T01:00:00.1Z"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	subsecondState := workflowStateRecordForTest("workflow-subsecond", api.WorkflowStatusDraft, now, string(subsecondPayload))
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), subsecondState); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveInputRecord(t.Context(), api.InputRecord{
		ID:            "subsecond-input",
		CanonicalPath: subsecondSource,
		SourceVersion: "changed-within-second",
		Manifest:      []byte(`{}`),
		UpdatedAt:     time.Date(2026, time.September, 25, 1, 0, 0, 110_000_000, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := migrateRetainInputWorkflow(t.Context(), repo.RawDB()); err != nil {
		t.Fatal(err)
	}
	workflowID, audioID, err = repo.LoadInputWorkflowAssociation(t.Context(), subsecondSource, "owner-1", "changed-within-second")
	if err != nil || workflowID != "" || audioID != "" {
		t.Fatalf("subsecond changed bytes backfill = %q audio = %q, %v", workflowID, audioID, err)
	}
	latestSource := filepath.Join(t.TempDir(), "Latest.Release.2026.mkv")
	latestPayload, err := json.Marshal(map[string]any{
		"SourcePath": latestSource,
		"AudioAnalyses": map[string]any{
			"older": map[string]any{"createdAt": "2026-09-25T01:00:00.1Z", "revision": 2},
			"newer": map[string]any{"createdAt": "2026-09-25T01:00:00.11Z", "revision": 3},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	latestState := workflowStateRecordForTest("workflow-latest", api.WorkflowStatusDraft, now, string(latestPayload))
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), latestState); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveInputRecord(t.Context(), api.InputRecord{
		ID:            "latest-input",
		CanonicalPath: latestSource,
		SourceVersion: "verified-before-analysis",
		Manifest:      []byte(`{}`),
		UpdatedAt:     time.Date(2026, time.September, 25, 0, 59, 59, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := migrateRetainInputWorkflow(t.Context(), repo.RawDB()); err != nil {
		t.Fatal(err)
	}
	workflowID, audioID, err = repo.LoadInputWorkflowAssociation(t.Context(), latestSource, "owner-1", "verified-before-analysis")
	if err != nil || workflowID != "workflow-latest" || audioID != "newer" {
		t.Fatalf("latest audio backfill = %q audio = %q, %v", workflowID, audioID, err)
	}
	disabledSource := filepath.Join(t.TempDir(), "Disabled.Release.2026.mkv")
	disabledPayload, err := json.Marshal(map[string]any{
		"SourcePath": disabledSource,
		"AudioAnalyses": map[string]any{
			"result": map[string]any{"createdAt": "2026-09-25T01:00:00Z", "revision": 2},
		},
		"Receipts": map[string]any{
			"set_audio_analysis_enabled\x00disable": map[string]any{
				"Result": map[string]any{"workflow": map[string]any{"revision": 3}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	disabledState := workflowStateRecordForTest("workflow-disabled", api.WorkflowStatusDraft, now, string(disabledPayload))
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), disabledState); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveInputRecord(t.Context(), api.InputRecord{
		ID:            "disabled-input",
		CanonicalPath: disabledSource,
		SourceVersion: "verified-disabled",
		Manifest:      []byte(`{}`),
		UpdatedAt:     time.Date(2026, time.September, 25, 0, 59, 59, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := migrateRetainInputWorkflow(t.Context(), repo.RawDB()); err != nil {
		t.Fatal(err)
	}
	workflowID, audioID, err = repo.LoadInputWorkflowAssociation(t.Context(), disabledSource, "owner-1", "verified-disabled")
	if err != nil || workflowID != "" || audioID != "" {
		t.Fatalf("disabled audio backfill = %q audio = %q, %v", workflowID, audioID, err)
	}
}

func TestActiveInputRejectsLateWorkflowMutation(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	now := time.Now().UTC()
	ctx := t.Context()
	state := workflowStateRecordForTest("workflow", api.WorkflowStatusActive, now, `{}`)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, state); err != nil {
		t.Fatal(err)
	}
	empty, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opening := api.ActiveInputRecord{
		State:          api.ActiveInputOpening,
		Revision:       1,
		Fence:          1,
		OwnerID:        state.OwnerID,
		CoordinatorID:  "first",
		LeaseExpiresAt: now.Add(time.Minute),
		ReservationID:  "open",
		RequestedPath:  filepath.Join(t.TempDir(), "source.mkv"),
		IdempotencyKey: "open",
	}
	if err := repo.CompareAndSwapActiveInput(ctx, empty, opening, now); err != nil {
		t.Fatal(err)
	}
	active := opening
	active.State, active.Revision = api.ActiveInputActive, 2
	active.InputID, active.SourceVersion, active.WorkflowID = "input", "verified", state.WorkflowID
	active.ReservationID, active.RequestedPath = "", ""
	if err := repo.CompareAndSwapActiveInput(ctx, opening, active, now); err != nil {
		t.Fatal(err)
	}
	oldContext := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: "first", Fence: 1})
	state.Revision++
	if err := repo.SaveReleaseWorkflowState(oldContext, state.Revision-1, state); err != nil {
		t.Fatal(err)
	}
	recovering := active
	recovering.State, recovering.Revision, recovering.Fence = api.ActiveInputRecovering, 3, 2
	recovering.CoordinatorID, recovering.LeaseExpiresAt = "second", now.Add(3*time.Minute)
	if err := repo.CompareAndSwapActiveInput(ctx, active, recovering, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	state.Revision++
	if err := repo.SaveReleaseWorkflowState(oldContext, state.Revision-1, state); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("old publisher after takeover = %v", err)
	}
	if err := repo.SaveReleaseWorkflowState(ctx, state.Revision-1, state); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("mutation without token = %v", err)
	}
}

func TestLegacyRecoveryFencesOwnerEffectsAndDoesNotBypassNormalAdmission(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	now := time.Now().UTC()
	state := workflowStateRecordForTest("legacy-workflow", api.WorkflowStatusActive, now, `{}`)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, state); err != nil {
		t.Fatal(err)
	}
	effect := workflowEffectForTest(state, "legacy-operation", "legacy-effect", "PTP", now)
	if _, _, err := repo.BeginReleaseWorkflowEffect(ctx, effect); err != nil {
		t.Fatalf("begin legacy effect: %v", err)
	}
	empty, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recovering := api.ActiveInputRecord{
		State:          api.ActiveInputRecovering,
		Revision:       empty.Revision + 1,
		Fence:          empty.Fence + 1,
		OwnerID:        state.OwnerID,
		CoordinatorID:  "legacy-recovery",
		WorkflowID:     state.WorkflowID,
		LeaseExpiresAt: now.Add(time.Minute),
	}
	if err := repo.CompareAndSwapActiveInput(ctx, empty, recovering, now); err != nil {
		t.Fatalf("claim legacy recovery: %v", err)
	}
	recoveryCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{
		CoordinatorID: recovering.CoordinatorID,
		Fence:         recovering.Fence,
	})
	effects, err := repo.RecoverLegacyReleaseWorkflowEffects(recoveryCtx, state.OwnerID, state.WorkflowID, now.Add(time.Second))
	if err != nil {
		t.Fatalf("recover legacy effects: %v", err)
	}
	if len(effects) != 1 || effects[0].Status != api.WorkflowEffectStatusUnknown || effects[0].EffectID != effect.EffectID {
		t.Fatalf("legacy effects = %#v", effects)
	}
	if _, err := repo.RecoverLegacyReleaseWorkflowEffects(recoveryCtx, "foreign-owner", state.WorkflowID, now.Add(2*time.Second)); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("foreign legacy recovery = %v", err)
	}
	closed := api.ActiveInputRecord{
		State:          api.ActiveInputEmpty,
		Revision:       recovering.Revision + 1,
		Fence:          recovering.Fence,
		OwnerID:        recovering.OwnerID,
		CoordinatorID:  recovering.CoordinatorID,
		LeaseExpiresAt: recovering.LeaseExpiresAt,
	}
	if err := repo.CompareAndSwapActiveInput(recoveryCtx, recovering, closed, now.Add(3*time.Second)); err != nil {
		t.Fatalf("close legacy recovery: %v", err)
	}
	opening := api.ActiveInputRecord{
		State:          api.ActiveInputOpening,
		Revision:       closed.Revision + 1,
		Fence:          closed.Fence + 1,
		OwnerID:        state.OwnerID,
		CoordinatorID:  "new-open",
		LeaseExpiresAt: now.Add(4 * time.Minute),
		ReservationID:  "new-open",
		RequestedPath:  filepath.Join(t.TempDir(), "source.mkv"),
		IdempotencyKey: "new-open",
	}
	if err := repo.CompareAndSwapActiveInput(ctx, closed, opening, now.Add(4*time.Second)); !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
		t.Fatalf("normal open after legacy close = %v", err)
	}
}

func TestLegacyRecoveryAllowsOwnersToReconcileSequentially(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	now := time.Now().UTC()
	first := workflowStateRecordForTest("legacy-first", api.WorkflowStatusActive, now, `{}`)
	second := workflowStateRecordForTest("legacy-second", api.WorkflowStatusActive, now, `{}`)
	second.OwnerID = "second-owner"
	for _, state := range []api.ReleaseWorkflowStateRecord{first, second} {
		if _, _, err := repo.CreateReleaseWorkflowState(ctx, state); err != nil {
			t.Fatal(err)
		}
	}
	firstEffect := workflowEffectForTest(first, "first-operation", "first-effect", "PTP", now)
	secondEffect := workflowEffectForTest(second, "second-operation", "second-effect", "BTN", now)
	for _, effect := range []api.ReleaseWorkflowEffectRecord{firstEffect, secondEffect} {
		if _, _, err := repo.BeginReleaseWorkflowEffect(ctx, effect); err != nil {
			t.Fatal(err)
		}
	}
	empty, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstSlot := api.ActiveInputRecord{
		State:          api.ActiveInputRecovering,
		Revision:       empty.Revision + 1,
		Fence:          empty.Fence + 1,
		OwnerID:        first.OwnerID,
		CoordinatorID:  "first-recovery",
		WorkflowID:     first.WorkflowID,
		LeaseExpiresAt: now.Add(time.Minute),
	}
	if err := repo.CompareAndSwapActiveInput(ctx, empty, firstSlot, now); err != nil {
		t.Fatalf("claim first legacy recovery: %v", err)
	}
	firstCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: firstSlot.CoordinatorID, Fence: firstSlot.Fence})
	if _, err := repo.RecoverLegacyReleaseWorkflowEffects(firstCtx, first.OwnerID, first.WorkflowID, now.Add(time.Second)); err != nil {
		t.Fatalf("recover first legacy effects: %v", err)
	}
	if err := repo.ResolveReleaseWorkflowEffectUnknown(
		firstCtx, first.OwnerID, first.WorkflowID, api.WorkflowExternalEffectTrackerSubmission, firstEffect.ScopeID, now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("resolve first legacy effect: %v", err)
	}
	firstClosed := api.ActiveInputRecord{
		State:          api.ActiveInputEmpty,
		Revision:       firstSlot.Revision + 1,
		Fence:          firstSlot.Fence,
		OwnerID:        firstSlot.OwnerID,
		CoordinatorID:  firstSlot.CoordinatorID,
		LeaseExpiresAt: firstSlot.LeaseExpiresAt,
	}
	if err := repo.CompareAndSwapActiveInput(firstCtx, firstSlot, firstClosed, now.Add(3*time.Second)); err != nil {
		t.Fatalf("close first legacy recovery while second is unknown: %v", err)
	}
	secondSlot := api.ActiveInputRecord{
		State:          api.ActiveInputRecovering,
		Revision:       firstClosed.Revision + 1,
		Fence:          firstClosed.Fence + 1,
		OwnerID:        second.OwnerID,
		CoordinatorID:  "second-recovery",
		WorkflowID:     second.WorkflowID,
		LeaseExpiresAt: now.Add(2 * time.Minute),
	}
	if err := repo.CompareAndSwapActiveInput(ctx, firstClosed, secondSlot, now.Add(4*time.Second)); err != nil {
		t.Fatalf("claim second legacy recovery: %v", err)
	}
	secondCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: secondSlot.CoordinatorID, Fence: secondSlot.Fence})
	effects, err := repo.RecoverLegacyReleaseWorkflowEffects(secondCtx, second.OwnerID, second.WorkflowID, now.Add(5*time.Second))
	if err != nil || len(effects) != 1 || effects[0].EffectID != secondEffect.EffectID || effects[0].Status != api.WorkflowEffectStatusUnknown {
		t.Fatalf("recover second legacy effects = %#v, %v", effects, err)
	}
}
