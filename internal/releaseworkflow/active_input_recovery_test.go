// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
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

func TestOpenInputReverifiesSourceAfterRecoveringPreviousProcess(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repo := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "first-coordinator")
	oldSource := writeActiveInputRecoverySource(t, "Example.Release.2026-GRP.mkv", "original source")
	opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input: api.PrepareInput{SourcePath: oldSource}, IdempotencyKey: "open-original",
	})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(workflowWorkLeaseTTL + time.Second)
	verifier := &hashingActiveInputVerifier{}
	second := newActiveInputRecoveryModule(t, persistent, repo, verifier, clock, "second-coordinator")
	newSource := writeActiveInputRecoverySource(t, "Example.Release.2026-GRP.mkv", "different verified source")
	reopened, err := second.OpenInput(ctx, testOwnerID, OpenInputRequest{
		ExpectedRevision: opened.Revision,
		Input:            api.PrepareInput{SourcePath: newSource},
		IdempotencyKey:   "open-verified-new-source",
	})
	if err != nil {
		t.Fatalf("open with previous-process snapshot: %v", err)
	}
	if verifier.calls != 1 || verifier.lastPath != newSource || reopened.SourceVersion != verifier.lastDigest ||
		reopened.WorkflowID == opened.WorkflowID || reopened.CoordinatorID != "second-coordinator" {
		t.Fatalf("verified reopen = %#v, verifier = %#v", reopened, verifier)
	}
	if _, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID); err != nil {
		t.Fatalf("original workflow history: %v", err)
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
	restored, err := second.recoverActiveInput(ctx, pending, testOwnerID, false)
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

func TestStartupDiscardsInterruptedOperations(t *testing.T) {
	for _, test := range []struct {
		name           string
		running        bool
		liveWork       bool
		terminal       bool
		checkpoint     bool
		ordinaryFirst  bool
		failList       bool
		failTerminal   bool
		failCompletion bool
		failRestore    bool
		failClose      bool
		maxWait        time.Duration
		parentWait     time.Duration
		wantErr        error
	}{
		{name: "queued without work"},
		{name: "running with expired work", running: true},
		{
			name:     "running with live work lease",
			running:  true,
			liveWork: true,
		},
		{
			name:          "ordinary recovery first with live work lease",
			running:       true,
			liveWork:      true,
			ordinaryFirst: true,
		},
		{name: "retry failed operation listing after input restoration", failList: true},
		{
			name:         "retry failed interruption after work claim",
			running:      true,
			failTerminal: true,
		},
		{
			name:           "retry incomplete work after terminal interruption",
			running:        true,
			failCompletion: true,
		},
		{name: "retry failed committed input restoration", failRestore: true},
		{name: "retry failed idle input close", failClose: true},
		{
			name:     "running beyond startup recovery deadline",
			running:  true,
			liveWork: true,
			maxWait:  40 * time.Millisecond,
			wantErr:  api.ErrActiveInputBusy,
		},
		{
			name:       "caller deadline during live work lease",
			running:    true,
			liveWork:   true,
			maxWait:    time.Second,
			parentWait: 40 * time.Millisecond,
			wantErr:    context.DeadlineExceeded,
		},
		{
			name:     "terminal operation with live work lease",
			liveWork: true,
			terminal: true,
		},
		{
			name:       "completed work checkpoint",
			running:    true,
			checkpoint: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			repo := openActiveInputRecoveryRepository(ctx, t)
			persistent, err := NewPersistentRepository(repo)
			if err != nil {
				t.Fatal(err)
			}
			previous, err := repo.InitializeConfigActivationFingerprint(ctx, "previous")
			if err != nil {
				t.Fatal(err)
			}
			clock := systemClock{}
			first := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "first-coordinator")
			opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
				Input:          api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "source.mkv", "source")},
				IdempotencyKey: "open-first",
			})
			if err != nil {
				t.Fatal(err)
			}
			state, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint, err := api.CanonicalWorkflowFingerprint(map[string]string{"command": "prepare"})
			if err != nil {
				t.Fatal(err)
			}
			operationID := api.WorkflowOperationID("interrupted-operation")
			started := clock.Now().Add(-2 * time.Minute)
			operation := api.ReleaseWorkflowOperationRecord{
				OwnerID:            testOwnerID,
				WorkflowID:         opened.WorkflowID,
				OperationID:        operationID,
				ExpectedRevision:   state.Workflow.Revision,
				CommandFingerprint: fingerprint,
				ProcessEpoch:       "first-coordinator",
				Status: api.WorkflowOperationStatus{
					ID:         operationID,
					WorkflowID: opened.WorkflowID,
					Revision:   state.Workflow.Revision,
					Sequence:   1,
					Command:    "prepare",
					Status:     api.StageStatusQueued,
					StartedAt:  started,
					UpdatedAt:  started,
				},
			}
			activeCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: opened.CoordinatorID, Fence: opened.Fence})
			if _, _, err := repo.CreateReleaseWorkflowOperation(activeCtx, operation); err != nil {
				t.Fatal(err)
			}
			if test.running || test.terminal {
				operation.Status.Sequence++
				operation.Status.Status = api.StageStatusRunning
				operation.Status.UpdatedAt = started.Add(time.Second)
				if test.terminal {
					completed := started.Add(2 * time.Second)
					operation.Status.Status = api.StageStatusCompleted
					operation.Status.UpdatedAt = completed
					operation.Status.CompletedAt = &completed
				}
				if err := repo.SaveReleaseWorkflowOperation(activeCtx, 1, operation); err != nil {
					t.Fatal(err)
				}
				leaseExpiry := started.Add(time.Minute)
				if test.liveWork {
					leaseExpiry = time.Now().Add(time.Second)
					if test.maxWait > 0 {
						leaseExpiry = time.Now().Add(5 * time.Second)
					}
				}
				work := api.ReleaseWorkflowWorkRecord{
					OwnerID:        testOwnerID,
					WorkflowID:     opened.WorkflowID,
					OperationID:    operationID,
					LeaseOwner:     "first-coordinator",
					LeaseExpiresAt: leaseExpiry,
					Checkpoint:     []byte(`{}`),
					UpdatedAt:      started,
				}
				if err := repo.ClaimReleaseWorkflowWork(activeCtx, work); err != nil {
					t.Fatal(err)
				}
				if test.checkpoint {
					completed := clock.Now()
					checkpoint := operation.Status
					checkpoint.Sequence++
					checkpoint.Status = api.StageStatusCompleted
					checkpoint.UpdatedAt = completed
					checkpoint.CompletedAt = &completed
					work.Checkpoint, err = json.Marshal(checkpoint)
					if err != nil {
						t.Fatal(err)
					}
					work.UpdatedAt = completed
					work.LeaseExpiresAt = completed.Add(time.Minute)
					work.CompletedAt = &completed
					if err := repo.CompleteReleaseWorkflowWork(activeCtx, work); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := first.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			if !test.liveWork || test.terminal {
				if _, err := repo.ReconcileConfigActivation(ctx, []byte(`{}`), previous, "current", ApplyConfigImpact); err != nil {
					t.Fatal(err)
				}
			}
			recoveryRepository := &failOnceStartupRecoveryRepository{PersistentRepository: persistent}
			recoveryRepository.failList.Store(test.failList)
			recoveryRepository.failTerminal.Store(test.failTerminal)
			recoveryRepository.failCompletion.Store(test.failCompletion)
			if test.failCompletion {
				if _, err := repo.RawDB().ExecContext(ctx, `INSERT INTO release_workflow_effects (
					owner_id, workflow_id, operation_id, effect_id, kind, scope_id, semantic_fingerprint, status, started_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, testOwnerID, opened.WorkflowID, operationID, "interrupted-effect",
					"tracker_submission", "ALPHA", "synthetic", api.WorkflowEffectStatusStarted,
					clock.Now().Format(time.RFC3339Nano), clock.Now().Format(time.RFC3339Nano)); err != nil {
					t.Fatalf("start interrupted operation effect: %v", err)
				}
			}
			recoveryInputs := &failOnceStartupActiveInputRepository{ActiveInputRepository: repo}
			recoveryInputs.failRestore.Store(test.failRestore)
			recoveryInputs.failClose.Store(test.failClose)
			restarted := newActiveInputRecoveryModule(t, recoveryRepository, recoveryInputs, &hashingActiveInputVerifier{}, clock, "second-coordinator")
			if test.ordinaryFirst {
				if err := restarted.ensureOperationRecovery(ctx); err != nil {
					t.Fatalf("ordinary recovery before startup reset: %v", err)
				}
			}
			if test.failList || test.failTerminal || test.failCompletion || test.failRestore || test.failClose {
				if err := restarted.ResetIdleInputOnStartup(ctx); err == nil {
					t.Fatal("startup recovery unexpectedly succeeded before injected failure")
				}
				claimed, err := repo.LoadActiveInput(ctx)
				wantState := api.ActiveInputActive
				if test.failRestore {
					wantState = api.ActiveInputRecovering
				}
				if err != nil || claimed.State != wantState || claimed.CoordinatorID != "second-coordinator" {
					t.Fatalf("input after failed startup recovery = %#v, err=%v", claimed, err)
				}
				if test.failCompletion {
					recoveryCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{
						CoordinatorID: claimed.CoordinatorID, Fence: claimed.Fence,
					})
					if err := restarted.recoverOperationsOnce(recoveryCtx, true, 40*time.Millisecond); !errors.Is(err, api.ErrActiveInputBusy) {
						t.Fatalf("live interrupted work recovery = %v, want busy", err)
					}
					work, err := repo.LoadReleaseWorkflowWork(ctx, testOwnerID, opened.WorkflowID, operationID)
					if err != nil || work.CompletedAt != nil {
						t.Fatalf("live interrupted work changed = %#v, err=%v", work, err)
					}
				}
				if test.failTerminal || test.failCompletion {
					if _, err := repo.RawDB().ExecContext(ctx, `UPDATE release_workflow_work SET lease_expires_at = ? WHERE operation_id = ?`,
						clock.Now().Add(-time.Second).Format(time.RFC3339Nano), operationID); err != nil {
						t.Fatalf("expire failed recovery work claim: %v", err)
					}
				}
				restarted = newActiveInputRecoveryModule(t, recoveryRepository, recoveryInputs, &hashingActiveInputVerifier{}, clock,
					"second-coordinator", WithCoordinator(restarted.Coordinator))
			}
			if test.maxWait > 0 {
				recoveryCtx := ctx
				if test.parentWait > 0 {
					var cancel context.CancelFunc
					recoveryCtx, cancel = context.WithTimeout(ctx, test.parentWait)
					defer cancel()
				}
				if err := restarted.recoverOperationsOnce(recoveryCtx, true, test.maxWait); !errors.Is(err, test.wantErr) {
					t.Fatalf("bounded recovery error = %v, want %v", err, test.wantErr)
				}
				unsettled, err := repo.LoadReleaseWorkflowOperation(ctx, testOwnerID, opened.WorkflowID, operationID)
				if err != nil || unsettled.Status.Status != api.StageStatusRunning {
					t.Fatalf("operation after bounded recovery = %#v, err=%v", unsettled.Status, err)
				}
				return
			}
			if err := restarted.ResetIdleInputOnStartup(ctx); err != nil {
				t.Fatal(err)
			}
			settled, err := repo.LoadReleaseWorkflowOperation(ctx, testOwnerID, opened.WorkflowID, operationID)
			wantStatus := api.StageStatusInterrupted
			if test.terminal || test.checkpoint {
				wantStatus = api.StageStatusCompleted
			}
			if err != nil || settled.Status.Status != wantStatus {
				t.Fatalf("settled operation = %#v, err=%v", settled.Status, err)
			}
			if test.failCompletion {
				work, err := repo.LoadReleaseWorkflowWork(ctx, testOwnerID, opened.WorkflowID, operationID)
				if err != nil || work.CompletedAt == nil {
					t.Fatalf("settled interrupted work = %#v, err=%v", work, err)
				}
				var effectStatus string
				if err := repo.RawDB().QueryRowContext(ctx, `SELECT status FROM release_workflow_effects WHERE effect_id = ?`,
					"interrupted-effect").Scan(&effectStatus); err != nil || effectStatus != string(api.WorkflowEffectStatusUnknown) {
					t.Fatalf("fenced interrupted effect status=%q err=%v", effectStatus, err)
				}
			}
			slot, err := repo.LoadActiveInput(ctx)
			if err != nil || slot.State != api.ActiveInputEmpty {
				t.Fatalf("input after recovery = %#v, err=%v", slot, err)
			}
		})
	}
}

func TestStartupInterruptedCompositeCanRetry(t *testing.T) {
	ctx := t.Context()
	repo := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := repo.InitializeConfigActivationFingerprint(ctx, "previous")
	if err != nil {
		t.Fatal(err)
	}
	clock := systemClock{}
	first := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "first-coordinator")
	opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input:          api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "source.mkv", "source")},
		IdempotencyKey: "open-first",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	operationID := api.WorkflowOperationID("interrupted-composite")
	sessionFingerprint, err := api.CanonicalWorkflowFingerprint(map[string]string{"session": "test"})
	if err != nil {
		t.Fatal(err)
	}
	before := state.Workflow.Revision
	state.Workflow.Revision++
	state.Workflow.UpdatedAt = clock.Now()
	state.Composite = &compositeUploadSession{
		Version:               compositeUploadSessionVersion,
		RequestFingerprint:    sessionFingerprint,
		Goal:                  api.WorkflowGoalUploaded,
		ActiveOperationID:     operationID,
		LastOperationID:       operationID,
		LastCommittedRevision: state.Workflow.Revision,
	}
	activeCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: opened.CoordinatorID, Fence: opened.Fence})
	if err := persistent.Save(activeCtx, testOwnerID, before, state); err != nil {
		t.Fatal(err)
	}
	command := CompositeUploadCommand{
		WorkflowID:         opened.WorkflowID,
		ExpectedRevision:   state.Workflow.Revision,
		SessionFingerprint: sessionFingerprint,
		Goal:               api.WorkflowGoalUploaded,
		IdempotencyKey:     "original-composite",
	}
	commandFingerprint, err := command.commandFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	started := clock.Now().Add(-time.Minute)
	if _, _, err := repo.CreateReleaseWorkflowOperation(activeCtx, api.ReleaseWorkflowOperationRecord{
		OwnerID:            testOwnerID,
		WorkflowID:         opened.WorkflowID,
		OperationID:        operationID,
		ExpectedRevision:   state.Workflow.Revision,
		IdempotencyKey:     command.IdempotencyKey,
		CommandFingerprint: commandFingerprint,
		ProcessEpoch:       "first-coordinator",
		Status: api.WorkflowOperationStatus{
			ID:         operationID,
			WorkflowID: opened.WorkflowID,
			Revision:   state.Workflow.Revision,
			Sequence:   1,
			Command:    command.commandName(),
			Operation:  api.OperationKindUploadExecute,
			Status:     api.StageStatusQueued,
			StartedAt:  started,
			UpdatedAt:  started,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReconcileConfigActivation(ctx, []byte(`{}`), previous, "current", ApplyConfigImpact); err != nil {
		t.Fatal(err)
	}
	restarted := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "second-coordinator")
	if err := restarted.ResetIdleInputOnStartup(ctx); err != nil {
		t.Fatal(err)
	}
	interrupted, err := repo.LoadReleaseWorkflowOperation(ctx, testOwnerID, opened.WorkflowID, operationID)
	if err != nil || interrupted.Status.Status != api.StageStatusInterrupted {
		t.Fatalf("interrupted composite operation = %#v, err=%v", interrupted.Status, err)
	}
	current, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
	if err != nil || current.Composite == nil || current.Composite.ActiveOperationID != "" {
		t.Fatalf("recovered composite session = %#v, err=%v", current.Composite, err)
	}
	retry := command
	retry.ExpectedRevision = current.Workflow.Revision
	retry.IdempotencyKey = "retry-composite"
	if _, err := restarted.Start(ctx, testOwnerID, retry); err != nil {
		t.Fatalf("retry interrupted composite: %v", err)
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

func TestOpenInputVerifiesPreInputRecordHistoryWithoutClaimingLegacyWorkflow(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := New(persistent, NewMemoryPrivateResourceStore(), ReleasePreparerFunc{}, WithProcessEpoch("legacy-before-input-records"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = legacy.Shutdown(context.Background()) })
	source := writeActiveInputRecoverySource(t, "Example.Release.2026-GRP.mkv", "legacy source")
	otherSource := writeActiveInputRecoverySource(t, "Example.Release.2026-GRP.mkv", "different source with the same tmp basename")
	for _, path := range []string{source, otherSource} {
		if err := repo.Save(ctx, db.FileMetadata{
			Path:       path,
			Title:      "Example Release",
			VideoPath:  path,
			FileList:   []string{path},
			SourceSize: 123,
			UpdatedAt:  time.Now().UTC(),
		}); err != nil {
			t.Fatalf("save legacy history: %v", err)
		}
	}
	legacyHistory, err := repo.ListHistoryEntries(ctx)
	if err != nil || len(legacyHistory) != 2 {
		t.Fatalf("legacy history = %#v, err=%v", legacyHistory, err)
	}
	older, err := legacy.Execute(ctx, testOwnerID, CreateWorkflowCommand{SourcePath: source, IdempotencyKey: "legacy-history"})
	if err != nil {
		t.Fatalf("create legacy workflow: %v", err)
	}
	other, err := legacy.Execute(ctx, testOwnerID, CreateWorkflowCommand{SourcePath: otherSource, IdempotencyKey: "other-legacy-history"})
	if err != nil {
		t.Fatalf("create other legacy workflow: %v", err)
	}
	if _, err := repo.LoadInputRecord(ctx, source); !errors.Is(err, api.ErrInputRecordNotFound) {
		t.Fatalf("legacy source input record = %v, want absent", err)
	}
	verifier := &hashingActiveInputVerifier{}
	reopened := newActiveInputRecoveryModule(t, persistent, repo, verifier, &mutableClock{now: time.Now().UTC()}, "after-input-records")
	input, err := reopened.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input: api.PrepareInput{SourcePath: source}, IdempotencyKey: "verified-reopen",
	})
	if err != nil {
		t.Fatalf("reopen legacy source: %v", err)
	}
	if verifier.calls != 1 || verifier.lastPath != source || input.SourceVersion != verifier.lastDigest ||
		input.WorkflowID == older.Workflow.ID || input.WorkflowID == other.Workflow.ID || input.InputID == "" {
		t.Fatalf("legacy reopen = %#v, verifier = %#v, old workflows = %s, %s", input, verifier, older.Workflow.ID, other.Workflow.ID)
	}
	if _, err := persistent.Load(ctx, testOwnerID, older.Workflow.ID); err != nil {
		t.Fatalf("retained legacy workflow: %v", err)
	}
	if _, err := persistent.Load(ctx, testOwnerID, other.Workflow.ID); err != nil {
		t.Fatalf("retained same-basename workflow: %v", err)
	}
	if _, err := repo.LoadInputRecord(ctx, source); err != nil {
		t.Fatalf("verified input record: %v", err)
	}
	currentHistory, err := repo.ListHistoryEntries(ctx)
	if err != nil || len(currentHistory) != 2 {
		t.Fatalf("history after verified reopen = %#v, err=%v", currentHistory, err)
	}
	historyPaths := map[string]bool{}
	for _, entry := range currentHistory {
		historyPaths[entry.SourcePath] = true
	}
	if !historyPaths[source] || !historyPaths[otherSource] {
		t.Fatalf("verified reopen changed legacy history paths: %#v", currentHistory)
	}
	if err := os.Remove(otherSource); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.OpenInput(ctx, testOwnerID, OpenInputRequest{
		ExpectedRevision: input.Revision,
		Input:            api.PrepareInput{SourcePath: otherSource},
		IdempotencyKey:   "missing-legacy-source",
	}); err == nil {
		t.Fatal("reopened missing legacy source without verification")
	}
	if _, err := repo.LoadInputRecord(ctx, otherSource); !errors.Is(err, api.ErrInputRecordNotFound) {
		t.Fatalf("missing legacy source input record = %v, want absent", err)
	}
	if _, err := persistent.Load(ctx, testOwnerID, other.Workflow.ID); err != nil {
		t.Fatalf("missing-source legacy history was lost: %v", err)
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

type failOnceStartupRecoveryRepository struct {
	*PersistentRepository
	failList       atomic.Bool
	failTerminal   atomic.Bool
	failCompletion atomic.Bool
}

type failOnceStartupActiveInputRepository struct {
	api.ActiveInputRepository
	failRestore atomic.Bool
	failClose   atomic.Bool
}

func (r *failOnceStartupActiveInputRepository) CompareAndSwapActiveInput(
	ctx context.Context, expected, next api.ActiveInputRecord, now time.Time,
) error {
	if expected.State == api.ActiveInputRecovering && r.failRestore.Swap(false) {
		return errors.New("synthetic committed input restoration failure")
	}
	if err := r.ActiveInputRepository.CompareAndSwapActiveInput(ctx, expected, next, now); err != nil {
		return fmt.Errorf("persist test input transition: %w", err)
	}
	return nil
}

func (r *failOnceStartupActiveInputRepository) CloseIdleActiveInput(ctx context.Context, expected api.ActiveInputRecord, now time.Time) error {
	if r.failClose.Swap(false) {
		return errors.New("synthetic idle input close failure")
	}
	if err := r.ActiveInputRepository.CloseIdleActiveInput(ctx, expected, now); err != nil {
		return fmt.Errorf("close test input: %w", err)
	}
	return nil
}

func (r *failOnceStartupRecoveryRepository) ListActiveOperations(ctx context.Context) ([]api.ReleaseWorkflowOperationRecord, error) {
	if r.failList.Swap(false) {
		return nil, errors.New("synthetic startup operation listing failure")
	}
	return r.PersistentRepository.ListActiveOperations(ctx)
}

func (r *failOnceStartupRecoveryRepository) SaveOperation(ctx context.Context, expectedSequence uint64, record api.ReleaseWorkflowOperationRecord) error {
	if isTerminalProgressStatus(record.Status.Status) && r.failTerminal.Swap(false) {
		return errors.New("synthetic startup operation publication failure")
	}
	return r.PersistentRepository.SaveOperation(ctx, expectedSequence, record)
}

func (r *failOnceStartupRecoveryRepository) CompleteWork(ctx context.Context, record api.ReleaseWorkflowWorkRecord) error {
	if r.failCompletion.Swap(false) {
		return errors.New("synthetic interrupted work completion failure")
	}
	return r.PersistentRepository.CompleteWork(ctx, record)
}

func newActiveInputRecoveryModule(
	t *testing.T,
	persistent Repository,
	activeInputs api.ActiveInputRepository,
	verifier *hashingActiveInputVerifier,
	clock Clock,
	epoch string,
	additionalOptions ...Option,
) *Module {
	t.Helper()
	options := append([]Option{
		WithActiveInputs(activeInputs, verifier.Verify),
		WithClock(clock),
		WithProcessEpoch(epoch),
	}, additionalOptions...)
	module, err := New(
		persistent,
		NewMemoryPrivateResourceStore(),
		ReleasePreparerFunc{},
		options...,
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
