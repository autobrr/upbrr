// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCompositeHDRComparisonSelectionIsCheckpointedBeforeAnalysis(t *testing.T) {
	module, repository, _ := newCompositeUploadTestModule(t)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "composite-comparison")
	path := filepath.Join(t.TempDir(), "Synthetic.Comparison.mkv")
	if err := os.WriteFile(path, []byte("synthetic comparison"), 0o600); err != nil {
		t.Fatal(err)
	}
	request.HDRComparisonSelection = &api.HDRComparisonSelection{Path: path}
	request.HDRAnalysis = &api.HDRAnalysisRequest{Decoded: &api.HDRDecodedOptions{Comparison: &api.HDRComparisonOptions{
		FrameOffset: new(0),
		Crop:        api.HDRCrop{Width: 192, Height: 112},
		Grade:       true,
	}}}
	preparer := testPreparer()
	preparer.DisplayFunc = func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
		return api.PreparedReleaseDisplay{HDRTargets: []api.HDRAnalysisTarget{{ID: api.HDRTargetID(request.Source.Path, ""), Supported: true}}}, nil
	}
	module.preparer = preparer
	builder := &hdrBuilderFixture{unavailable: true}
	module.hdrAnalysisBuilder = builder
	inspections := 0
	module.hdrComparisonInspector = func(context.Context, string, string) (api.HDRSourceEvidence, error) {
		inspections++
		return api.HDRSourceEvidence{UniqueHEVC: true}, nil
	}
	module.inputVerifier = func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
		payload, err := json.Marshal(api.VerifiedInputSource{Identity: api.SourceContentIdentity{
			Digest: "comparison", Files: []api.VerifiedSourceFile{{LocalPath: input.SourcePath}},
		}})
		if err != nil {
			return api.InputRecord{}, fmt.Errorf("encode comparison fixture: %w", err)
		}
		return api.InputRecord{
			CanonicalPath: input.SourcePath,
			SourceVersion: "comparison",
			Manifest:      payload,
		}, nil
	}
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	current := approveCompositeUploadTrackers(t, module, blocked, []api.TrackerID{"ALPHA", "BETA"}, "approve-comparison")
	state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inspections != 1 || builder.builds != 1 || state.Composite == nil || state.Composite.ComparisonSelection != nil || state.Workflow.HDRComparison == nil {
		t.Fatalf("comparison selection did not precede analysis: inspections=%d builds=%d state=%+v", inspections, builder.builds, state.Composite)
	}
	if state.Composite.Intent.HDRAnalysis.Decoded.Comparison.SourceID != state.Workflow.HDRComparison.ID ||
		current.Release.Release.Source.SourcePath != request.Source.Path {
		t.Fatal("comparison binding was not checkpointed or replaced primary input")
	}
	// The actual initial request retains the reference in the serialized continuation,
	// rather than requiring a repeated source selection after restart.
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored State
	if err := json.Unmarshal(payload, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Composite.ComparisonSelection != nil ||
		restored.Composite.Intent.HDRAnalysis.Decoded.Comparison.SourceID != restored.Workflow.HDRComparison.ID {
		t.Fatal("serialized continuation lost its resolved comparison handle")
	}
}

func TestHDRComparisonSelectionPreservesPrimaryAndIsolatesAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "comparison.mkv")
	if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	version := "initial"
	verifies := 0
	verifier := func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
		verifies++
		payload, err := json.Marshal(api.VerifiedInputSource{Identity: api.SourceContentIdentity{
			Digest: version, Files: []api.VerifiedSourceFile{{LocalPath: input.SourcePath}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return api.InputRecord{
			CanonicalPath: input.SourcePath,
			SourceVersion: version,
			Manifest:      payload,
		}, nil
	}
	mutate := false
	module, repo := newTestModule(t, testPreparer(), WithHDRComparisonInspector(func(context.Context, string, string) (api.HDRSourceEvidence, error) {
		if mutate {
			version = "changed"
		}
		return api.HDRSourceEvidence{}, nil
	}))
	module.inputVerifier = verifier
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "comparison-selection"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "Synthetic.Primary.mkv"},
	})
	release := api.ReleaseRef{SourcePath: prepared.Release.Release.Source.SourcePath, Generation: prepared.Release.Release.Generation}
	command := SelectHDRComparisonCommand{
		WorkflowID:       prepared.Workflow.ID,
		ExpectedRevision: prepared.Workflow.Revision,
		Release:          release,
		SourcePath:       path,
		IdempotencyKey:   "select",
	}
	selected := executeCommand(t, module, command)
	if selected.HDRComparison == nil || selected.Workflow.Release == nil || *selected.Workflow.Release != *prepared.Workflow.Release || verifies != 2 {
		t.Fatalf("selection replaced primary or skipped verification: %+v verifies=%d", selected, verifies)
	}
	replayed := executeCommand(t, module, command)
	if replayed.HDRComparison.ID != selected.HDRComparison.ID || verifies != 2 {
		t.Fatal("selection replay performed source IO")
	}
	state, err := repo.Load(t.Context(), testOwnerID, selected.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := api.HDRAnalysisInstructions{
		Release: release,
		Decoded: &api.HDRDecodedOptions{Comparison: &api.HDRComparisonOptions{SourceID: selected.HDRComparison.ID}},
	}
	source, err := module.resolveHDRComparison(testOwnerID, state, request)
	if err != nil || source.Path != path {
		t.Fatalf("resolve comparison: %+v %v", source, err)
	}
	if _, err := module.resolveHDRComparison("foreign", state, request); err == nil {
		t.Fatal("foreign owner obtained comparison")
	}
	foreign := state
	foreign.Workflow.ID = "other-workflow"
	if _, err := module.resolveHDRComparison(testOwnerID, foreign, request); err == nil {
		t.Fatal("foreign workflow obtained comparison")
	}
	request.Release.Generation++
	if _, err := module.resolveHDRComparison(testOwnerID, state, request); err == nil {
		t.Fatal("other generation obtained comparison")
	}
	public, err := json.Marshal(selected)
	if err != nil || strings.Contains(string(public), "comparison.mkv") {
		t.Fatal("public source reference exposed comparison path")
	}
	root := t.TempDir()
	vault, err := NewPrivateArtifactVault(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Put(testOwnerID, state.Workflow.ID, source.Ref.ID, *source, source.Ref.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewPrivateArtifactVault(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Get(testOwnerID, state.Workflow.ID, source.Ref.ID, source.Ref.ExpiresAt.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := loaded.(HDRComparisonSource)
	if !ok || got.Ref != source.Ref || got.Path != path {
		t.Fatal("comparison authority did not survive restart")
	}
	if _, err := reopened.Get(testOwnerID, state.Workflow.ID, source.Ref.ID, source.Ref.ExpiresAt.Add(time.Second)); err == nil {
		t.Fatal("expired comparison remained accessible")
	}
	mutate = true
	command.ExpectedRevision, command.IdempotencyKey = selected.Workflow.Revision, "mutation"
	if _, err := module.Execute(t.Context(), testOwnerID, command); !errors.Is(err, api.ErrActiveInputChanged) {
		t.Fatalf("source mutation granted authority: %v", err)
	}
	unchanged, err := repo.Load(t.Context(), testOwnerID, selected.Workflow.ID)
	if err != nil || unchanged.Workflow.Revision != selected.Workflow.Revision || unchanged.Workflow.HDRComparison.ID != selected.HDRComparison.ID {
		t.Fatal("failed selection replaced retained authority")
	}
}
