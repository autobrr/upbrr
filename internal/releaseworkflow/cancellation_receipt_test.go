// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type blockedTerminalReceiptRepository struct {
	*MemoryRepository
	blocked chan struct{}
	finish  chan struct{}
}

func (r *blockedTerminalReceiptRepository) SaveOperation(ctx context.Context, sequence uint64, record api.ReleaseWorkflowOperationRecord) error {
	if isTerminalProgressStatus(record.Status.Status) {
		close(r.blocked)
		<-r.finish
	}
	return r.MemoryRepository.SaveOperation(ctx, sequence, record)
}

func TestCancellationAndPollingDoNotWaitForTerminalWriter(t *testing.T) {
	t.Parallel()
	repository := &blockedTerminalReceiptRepository{
		MemoryRepository: NewMemoryRepository(),
		blocked:          make(chan struct{}),
		finish:           make(chan struct{}),
	}
	module, err := New(repository, NewMemoryPrivateResourceStore(), testPreparer())
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	unblock := func() { once.Do(func() { close(repository.finish) }) }
	t.Cleanup(func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := module.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	created := executeCommand(t, module, CreateWorkflowCommand{})
	operation, err := module.Start(t.Context(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "synthetic-source"},
		IdempotencyKey:   "cancel-terminal-writer",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-repository.blocked:
	case <-time.After(10 * time.Second):
		t.Fatal("terminal writer did not reach its receipt")
	}
	module.operationWorkersMu.Lock()
	done := module.operationWorkers[operation.ID].done
	module.operationWorkersMu.Unlock()
	before, err := repository.LoadWork(t.Context(), testOwnerID, operation.WorkflowID, operation.ID)
	if err != nil || before.CompletedAt == nil {
		t.Fatalf("terminal checkpoint missing: %#v err=%v", before, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		acknowledgement, err := module.CancelOperation(ctx, testOwnerID, operation.WorkflowID, operation.ID)
		if err == nil && (!workflowOperationActive(acknowledgement.Status) || acknowledgement.Message != "Cancellation requested.") {
			err = errors.New("request acknowledgement falsely reported terminal completion")
		}
		if err == nil {
			var current api.WorkflowOperationStatus
			current, err = module.Operation(ctx, testOwnerID, operation.WorkflowID, operation.ID)
			if err == nil && !workflowOperationActive(current.Status) {
				err = errors.New("poll falsely reported completion before the worker stopped")
			}
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("cancellation or polling waited behind the terminal writer")
	}
	after, err := repository.LoadWork(t.Context(), testOwnerID, operation.WorkflowID, operation.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("cancellation changed the durable completion checkpoint: %#v err=%v", after, err)
	}
	unblock()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not finish")
	}
	terminal, err := module.Operation(t.Context(), testOwnerID, operation.WorkflowID, operation.ID)
	if err != nil || terminal.Status != api.StageStatusCompleted || terminal.Result == nil {
		t.Fatalf("cancellation lost the successful result: %#v err=%v", terminal, err)
	}
}
