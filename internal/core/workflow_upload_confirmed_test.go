// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowUploadExecutionAlreadySucceededSkipsClientInjection(t *testing.T) {
	t.Parallel()

	clients := &dryRunClientService{}
	execution := &workflowUploadExecution{
		plan: &workflowRetainedUploadPlanFake{results: []trackers.RetainedTrackerResult{{
			Tracker:          "ALPHA",
			AlreadySucceeded: true,
			Summary:          api.UploadSummary{Uploaded: 1},
		}}},
		clients: clients,
	}

	results, err := execution.Execute(t.Context(), nil)
	if err != nil {
		t.Fatalf("execute workflow upload: %v", err)
	}
	if len(results) != 1 || results[0].Status != api.StageStatusCompleted ||
		results[0].SubmissionStatus != api.StageStatusCompleted ||
		results[0].ClientInjectionStatus != api.StageStatusSkipped ||
		results[0].ClientInjectionMessage != "Client injection skipped because the tracker submission was already completed." ||
		results[0].ClientInjected || results[0].CrossSeeded || results[0].RemoteID != "" || results[0].RemoteURL != "" ||
		len(results[0].Failures) != 0 || len(clients.injections) != 0 || len(execution.registeredArtifacts) != 0 {
		t.Fatalf("already-succeeded execution results=%#v injections=%#v artifacts=%#v", results, clients.injections, execution.registeredArtifacts)
	}
}
