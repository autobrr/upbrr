// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestNativeHDRDescriptionSelectionRetainsExactImages(t *testing.T) {
	builder := &hdrBuilderFixture{dual: true}
	module, repository := newTestModule(t, testPreparer(), WithHDRAnalysisBuilder(builder))
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "native-selection"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "Synthetic.HDR.mkv"},
	})
	target := api.HDRTargetID("synthetic", "")
	current := executeCommand(t, module, AnalyzeHDRCommand{
		WorkflowID:       prepared.Workflow.ID,
		ExpectedRevision: prepared.Workflow.Revision,
		Instructions:     api.HDRAnalysisInstructions{Release: api.ReleaseRef{SourcePath: "Synthetic.HDR.mkv", Generation: 1}, TargetIDs: []string{target}},
	})
	images := current.HDRAnalysis.Targets[0].DescriptionImages()
	if len(images) != 2 {
		t.Fatalf("images=%v", images)
	}
	selected := images[1].ID
	current = executeCommand(t, module, SetHDRAnalysisEnabledCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Enabled:          true,
		ArtifactIDs:      []api.PublicResourceID{selected},
	})
	state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Workflow.HDRImages == nil || !slices.Equal(state.Workflow.HDRImages.ArtifactIDs, []api.PublicResourceID{selected}) {
		t.Fatal("selection was not persisted")
	}
	filtered, _, err := module.resolveHDRDescriptionResources(t.Context(), testOwnerID, &state, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := filtered.Targets[0].DescriptionImages(); len(got) != 1 || got[0].ID != selected {
		t.Fatalf("description selection=%v", got)
	}
	if len(state.HDRAnalyses[state.Workflow.HDRAnalysis.ID].Targets[0].DescriptionImages()) != 2 || builder.builds != 1 {
		t.Fatal("selection mutated or rebuilt analysis")
	}
	for _, ids := range [][]api.PublicResourceID{{"report"}, {selected, selected}, {"another-attempt"}} {
		if err := module.setHDRAnalysisEnabled(t.Context(), &state, SetHDRAnalysisEnabledCommand{Enabled: true, ArtifactIDs: ids}); err == nil {
			t.Fatalf("accepted invalid image selection %v", ids)
		}
	}
	if err := module.setHDRAnalysisEnabled(t.Context(), &state, SetHDRAnalysisEnabledCommand{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if state.Workflow.HDRImages == nil || len(state.Workflow.HDRImages.ArtifactIDs) != 0 || state.Workflow.HDRAnalysisEnabled {
		t.Fatal("explicit empty selection was lost")
	}
}
