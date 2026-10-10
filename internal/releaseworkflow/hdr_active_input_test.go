// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type hdrInputRestorerFixture struct {
	hdrBuilderFixture
	clones int
}

func (b *hdrInputRestorerFixture) CloneExtractions(
	_ context.Context,
	release api.ReleaseRef,
	entries map[api.HDRExtractionID]HDRExtractionRecord,
	resources map[api.HDRExtractionID]RetainedHDRExtractionResource,
	attempt string,
) (map[api.HDRExtractionID]HDRExtractionRecord, map[api.HDRExtractionID]RetainedHDRExtractionResource, error) {
	b.clones++
	cloned := make(map[api.HDRExtractionID]HDRExtractionRecord)
	retained := make(map[api.HDRExtractionID]RetainedHDRExtractionResource)
	for id, entry := range entries {
		if resources[id] == nil {
			continue
		}
		entry.ID, entry.Release = api.HDRExtractionID(attempt+"-"+entry.TargetID), release
		cloned[entry.ID], retained[entry.ID] = entry, resources[id]
	}
	return cloned, retained, nil
}

func TestRepeatedInputVerificationPreservesPendingHDR(t *testing.T) {
	for _, test := range []struct {
		name          string
		reopen        bool
		disabled      bool
		metadataOnly  bool
		pngOnly       bool
		changedSource bool
	}{
		{name: "reopened enabled", reopen: true},
		{
			name:     "reopened disabled",
			reopen:   true,
			disabled: true,
		},
		{
			name:         "reopened metadata only",
			reopen:       true,
			metadataOnly: true,
		},
		{name: "current enabled"},
		{
			name:    "reopened PNG only",
			reopen:  true,
			pngOnly: true,
		},
		{name: "current PNG only", pngOnly: true},
		{name: "current disabled", disabled: true},
		{name: "current metadata only", metadataOnly: true},
		{
			name:          "changed source after reopen",
			reopen:        true,
			changedSource: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, err := db.Open(filepath.Join(t.TempDir(), "input.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			persistent, err := NewPersistentRepository(repo)
			if err != nil {
				t.Fatal(err)
			}
			version := "verified-a"
			builder := &hdrInputRestorerFixture{failRender: test.metadataOnly, pngOnly: test.pngOnly}
			reads := 0
			builder.checkpoint = func(HDRExtractionRecord) { reads++ }
			preparer := testPreparer()
			prepare := preparer.PrepareFunc
			var generation api.PreparedGeneration
			preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				result, err := prepare(ctx, input)
				generation++
				result.Release.Generation = generation
				return result, err
			}
			module, err := New(persistent, NewMemoryPrivateResourceStore(), preparer,
				WithHDRAnalysisBuilder(builder),
				WithActiveInputs(repo, func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
					return api.InputRecord{
						CanonicalPath: input.SourcePath,
						SourceVersion: version,
						Manifest:      []byte(`{"Identity":{"Digest":"` + version + `"}}`),
					}, nil
				}))
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
			input := api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Synthetic.HDR.2026.mkv")}
			active, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{Input: input, IdempotencyKey: "open-first"})
			if err != nil {
				t.Fatal(err)
			}
			originalWorkflow := active.WorkflowID
			current, err := module.Current(t.Context(), testOwnerID, active.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			prepared := executeCommand(t, module, PrepareReleaseCommand{
				WorkflowID:       active.WorkflowID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            input,
				IdempotencyKey:   "prepare-first",
			})
			targets := []string{api.HDRTargetID(input.SourcePath, "")}
			instructions := api.HDRAnalysisInstructions{
				Release:    api.ReleaseRef{SourcePath: input.SourcePath, Generation: prepared.Release.Release.Generation},
				TargetIDs:  targets,
				PeakSource: api.HDRPeakMaxSCL,
			}
			analyzed, err := module.Execute(t.Context(), testOwnerID, AnalyzeHDRCommand{
				WorkflowID:       active.WorkflowID,
				ExpectedRevision: prepared.Workflow.Revision,
				Instructions:     instructions,
				IdempotencyKey:   "analyze-first",
			})
			if (err != nil) != test.metadataOnly || reads != 1 {
				t.Fatalf("initial analysis reads=%d err=%v", reads, err)
			}
			if test.disabled {
				analyzed = executeCommand(t, module, SetHDRAnalysisEnabledCommand{
					WorkflowID:       active.WorkflowID,
					ExpectedRevision: analyzed.Workflow.Revision,
					Enabled:          false,
				})
			}
			var expectedPending *api.HDRAnalysisRef
			if !test.metadataOnly {
				expectedPending = analyzed.Workflow.HDRAnalysis
			}
			if test.reopen {
				if err := module.ReleaseInput(t.Context(), testOwnerID, active.Revision); err != nil {
					t.Fatal(err)
				}
				active, err = module.ActiveInput(t.Context(), testOwnerID)
				if err != nil {
					t.Fatal(err)
				}
				active, err = module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
					ExpectedRevision: active.Revision,
					Input:            input,
					IdempotencyKey:   "open-again",
				})
				if err != nil || active.WorkflowID == originalWorkflow {
					t.Fatalf("reopened input=%#v err=%v", active, err)
				}
			}
			if test.changedSource {
				version = "verified-b"
			}
			for _, key := range []string{"reverify-first", "reverify-second"} {
				request := OpenInputRequest{
					ExpectedRevision: active.Revision,
					Input:            input,
					IdempotencyKey:   key,
				}
				active, err = module.OpenInput(t.Context(), testOwnerID, request)
				if err != nil {
					t.Fatal(err)
				}
				replay, err := module.OpenInput(t.Context(), testOwnerID, request)
				if err != nil || replay.Revision != active.Revision || replay.WorkflowID != active.WorkflowID {
					t.Fatalf("reverification replay=%#v err=%v", replay, err)
				}
				stored, err := persistent.Load(t.Context(), testOwnerID, active.WorkflowID)
				if err != nil {
					t.Fatal(err)
				}
				if test.changedSource {
					if stored.PendingHDRAnalysis != nil || stored.PendingHDRAnalysisWorkflowID != "" {
						t.Fatalf("changed source retained pending HDR: %#v", stored)
					}
				} else {
					if stored.PendingHDRAnalysisWorkflowID != originalWorkflow {
						t.Fatalf(
							"pending authority lost after %s: workflow=%s",
							key,
							stored.PendingHDRAnalysisWorkflowID,
						)
					}
					if (stored.PendingHDRAnalysis == nil) != (expectedPending == nil) ||
						(expectedPending != nil && *stored.PendingHDRAnalysis != *expectedPending) {
						t.Fatalf("pending result lost after %s: got=%#v want=%#v", key, stored.PendingHDRAnalysis, expectedPending)
					}
				}
			}
			builder.failRender = false
			current, err = module.Current(t.Context(), testOwnerID, active.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			restored := executeCommand(t, module, PrepareReleaseCommand{
				WorkflowID:       active.WorkflowID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            input,
				IdempotencyKey:   "prepare-again",
			})
			stored, err := persistent.Load(t.Context(), testOwnerID, active.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			if test.changedSource {
				if builder.clones != 0 || restored.HDRAnalysis != nil || len(stored.HDRExtractions) != 0 {
					t.Fatalf("changed source reused old HDR: clones=%d state=%#v", builder.clones, stored)
				}
				return
			}
			wantReads := 1
			wantEntries := 1
			if test.pngOnly {
				// The synthetic builder republishes a metadata fixture; the native builder's
				// PNG-only reuse and zero source reads are exercised in core's recovery test.
				wantReads++
				wantEntries = 0
			}
			if builder.clones != 1 || len(stored.HDRExtractions) != wantEntries || reads != wantReads {
				t.Fatalf("retained metadata not reused: clones=%d entries=%d reads=%d", builder.clones, len(stored.HDRExtractions), reads)
			}
			if !test.metadataOnly && (builder.prior == nil || builder.priorResource == nil || builder.prior.ID != expectedPending.ID) {
				t.Fatal("restoration discarded the prior PNG authority")
			}
			for _, entry := range stored.HDRExtractions {
				if entry.Release.Generation != restored.Release.Release.Generation || entry.Release.Generation <= instructions.Release.Generation {
					t.Fatalf("metadata not rebound: %#v", entry)
				}
			}
			if test.metadataOnly {
				if restored.HDRAnalysis != nil || restored.Workflow.HDRAnalysisEnabled {
					t.Fatal("metadata-only recovery exposed a plot")
				}
				instructions.Release.Generation = restored.Release.Release.Generation
				restored = executeCommand(t, module, AnalyzeHDRCommand{
					WorkflowID:       active.WorkflowID,
					ExpectedRevision: restored.Workflow.Revision,
					Instructions:     instructions,
					IdempotencyKey:   "analyze-recovered",
				})
			}
			if restored.HDRAnalysis == nil || restored.HDRAnalysis.WorkflowID != active.WorkflowID ||
				!slices.Equal(restored.HDRAnalysis.TargetIDs, targets) || restored.HDRAnalysis.PeakSource != api.HDRPeakMaxSCL ||
				!restored.Workflow.HDRAnalysisEnabled || reads != wantReads {
				t.Fatalf("restored analysis=%#v enabled=%v reads=%d", restored.HDRAnalysis, restored.Workflow.HDRAnalysisEnabled, reads)
			}
		})
	}
}
