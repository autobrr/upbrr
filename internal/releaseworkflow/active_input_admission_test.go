// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"errors"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestActiveInputMutationAdmissionRecoversExpiredSlotWithoutRehashing(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repository := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repository)
	if err != nil {
		t.Fatal(err)
	}
	firstVerifier := &hashingActiveInputVerifier{}
	first := newActiveInputRecoveryModule(t, persistent, repository, firstVerifier, clock, "first-coordinator")
	opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input:          api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "source.mkv", "source")},
		IdempotencyKey: "open-first",
	})
	if err != nil {
		t.Fatalf("open first input: %v", err)
	}
	clock.now = clock.now.Add(workflowWorkLeaseTTL + time.Second)
	secondVerifier := &hashingActiveInputVerifier{}
	second := newActiveInputRecoveryModule(t, persistent, repository, secondVerifier, clock, "second-coordinator")
	stale := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{
		CoordinatorID: opened.CoordinatorID,
		Fence:         opened.Fence,
	})
	command := CancelWorkflowCommand{
		WorkflowID:       opened.WorkflowID,
		ExpectedRevision: 1,
		Reason:           "operator cancellation",
		IdempotencyKey:   "cancel-after-restart",
	}
	if _, err := second.Execute(stale, testOwnerID, command); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("stale mutation admission = %v, want %v", err, api.ErrActiveInputLeaseLost)
	}
	before, err := repository.LoadActiveInput(ctx)
	if err != nil {
		t.Fatalf("load slot after stale admission: %v", err)
	}
	if before.CoordinatorID != opened.CoordinatorID || before.Fence != opened.Fence || secondVerifier.calls != 0 {
		t.Fatalf("stale admission changed slot=%#v hashes=%d", before, secondVerifier.calls)
	}

	result, err := second.Execute(ctx, testOwnerID, command)
	if err != nil {
		t.Fatalf("fresh mutation admission: %v", err)
	}
	if result.Workflow.ID != opened.WorkflowID || result.Workflow.Status != api.WorkflowStatusCanceled || secondVerifier.calls != 0 {
		t.Fatalf("recovered mutation result=%#v hashes=%d", result.Workflow, secondVerifier.calls)
	}
	recovered, err := repository.LoadActiveInput(ctx)
	if err != nil {
		t.Fatalf("load recovered slot: %v", err)
	}
	if recovered.WorkflowID != opened.WorkflowID || recovered.InputID != opened.InputID || recovered.CoordinatorID != "second-coordinator" ||
		recovered.Fence <= opened.Fence || recovered.State != api.ActiveInputActive {
		t.Fatalf("recovered slot = %#v", recovered)
	}
}

func TestWorkflowShutdownRelinquishesActiveInputLeaseAfterCleanup(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repository := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repository)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &hashingActiveInputVerifier{}
	module := newActiveInputRecoveryModule(t, persistent, repository, verifier, clock, "shutting-down")
	opened, err := module.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input:          api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "source.mkv", "source")},
		IdempotencyKey: "open-first",
	})
	if err != nil {
		t.Fatalf("open input: %v", err)
	}
	if err := module.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown workflow module: %v", err)
	}
	relinquished, err := repository.LoadActiveInput(ctx)
	if err != nil {
		t.Fatalf("load relinquished slot: %v", err)
	}
	if relinquished.WorkflowID != opened.WorkflowID || relinquished.InputID != opened.InputID ||
		relinquished.CoordinatorID != opened.CoordinatorID || relinquished.Fence != opened.Fence ||
		relinquished.LeaseExpiresAt.After(clock.Now()) {
		t.Fatalf("relinquished slot = %#v", relinquished)
	}
}
