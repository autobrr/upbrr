// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRProductionAuthoritySurvivesRestartWithoutMedia(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "Synthetic.Hybrid.mkv")
	data, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "reordered-hybrid.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	database, vaultRoot, artifacts := filepath.Join(root, "state.sqlite"), filepath.Join(root, "vault"), filepath.Join(root, "artifacts")
	open := func() (*Core, *db.SQLiteRepository) {
		t.Helper()
		repo, err := db.Open(database)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Migrate(); err != nil {
			t.Fatal(err)
		}
		prepared, err := preparedrelease.New(repo, workflowMediaIdentityResolver{}, hdrWorkflowCollector{evidence: api.HDRSourceEvidence{
			UniqueHEVC:  true,
			HDR10Plus:   true,
			DolbyVision: true,
		}})
		if err != nil {
			t.Fatal(err)
		}
		builder := workflowHDRAnalysisBuilder{
			resolver: prepared,
			service:  hdranalysis.New(hdranalysis.NewAdmission(), nil),
			root:     artifacts,
		}
		persistent, err := releaseworkflow.NewPersistentRepository(repo)
		if err != nil {
			t.Fatal(err)
		}
		vault, err := releaseworkflow.NewPrivateArtifactVault(vaultRoot, hdrResourceCodec(artifacts, "analysis"), hdrResourceCodec(artifacts, "extraction"))
		if err != nil {
			t.Fatal(err)
		}
		workflow, err := releaseworkflow.New(persistent, vault, prepared,
			releaseworkflow.WithHDRAnalysisBuilder(builder), releaseworkflow.WithActiveInputs(repo, verifyWorkflowInput))
		if err != nil {
			t.Fatal(err)
		}
		if err := workflow.ResetIdleInputOnStartup(t.Context()); err != nil {
			t.Fatal(err)
		}
		return &Core{
			workflow:      workflow,
			preparedFacts: prepared,
			logger:        api.NopLogger{},
		}, repo
	}
	first, repo := open()
	input := api.PrepareInput{SourcePath: source}
	slot, err := first.workflow.OpenInput(t.Context(), "cli", releaseworkflow.OpenInputRequest{Input: input, IdempotencyKey: "open"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := first.workflow.Current(t.Context(), "cli", slot.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := first.workflow.Execute(t.Context(), "cli", releaseworkflow.PrepareReleaseCommand{
		WorkflowID:       slot.WorkflowID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            input,
		IdempotencyKey:   "prepare",
	})
	if err != nil {
		t.Fatal(err)
	}
	instructions := api.HDRAnalysisInstructions{
		Release:        api.ReleaseRef{SourcePath: source, Generation: prepared.Release.Release.Generation},
		TargetIDs:      []string{prepared.Release.Display.HDRTargets[0].ID},
		ProfileVersion: api.HDRAnalysisProfileV2,
	}
	analyzed, err := first.workflow.Execute(t.Context(), "cli", releaseworkflow.AnalyzeHDRCommand{
		WorkflowID:       slot.WorkflowID,
		ExpectedRevision: prepared.Workflow.Revision,
		Instructions:     instructions,
		IdempotencyKey:   "analyze",
	})
	if err != nil || analyzed.HDRAnalysis == nil || analyzed.HDRAnalysis.Status != api.StageStatusCompleted {
		t.Fatalf("initial analysis: %+v %v", analyzed, err)
	}
	if err := first.workflow.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	parkedSource := source + ".unavailable"
	if err := os.Rename(source, parkedSource); err != nil {
		t.Fatal(err)
	}
	second, repo := open()
	t.Cleanup(func() { _ = second.workflow.Shutdown(t.Context()); _ = repo.Close() })
	active, err := second.GetActiveInput(t.Context(), "cli")
	if err != nil || active.State != api.ActiveInputEmpty || active.Current != nil {
		t.Fatalf("restart restored the missing input: %+v %v", active, err)
	}
	retained, err := second.workflow.Current(t.Context(), "cli", slot.WorkflowID)
	if err != nil || retained.HDRAnalysis == nil {
		t.Fatalf("retained CLI workflow: %+v %v", retained, err)
	}
	imageID := retained.HDRAnalysis.Targets[0].DescriptionImages()[0].ID
	content, err := second.workflow.HDRAnalysisArtifact(t.Context(), "cli", slot.WorkflowID, *retained.Workflow.HDRAnalysis, imageID)
	if err != nil {
		t.Fatal(err)
	}
	if err := content.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.workflow.HDRAnalysisArtifact(t.Context(), "foreign", slot.WorkflowID, *retained.Workflow.HDRAnalysis, imageID); err == nil {
		t.Fatal("foreign owner read retained image")
	}
	if _, err := second.workflow.OpenInput(t.Context(), "cli", releaseworkflow.OpenInputRequest{
		ExpectedRevision: active.Revision,
		Input:            input,
		IdempotencyKey:   "missing-reopen",
	}); err == nil {
		t.Fatal("explicit reopen accepted missing media")
	}
	if err := os.Rename(parkedSource, source); err != nil {
		t.Fatal(err)
	}
	active, err = second.GetActiveInput(t.Context(), "cli")
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := second.workflow.OpenInput(t.Context(), "cli", releaseworkflow.OpenInputRequest{
		ExpectedRevision: active.Revision,
		Input:            input,
		IdempotencyKey:   "reopen-after-restart",
	})
	if err != nil || reopened.WorkflowID == slot.WorkflowID {
		t.Fatalf("explicit reopen did not establish a fresh workflow: %+v %v", reopened, err)
	}
	retained, err = second.workflow.Current(t.Context(), "cli", reopened.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	retained, err = second.workflow.Execute(t.Context(), "cli", releaseworkflow.PrepareReleaseCommand{
		WorkflowID:       reopened.WorkflowID,
		ExpectedRevision: retained.Workflow.Revision,
		Input:            input,
		IdempotencyKey:   "prepare-reopened",
	})
	if err != nil || retained.HDRAnalysis == nil {
		t.Fatalf("explicit preparation lost reusable HDR artifacts: %+v %v", retained, err)
	}
	imageID = retained.HDRAnalysis.Targets[0].DescriptionImages()[0].ID
	instructions.Release = retained.HDRAnalysis.Release
	// After explicit verification, cached rendering still does not need the media.
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	selected, err := second.workflow.Execute(t.Context(), "cli", releaseworkflow.SetHDRAnalysisEnabledCommand{
		WorkflowID:       reopened.WorkflowID,
		ExpectedRevision: retained.Workflow.Revision,
		Enabled:          true,
		ArtifactIDs:      []api.PublicResourceID{imageID},
		IdempotencyKey:   "select",
	})
	if err != nil {
		t.Fatal(err)
	}
	// A different peak invalidates the PNG cache, requiring retained metadata to render again.
	instructions.PeakSource = api.HDRPeakMaxSCLLuminance
	rerendered, err := second.workflow.Execute(t.Context(), "cli", releaseworkflow.AnalyzeHDRCommand{
		WorkflowID:       reopened.WorkflowID,
		ExpectedRevision: selected.Workflow.Revision,
		Instructions:     instructions,
		IdempotencyKey:   "rerender",
	})
	if err != nil || rerendered.HDRAnalysis == nil || rerendered.HDRAnalysis.Status != api.StageStatusCompleted {
		t.Fatalf("retained render: %+v %v", rerendered, err)
	}
	if err := second.preparedFacts.ValidateHDRAnalysisSource(t.Context(), instructions.Release); err == nil {
		t.Fatal("new source work accepted missing media")
	}
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := second.preparedFacts.Prepare(t.Context(), api.PrepareInput{SourcePath: source, Force: true}); err != nil {
		t.Fatal(err)
	}
	imageID = rerendered.HDRAnalysis.Targets[0].DescriptionImages()[0].ID
	if _, err := second.workflow.HDRAnalysisArtifact(t.Context(), "cli", reopened.WorkflowID, *rerendered.Workflow.HDRAnalysis, imageID); err == nil {
		t.Fatal("superseded generation retained image authority")
	}
}
