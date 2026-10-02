// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type recoverySaveFailureRepository struct {
	Repository
	saveErr error
	loadErr error
}

func (r recoverySaveFailureRepository) Save(context.Context, string, api.WorkflowRevision, State) error {
	return r.saveErr
}

func (r recoverySaveFailureRepository) Load(context.Context, string, api.WorkflowID) (State, error) {
	return State{}, r.loadErr
}

func TestSaveRecoveredStatePreservesFailures(t *testing.T) {
	saveFailure := errors.New("synthetic recovery save failure")
	loadFailure := errors.New("synthetic recovery reload failure")
	for _, test := range []struct {
		name    string
		saveErr error
		loadErr error
		wantErr error
	}{
		{
			name:    "save failure",
			saveErr: saveFailure,
			wantErr: saveFailure,
		},
		{
			name:    "reload failure",
			saveErr: ErrRevisionConflict,
			loadErr: loadFailure,
			wantErr: loadFailure,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			module, _ := newTestModule(t, testPreparer())
			module.repository = recoverySaveFailureRepository{saveErr: test.saveErr, loadErr: test.loadErr}
			if err := module.saveRecoveredState(t.Context(), testOwnerID, 1, &State{}); !errors.Is(err, test.wantErr) {
				t.Fatalf("save recovered state error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestSaveRecoveredStatePreservesOtherProcessConflict(t *testing.T) {
	module, repository := newTestModule(t, testPreparer(), WithProcessEpoch("recovering-process"))
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "concurrent-recovery"})
	state, err := repository.Load(t.Context(), testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	expected := state.Workflow.Revision
	state.Workflow.Revision++
	state.ProcessEpoch = "other-process"
	if err := repository.Save(t.Context(), testOwnerID, expected, state); err != nil {
		t.Fatal(err)
	}
	state.ProcessEpoch = module.processEpoch
	if err := module.saveRecoveredState(t.Context(), testOwnerID, expected, &state); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("another process's update suppressed revision conflict: %v", err)
	}
}
