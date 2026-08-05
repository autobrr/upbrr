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

	repo, err := Open(filepath.Join(t.TempDir(), "workflow-state.sqlite"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}

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

	repo, err := Open(filepath.Join(t.TempDir(), "workflow-cas.sqlite"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}

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
