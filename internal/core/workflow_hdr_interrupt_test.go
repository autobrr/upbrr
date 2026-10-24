// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/db/dbfixture"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

type hdrRestartClock struct{ offset atomic.Int64 }

func (c *hdrRestartClock) Now() time.Time {
	return time.Now().UTC().Add(time.Duration(c.offset.Load()))
}

type hdrCrashBuilder struct {
	workflowHDRAnalysisBuilder
	phase string
}

func (b *hdrCrashBuilder) Build(
	ctx context.Context, preparation releaseworkflow.HDRPreparation, instructions api.HDRAnalysisInstructions, attempt string, now time.Time,
	prior *api.HDRAnalysisResult, priorResource releaseworkflow.RetainedHDRAnalysisResource,
	entries map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord, resources map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource,
	publish func(releaseworkflow.HDRExtractionRecord, releaseworkflow.RetainedHDRExtractionResource) error, store *releaseworkflow.HDRMeasurementStore,
) (api.HDRAnalysisResult, releaseworkflow.RetainedHDRAnalysisResource, error) {
	checkpoint := func(record releaseworkflow.HDRExtractionRecord, resource releaseworkflow.RetainedHDRExtractionResource) error {
		if err := publish(record, resource); err != nil {
			return err
		}
		if b.phase == "native" {
			os.Exit(0) // Deliberately skip cancellation, terminal publication and shutdown.
		}
		return nil
	}
	if store != nil && b.phase != "" && b.phase != "native" {
		publishMeasurement := store.Publish
		store.Publish = func(record releaseworkflow.HDRMeasurementRecord, resource releaseworkflow.RetainedHDRMeasurementResource) error {
			if err := publishMeasurement(record, resource); err != nil {
				return err
			}
			os.Exit(0)
			return nil
		}
	}
	return b.workflowHDRAnalysisBuilder.Build(ctx, preparation, instructions, attempt, now, prior, priorResource, entries, resources, checkpoint, store)
}

type hdrRestartFixture struct {
	core       *Core
	repository *db.SQLiteRepository
	persistent *releaseworkflow.PersistentRepository
	vault      *releaseworkflow.PrivateArtifactVault
	builder    *hdrCrashBuilder
	clock      *hdrRestartClock
	source     string
	comparison string
}

func openHDRRestartFixture(t *testing.T, root string, decoded bool) *hdrRestartFixture {
	t.Helper()
	repo, err := db.Open(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	evidence := api.HDRSourceEvidence{
		UniqueHEVC:  true,
		HDR10Plus:   true,
		DolbyVision: true,
	}
	if decoded {
		evidence = api.HDRSourceEvidence{UniqueHEVC: true, Decoded: &api.HDRDecodedSourceEvidence{
			VideoStreams:           1,
			Codec:                  "hevc",
			Width:                  192,
			Height:                 112,
			BitDepth:               10,
			PixelAspectNumerator:   1,
			PixelAspectDenominator: 1,
			Range:                  "limited",
			Matrix:                 "bt2020nc",
			Transfer:               "smpte2084",
			Primaries:              "bt2020",
		}}
	}
	prepared, err := preparedrelease.New(repo, workflowMediaIdentityResolver{}, hdrWorkflowCollector{evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	rootArtifacts := filepath.Join(root, "artifacts")
	builder := &hdrCrashBuilder{
		resolver: prepared,
		service:  hdranalysis.New(hdranalysis.NewAdmission(), nil),
		root:     rootArtifacts,
	}
	persistent, err := releaseworkflow.NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := releaseworkflow.NewPrivateArtifactVault(filepath.Join(root, "vault"),
		hdrResourceCodec(rootArtifacts, "analysis"), hdrResourceCodec(rootArtifacts, "extraction"), hdrResourceCodec(rootArtifacts, "measurement"))
	if err != nil {
		t.Fatal(err)
	}
	clock := &hdrRestartClock{}
	options := []releaseworkflow.Option{releaseworkflow.WithHDRAnalysisBuilder(builder), releaseworkflow.WithClock(clock),
		releaseworkflow.WithActiveInputs(repo, verifyWorkflowInput),
		releaseworkflow.WithHDRComparisonInspector(func(context.Context, string, string) (api.HDRSourceEvidence, error) { return evidence, nil })}
	module, err := releaseworkflow.New(persistent, vault, prepared, options...)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &hdrRestartFixture{
		core: &Core{
			workflow:      module,
			preparedFacts: prepared,
			logger:        api.NopLogger{},
		},
		repository: repo,
		persistent: persistent,
		vault:      vault,
		builder:    builder,
		clock:      clock,
		source:     filepath.Join(root, "Synthetic.Primary.mkv"),
		comparison: filepath.Join(root, "Synthetic.Comparison.mkv"),
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = module.Shutdown(ctx)
		_ = repo.Close()
	})
	return fixture
}

func writeHDRRestartFixture(t *testing.T, root string, decoded bool) {
	t.Helper()
	// Seed only fresh cases. The crash helper and recovery path reopen this
	// database through the production API without replacing retained state.
	dbfixture.WriteMigrated(t, filepath.Join(root, "state.sqlite"))
	name := "reordered-hybrid.mkv"
	if decoded {
		name = "decoded-pq-cuts-25.mkv"
	}
	data, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Synthetic.Primary.mkv", "Synthetic.Comparison.mkv"} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *hdrRestartFixture) prepare(t *testing.T) releaseworkflow.CommandResult {
	t.Helper()
	slot, err := f.core.workflow.OpenInput(t.Context(), "cli", releaseworkflow.OpenInputRequest{
		Input: api.PrepareInput{SourcePath: f.source}, IdempotencyKey: "open",
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.core.workflow.Current(t.Context(), "cli", slot.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	current, err = f.core.workflow.Execute(t.Context(), "cli", releaseworkflow.PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: f.source},
		IdempotencyKey:   "prepare",
	})
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func (f *hdrRestartFixture) selectComparison(t *testing.T, current releaseworkflow.CommandResult) (releaseworkflow.CommandResult, *api.HDRComparisonOptions) {
	t.Helper()
	selected, err := f.core.workflow.Execute(t.Context(), "cli", releaseworkflow.SelectHDRComparisonCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		SourcePath:       f.comparison,
		IdempotencyKey:   "comparison",
		Release:          api.ReleaseRef{SourcePath: f.source, Generation: current.Release.Release.Generation},
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err = f.core.workflow.Current(t.Context(), "cli", current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	return current, &api.HDRComparisonOptions{
		SourceID:    selected.HDRComparison.ID,
		FrameOffset: new(0),
		Crop:        api.HDRCrop{Width: 192, Height: 112},
		Grade:       true,
		Colour:      true,
	}
}

func TestHDRCheckpointProcessDeathHelper(t *testing.T) {
	root, phase := os.Getenv("UPBRR_HDR_CRASH_ROOT"), os.Getenv("UPBRR_HDR_CRASH_PHASE")
	if root == "" || phase == "" {
		t.Skip("subprocess only")
	}
	started := time.Now()
	mark := func(stage string) {
		t.Logf("checkpoint phase=%s stage=%s elapsed=%s", phase, stage, time.Since(started))
	}
	mark("open fixture begin")
	f := openHDRRestartFixture(t, root, phase == "decoded")
	mark("open fixture complete")
	mark("prepare begin")
	current := f.prepare(t)
	mark("prepare complete")
	instructions := api.HDRAnalysisInstructions{
		Release:        api.ReleaseRef{SourcePath: f.source, Generation: current.Release.Release.Generation},
		TargetIDs:      []string{current.Release.Display.HDRTargets[0].ID},
		ProfileVersion: api.HDRAnalysisProfileV2,
	}
	if strings.HasPrefix(phase, "recovery-") {
		var err error
		mark("initial analysis begin")
		current, err = f.core.workflow.Execute(t.Context(), "cli", releaseworkflow.AnalyzeHDRCommand{
			WorkflowID:       current.Workflow.ID,
			ExpectedRevision: current.Workflow.Revision,
			Instructions:     instructions,
			IdempotencyKey:   "initial",
		})
		mark("initial analysis complete")
		if err != nil || current.HDRAnalysis == nil || current.HDRAnalysis.Status != api.StageStatusCompleted {
			t.Fatalf("initial analysis: %+v %v", current.HDRAnalysis, err)
		}
		state, err := f.persistent.Load(t.Context(), "cli", current.Workflow.ID)
		if err != nil {
			t.Fatal(err)
		}
		for id := range state.HDRExtractions {
			value, err := f.vault.Get("cli", current.Workflow.ID, "hdr-extraction:"+string(id), time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			resource, ok := value.(hdrManagedResource)
			if !ok {
				t.Fatalf("unexpected extraction resource %T", value)
			}
			if err := resource.Release(); err != nil {
				t.Fatal(err)
			}
		}
		instructions.PeakSource = api.HDRPeakMaxSCLLuminance
		mark("missing-evidence analysis begin")
		current, err = f.core.workflow.Execute(t.Context(), "cli", releaseworkflow.AnalyzeHDRCommand{
			WorkflowID:       current.Workflow.ID,
			ExpectedRevision: current.Workflow.Revision,
			Instructions:     instructions,
			IdempotencyKey:   "missing-evidence",
		})
		mark("missing-evidence analysis complete")
		if err != nil || current.HDRAnalysis == nil || !current.HDRAnalysis.TargetNeedsSourceRetry(instructions.TargetIDs[0]) {
			t.Fatalf("damaged evidence did not require explicit source recovery: %+v %v", current.HDRAnalysis, err)
		}
	}
	if phase == "decoded" {
		var comparison *api.HDRComparisonOptions
		current, comparison = f.selectComparison(t, current)
		instructions.Decoded = &api.HDRDecodedOptions{Comparison: comparison}
	}
	if phase == "newer" || phase == "retry" {
		if phase == "retry" {
			instructions.Decoded = &api.HDRDecodedOptions{SceneSync: true}
		}
		var err error
		mark("older analysis begin")
		current, err = f.core.workflow.Execute(t.Context(), "cli", releaseworkflow.AnalyzeHDRCommand{
			WorkflowID:       current.Workflow.ID,
			ExpectedRevision: current.Workflow.Revision,
			Instructions:     instructions,
			IdempotencyKey:   "older",
		})
		mark("older analysis complete")
		if err != nil || current.HDRAnalysis == nil {
			t.Fatalf("older result: %+v %v", current, err)
		}
		instructions.Decoded = &api.HDRDecodedOptions{SceneSync: true}
		if phase == "newer" {
			instructions.PeakSource = api.HDRPeakMaxSCLLuminance
		}
		if phase == "retry" {
			instructions.DecodedRetry = &api.HDRDecodedRetry{AnalysisAttemptID: current.HDRAnalysis.AttemptID, Modes: []api.HDRDecodedMode{api.HDRDecodedSceneSync}}
		}
	}
	f.builder.phase = phase
	if strings.HasPrefix(phase, "recovery-") {
		f.builder.phase = "native"
	}
	mark("crash command start begin")
	if _, err := f.core.workflow.Start(t.Context(), "cli", releaseworkflow.AnalyzeHDRCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Instructions:     instructions,
		IdempotencyKey:   "crash",
	}); err != nil {
		t.Fatal(err)
	}
	mark("crash command start returned")
	select {
	case <-time.After(25 * time.Second):
		t.Fatal("analysis did not reach the crash checkpoint")
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
}

func TestHDRCheckpointProcessDeathRetainsExactResumeRequest(t *testing.T) {
	for _, phase := range []string{"native", "decoded", "newer", "retry", "recovery-v2"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "decoded" && os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
				t.Skip("requires calibrated actual decoder")
			}
			// Each child performs real extraction and rendering under the race detector.
			// Keep them sequential so they do not exhaust the hosted runner's CPU budget.
			root := t.TempDir()
			writeHDRRestartFixture(t, root, phase == "decoded")
			ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHDRCheckpointProcessDeathHelper$", "-test.count=1", "-test.v")
			command.Env = append(os.Environ(), "UPBRR_HDR_CRASH_ROOT="+root, "UPBRR_HDR_CRASH_PHASE="+phase)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("checkpoint subprocess: %v; context: %v\n%s", err, ctx.Err(), output)
			}
			f := openHDRRestartFixture(t, root, phase == "decoded")
			previous, err := f.repository.LoadActiveInput(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			retainedBeforeReset, err := f.persistent.Load(t.Context(), "cli", previous.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			// Expire the dead process's leases without waiting a real minute. The
			// clock is restored before starting new work; DB fencing stays active.
			f.clock.offset.Store(int64(2 * time.Minute))
			if err := f.core.workflow.ResetIdleInputOnStartup(t.Context()); err != nil {
				t.Fatal(err)
			}
			f.clock.offset.Store(0)
			active, err := f.core.GetActiveInput(t.Context(), "cli")
			if err != nil || active.State != api.ActiveInputEmpty || active.Current != nil {
				t.Fatalf("restart restored the previous input: %+v %v", active, err)
			}
			current, err := f.core.workflow.Current(t.Context(), "cli", previous.WorkflowID)
			if err != nil || current.HDRPendingRequest == nil {
				t.Fatalf("retained pending request lost: %+v %v", current, err)
			}
			pending := *current.HDRPendingRequest
			if pending.Release.Generation != current.Release.Release.Generation || pending.Release.SourcePath != f.source {
				t.Fatal("pending request crossed generation")
			}
			before, err := f.persistent.Load(t.Context(), "cli", current.Workflow.ID)
			if err != nil || len(before.HDRExtractions) == 0 {
				t.Fatalf("native checkpoints lost: %+v %v", before, err)
			}
			if phase == "native" || phase == "decoded" {
				if current.HDRAnalysis != nil {
					t.Fatal("crash unexpectedly published a terminal result")
				}
			} else if current.HDRAnalysis == nil {
				t.Fatal("older terminal result lost")
			}
			if phase == "newer" && (pending.PeakSource != api.HDRPeakMaxSCLLuminance || pending.Decoded == nil || !pending.Decoded.SceneSync) {
				t.Fatal("resume fell back to older options")
			}
			if !reflect.DeepEqual(retainedBeforeReset.HDRPendingRequest, before.HDRPendingRequest) ||
				!reflect.DeepEqual(retainedBeforeReset.HDRExtractions, before.HDRExtractions) ||
				!reflect.DeepEqual(retainedBeforeReset.HDRMeasurements, before.HDRMeasurements) {
				t.Fatal("startup changed retained request or checkpoints")
			}
			if phase != "native" && !strings.HasPrefix(phase, "recovery-") && len(before.HDRMeasurements) == 0 {
				t.Fatal("decoded checkpoint lost")
			}
			if phase == "decoded" {
				for _, record := range before.HDRMeasurements {
					if record.Result.Mode != api.HDRDecodedGrade || record.Result.State != api.HDRDecodedComplete {
						t.Fatalf("not a completed grade checkpoint: %+v", record)
					}
				}
			}
			if _, err := f.core.workflow.Current(t.Context(), "foreign", current.Workflow.ID); err == nil {
				t.Fatal("foreign owner obtained pending request")
			}
			_, err = f.core.workflow.Execute(t.Context(), "cli", releaseworkflow.AnalyzeHDRCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Instructions:     pending,
				IdempotencyKey:   "inactive-resume",
			})
			if !errors.Is(err, api.ErrActiveInputChanged) {
				t.Fatalf("inactive workflow resumed without an explicit open: %v", err)
			}
			reopened, err := f.core.workflow.OpenInput(t.Context(), "cli", releaseworkflow.OpenInputRequest{
				ExpectedRevision: active.Revision,
				Input:            api.PrepareInput{SourcePath: f.source},
				IdempotencyKey:   "explicit-reopen",
			})
			if err != nil || reopened.WorkflowID == current.Workflow.ID {
				t.Fatalf("reopen did not establish a fresh workflow: %+v %v", reopened, err)
			}
			fresh, err := f.core.workflow.Current(t.Context(), "cli", reopened.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err = f.core.workflow.Execute(t.Context(), "cli", releaseworkflow.PrepareReleaseCommand{
				WorkflowID:       fresh.Workflow.ID,
				ExpectedRevision: fresh.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: f.source},
				IdempotencyKey:   "explicit-prepare",
			})
			if err != nil || fresh.Release == nil || fresh.HDRPendingRequest != nil {
				t.Fatalf("fresh preparation replayed pending authority: %+v %v", fresh, err)
			}
			freshState, err := f.persistent.Load(t.Context(), "cli", reopened.WorkflowID)
			if err != nil || freshState.Workflow.HDRComparison != nil || len(freshState.HDRMeasurements) != 0 || freshState.HDRPendingRequest != nil {
				t.Fatalf("fresh workflow inherited or executed decoded authority: %+v %v", freshState, err)
			}
			after, err := f.persistent.Load(t.Context(), "cli", current.Workflow.ID)
			if err != nil || !reflect.DeepEqual(before.HDRPendingRequest, after.HDRPendingRequest) ||
				!reflect.DeepEqual(before.HDRExtractions, after.HDRExtractions) || !reflect.DeepEqual(before.HDRMeasurements, after.HDRMeasurements) {
				t.Fatalf("explicit reopen changed retained request or checkpoints: %v", err)
			}
		})
	}
}

func TestHDRPreparationRetainsNativeEvidenceWithoutInvalidComparisonReplay(t *testing.T) {
	for _, transition := range []string{"new generation", "expired comparison", "same generation"} {
		t.Run(transition, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeHDRRestartFixture(t, root, false)
			f := openHDRRestartFixture(t, root, false)
			current := f.prepare(t)
			current, comparison := f.selectComparison(t, current)
			request := api.HDRAnalysisInstructions{
				Release:        api.ReleaseRef{SourcePath: f.source, Generation: current.Release.Release.Generation},
				TargetIDs:      []string{current.Release.Display.HDRTargets[0].ID},
				ProfileVersion: api.HDRAnalysisProfileV2,
				Decoded:        &api.HDRDecodedOptions{SceneSync: true, Comparison: comparison},
			}
			analyzed, err := f.core.workflow.Execute(t.Context(), "cli", releaseworkflow.AnalyzeHDRCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Instructions:     request,
				IdempotencyKey:   "analyze",
			})
			if err != nil || analyzed.HDRAnalysis == nil || !analyzed.HDRAnalysis.CanIncludeImages() {
				t.Fatalf("initial native/comparison result: %+v %v", analyzed, err)
			}
			before, err := f.persistent.Load(t.Context(), "cli", current.Workflow.ID)
			if err != nil || len(before.HDRMeasurements) != 3 {
				t.Fatalf("retained requested modes: %d %v", len(before.HDRMeasurements), err)
			}
			if transition != "new generation" {
				// Force the preparation restoration path rather than its intact-image shortcut.
				f.vault.Delete("cli", current.Workflow.ID, "hdr-analysis:"+analyzed.HDRAnalysis.AttemptID)
			}
			if transition == "expired comparison" {
				ref := before.Workflow.HDRComparison
				key := "hdr-comparison:" + ref.ID
				value, err := f.vault.Get("cli", current.Workflow.ID, key, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				source, ok := value.(releaseworkflow.HDRComparisonSource)
				if !ok {
					t.Fatal("comparison resource type")
				}
				// Shorten only the fixture's authorization lifetime; private decoding
				// and expiry checks still use the production vault and resolver.
				source.Ref.ExpiresAt = time.Now().Add(-time.Second)
				if err := f.vault.Put("cli", current.Workflow.ID, key, source, source.Ref.ExpiresAt); err != nil {
					t.Fatal(err)
				}
				before.Workflow.HDRComparison = &source.Ref
				expected := before.Workflow.Revision
				before.Workflow.Revision++
				before.Workflow.UpdatedAt = time.Now().UTC()
				slot, err := f.repository.LoadActiveInput(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				ctx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: slot.CoordinatorID, Fence: slot.Fence})
				if err := f.persistent.Save(ctx, "cli", expected, before); err != nil {
					t.Fatal(err)
				}
			}
			current, err = f.core.workflow.Current(t.Context(), "cli", current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			reads := false
			ctx := hdranalysis.WithProgress(t.Context(), func(progress hdranalysis.Progress) {
				reads = reads || progress.Phase == "reading_metadata" || progress.Phase == "decoding"
			})
			prepared, err := f.core.workflow.Execute(ctx, "cli", releaseworkflow.PrepareReleaseCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: f.source, Force: transition == "new generation"},
				IdempotencyKey:   "prepare-again",
			})
			if err != nil || prepared.HDRAnalysis == nil || !prepared.HDRAnalysis.CanIncludeImages() || reads {
				t.Fatalf("preparation did not restore native-only evidence safely: %+v reads=%t %v", prepared, reads, err)
			}
			after, err := f.persistent.Load(t.Context(), "cli", current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			decoded := prepared.HDRAnalysis.Decoded
			switch transition {
			case "new generation":
				if prepared.Release.Release.Generation == request.Release.Generation || !reflect.DeepEqual(decoded, request.Decoded) ||
					prepared.Workflow.HDRComparison != nil || len(after.HDRMeasurements) != 0 ||
					len(prepared.HDRAnalysis.Targets[0].Artifacts) != len(analyzed.HDRAnalysis.Targets[0].Artifacts) {
					t.Fatal("new generation lost saved outputs or retained comparison-read authority")
				}
				// Saved PNGs describe the previous run; the old handle cannot authorize
				// a new paired measurement in this generation.
				stale := request
				stale.Release.Generation = prepared.Release.Release.Generation
				_, err := f.core.workflow.Execute(ctx, "cli", releaseworkflow.AnalyzeHDRCommand{
					WorkflowID:       prepared.Workflow.ID,
					ExpectedRevision: prepared.Workflow.Revision,
					Instructions:     stale,
					IdempotencyKey:   "reject-old-comparison",
				})
				if err == nil || reads {
					t.Fatal("retained comparison PNGs authorized another source read")
				}
			case "expired comparison":
				if decoded == nil || !decoded.SceneSync || decoded.Comparison != nil {
					t.Fatalf("expired comparison blocked other retained modes: %+v", decoded)
				}
				if !reflect.DeepEqual(before.HDRExtractions, after.HDRExtractions) || !reflect.DeepEqual(before.HDRMeasurements, after.HDRMeasurements) {
					t.Fatal("expired comparison repeated retained evidence")
				}
			case "same generation":
				if !reflect.DeepEqual(decoded, request.Decoded) || !reflect.DeepEqual(before.HDRExtractions, after.HDRExtractions) || !reflect.DeepEqual(before.HDRMeasurements, after.HDRMeasurements) {
					t.Fatal("same-generation re-render replaced evidence or lost valid comparison options")
				}
			}
		})
	}
}
