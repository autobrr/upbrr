// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/db/dbfixture"
	"github.com/autobrr/upbrr/pkg/api"
)

// missingHDRArtifactFixture retains the resource handle after its plot becomes unavailable.
type missingHDRArtifactFixture struct {
	hdrResourceFixture
}

func (missingHDRArtifactFixture) LocalArtifactPath(api.HDRAnalysisResult, api.PublicResourceID) (string, error) {
	return "", errors.New("synthetic HDR artifact is missing")
}

type hdrInputRestorerFixture struct {
	hdrBuilderFixture
	clones int
}

func (b *hdrInputRestorerFixture) CloneExtractions(
	_ context.Context,
	preparation HDRPreparation,
	entries map[api.HDRExtractionID]HDRExtractionRecord,
	resources map[api.HDRExtractionID]RetainedHDRExtractionResource,
	attempt string,
) (map[api.HDRExtractionID]HDRExtractionRecord, map[api.HDRExtractionID]RetainedHDRExtractionResource, error) {
	release := preparation.Release
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
		name               string
		reopen             bool
		disabled           bool
		metadataOnly       bool
		pngOnly            bool
		changedSource      bool
		changedCorrections bool
		missingResource    bool
		missingArtifact    bool
		native             bool
		selectDV           bool
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
		{name: "changed source while active", changedSource: true},
		{name: "changed corrections while active", changedCorrections: true},
		{name: "missing HDR resource while active", missingResource: true},
		{name: "missing HDR artifact while active", missingArtifact: true},
		{
			name:    "reopened PNG only",
			reopen:  true,
			pngOnly: true,
		},
		{name: "current PNG only", pngOnly: true},
		{name: "current disabled", disabled: true},
		{name: "current metadata only", metadataOnly: true},
		{
			name:     "native selected image reopened",
			native:   true,
			selectDV: true,
			reopen:   true,
		},
		{
			name:     "native selected image current",
			native:   true,
			selectDV: true,
		},
		{
			name:     "native disabled reopened",
			native:   true,
			disabled: true,
			reopen:   true,
		},
		{
			name:     "native disabled current",
			native:   true,
			disabled: true,
		},
		{
			name:          "changed source after reopen",
			reopen:        true,
			changedSource: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "input.sqlite")
			dbfixture.WriteMigrated(t, databasePath)
			repo, err := db.Open(databasePath)
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
			builder.dual = test.native
			reads := 0
			builder.checkpoint = func(HDRExtractionRecord) { reads++ }
			preparer := testPreparer()
			correctionsCurrent := true
			preparer.CorrectionsCurrentFunc = func(context.Context, string, uint64) (bool, error) {
				return correctionsCurrent, nil
			}
			prepare := preparer.PrepareFunc
			var generation api.PreparedGeneration
			preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				result, err := prepare(ctx, input)
				generation++
				result.Release.Generation = generation
				if err != nil {
					return result, err
				}
				return persistActiveInputTestRelease(ctx, repo, result)
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
				ProfileVersion: api.HDRAnalysisProfileVersion,
				Release:        api.ReleaseRef{SourcePath: input.SourcePath, Generation: prepared.Release.Release.Generation},
				TargetIDs:      targets,
				PeakSource:     api.HDRPeakMaxSCL,
			}
			if test.native {
				instructions.ProfileVersion = api.HDRAnalysisProfileV2
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
			if test.selectDV {
				analyzed = executeCommand(t, module, SetHDRAnalysisEnabledCommand{
					WorkflowID:       active.WorkflowID,
					ExpectedRevision: analyzed.Workflow.Revision,
					Enabled:          true,
					ArtifactIDs:      []api.PublicResourceID{analyzed.HDRAnalysis.Targets[0].DescriptionImages()[1].ID},
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
			if test.missingResource {
				module.private.Delete(testOwnerID, active.WorkflowID, hdrAnalysisPrivateResourceID(analyzed.HDRAnalysis.AttemptID))
			}
			if test.missingArtifact {
				if err := module.private.PutWithoutExpiry(testOwnerID, active.WorkflowID,
					hdrAnalysisPrivateResourceID(analyzed.HDRAnalysis.AttemptID), missingHDRArtifactFixture{}); err != nil {
					t.Fatal(err)
				}
			}
			correctionsCurrent = !test.changedCorrections
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
				switch {
				case test.changedSource:
					if stored.PendingHDRAnalysis != nil || stored.PendingHDRAnalysisWorkflowID != "" {
						t.Fatalf("changed source retained pending HDR: %#v", stored)
					}
					// Obsolete checkpoints must not restage pending HDR on another
					// verification or when the changed source is prepared.
					if len(stored.HDRExtractions) != 0 || stored.Workflow.HDRAnalysis != nil || stored.Workflow.Release != nil {
						t.Fatalf("changed source retained HDR checkpoints or current preparation after %s", key)
					}
				case !test.reopen && !test.changedCorrections && !test.missingResource && !test.missingArtifact:
					// An unchanged active input keeps its current generation and HDR
					// authority; only invalidated/reopened inputs need pending restoration.
					if stored.Workflow.Release == nil || *stored.Workflow.Release != *prepared.Workflow.Release {
						t.Fatalf("unchanged input replaced release after %s", key)
					}
					if (stored.Workflow.HDRAnalysis == nil) != (expectedPending == nil) ||
						(expectedPending != nil && *stored.Workflow.HDRAnalysis != *expectedPending) {
						t.Fatalf("current HDR authority lost after %s: got=%#v want=%#v", key, stored.Workflow.HDRAnalysis, expectedPending)
					}
					if !test.metadataOnly && stored.Workflow.HDRAnalysisEnabled != analyzed.Workflow.HDRAnalysisEnabled {
						t.Fatalf("unchanged input changed HDR selection after %s", key)
					}
					gotImages, wantImages := stored.Workflow.HDRImages, analyzed.Workflow.HDRImages
					if (gotImages == nil) != (wantImages == nil) ||
						(wantImages != nil && (!slices.Equal(gotImages.ArtifactIDs, wantImages.ArtifactIDs) || gotImages.RecoveryNote != wantImages.RecoveryNote)) {
						t.Fatalf("unchanged input changed exact HDR images after %s: got=%#v want=%#v", key, gotImages, wantImages)
					}
					if test.metadataOnly && len(stored.HDRExtractions) == 0 {
						t.Fatalf("unchanged input discarded HDR metadata after %s", key)
					}
				default:
					if stored.Workflow.Release != nil || stored.Workflow.HDRAnalysis != nil {
						t.Fatalf("invalidated input retained current release or HDR after %s", key)
					}
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
			correctionsCurrent = true
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
			if !test.metadataOnly && (builder.prior == nil || (!test.missingResource && builder.priorResource == nil) || builder.prior.ID != expectedPending.ID) {
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
				restored.Workflow.HDRAnalysisEnabled != (!test.native || !test.disabled) || reads != wantReads {
				t.Fatalf("restored analysis=%#v enabled=%v reads=%d", restored.HDRAnalysis, restored.Workflow.HDRAnalysisEnabled, reads)
			}
			if (test.missingResource || test.missingArtifact) &&
				!module.retainedHDRPlotsValid(testOwnerID, active.WorkflowID, *restored.HDRAnalysis, module.clock.Now()) {
				t.Fatal("restoration retained unavailable HDR plots")
			}
			if test.selectDV {
				images := restored.HDRAnalysis.Targets[0].DescriptionImages()
				if restored.Workflow.HDRImages == nil || !slices.Equal(restored.Workflow.HDRImages.ArtifactIDs, []api.PublicResourceID{images[1].ID}) {
					t.Fatal("rebind did not preserve exact Dolby Vision image selection")
				}
			}
		})
	}
}
