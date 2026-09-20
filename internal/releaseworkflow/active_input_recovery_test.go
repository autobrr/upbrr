// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestActiveInputRecoveryRestoresExpiredIdleWorkflowAndRejectsOldCoordinatorState(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repo := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := writeActiveInputRecoverySource(t, "first.mkv", "first source")
	firstVerifier := &hashingActiveInputVerifier{}
	first := newActiveInputRecoveryModule(t, persistent, repo, firstVerifier, clock, "first-coordinator")
	request := OpenInputRequest{
		Input:          api.PrepareInput{SourcePath: sourcePath},
		IdempotencyKey: "open-first",
	}
	opened, err := first.OpenInput(ctx, testOwnerID, request)
	if err != nil {
		t.Fatalf("open first input: %v", err)
	}
	if firstVerifier.calls != 1 || opened.State != api.ActiveInputActive || opened.WorkflowID == "" {
		t.Fatalf("first open = %#v hashes=%d", opened, firstVerifier.calls)
	}

	clock.now = clock.now.Add(workflowWorkLeaseTTL + time.Second)
	secondVerifier := &hashingActiveInputVerifier{}
	second := newActiveInputRecoveryModule(t, persistent, repo, secondVerifier, clock, "second-coordinator")
	recovered, err := second.OpenInput(ctx, testOwnerID, request)
	if err != nil {
		t.Fatalf("recover idle input: %v", err)
	}
	if recovered.State != api.ActiveInputActive || recovered.WorkflowID != opened.WorkflowID ||
		recovered.InputID != opened.InputID || recovered.CoordinatorID != "second-coordinator" ||
		recovered.Fence <= opened.Fence || secondVerifier.calls != 0 {
		t.Fatalf("recovered idle input = %#v hashes=%d", recovered, secondVerifier.calls)
	}

	lateContext := api.WithActiveInputAuthority(context.Background(), api.ActiveInputAuthority{
		CoordinatorID: "first-coordinator",
		Fence:         opened.Fence,
	})
	state, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
	if err != nil {
		t.Fatalf("load workflow for late write: %v", err)
	}
	expected := state.Workflow.Revision
	state.Workflow.Revision++
	state.Workflow.UpdatedAt = time.Now().UTC()
	if err := persistent.Save(lateContext, testOwnerID, expected, state); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("late coordinator state write = %v, want %v", err, api.ErrActiveInputLeaseLost)
	}
	if _, err := first.activeMutationContext(lateContext, testOwnerID, opened.WorkflowID); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("late coordinator work admission = %v, want %v", err, api.ErrActiveInputLeaseLost)
	}
}

func TestOpenInputForeignExpiredRecoveryDoesNotExposeOwnerState(t *testing.T) {
	ctx := t.Context()
	const foreignOwner = "foreign-owner"

	t.Run("idle slot releases safely", func(t *testing.T) {
		clock := &mutableClock{now: time.Now().UTC()}
		repo := openActiveInputRecoveryRepository(ctx, t)
		persistent, err := NewPersistentRepository(repo)
		if err != nil {
			t.Fatal(err)
		}
		first := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "first-coordinator")
		opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
			Input:          api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "idle.mkv", "idle")},
			IdempotencyKey: "open-idle",
		})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(workflowWorkLeaseTTL + time.Second)
		foreignVerifier := &hashingActiveInputVerifier{}
		foreign := newActiveInputRecoveryModule(t, persistent, repo, foreignVerifier, clock, "foreign-coordinator")
		request := OpenInputRequest{
			ExpectedRevision: opened.Revision,
			Input:            api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "foreign.mkv", "foreign")},
			IdempotencyKey:   "open-foreign",
		}
		if _, err := foreign.OpenInput(ctx, foreignOwner, request); !errors.Is(err, api.ErrActiveInputChanged) {
			t.Fatalf("foreign expired idle open = %v, want fresh-revision error", err)
		}
		if foreignVerifier.calls != 0 {
			t.Fatalf("foreign idle recovery hashed source %d times", foreignVerifier.calls)
		}
		empty, err := foreign.ActiveInput(ctx, foreignOwner)
		if err != nil || empty.State != api.ActiveInputEmpty {
			t.Fatalf("foreign idle recovery slot = %#v, err=%v", empty, err)
		}
		request.ExpectedRevision = empty.Revision
		if reopened, err := foreign.OpenInput(ctx, foreignOwner, request); err != nil || reopened.OwnerID != foreignOwner {
			t.Fatalf("foreign open after safe release = %#v, err=%v", reopened, err)
		}
	})

	t.Run("live slot is busy", func(t *testing.T) {
		clock := &mutableClock{now: time.Now().UTC()}
		repo := openActiveInputRecoveryRepository(ctx, t)
		persistent, err := NewPersistentRepository(repo)
		if err != nil {
			t.Fatal(err)
		}
		first := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "first-coordinator")
		opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
			Input:          api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "live.mkv", "live")},
			IdempotencyKey: "open-live",
		})
		if err != nil {
			t.Fatal(err)
		}
		foreignVerifier := &hashingActiveInputVerifier{}
		foreign := newActiveInputRecoveryModule(t, persistent, repo, foreignVerifier, clock, "foreign-coordinator")
		if _, err := foreign.OpenInput(ctx, foreignOwner, OpenInputRequest{
			ExpectedRevision: opened.Revision,
			Input:            api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "foreign-live.mkv", "foreign")},
			IdempotencyKey:   "open-foreign-live",
		}); !errors.Is(err, api.ErrActiveInputBusy) {
			t.Fatalf("foreign live open = %v, want busy", err)
		}
		if foreignVerifier.calls != 0 {
			t.Fatalf("foreign live request hashed source %d times", foreignVerifier.calls)
		}
	})

	t.Run("unknown effects remain private", func(t *testing.T) {
		clock := &mutableClock{now: time.Now().UTC()}
		repo := openActiveInputRecoveryRepository(ctx, t)
		persistent, err := NewPersistentRepository(repo)
		if err != nil {
			t.Fatal(err)
		}
		first := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "first-coordinator")
		opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
			Input:          api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "unknown.mkv", "unknown")},
			IdempotencyKey: "open-unknown",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.RawDB().ExecContext(ctx, `INSERT INTO release_workflow_effects (
			owner_id, workflow_id, operation_id, effect_id, kind, scope_id, semantic_fingerprint, status, started_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			testOwnerID, opened.WorkflowID, "unknown-operation", "unknown-effect", "tracker_submission", "ALPHA", "unknown", "unknown",
			clock.Now().Format(time.RFC3339Nano), clock.Now().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(workflowWorkLeaseTTL + time.Second)
		foreign := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "foreign-coordinator")
		if _, err := foreign.OpenInput(ctx, foreignOwner, OpenInputRequest{
			ExpectedRevision: opened.Revision,
			Input:            api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "foreign-unknown.mkv", "foreign")},
			IdempotencyKey:   "open-foreign-unknown",
		}); !errors.Is(err, api.ErrActiveInputBusy) || errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
			t.Fatalf("foreign unknown recovery error = %v, want private busy", err)
		}
	})
}

func TestActiveInputRecoveryClearsAbandonedSwitchReceiptBeforeRetry(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repo := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	firstPath := writeActiveInputRecoverySource(t, "first.mkv", "first source")
	switchPath := writeActiveInputRecoverySource(t, "switch.mkv", "switched source")
	firstVerifier := &hashingActiveInputVerifier{}
	first := newActiveInputRecoveryModule(t, persistent, repo, firstVerifier, clock, "first-coordinator")
	opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input: api.PrepareInput{SourcePath: firstPath}, IdempotencyKey: "open-first",
	})
	if err != nil {
		t.Fatalf("open first input: %v", err)
	}

	pending := opened
	pending.Revision++
	pending.State = api.ActiveInputSwitchPending
	pending.ReservationID = "abandoned-switch"
	pending.RequestedPath = switchPath
	pending.IdempotencyKey = "switch-request"
	pending.RequestFingerprint = "abandoned-request-fingerprint"
	pending.LeaseExpiresAt = clock.Now().Add(workflowWorkLeaseTTL)
	if err := repo.CompareAndSwapActiveInput(ctx, opened, pending, clock.Now()); err != nil {
		t.Fatalf("reserve abandoned switch: %v", err)
	}

	clock.now = clock.now.Add(workflowWorkLeaseTTL + time.Second)
	secondVerifier := &hashingActiveInputVerifier{}
	second := newActiveInputRecoveryModule(t, persistent, repo, secondVerifier, clock, "second-coordinator")
	restored, err := second.recoverActiveInput(ctx, pending, testOwnerID)
	if err != nil {
		t.Fatalf("recover abandoned switch: %v", err)
	}
	if restored.State != api.ActiveInputActive || restored.InputID != opened.InputID || restored.WorkflowID != opened.WorkflowID ||
		restored.IdempotencyKey != "" || restored.RequestFingerprint != "" || restored.ReservationID != "" {
		t.Fatalf("restored abandoned switch = %#v", restored)
	}

	retried, err := second.OpenInput(ctx, testOwnerID, OpenInputRequest{
		ExpectedRevision: restored.Revision,
		Input:            api.PrepareInput{SourcePath: switchPath},
		IdempotencyKey:   "switch-request",
	})
	if err != nil {
		t.Fatalf("retry abandoned switch: %v", err)
	}
	if secondVerifier.calls != 1 || secondVerifier.lastPath != switchPath || retried.InputID == opened.InputID ||
		retried.WorkflowID == opened.WorkflowID || retried.SourceVersion != secondVerifier.lastDigest {
		t.Fatalf("retried switch = %#v hashes=%d path=%q digest=%q", retried, secondVerifier.calls, secondVerifier.lastPath, secondVerifier.lastDigest)
	}
}

func TestReleaseInputClosesIdlePreviousCoordinatorWithoutRecovery(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repo := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "first-coordinator")
	opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input:          api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "source.mkv", "source")},
		IdempotencyKey: "open-first",
	})
	if err != nil {
		t.Fatalf("open input: %v", err)
	}
	restartedVerifier := &hashingActiveInputVerifier{}
	restarted := newActiveInputRecoveryModule(t, persistent, repo, restartedVerifier, clock, "second-coordinator")
	if err := restarted.ReleaseInput(ctx, testOwnerID, opened.Revision); err != nil {
		t.Fatalf("close prior coordinator input: %v", err)
	}
	closed, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if closed.State != api.ActiveInputEmpty || closed.Fence != opened.Fence || restartedVerifier.calls != 0 {
		t.Fatalf("close prior coordinator input = %#v hashes=%d", closed, restartedVerifier.calls)
	}
}

func TestResetIdleInputOnStartupClearsOnlyForeignIdleInput(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repo := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "first-coordinator")
	opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input:          api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "source.mkv", "source")},
		IdempotencyKey: "open-first",
	})
	if err != nil {
		t.Fatalf("open input: %v", err)
	}
	if err := first.ResetIdleInputOnStartup(ctx); err != nil {
		t.Fatalf("reset same process input: %v", err)
	}
	preserved, err := repo.LoadActiveInput(ctx)
	if err != nil || preserved != opened || !first.OwnsActiveInput(preserved) {
		t.Fatalf("same process reset input = %#v, err=%v", preserved, err)
	}

	restartedVerifier := &hashingActiveInputVerifier{}
	restarted := newActiveInputRecoveryModule(t, persistent, repo, restartedVerifier, clock, "second-coordinator")
	if restarted.OwnsActiveInput(preserved) {
		t.Fatal("new process owns prior input")
	}
	if err := restarted.ResetIdleInputOnStartup(ctx); err != nil {
		t.Fatalf("reset foreign idle input: %v", err)
	}
	closed, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if closed.State != api.ActiveInputEmpty || restartedVerifier.calls != 0 {
		t.Fatalf("foreign startup reset input = %#v hashes=%d", closed, restartedVerifier.calls)
	}
}

func TestResetIdleInputOnStartupRetainsPendingCompositeWorkflow(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repo := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "first-coordinator")
	opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input:          api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "source.mkv", "source")},
		IdempotencyKey: "open-first",
	})
	if err != nil {
		t.Fatalf("open input: %v", err)
	}
	state, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
	if err != nil {
		t.Fatalf("load opened workflow: %v", err)
	}
	expectedRevision := state.Workflow.Revision
	state.Composite = &compositeUploadSession{Version: compositeUploadSessionVersion, TerminalReason: "feedback_required"}
	state.Workflow.Revision++
	state.Workflow.UpdatedAt = clock.Now()
	activeCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: opened.CoordinatorID, Fence: opened.Fence})
	if err := persistent.Save(activeCtx, testOwnerID, expectedRevision, state); err != nil {
		t.Fatalf("persist pending composite workflow: %v", err)
	}

	restarted := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "second-coordinator")
	if err := restarted.ResetIdleInputOnStartup(ctx); err != nil {
		t.Fatalf("reset pending composite workflow: %v", err)
	}
	persisted, err := repo.LoadActiveInput(ctx)
	if err != nil || persisted != opened {
		t.Fatalf("pending composite input after reset = %#v, err=%v", persisted, err)
	}
}

func TestResetIdleInputOnStartupDetachesUncertainInputForLegacyRecovery(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repo := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "first-coordinator")
	opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input:          api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "source.mkv", "source")},
		IdempotencyKey: "open-first",
	})
	if err != nil {
		t.Fatalf("open input: %v", err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, `INSERT INTO release_workflow_effects (
		owner_id, workflow_id, operation_id, effect_id, kind, scope_id, semantic_fingerprint, status, started_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		testOwnerID, opened.WorkflowID, "operation", "effect", "tracker_submission", "PTP", "unknown", "unknown",
		clock.Now().Format(time.RFC3339Nano), clock.Now().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	restartedVerifier := &hashingActiveInputVerifier{}
	restarted := newActiveInputRecoveryModule(t, persistent, repo, restartedVerifier, clock, "second-coordinator")
	if err := restarted.ResetIdleInputOnStartup(ctx); err != nil {
		t.Fatalf("detach uncertain input reset: %v", err)
	}
	persisted, err := repo.LoadActiveInput(ctx)
	if err != nil || persisted.State != api.ActiveInputEmpty || restartedVerifier.calls != 0 {
		t.Fatalf("uncertain input reset = %#v hashes=%d err=%v", persisted, restartedVerifier.calls, err)
	}
	workflowIDs, err := restarted.LegacyRecoveryWorkflowIDs(ctx, testOwnerID)
	if err != nil || len(workflowIDs) != 1 || workflowIDs[0] != opened.WorkflowID {
		t.Fatalf("uncertain input legacy recovery workflows = %#v, err=%v", workflowIDs, err)
	}
}

func openActiveInputRecoveryRepository(ctx context.Context, t *testing.T) *db.SQLiteRepository {
	t.Helper()
	repo, err := db.OpenContext(ctx, filepath.Join(t.TempDir(), "active-input-recovery.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	return repo
}

func newActiveInputRecoveryModule(
	t *testing.T,
	persistent *PersistentRepository,
	activeInputs api.ActiveInputRepository,
	verifier *hashingActiveInputVerifier,
	clock Clock,
	epoch string,
) *Module {
	t.Helper()
	module, err := New(
		persistent,
		NewMemoryPrivateResourceStore(),
		ReleasePreparerFunc{},
		WithActiveInputs(activeInputs, verifier.Verify),
		WithClock(clock),
		WithProcessEpoch(epoch),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		module.activeMu.Lock()
		cancel, done := module.activeCancel, module.activeDone
		module.activeMu.Unlock()
		if cancel != nil {
			cancel()
			<-done
		}
	})
	return module
}

type hashingActiveInputVerifier struct {
	calls      int
	lastPath   string
	lastDigest string
}

func (v *hashingActiveInputVerifier) Verify(ctx context.Context, input api.PrepareInput) (api.InputRecord, error) {
	if err := ctx.Err(); err != nil {
		return api.InputRecord{}, fmt.Errorf("verify test input: %w", err)
	}
	content, err := os.ReadFile(input.SourcePath)
	if err != nil {
		return api.InputRecord{}, fmt.Errorf("read test input: %w", err)
	}
	digest := sha256.Sum256(content)
	v.calls++
	v.lastPath = input.SourcePath
	v.lastDigest = hex.EncodeToString(digest[:])
	return api.InputRecord{
		CanonicalPath: input.SourcePath,
		SourceVersion: v.lastDigest,
		Manifest:      []byte(`{}`),
	}, nil
}

func writeActiveInputRecoverySource(t *testing.T, name, content string) string {
	t.Helper()
	pathValue := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(pathValue, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return pathValue
}
