// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/imagehosting"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRReopenPreservesSiblingOfArtifactlessTarget(t *testing.T) {
	builder, resolver, _ := hdrFixture(t)
	source := resolver.subject.Release.SourcePath
	data, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "single-hybrid.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	failed := resolver.subject.Targets[0]
	failed.Target.ID = api.HDRTargetID(source, "failed")
	failed.CapturedFailure = &api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceLimit, Message: "synthetic capture limit"}
	resolver.subject.Targets = append(resolver.subject.Targets, failed)
	targets := []api.HDRAnalysisTarget{resolver.subject.Targets[0].Target, failed.Target}
	var generation api.PreparedGeneration
	preparer := releaseworkflow.ReleasePreparerFunc{
		PrepareFunc: func(context.Context, api.PrepareInput) (api.PrepareResult, error) {
			generation++
			return api.PrepareResult{Release: api.PreparedRelease{
				Generation: generation,
				Source:     api.SourceManifest{SourcePath: source},
				Naming:     api.NamingFacts{ReleaseName: resolver.subject.Title},
			}}, nil
		},
		DisplayFunc: func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
			return api.PreparedReleaseDisplay{HDRTargets: targets}, nil
		},
	}
	repo, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	persistent, err := releaseworkflow.NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := releaseworkflow.NewPrivateArtifactVault(t.TempDir(),
		hdrResourceCodec(builder.root, "analysis"), hdrResourceCodec(builder.root, "extraction"))
	if err != nil {
		t.Fatal(err)
	}
	module, err := releaseworkflow.New(persistent, vault, preparer,
		releaseworkflow.WithHDRAnalysisBuilder(builder), releaseworkflow.WithActiveInputs(repo, verifyWorkflowInput))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Shutdown(t.Context()) })
	input := api.PrepareInput{SourcePath: source}
	slot, err := module.OpenInput(t.Context(), "owner", releaseworkflow.OpenInputRequest{Input: input, IdempotencyKey: "open"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	prepare := func(slot api.ActiveInputRecord, key string) releaseworkflow.CommandResult {
		t.Helper()
		current, err := module.Current(ctx, "owner", slot.WorkflowID)
		if err != nil {
			t.Fatal(err)
		}
		result, err := module.Execute(ctx, "owner", releaseworkflow.PrepareReleaseCommand{
			WorkflowID:       slot.WorkflowID,
			ExpectedRevision: current.Workflow.Revision,
			Input:            input,
			IdempotencyKey:   key,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	prepared := prepare(slot, "prepare")
	analyzed, err := module.Execute(t.Context(), "owner", releaseworkflow.AnalyzeHDRCommand{
		WorkflowID:       slot.WorkflowID,
		ExpectedRevision: prepared.Workflow.Revision,
		IdempotencyKey:   "analyze",
		Instructions: api.HDRAnalysisInstructions{
			Release:        api.ReleaseRef{SourcePath: source, Generation: generation},
			TargetIDs:      []string{targets[0].ID, targets[1].ID},
			ProfileVersion: api.HDRAnalysisProfileV2,
		},
	})
	if err != nil || analyzed.HDRAnalysis == nil || analyzed.HDRAnalysis.Status != api.StageStatusPartial {
		t.Fatalf("original partial analysis: %+v %v", analyzed.HDRAnalysis, err)
	}
	original := *analyzed.HDRAnalysis
	stored, err := persistent.Load(t.Context(), "owner", slot.WorkflowID)
	if err != nil || len(stored.HDRExtractions) != 1 || len(original.Targets[0].Artifacts) == 0 ||
		len(original.Targets[1].Artifacts) != 0 || original.Targets[1].Status != api.StageStatusFailed {
		t.Fatalf("production builder did not produce checkpointless terminal sibling: %+v %+v %v", original, stored.HDRExtractions, err)
	}
	for _, record := range stored.HDRExtractions {
		if record.TargetID != targets[0].ID {
			t.Fatal("failed target unexpectedly has a checkpoint")
		}
	}
	if err := module.ReleaseInput(t.Context(), "owner", slot.Revision); err != nil {
		t.Fatal(err)
	}
	empty, err := module.ActiveInput(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := module.OpenInput(t.Context(), "owner", releaseworkflow.OpenInputRequest{
		ExpectedRevision: empty.Revision,
		Input:            input,
		IdempotencyKey:   "reopen",
	})
	if err != nil || reopened.WorkflowID == slot.WorkflowID {
		t.Fatalf("reopened workflow: %+v %v", reopened, err)
	}
	phases := make(map[string]bool)
	ctx = hdranalysis.WithProgress(t.Context(), func(progress hdranalysis.Progress) { phases[progress.Phase] = true })
	restored := prepare(reopened, "reprepare")
	if restored.HDRAnalysis == nil || len(restored.HDRAnalysis.Targets) != 2 ||
		len(restored.HDRAnalysis.Targets[0].Artifacts) != len(original.Targets[0].Artifacts) ||
		len(restored.HDRAnalysis.Targets[1].Artifacts) != 0 || restored.HDRAnalysis.Targets[1].Status != api.StageStatusFailed {
		t.Fatalf("terminal target hid saved sibling on actual reopen: %+v", restored.HDRAnalysis)
	}
	if phases["reading_metadata"] || phases["rendering"] || !reflect.DeepEqual(restored.HDRAnalysis.Targets[1].Formats, original.Targets[1].Formats) {
		t.Fatalf("reopen reran analysis or changed the terminal outcome: phases=%v result=%+v", phases, restored.HDRAnalysis)
	}
	for i, artifact := range restored.HDRAnalysis.Targets[0].Artifacts {
		content, err := module.HDRAnalysisArtifact(t.Context(), "owner", reopened.WorkflowID, *restored.Workflow.HDRAnalysis, artifact.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := content.Body.Close(); err != nil || artifact.ID == original.Targets[0].Artifacts[i].ID {
			t.Fatalf("saved sibling is not accessible through its new identity: %s %v", artifact.ID, err)
		}
	}
}

func TestHDRReopenRetainsAllArtifactsAndHostedLinks(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	for _, test := range []struct {
		name           string
		audioOnly      bool
		pngOnly        bool
		native         bool
		damaged        bool
		missing        bool
		defaults       bool
		oldAudioStatus bool
	}{
		{name: "sampled_picture_audio"},
		{
			name:      "metadata_audio",
			audioOnly: true,
			defaults:  true,
		},
		{
			name:           "historical_partial_audio",
			audioOnly:      true,
			defaults:       true,
			oldAudioStatus: true,
		},
		{name: "saved_artifacts_without_numeric_checkpoints", pngOnly: true},
		{name: "native_partial", native: true},
		{
			name:    "native_partial_artifact_only",
			native:  true,
			pngOnly: true,
		},
		{
			name:    "damaged_native_artifact_only",
			native:  true,
			pngOnly: true,
			damaged: true,
		},
		{
			name:    "missing_native_artifact_resource",
			native:  true,
			pngOnly: true,
			missing: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "Synthetic.HDR.Audio.mkv")
			executable, err := screenshots.ResolveFFmpegExecutable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), executable, "-hide_banner", "-nostdin", "-loglevel", "error",
				"-i", filepath.Join("..", "services", "hdranalysis", "testdata", "decoded-pq-cuts-25.mkv"),
				"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000:duration=4.8",
				"-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "pcm_s24le", source)
			if test.native {
				data, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "single-hybrid.mkv"))
				if err != nil {
					t.Fatal(err)
				}
				oracle, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "single-hybrid.hevc"))
				if err != nil {
					t.Fatal(err)
				}
				changed := false
				for nal := range bytes.SplitSeq(oracle, []byte{0, 0, 1}) {
					if len(nal) < 2 || (nal[0]>>1)&63 != 62 {
						continue
					}
					if bytes.Count(data, nal) != 1 {
						t.Fatal("single-picture metadata oracle differs")
					}
					// Relabel the synthetic RPU as ignored filler, preserving every
					// Matroska length and timestamp and the independent HDR10+ oracle.
					filler := bytes.Clone(nal)
					filler[0] = filler[0]&0x81 | 38<<1
					data = bytes.ReplaceAll(data, nal, filler)
					changed = true
				}
				if !changed {
					t.Fatal("synthetic RPU missing")
				}
				if err := os.WriteFile(source, data, 0o600); err != nil {
					t.Fatal(err)
				}
			} else if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("author HDR/audio source: %v %s", err, output)
			}
			facts, err := hdranalysis.InspectFileSource(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			database, vaultRoot, artifacts := filepath.Join(root, "state.sqlite"), filepath.Join(root, "vault"), filepath.Join(root, "artifacts")
			open := func() (*releaseworkflow.Module, *db.SQLiteRepository, *releaseworkflow.PrivateArtifactVault) {
				t.Helper()
				repo, err := db.Open(database)
				if err != nil {
					t.Fatal(err)
				}
				if err := repo.Migrate(); err != nil {
					t.Fatal(err)
				}
				prepared, err := preparedrelease.New(repo, workflowMediaIdentityResolver{}, hdrWorkflowCollector{evidence: facts})
				if err != nil {
					t.Fatal(err)
				}
				persistent, err := releaseworkflow.NewPersistentRepository(repo)
				if err != nil {
					t.Fatal(err)
				}
				vault, err := releaseworkflow.NewPrivateArtifactVault(vaultRoot,
					hdrResourceCodec(artifacts, "analysis"), hdrResourceCodec(artifacts, "extraction"), hdrResourceCodec(artifacts, "measurement"))
				if err != nil {
					t.Fatal(err)
				}
				module, err := releaseworkflow.New(persistent, vault, prepared,
					releaseworkflow.WithHDRAnalysisBuilder(workflowHDRAnalysisBuilder{
						uploads:  repo,
						resolver: prepared,
						service:  hdranalysis.New(hdranalysis.NewAdmission(), nil),
						root:     artifacts,
					}), releaseworkflow.WithActiveInputs(repo, verifyWorkflowInput))
				if err != nil {
					t.Fatal(err)
				}
				return module, repo, vault
			}
			module, repo, vault := open()
			t.Cleanup(func() { _ = module.Shutdown(t.Context()); _ = repo.Close() })
			input := api.PrepareInput{SourcePath: source}
			slot, err := module.OpenInput(t.Context(), "owner", releaseworkflow.OpenInputRequest{Input: input, IdempotencyKey: "open"})
			if err != nil {
				t.Fatal(err)
			}
			current, err := module.Current(t.Context(), "owner", slot.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := module.Execute(t.Context(), "owner", releaseworkflow.PrepareReleaseCommand{
				WorkflowID:       slot.WorkflowID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            input,
				IdempotencyKey:   "prepare",
			})
			if err != nil {
				t.Fatal(err)
			}
			binding, err := prepared.Release.Release.MediaBinding()
			if err != nil {
				t.Fatal(err)
			}
			var decoded *api.HDRDecodedOptions
			formats := api.HDRAnalysisFormatsAuto
			if test.native {
				formats = api.HDRAnalysisFormatsBoth
			} else {
				decoded = &api.HDRDecodedOptions{
					Automatic: !test.audioOnly,
					Audio:     test.audioOnly,
					Sampled:   !test.audioOnly,
				}
			}
			analyzed, err := module.Execute(t.Context(), "owner", releaseworkflow.AnalyzeHDRCommand{
				WorkflowID:       slot.WorkflowID,
				ExpectedRevision: prepared.Workflow.Revision,
				IdempotencyKey:   "analyze",
				Instructions: api.HDRAnalysisInstructions{
					Release:        api.ReleaseRef{SourcePath: source, Generation: prepared.Release.Release.Generation},
					TargetIDs:      []string{prepared.Release.Display.HDRTargets[0].ID},
					ProfileVersion: api.HDRAnalysisProfileV2,
					Decoded:        decoded,
					Formats:        formats,
				},
			})
			if err != nil || analyzed.HDRAnalysis == nil || !analyzed.HDRAnalysis.CanIncludeImages() {
				if analyzed.HDRAnalysis != nil {
					for _, format := range analyzed.HDRAnalysis.Targets[0].Formats {
						t.Logf("format=%s failure=%+v facts=%+v", format.Format, format.Failure, facts)
					}
				}
				t.Fatalf("original analysis: %+v %v", analyzed.HDRAnalysis, err)
			}
			original := *analyzed.HDRAnalysis
			if test.defaults && (!analyzed.Workflow.HDRAnalysisEnabled || analyzed.Workflow.HDRImages != nil) {
				t.Fatal("combined audio default was not enabled")
			}
			if test.native && original.Status != api.StageStatusPartial {
				t.Fatalf("native fixture did not produce partial HDR10+/absent DV evidence: %+v", original)
			}
			originalIDs := make(map[api.HDRArtifactKind]api.PublicResourceID)
			digests := make(map[api.HDRArtifactKind][32]byte)
			paths := make(map[api.HDRArtifactKind]string)
			cfg := config.Config{ImageHosting: config.ImageHostingConfig{Host1: "onlyimage"}}
			registry := mediaImageHostRegistry(t)
			account, err := imagehosting.AnalysisAccountScope(cfg, registry, "onlyimage", api.ScreenshotPurposeHDRAnalysis)
			if err != nil {
				t.Fatal(err)
			}
			value, err := vault.Get("owner", slot.WorkflowID, "hdr-analysis:"+original.AttemptID, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			originalResource, ok := value.(releaseworkflow.RetainedHDRAnalysisResource)
			if !ok {
				t.Fatal("original HDR artifact authority unavailable")
			}
			var selected []api.PublicResourceID
			for _, artifact := range original.Targets[0].Artifacts {
				content, err := module.HDRAnalysisArtifact(t.Context(), "owner", slot.WorkflowID, *analyzed.Workflow.HDRAnalysis, artifact.ID)
				if err != nil {
					t.Fatal(err)
				}
				data, readErr := io.ReadAll(content.Body)
				if err := content.Body.Close(); err != nil || readErr != nil {
					t.Fatalf("read original artifact: %v %v", readErr, err)
				}
				digests[artifact.Kind], originalIDs[artifact.Kind] = sha256.Sum256(data), artifact.ID
				if artifact.MIME != "image/png" {
					continue
				}
				selected = append(selected, artifact.ID)
				path, err := originalResource.LocalArtifactPath(original, artifact.ID)
				if err != nil {
					t.Fatal(err)
				}
				paths[artifact.Kind] = path
				for _, scope := range []string{"global", "tracker:ONE"} {
					if err := repo.SaveUploadedImages(t.Context(), binding, "onlyimage", []api.UploadedImageLink{{
						ImagePath:    path,
						Purpose:      api.ScreenshotPurposeHDRAnalysis,
						AccountScope: account,
						UsageScope:   scope,
						ImgURL:       "https://images.example.invalid/" + string(artifact.Kind) + "/preview.png",
						RawURL:       "https://images.example.invalid/" + string(artifact.Kind) + "/full.png",
						WebURL:       "https://images.example.invalid/" + string(artifact.Kind) + "/page",
						SizeBytes:    123,
					}}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !test.defaults {
				_, err = module.Execute(t.Context(), "owner", releaseworkflow.SetHDRAnalysisEnabledCommand{
					WorkflowID:       slot.WorkflowID,
					ExpectedRevision: analyzed.Workflow.Revision,
					Enabled:          true,
					ArtifactIDs:      selected,
					IdempotencyKey:   "select",
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if test.pngOnly {
				persistent, err := releaseworkflow.NewPersistentRepository(repo)
				if err != nil {
					t.Fatal(err)
				}
				stored, err := persistent.Load(t.Context(), "owner", slot.WorkflowID)
				if err != nil {
					t.Fatal(err)
				}
				for id := range stored.HDRExtractions {
					vault.Delete("owner", slot.WorkflowID, "hdr-extraction:"+string(id))
				}
				for id := range stored.HDRMeasurements {
					vault.Delete("owner", slot.WorkflowID, "hdr-measurement:"+id)
				}
			}
			if test.damaged {
				if err := os.WriteFile(paths[api.HDRArtifactHDR10PlusPlot], []byte("damaged"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.missing {
				vault.Delete("owner", slot.WorkflowID, "hdr-analysis:"+original.AttemptID)
			}
			if test.oldAudioStatus {
				// Earlier aggregation saved usable partial audio as Failed when both native formats were absent.
				original.Status, original.Targets[0].Status = api.StageStatusFailed, api.StageStatusFailed
				original.Targets[0].Decoded[0].State = api.HDRDecodedInconclusive
				original.Targets[0].Artifacts = slices.DeleteFunc(original.Targets[0].Artifacts, func(artifact api.HDRTypedArtifact) bool {
					return artifact.Kind == api.HDRArtifactAudioSpectrogram
				})
				original.Targets[0].Decoded[0].Artifacts = slices.DeleteFunc(original.Targets[0].Decoded[0].Artifacts, func(id api.PublicResourceID) bool {
					return id == originalIDs[api.HDRArtifactAudioSpectrogram]
				})
				delete(paths, api.HDRArtifactAudioSpectrogram)
				selected = slices.DeleteFunc(selected, func(id api.PublicResourceID) bool {
					return id == originalIDs[api.HDRArtifactAudioSpectrogram]
				})
				persistent, err := releaseworkflow.NewPersistentRepository(repo)
				if err != nil {
					t.Fatal(err)
				}
				stored, err := persistent.Load(t.Context(), "owner", slot.WorkflowID)
				if err != nil {
					t.Fatal(err)
				}
				stored.HDRAnalyses[original.ID] = original
				stored.Workflow.HDRAnalysisEnabled = false
				expected := stored.Workflow.Revision
				stored.Workflow.Revision++
				active, err := repo.LoadActiveInput(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				writeContext := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: active.CoordinatorID, Fence: active.Fence})
				if err := persistent.Save(writeContext, "owner", expected, stored); err != nil {
					t.Fatal(err)
				}
				sameGeneration, err := module.Execute(t.Context(), "owner", releaseworkflow.PrepareReleaseCommand{
					WorkflowID:       slot.WorkflowID,
					ExpectedRevision: stored.Workflow.Revision,
					Input:            input,
					IdempotencyKey:   "refresh-historical-audio",
				})
				if err != nil || sameGeneration.HDRAnalysis == nil || sameGeneration.HDRAnalysis.ID != original.ID ||
					!sameGeneration.Workflow.HDRAnalysisEnabled || sameGeneration.HDRAnalysis.Validate() != nil {
					t.Fatalf("same-generation historical audio did not retain usable artifacts: %+v %v", sameGeneration.HDRAnalysis, err)
				}
				stored, err = persistent.Load(t.Context(), "owner", slot.WorkflowID)
				if err != nil {
					t.Fatal(err)
				}
				stored.HDRAnalyses[original.ID] = original
				stored.Workflow.HDRAnalysisEnabled = false
				expected = stored.Workflow.Revision
				stored.Workflow.Revision++
				if err := persistent.Save(writeContext, "owner", expected, stored); err != nil {
					t.Fatal(err)
				}
			}
			if err := module.ReleaseInput(t.Context(), "owner", slot.Revision); err != nil {
				t.Fatal(err)
			}
			if err := module.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			module, repo, vault = open()
			empty, err := module.ActiveInput(t.Context(), "owner")
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := module.OpenInput(t.Context(), "owner", releaseworkflow.OpenInputRequest{
				ExpectedRevision: empty.Revision,
				Input:            input,
				IdempotencyKey:   "reopen",
			})
			if err != nil || reopened.WorkflowID == slot.WorkflowID {
				t.Fatalf("reopened workflow: %+v %v", reopened, err)
			}
			current, err = module.Current(t.Context(), "owner", reopened.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			phases := make(map[string]bool)
			ctx := hdranalysis.WithProgress(t.Context(), func(progress hdranalysis.Progress) { phases[progress.Phase] = true })
			input.Force = true
			restored, err := module.Execute(ctx, "owner", releaseworkflow.PrepareReleaseCommand{
				WorkflowID:       reopened.WorkflowID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            input,
				IdempotencyKey:   "reprepare",
			})
			if test.damaged || test.missing {
				failure, ok := api.AsHDRAnalysisFailure(err)
				if !ok || failure.Code != api.HDRAnalysisFailureResourceUnavailable || phases["reading_metadata"] || phases["rendering"] {
					t.Fatalf("unavailable native artifacts silently discarded or replaced: phases=%v err=%v", phases, err)
				}
				return
			}
			if err != nil || restored.HDRAnalysis == nil || !reflect.DeepEqual(restored.HDRAnalysis.Decoded, original.Decoded) ||
				len(restored.HDRAnalysis.Targets[0].Artifacts) != len(original.Targets[0].Artifacts) {
				t.Fatalf("reopen dropped retained HDR picture/audio artifacts: %+v %v", restored.HDRAnalysis, err)
			}
			if phases["reading_metadata"] || phases["reading_shared_media"] || phases["measuring_pictures"] || phases["rendering"] || phases["rendering_dolby_vision"] {
				t.Fatalf("reopen reanalyzed or rendered media: %v", phases)
			}
			if !restored.Workflow.HDRAnalysisEnabled || test.defaults && restored.Workflow.HDRImages != nil ||
				!test.defaults && (restored.Workflow.HDRImages == nil || len(restored.Workflow.HDRImages.ArtifactIDs) != len(selected)) {
				t.Fatal("reopen lost selected HDR images")
			}
			value, err = vault.Get("owner", reopened.WorkflowID, "hdr-analysis:"+restored.HDRAnalysis.AttemptID, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			restoredResource, ok := value.(releaseworkflow.RetainedHDRAnalysisResource)
			if !ok {
				t.Fatal("restored HDR artifact authority unavailable")
			}
			newBinding, err := restored.Release.Release.MediaBinding()
			if err != nil {
				t.Fatal(err)
			}
			links, err := repo.ListUploadedImagesByPath(t.Context(), newBinding)
			if err != nil || len(links) != len(selected)*2 {
				t.Fatalf("reopen lost hosted HDR links: %d %v", len(links), err)
			}
			var images []api.ScreenshotImage
			for _, artifact := range restored.HDRAnalysis.Targets[0].Artifacts {
				content, err := module.HDRAnalysisArtifact(t.Context(), "owner", reopened.WorkflowID, *restored.Workflow.HDRAnalysis, artifact.ID)
				if err != nil {
					t.Fatal(err)
				}
				data, readErr := io.ReadAll(content.Body)
				if err := content.Body.Close(); err != nil || readErr != nil || sha256.Sum256(data) != digests[artifact.Kind] || artifact.ID == originalIDs[artifact.Kind] {
					t.Fatalf("rebound artifact changed or shared prior identity: %s read=%v close=%v", artifact.Kind, readErr, err)
				}
				if artifact.MIME != "image/png" {
					continue
				}
				path, err := restoredResource.LocalArtifactPath(*restored.HDRAnalysis, artifact.ID)
				if err != nil || path == paths[artifact.Kind] {
					t.Fatalf("restored image path: %s %v", artifact.Kind, err)
				}
				if _, err := os.Stat(paths[artifact.Kind]); err != nil {
					t.Fatal("reopen deleted the prior image")
				}
				found := 0
				for _, link := range links {
					if link.ImagePath != path {
						continue
					}
					found++
					if link.AccountScope != account || link.Purpose != api.ScreenshotPurposeHDRAnalysis || link.SizeBytes != 123 ||
						link.ImgURL != "https://images.example.invalid/"+string(artifact.Kind)+"/preview.png" ||
						link.RawURL != "https://images.example.invalid/"+string(artifact.Kind)+"/full.png" ||
						link.WebURL != "https://images.example.invalid/"+string(artifact.Kind)+"/page" {
						t.Fatalf("reopen changed upload provenance: %+v", link)
					}
				}
				if found != 2 {
					t.Fatal("reopen lost a hosted usage scope")
				}
				images = append(images, api.ScreenshotImage{
					Path:    path,
					Purpose: api.ScreenshotPurposeHDRAnalysis,
					Width:   artifact.Width,
					Height:  artifact.Height,
				})
			}
			host := &descriptionAudioHostFake{}
			media := &mediaModule{
				cfg:      cfg,
				registry: registry,
				logger:   api.NopLogger{},
				repo:     repo,
				images:   host,
			}
			var exact *api.ExactMediaAssets
			if test.defaults {
				exact = &api.ExactMediaAssets{ImageHostUploadSkipped: true}
				if err := appendWorkflowHDRAssets(t.Context(), exact, *restored.HDRAnalysis, restoredResource); err != nil {
					t.Fatal(err)
				}
				if len(exact.HDRPlots) != len(images) || len(exact.HDRStats) != 1 ||
					sha256.Sum256([]byte(exact.HDRStats[0].Text)) != digests[api.HDRArtifactAudioStats] {
					t.Fatalf("restored default description images/statistics: %+v", exact)
				}
				if err := exact.Validate(); err != nil {
					t.Fatal(err)
				}
			}
			for _, scope := range []string{"global", "tracker:ONE"} {
				cached, err := media.uploadImagesToTarget(t.Context(), api.UploadSubject{SourcePath: source, MediaBinding: newBinding},
					trackers.ImageUploadTarget{Host: "onlyimage", UsageScope: scope}, images, nil, nil, false)
				if err != nil || len(cached) != len(images) || len(host.images) != 0 {
					t.Fatalf("reopen reuploaded existing analysis images: cached=%d calls=%d err=%v", len(cached), len(host.images), err)
				}
				if exact != nil {
					exact.HDRUploads, exact.HDRUploadHosts = cached, map[string]string{"ONE": "onlyimage"}
					assets, err := trackers.ResolveDescriptionAssets(t.Context(), "ONE", api.UploadSubject{
						SourcePath:   source,
						MediaBinding: newBinding,
						ExactMedia:   exact,
					}, repo, api.NopLogger{}, registry)
					if err != nil || !strings.Contains(assets.Description, "[code]"+exact.HDRStats[0].Text) ||
						strings.Count(assets.Description, "[img]") != len(images) || len(host.images) != 0 {
						t.Fatalf("restored combined description did not reuse graphs and statistics: %+v %v", assets, err)
					}
				}
			}
		})
	}
}
