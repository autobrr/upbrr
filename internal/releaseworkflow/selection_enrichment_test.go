// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestContinueRefreshesPreparationForChangedSelectedMetadataDemand(t *testing.T) {
	t.Parallel()

	client := "example-client"
	forceRecheck := true
	preparation := api.PrepareInput{
		SourcePath: `C:\releases\Example.Release.2026.1080p-GRP`,
		Policy:     api.PreparationPolicy{KeepFolder: true},
		Search:     api.ClientSearchPolicy{Client: &client},
		Controls: api.PreparationControls{
			ConfirmBDMVRescan: true,
			ForceRecheck:      &forceRecheck,
		},
	}
	preparer := testPreparer()
	basePrepare := preparer.PrepareFunc
	var prepareCalls atomic.Int32
	var inputsMu sync.Mutex
	inputs := make([]api.PrepareInput, 0, 2)
	preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
		prepareCalls.Add(1)
		inputsMu.Lock()
		inputs = append(inputs, input)
		inputsMu.Unlock()
		return basePrepare(ctx, input)
	}
	module, _ := newTestModule(
		t,
		preparer,
		WithClock(&selectionEnrichmentClock{}),
		WithInputReadinessEvaluator(selectionDemandEvaluator{}),
	)
	current := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            preparation,
		TrackerIDs:       []api.TrackerID{"ALPHA"},
	})

	current = continueForSelectionEnrichment(t, module, current, []api.TrackerID{"ALPHA"})
	if prepareCalls.Load() != 1 || current.InputReadiness == nil {
		t.Fatalf("no-demand selection = prepares %d, readiness %#v", prepareCalls.Load(), current.InputReadiness)
	}

	current = continueForSelectionEnrichment(t, module, current, []api.TrackerID{"BETA"})
	if prepareCalls.Load() != 2 || current.Release == nil || current.InputReadiness != nil {
		t.Fatalf("demand refresh = prepares %d, release %#v, readiness %#v", prepareCalls.Load(), current.Release, current.InputReadiness)
	}
	inputsMu.Lock()
	refreshedInput := inputs[1]
	inputsMu.Unlock()
	if !refreshedInput.Policy.KeepFolder || refreshedInput.Search.Client == nil || *refreshedInput.Search.Client != client ||
		!refreshedInput.Controls.ConfirmBDMVRescan || refreshedInput.Controls.ForceRecheck == nil || !*refreshedInput.Controls.ForceRecheck {
		t.Fatalf("refreshed input did not retain accepted policy, search, and controls: %#v", refreshedInput)
	}

	current = continueForSelectionEnrichment(t, module, current, []api.TrackerID{"BETA"})
	if prepareCalls.Load() != 2 || current.InputReadiness == nil {
		t.Fatalf("refreshed readiness = prepares %d, readiness %#v", prepareCalls.Load(), current.InputReadiness)
	}

	current = continueForSelectionEnrichment(t, module, current, []api.TrackerID{"GAMMA"})
	if prepareCalls.Load() != 2 || current.InputReadiness == nil ||
		len(current.InputReadiness.SelectedTrackerIDs) != 1 || current.InputReadiness.SelectedTrackerIDs[0] != "GAMMA" {
		t.Fatalf("same demand union = prepares %d, readiness %#v", prepareCalls.Load(), current.InputReadiness)
	}
}

func continueForSelectionEnrichment(
	t *testing.T,
	module *Module,
	current CommandResult,
	trackerIDs []api.TrackerID,
) CommandResult {
	t.Helper()
	updated, err := module.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{
		Authority: &api.WorkflowAuthority{
			WorkflowID:       current.Workflow.ID,
			ExpectedRevision: current.Workflow.Revision,
		},
		IdempotencyKey: "selection-enrichment",
		Goal:           api.WorkflowGoalInputReady,
		Intent: api.WorkflowIntent{
			TrackerIDs: trackerIDs,
		},
	})
	if err != nil {
		t.Fatalf("continue selected trackers %v: %v", trackerIDs, err)
	}
	if updated.Operation != nil && !isTerminalProgressStatus(updated.Operation.Status) {
		waitForWorkflowOperation(t, module, updated.Workflow.ID, updated.Operation.ID, func(status api.WorkflowOperationStatus) bool {
			return isTerminalProgressStatus(status.Status)
		})
	}
	updated, err = module.Current(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatalf("load selected tracker continuation: %v", err)
	}
	return updated
}

type selectionDemandEvaluator struct{}

type selectionEnrichmentClock struct{ ticks atomic.Int64 }

func (c *selectionEnrichmentClock) Now() time.Time {
	return time.Unix(c.ticks.Add(1), 0).UTC()
}

func (selectionDemandEvaluator) Requirements(
	_ context.Context,
	trackerIDs []api.TrackerID,
) (api.MetadataRequirementSet, error) {
	if trackerIDs[0] != "BETA" && trackerIDs[0] != "GAMMA" {
		return api.MetadataRequirementSet{Version: "selection-demand-v1"}, nil
	}
	return api.MetadataRequirementSet{
		Version: "selection-demand-v1",
		Requirements: []api.MetadataRequirement{{
			Scope:       api.MetadataRequirementScopeAny,
			AnyOf:       []api.MetadataRequirementField{"original_title"},
			Disposition: api.RuleDispositionStrict,
		}},
	}, nil
}

func (selectionDemandEvaluator) Evaluate(
	_ context.Context,
	_ api.UploadSubject,
	trackerIDs []api.TrackerID,
) (api.InputReadinessEvaluation, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(trackerIDs)
	if err != nil {
		return api.InputReadinessEvaluation{}, fmt.Errorf("fingerprint selection requirements: %w", err)
	}
	return api.InputReadinessEvaluation{
		RequirementsFingerprint: fingerprint,
		Fields: []api.InputReadinessFieldOutcome{{
			Key:         "source",
			Status:      api.InputReadinessFieldReady,
			Disposition: api.RuleDispositionStrict,
		}},
	}, nil
}
