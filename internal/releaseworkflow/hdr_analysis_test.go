// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type hdrBuilderFixture struct {
	dual              bool
	decodedOnly       bool
	builds            int
	partial           bool
	failRender        bool
	failBeforePublish bool
	unavailable       bool
	pngOnly           bool
	checkpoint        func(HDRExtractionRecord)
	prior             *api.HDRAnalysisResult
	priorResource     RetainedHDRAnalysisResource
}

func (b *hdrBuilderFixture) Build(
	_ context.Context,
	preparation HDRPreparation,
	instructions api.HDRAnalysisInstructions,
	attempt string,
	now time.Time,
	prior *api.HDRAnalysisResult,
	priorResource RetainedHDRAnalysisResource,
	entries map[api.HDRExtractionID]HDRExtractionRecord,
	_ map[api.HDRExtractionID]RetainedHDRExtractionResource,
	publish func(HDRExtractionRecord, RetainedHDRExtractionResource) error,
	_ *HDRMeasurementStore,
) (api.HDRAnalysisResult, RetainedHDRAnalysisResource, error) {
	release := preparation.Release
	b.builds++
	b.prior, b.priorResource = prior, priorResource
	if b.failBeforePublish {
		return api.HDRAnalysisResult{}, nil, errors.New("synthetic extraction interruption")
	}
	instructions, _ = instructions.Normalize()
	for _, target := range instructions.TargetIDs {
		found := false
		for _, entry := range entries {
			if entry.TargetID == target {
				found = true
			}
		}
		if found {
			continue
		}
		record := HDRExtractionRecord{
			ID:                api.HDRExtractionID(attempt + "-" + target),
			Release:           release,
			SourceFingerprint: "source",
			TargetID:          target,
			Schema:            api.HDRExtractionSchemaVersion,
			SelectionPolicy:   "unique_hevc",
			ResolvedTrackID:   1,
			Dependency:        "fixture",
			Size:              1,
			SHA256:            strings.Repeat("a", 64),
		}
		if err := publish(record, hdrResourceFixture{}); err != nil {
			return api.HDRAnalysisResult{}, nil, err
		}
		if b.checkpoint != nil {
			b.checkpoint(record)
		}
	}
	if b.failRender {
		return api.HDRAnalysisResult{}, nil, errors.New("synthetic renderer failure")
	}
	if b.pngOnly {
		clear(entries)
	}
	result := api.HDRAnalysisResult{
		Decoded:             instructions.Decoded,
		Release:             release,
		AttemptID:           attempt,
		ManifestFingerprint: "manifest",
		TargetIDs:           instructions.TargetIDs,
		PeakSource:          instructions.PeakSource,
		ProfileVersion:      instructions.ProfileVersion,
		Formats:             instructions.Formats,
		Status:              api.StageStatusCompleted,
		CreatedAt:           now,
		CompletedAt:         &now,
	}
	for index, target := range instructions.TargetIDs {
		item := api.HDRAnalysisTargetResult{
			TargetID: target,
			Label:    "Video",
			Status:   api.StageStatusCompleted,
			Frames:   1,
			Scenes:   1,
			Profile:  "A",
			Artifact: &api.HDRAnalysisArtifact{
				ID:     api.PublicResourceID(attempt + "-" + target),
				Width:  3000,
				Height: 1200,
			},
		}
		if b.partial && index == 1 {
			item.Status = api.StageStatusFailed
			item.Artifact = nil
			item.Failure = &api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureIncomplete, Message: "incomplete metadata"}
			result.Status = api.StageStatusPartial
		}
		if b.unavailable {
			item.Status, item.Artifact = api.StageStatusFailed, nil
			item.Failure = &api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceUnavailable, Message: "retained metadata is missing"}
			result.Status = api.StageStatusFailed
		}
		if instructions.ProfileVersion == api.HDRAnalysisProfileV2 {
			format := api.HDRFormatResult{
				Format:   api.HDRNativeHDR10Plus,
				State:    api.HDRExtractionComplete,
				Coverage: api.HDRCoverageComplete,
				TrackID:  "1",
				Frames:   1,
				Retained: 1,
				Observed: 1,
				Scenes:   1,
				Profile:  "A",
			}
			if item.Failure != nil {
				format.State, format.Coverage, format.Failure = api.HDRExtractionFailed, api.HDRCoverageUnknown, item.Failure
			}
			item.Formats = []api.HDRFormatResult{format, {
				Format:   api.HDRNativeDolbyVision,
				State:    api.HDRExtractionAbsent,
				Coverage: api.HDRCoverageAbsent,
			}}
			if item.Artifact != nil {
				item.Artifacts = []api.HDRTypedArtifact{{
					ID:      item.Artifact.ID,
					Kind:    api.HDRArtifactHDR10PlusPlot,
					Format:  api.HDRNativeHDR10Plus,
					Width:   item.Artifact.Width,
					Height:  item.Artifact.Height,
					MIME:    "image/png",
					Profile: api.HDRAnalysisProfileV2,
				}}
				if b.dual {
					item.Formats[1] = api.HDRFormatResult{
						Format:    api.HDRNativeDolbyVision,
						State:     api.HDRExtractionComplete,
						Coverage:  api.HDRCoverageComplete,
						TrackID:   "1",
						Frames:    1,
						Retained:  1,
						Observed:  1,
						L1Samples: 1,
					}
					item.Artifacts = append(item.Artifacts, api.HDRTypedArtifact{
						ID:      api.PublicResourceID(string(item.Artifact.ID) + "-dv"),
						Kind:    api.HDRArtifactDolbyVisionL1Plot,
						Format:  api.HDRNativeDolbyVision,
						Width:   3000,
						Height:  1200,
						MIME:    "image/png",
						Profile: api.HDRAnalysisProfileV2,
					})
				}
			}
			item.Artifact, item.Failure, item.Frames, item.Scenes, item.Profile = nil, nil, 0, 0, ""
		}
		if instructions.Decoded != nil {
			if b.decodedOnly {
				item.Formats = []api.HDRFormatResult{
					{
						Format:   api.HDRNativeHDR10Plus,
						State:    api.HDRExtractionAbsent,
						Coverage: api.HDRCoverageAbsent,
					},
					{
						Format:   api.HDRNativeDolbyVision,
						State:    api.HDRExtractionAbsent,
						Coverage: api.HDRCoverageAbsent,
					},
				}
				pictureID, statsID := api.PublicResourceID(attempt+"-picture"), api.PublicResourceID(attempt+"-stats")
				width, height := api.HDRArtifactPicturePlot.ImageDimensions()
				item.Artifacts = []api.HDRTypedArtifact{
					{
						ID:      pictureID,
						Kind:    api.HDRArtifactPicturePlot,
						Width:   width,
						Height:  height,
						MIME:    "image/png",
						Profile: api.HDRAnalysisProfileV2,
					},
					{
						ID:      statsID,
						Kind:    api.HDRArtifactAudioStats,
						MIME:    "text/plain",
						Profile: api.HDRAnalysisProfileV2,
					},
				}
				item.Decoded = []api.HDRDecodedResult{
					{
						Mode:      api.HDRDecodedAutomatic,
						State:     api.HDRDecodedComplete,
						Selected:  1,
						Measured:  1,
						Profile:   "synthetic-auto",
						Artifacts: []api.PublicResourceID{pictureID},
					},
					{
						Mode:      api.HDRDecodedAudio,
						State:     api.HDRDecodedComplete,
						Selected:  1,
						Measured:  1,
						Profile:   "synthetic-audio",
						Artifacts: []api.PublicResourceID{statsID},
					},
				}
				if instructions.Decoded.Audio {
					item.Artifacts = item.Artifacts[1:]
					item.Decoded = item.Decoded[1:]
					if b.partial && index == 1 {
						item.Artifacts = nil
						item.Decoded = []api.HDRDecodedResult{{
							Mode:   api.HDRDecodedAudio,
							State:  api.HDRDecodedFailed,
							Reason: "audio_analysis_failed",
						}}
					}
				}
			}
			for _, mode := range []api.HDRDecodedMode{api.HDRDecodedSceneSync, api.HDRDecodedActiveArea, api.HDRDecodedGrade, api.HDRDecodedColour} {
				if instructions.Decoded.Requested(mode) {
					item.Decoded = append(item.Decoded, api.HDRDecodedResult{
						Mode:   mode,
						State:  api.HDRDecodedUnsupported,
						Reason: "fixture_unsupported",
					})
				}
			}
			item.Status = item.AnalysisStatus()
			if b.decodedOnly {
				result.Status = item.Status
			}
		}
		result.Targets = append(result.Targets, item)
	}
	if b.decodedOnly {
		result.Status = api.NativeHDRAggregate(result.Targets)
	}
	if b.unavailable {
		return result, nil, nil
	}
	return result, hdrResourceFixture{}, nil
}

type hdrResourceFixture struct{}

func TestAutomaticHDRDefaultDescriptionAndExpiredReferenceRecovery(t *testing.T) {
	builder := &hdrInputRestorerFixture{}
	builder.decodedOnly = true
	module, repo := newTestModule(t, testPreparer(), WithHDRAnalysisBuilder(builder), WithOperationErrorClassifier(func(_ api.OperationKind, err error) error {
		t.Logf("automatic fixture operation: %v", err)
		return err
	}))
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "automatic-default"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "Synthetic.HDR.mkv"},
	})
	release := api.ReleaseRef{SourcePath: prepared.Release.Release.Source.SourcePath, Generation: prepared.Release.Release.Generation}
	state, err := repo.Load(t.Context(), testOwnerID, prepared.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	state.Workflow.HDRComparison = &api.HDRComparisonSourceRef{
		ID:          "expired",
		Release:     release,
		Fingerprint: "reference",
		ExpiresAt:   time.Now().Add(-time.Hour),
	}
	previousRevision := state.Workflow.Revision
	state.Workflow.Revision++
	if err := repo.Save(t.Context(), testOwnerID, previousRevision, state); err != nil {
		t.Fatal(err)
	}
	instructions := api.HDRAnalysisInstructions{
		Release:        release,
		TargetIDs:      []string{api.HDRTargetID("source", "")},
		ProfileVersion: api.HDRAnalysisProfileV2,
		Decoded: &api.HDRDecodedOptions{Automatic: true, Comparison: &api.HDRComparisonOptions{
			SourceID:    "expired",
			FrameOffset: new(0),
			Crop:        api.HDRCrop{Width: 192, Height: 112},
			Grade:       true,
		}},
	}
	if _, err := module.resolveHDRComparison("foreign", state, instructions); err == nil {
		t.Fatal("automatic fallback accepted foreign owner authority")
	}
	foreignGeneration := instructions
	foreignGeneration.Release.Generation++
	if _, err := module.resolveHDRComparison(testOwnerID, state, foreignGeneration); err == nil {
		t.Fatal("automatic fallback accepted a foreign generation")
	}
	foreignHandle := instructions
	foreignHandle.Decoded = new(*instructions.Decoded)
	foreignHandle.Decoded.Comparison = new(*instructions.Decoded.Comparison)
	foreignHandle.Decoded.Comparison.SourceID = "foreign-handle"
	if _, err := module.resolveHDRComparison(testOwnerID, state, foreignHandle); err == nil {
		t.Fatal("automatic fallback accepted a handle that was never selected")
	}
	started, err := module.Start(t.Context(), testOwnerID, AnalyzeHDRCommand{
		WorkflowID:       state.Workflow.ID,
		ExpectedRevision: state.Workflow.Revision,
		Instructions:     instructions,
		IdempotencyKey:   "automatic",
	})
	if err != nil {
		t.Fatalf("expired optional reference blocked operation start: %v", err)
	}
	waitForWorkflowOperation(t, module, state.Workflow.ID, started.ID, func(status api.WorkflowOperationStatus) bool {
		return status.Status == api.StageStatusCompleted || status.Status == api.StageStatusPartial || status.Status == api.StageStatusFailed
	})
	current, err := module.Current(t.Context(), testOwnerID, state.Workflow.ID)
	if err != nil || current.HDRAnalysis == nil || !current.Workflow.HDRAnalysisEnabled || !current.HDRAnalysis.CanIncludeImages() {
		t.Fatalf("primary audio statistics default was lost: %+v %v", current, err)
	}
	state, err = repo.Load(t.Context(), testOwnerID, state.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.descriptionResources(t.Context(), testOwnerID, &state, nil, time.Now()); err != nil {
		t.Fatalf("default combined outputs blocked normal descriptions: %v", err)
	}
	filtered, _, err := module.resolveHDRDescriptionResources(t.Context(), testOwnerID, &state, time.Now())
	if err != nil || len(filtered.Targets[0].SelectableImages()) != 0 || len(filtered.Targets[0].Artifacts) != 1 ||
		filtered.Targets[0].Artifacts[0].Kind != api.HDRArtifactAudioStats {
		t.Fatalf("default statistics or picture opt-in projection: %+v %v", filtered, err)
	}
	images := current.HDRAnalysis.Targets[0].SelectableImages()
	if len(images) != 1 || images[0].Kind != api.HDRArtifactPicturePlot {
		t.Fatalf("non-image statistics leaked into image selection: %+v", images)
	}
	selected := executeCommand(t, module, SetHDRAnalysisEnabledCommand{
		WorkflowID:       state.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Enabled:          true,
		ArtifactIDs:      []api.PublicResourceID{images[0].ID},
	})
	state, err = repo.Load(t.Context(), testOwnerID, state.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	filtered, _, err = module.resolveHDRDescriptionResources(t.Context(), testOwnerID, &state, time.Now())
	if err != nil || len(filtered.Targets[0].SelectableImages()) != 1 || len(filtered.Targets[0].Artifacts) != 2 {
		t.Fatalf("explicit automatic image selection lost description authority: %v", err)
	}
	// Force regeneration of images without changing the generation or the
	// original comparison options. The private reference has already expired.
	prior := state.HDRAnalyses[selected.Workflow.HDRAnalysis.ID]
	module.private.Delete(testOwnerID, state.Workflow.ID, hdrAnalysisPrivateResourceID(prior.AttemptID))
	state.PendingHDRAnalysisWorkflowID, state.PendingHDRAnalysis = state.Workflow.ID, state.Workflow.HDRAnalysis
	if err := module.restorePendingHDR(t.Context(), testOwnerID, &state, release, state.Workflow.Revision, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	restored := currentHDRResult(&state)
	if restored == nil || restored.Decoded == nil || !restored.Decoded.Automatic || restored.Decoded.Comparison == nil || restored.Decoded.Comparison.SourceID != "expired" {
		t.Fatal("recovery changed automatic checkpoint identity or dropped primary modes")
	}
}

func TestHDROperationRetainsTypedTargetFailures(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(strconv.FormatBool(partial), func(t *testing.T) {
			builder := &hdrBuilderFixture{partial: partial, unavailable: !partial}
			module, _ := newTestModule(t, testPreparer(), WithHDRAnalysisBuilder(builder), WithOperationErrorClassifier(func(operation api.OperationKind, err error) error {
				failure, ok := api.AsHDRAnalysisFailure(err)
				if !ok {
					t.Fatalf("classifier received an untyped HDR cause: %v", err)
				}
				return api.NewOperationError(api.OperationFailure{
					Code:            api.OperationFailureHDRAnalysis,
					Operation:       operation,
					Message:         failure.Message,
					HDRAnalysisCode: failure.Code,
					Recovery:        api.OperationRecoveryRetry,
				}, err)
			}))
			created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "hdr-terminal"})
			prepared := executeCommand(t, module, PrepareReleaseCommand{
				WorkflowID:       created.Workflow.ID,
				ExpectedRevision: created.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: "Synthetic.HDR.mkv"},
			})
			operation, err := module.Start(t.Context(), testOwnerID, AnalyzeHDRCommand{
				WorkflowID:       prepared.Workflow.ID,
				ExpectedRevision: prepared.Workflow.Revision,
				Instructions: api.HDRAnalysisInstructions{
					ProfileVersion: api.HDRAnalysisProfileVersion,
					Release:        api.ReleaseRef{SourcePath: "Synthetic.HDR.mkv", Generation: 1},
					TargetIDs:      []string{api.HDRTargetID("first", ""), api.HDRTargetID("second", "")},
				},
				IdempotencyKey: "terminal-failure",
			})
			if err != nil {
				t.Fatal(err)
			}
			want := api.StageStatusFailed
			if partial {
				want = api.StageStatusPartial
			}
			terminal := waitForWorkflowOperation(t, module, prepared.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool { return status.Status == want })
			wantCount := 2
			if partial {
				wantCount = 1
			}
			if len(terminal.Failures) != wantCount {
				t.Fatalf("terminal failures=%#v", terminal)
			}
			for _, failure := range terminal.Failures {
				if failure.Failure.Code != api.OperationFailureHDRAnalysis || failure.Failure.Operation != api.OperationKindHDRAnalysis || failure.Failure.HDRAnalysisCode == "" {
					t.Fatalf("untyped target failure: %#v", failure)
				}
			}
			current, err := module.Current(t.Context(), testOwnerID, prepared.Workflow.ID)
			if err != nil || current.HDRAnalysis == nil || current.HDRAnalysis.Status != want {
				t.Fatalf("retained analysis=%#v err=%v", current, err)
			}
		})
	}
}

func (hdrResourceFixture) OpenExtraction(context.Context, HDRExtractionRecord) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("metadata")), nil
}
func (hdrResourceFixture) OpenArtifact(context.Context, api.HDRAnalysisResult, api.PublicResourceID) (MediaArtifactContent, error) {
	return MediaArtifactContent{Body: io.NopCloser(strings.NewReader("png")), ContentType: "image/png"}, nil
}
func (hdrResourceFixture) LocalArtifactPath(api.HDRAnalysisResult, api.PublicResourceID) (string, error) {
	return "plot.png", nil
}

func TestHDRWorkflowCheckpointBeforeRenderAndRetry(t *testing.T) {
	builder := &hdrBuilderFixture{failRender: true}
	module, repo := newTestModule(t, testPreparer(), WithHDRAnalysisBuilder(builder))
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "hdr-checkpoint"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "Synthetic.HDR.mkv"},
	})
	instructions := api.HDRAnalysisInstructions{
		ProfileVersion: api.HDRAnalysisProfileVersion,
		Release:        api.ReleaseRef{SourcePath: prepared.Release.Release.Source.SourcePath, Generation: 1},
		TargetIDs:      []string{api.HDRTargetID("source", "")},
	}
	builder.checkpoint = func(record HDRExtractionRecord) {
		stored, err := repo.Load(t.Context(), testOwnerID, prepared.Workflow.ID)
		if err != nil || stored.HDRExtractions[record.ID].SHA256 != record.SHA256 || stored.Workflow.Revision != prepared.Workflow.Revision {
			t.Fatalf("metadata not checkpointed before render: %#v %v", stored, err)
		}
		if stored.HDRPendingRequest == nil || stored.HDRPendingRequest.Instructions.Release != instructions.Release {
			t.Fatal("native checkpoint preceded durable request authority")
		}
	}
	command := AnalyzeHDRCommand{
		WorkflowID:       prepared.Workflow.ID,
		ExpectedRevision: prepared.Workflow.Revision,
		Instructions:     instructions,
		IdempotencyKey:   "first",
	}
	if _, err := module.execute(t.Context(), testOwnerID, command); err == nil {
		t.Fatal("render failure accepted")
	}
	stored, err := repo.Load(t.Context(), testOwnerID, prepared.Workflow.ID)
	if err != nil || len(stored.HDRExtractions) != 1 || stored.Workflow.HDRAnalysis != nil {
		t.Fatalf("extraction-only recovery=%#v %v", stored, err)
	}
	pending := *stored.HDRPendingRequest
	if pending.NativeRetryAfterSequence == nil {
		t.Fatal("native retry boundary missing")
	}
	boundary := *pending.NativeRetryAfterSequence
	if err := repo.CheckpointHDRRequest(t.Context(), testOwnerID, stored.Workflow.ID, stored.Workflow.Revision, pending); err != nil {
		t.Fatal(err)
	}
	*pending.NativeRetryAfterSequence++
	owned, err := repo.Load(t.Context(), testOwnerID, stored.Workflow.ID)
	if err != nil || *owned.HDRPendingRequest.NativeRetryAfterSequence != boundary {
		t.Fatal("request checkpoint retained caller-owned boundary pointer")
	}
	pending = *owned.HDRPendingRequest
	if err := repo.CheckpointHDRRequest(t.Context(), "foreign", stored.Workflow.ID, stored.Workflow.Revision, pending); !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("foreign request checkpoint: %v", err)
	}
	if err := repo.CheckpointHDRRequest(t.Context(), testOwnerID, stored.Workflow.ID, stored.Workflow.Revision+1, pending); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale request checkpoint: %v", err)
	}
	pending.Instructions.Release.Generation++
	if err := repo.CheckpointHDRRequest(t.Context(), testOwnerID, stored.Workflow.ID, stored.Workflow.Revision, pending); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("different-generation request checkpoint: %v", err)
	}
	builder.failRender = false
	command.IdempotencyKey = "retry"
	retry := executeCommand(t, module, command)
	if retry.HDRAnalysis == nil || !retry.Workflow.HDRAnalysisEnabled {
		t.Fatalf("retry=%#v", retry)
	}
	stored, err = repo.Load(t.Context(), testOwnerID, retry.Workflow.ID)
	if err != nil || stored.HDRPendingRequest != nil {
		t.Fatalf("terminal publication retained pending request: %+v %v", stored.HDRPendingRequest, err)
	}
	replay := executeCommand(t, module, command)
	if replay.HDRAnalysis.ID != retry.HDRAnalysis.ID || builder.builds != 2 {
		t.Fatal("idempotent HDR replay reran analysis")
	}
	if _, err := module.HDRAnalysisArtifact(
		t.Context(),
		"foreign",
		retry.Workflow.ID,
		*retry.Workflow.HDRAnalysis,
		retry.HDRAnalysis.Targets[0].Artifact.ID,
	); !errors.Is(
		err,
		ErrWorkflowNotFound,
	) {
		t.Fatalf("foreign owner=%v", err)
	}
	disabled := executeCommand(t, module, SetHDRAnalysisEnabledCommand{
		WorkflowID:       retry.Workflow.ID,
		ExpectedRevision: retry.Workflow.Revision,
		Enabled:          false,
	})
	if disabled.Workflow.HDRAnalysis == nil || disabled.Workflow.HDRAnalysisEnabled {
		t.Fatal("disable lost inspectable HDR result")
	}
	content, err := module.HDRAnalysisArtifact(
		t.Context(),
		testOwnerID,
		disabled.Workflow.ID,
		*disabled.Workflow.HDRAnalysis,
		retry.HDRAnalysis.Targets[0].Artifact.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = content.Body.Close()
	canceled := executeCommand(t, module, CancelWorkflowCommand{WorkflowID: disabled.Workflow.ID, ExpectedRevision: disabled.Workflow.Revision})
	stored, err = repo.Load(t.Context(), testOwnerID, canceled.Workflow.ID)
	if err != nil || len(stored.HDRExtractions) != 0 || len(stored.HDRAnalyses) != 0 {
		t.Fatalf("canceled registry retained authority: %#v %v", stored, err)
	}
}

func TestHDRSourceRecoveryPreservesNewerV1Checkpoint(t *testing.T) {
	builder := &hdrBuilderFixture{unavailable: true}
	module, repo := newTestModule(t, testPreparer(), WithHDRAnalysisBuilder(builder))
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "hdr-source-recovery"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "Synthetic.HDR.mkv"},
	})
	command := AnalyzeHDRCommand{
		WorkflowID:       prepared.Workflow.ID,
		ExpectedRevision: prepared.Workflow.Revision,
		IdempotencyKey:   "unavailable",
		Instructions: api.HDRAnalysisInstructions{
			ProfileVersion: api.HDRAnalysisProfileVersion,
			Release:        api.ReleaseRef{SourcePath: prepared.Release.Release.Source.SourcePath, Generation: 1},
			TargetIDs:      []string{api.HDRTargetID("source", "")},
		},
	}
	failed := executeCommand(t, module, command)
	if failed.HDRAnalysis == nil || !failed.HDRAnalysis.TargetNeedsSourceRetry(command.Instructions.TargetIDs[0]) {
		t.Fatal("missing prior failure")
	}
	command.ExpectedRevision, command.IdempotencyKey = failed.Workflow.Revision, "recover"
	builder.unavailable, builder.failRender = false, true
	builder.failBeforePublish = true
	if _, err := module.execute(t.Context(), testOwnerID, command); err == nil {
		t.Fatal("interrupted extraction completed")
	}
	retired, err := repo.Load(t.Context(), testOwnerID, command.WorkflowID)
	if err != nil || len(retired.HDRExtractions) != 0 || retired.HDRPendingRequest == nil {
		t.Fatalf("old evidence was not retired: %+v %v", retired, err)
	}
	builder.failBeforePublish = false
	command.IdempotencyKey = "recover-after-retirement"
	if _, err := module.execute(t.Context(), testOwnerID, command); err == nil {
		t.Fatal("interrupted recovery completed")
	}
	before, err := repo.Load(t.Context(), testOwnerID, command.WorkflowID)
	if err != nil || len(before.HDRExtractions) != 1 || before.HDRPendingRequest == nil {
		t.Fatalf("recovery checkpoint lost: %+v %v", before, err)
	}
	for _, record := range before.HDRExtractions {
		if record.Sequence <= 1 {
			t.Fatal("retirement reset the extraction sequence")
		}
	}
	builder.failRender = false
	command.IdempotencyKey = "resume"
	resumed := executeCommand(t, module, command)
	after, err := repo.Load(t.Context(), testOwnerID, command.WorkflowID)
	if err != nil || !maps.Equal(before.HDRExtractions, after.HDRExtractions) || resumed.HDRAnalysis.Status != api.StageStatusCompleted {
		t.Fatalf("resume retired completed recovery evidence: before=%+v after=%+v error=%v", before.HDRExtractions, after.HDRExtractions, err)
	}
}

func TestHDRPartialResultCannotEnableRequestedInclusion(t *testing.T) {
	builder := &hdrBuilderFixture{partial: true}
	module, _ := newTestModule(t, testPreparer(), WithHDRAnalysisBuilder(builder))
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "hdr-partial"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "Synthetic.HDR.mkv"},
	})
	ids := []string{api.HDRTargetID("first", ""), api.HDRTargetID("second", "")}
	analysis := executeCommand(t, module, AnalyzeHDRCommand{
		WorkflowID:       prepared.Workflow.ID,
		ExpectedRevision: prepared.Workflow.Revision,
		Instructions: api.HDRAnalysisInstructions{
			ProfileVersion: api.HDRAnalysisProfileVersion,
			Release:        api.ReleaseRef{SourcePath: "Synthetic.HDR.mkv", Generation: 1},
			TargetIDs:      ids,
		},
	})
	if analysis.Workflow.HDRAnalysisEnabled || analysis.HDRAnalysis.Status != api.StageStatusPartial {
		t.Fatalf("partial=%#v", analysis)
	}
	_, err := module.execute(t.Context(), testOwnerID, SetHDRAnalysisEnabledCommand{
		WorkflowID:       analysis.Workflow.ID,
		ExpectedRevision: analysis.Workflow.Revision,
		Enabled:          true,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("partial enabled=%v", err)
	}
	current, err := module.Current(t.Context(), testOwnerID, analysis.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	instructions, err := requestedHDRInstructions(current, &api.HDRAnalysisRequest{TargetIDs: ids})
	if err != nil || requestedHDRMatches(analysis, instructions) {
		t.Fatalf("partial selection=%v", err)
	}
	if _, ok := api.AsHDRAnalysisFailure(requestedHDRFailure("hdr-analysis-failed", current, &api.HDRAnalysisRequest{TargetIDs: ids})); !ok {
		t.Fatal("requested partial failure was not typed")
	}
	content, err := module.HDRAnalysisArtifact(
		t.Context(),
		testOwnerID,
		analysis.Workflow.ID,
		*analysis.Workflow.HDRAnalysis,
		analysis.HDRAnalysis.Targets[0].Artifact.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = content.Body.Close()
}

func TestHDRExplicitRetryRetiresUnavailableMetadataWithoutPlot(t *testing.T) {
	builder := &hdrBuilderFixture{unavailable: true}
	module, repo := newTestModule(t, testPreparer(), WithHDRAnalysisBuilder(builder))
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "hdr-missing-metadata"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "Synthetic.HDR.mkv"},
	})
	instructions := api.HDRAnalysisInstructions{
		ProfileVersion: api.HDRAnalysisProfileVersion,
		Release:        api.ReleaseRef{SourcePath: "Synthetic.HDR.mkv", Generation: 1},
		TargetIDs:      []string{api.HDRTargetID("source", "")},
	}
	first := executeCommand(t, module, AnalyzeHDRCommand{
		WorkflowID:       prepared.Workflow.ID,
		ExpectedRevision: prepared.Workflow.Revision,
		Instructions:     instructions,
	})
	before, err := repo.Load(t.Context(), testOwnerID, first.Workflow.ID)
	if err != nil || len(before.HDRExtractions) != 1 || first.HDRAnalysis.Status != api.StageStatusFailed {
		t.Fatalf("initial missing metadata result=%#v err=%v", first, err)
	}
	builder.unavailable = false
	retry := executeCommand(t, module, AnalyzeHDRCommand{
		WorkflowID:       first.Workflow.ID,
		ExpectedRevision: first.Workflow.Revision,
		Instructions:     instructions,
	})
	after, err := repo.Load(t.Context(), testOwnerID, retry.Workflow.ID)
	if err != nil || len(after.HDRExtractions) != 1 || !retry.Workflow.HDRAnalysisEnabled {
		t.Fatalf("retry=%#v err=%v", retry, err)
	}
	for id := range before.HDRExtractions {
		if _, exists := after.HDRExtractions[id]; exists {
			t.Fatal("explicit retry preserved unusable metadata authority")
		}
	}
}

type hdrCleanupBuilderFixture struct {
	hdrInputRestorerFixture
	beforeAllocation     func(context.Context, api.ReleaseRef)
	stopBeforeCheckpoint bool
	cleaned              []api.ReleaseRef
	protected            map[api.HDRExtractionID]HDRExtractionRecord
}

type hdrAttemptCheckpointFailureRepository struct{ *MemoryRepository }

func (r hdrAttemptCheckpointFailureRepository) SaveOperation(ctx context.Context, sequence uint64, record api.ReleaseWorkflowOperationRecord) error {
	if record.HDRCleanupRelease != (api.ReleaseRef{}) {
		return errors.New("synthetic HDR cleanup checkpoint failure")
	}
	return r.MemoryRepository.SaveOperation(ctx, sequence, record)
}

func (b *hdrCleanupBuilderFixture) Build(
	ctx context.Context,
	preparation HDRPreparation,
	instructions api.HDRAnalysisInstructions,
	attempt string,
	now time.Time,
	prior *api.HDRAnalysisResult,
	priorResource RetainedHDRAnalysisResource,
	entries map[api.HDRExtractionID]HDRExtractionRecord,
	resources map[api.HDRExtractionID]RetainedHDRExtractionResource,
	publish func(HDRExtractionRecord, RetainedHDRExtractionResource) error,
	measurements *HDRMeasurementStore,
) (api.HDRAnalysisResult, RetainedHDRAnalysisResource, error) {
	release := preparation.Release
	if b.beforeAllocation != nil {
		b.beforeAllocation(ctx, release)
	}
	if b.stopBeforeCheckpoint {
		return api.HDRAnalysisResult{}, nil, errors.New("synthetic interruption before metadata checkpoint")
	}
	return b.hdrBuilderFixture.Build(ctx, preparation, instructions, attempt, now, prior, priorResource, entries, resources, publish, measurements)
}

func (b *hdrCleanupBuilderFixture) CloneExtractions(
	ctx context.Context,
	preparation HDRPreparation,
	entries map[api.HDRExtractionID]HDRExtractionRecord,
	resources map[api.HDRExtractionID]RetainedHDRExtractionResource,
	attempt string,
) (map[api.HDRExtractionID]HDRExtractionRecord, map[api.HDRExtractionID]RetainedHDRExtractionResource, error) {
	release := preparation.Release
	if b.beforeAllocation != nil {
		b.beforeAllocation(ctx, release)
	}
	if b.stopBeforeCheckpoint {
		return nil, nil, errors.New("synthetic interruption before rebind checkpoint")
	}
	return b.hdrInputRestorerFixture.CloneExtractions(ctx, preparation, entries, resources, attempt)
}

func (b *hdrCleanupBuilderFixture) CleanupAttempt(release api.ReleaseRef, _ string, entries map[api.HDRExtractionID]HDRExtractionRecord, _ map[string]HDRMeasurementRecord) error {
	b.cleaned = append(b.cleaned, release)
	b.protected = maps.Clone(entries)
	return nil
}

func TestHDRPreparationRecoveryHasAuthorityBeforeAllocation(t *testing.T) {
	for _, test := range []struct {
		name              string
		reset             bool
		rebind            bool
		checkpoint        bool
		checkpointFailure bool
	}{
		{name: "first before checkpoint"},
		{name: "first after checkpoint", checkpoint: true},
		{name: "reset before checkpoint", reset: true},
		{
			name:       "reset after checkpoint",
			reset:      true,
			checkpoint: true,
		},
		{
			name:   "rebind before checkpoint",
			reset:  true,
			rebind: true,
		},
		{
			name:       "rebind after checkpoint",
			reset:      true,
			rebind:     true,
			checkpoint: true,
		},
		{name: "checkpoint failure prevents allocation", checkpointFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			const source = "Synthetic.Disc"
			target := api.HDRTargetID(source, "00001.MPLS")
			builder := &hdrCleanupBuilderFixture{}
			preparer := testPreparer()
			prepare := preparer.PrepareFunc
			var generation api.PreparedGeneration
			preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				result, err := prepare(ctx, input)
				generation++
				result.Release.Generation = generation
				return result, err
			}
			preparer.DisplayFunc = func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
				return api.PreparedReleaseDisplay{HDRTargets: []api.HDRAnalysisTarget{{
					ID:              target,
					Label:           "Disc 1",
					Supported:       true,
					SelectionPolicy: "primary_hevc_angle_zero",
				}}}, nil
			}
			module, repo := newTestModule(t, preparer, WithHDRAnalysisBuilder(builder))
			before := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "hdr-preparation-recovery"})
			if test.reset {
				before = executeCommand(t, module, PrepareReleaseCommand{
					WorkflowID:       before.Workflow.ID,
					ExpectedRevision: before.Workflow.Revision,
					Input:            api.PrepareInput{SourcePath: source},
				})
			}
			if test.rebind {
				before = executeCommand(t, module, AnalyzeHDRCommand{
					WorkflowID:       before.Workflow.ID,
					ExpectedRevision: before.Workflow.Revision,
					Instructions: api.HDRAnalysisInstructions{
						ProfileVersion: api.HDRAnalysisProfileVersion,
						Release:        api.ReleaseRef{SourcePath: source, Generation: generation},
						TargetIDs:      []string{target},
					},
				})
			}
			var observed []api.ReleaseRef
			builder.beforeAllocation = func(ctx context.Context, release api.ReleaseRef) {
				id, _ := ctx.Value(operationExecutionContextKey{}).(api.WorkflowOperationID)
				record, err := repo.LoadOperation(ctx, testOwnerID, before.Workflow.ID, id)
				if err != nil {
					t.Errorf("load authority before allocation: %v", err)
				}
				observed = append(observed, record.HDRCleanupRelease)
				if record.HDRCleanupRelease != release {
					t.Errorf("allocation release=%v durable cleanup authority=%v", release, record.HDRCleanupRelease)
				}
			}
			builder.stopBeforeCheckpoint = !test.checkpoint
			builder.failRender = true
			if test.checkpointFailure {
				module.operations = hdrAttemptCheckpointFailureRepository{repo}
			}
			input := api.PrepareInput{SourcePath: source, Controls: api.PreparationControls{CaptureHDRMetadata: true}}
			var command Command = PrepareReleaseCommand{
				WorkflowID:       before.Workflow.ID,
				ExpectedRevision: before.Workflow.Revision,
				Input:            input,
			}
			if test.reset {
				command = ResetReleaseCommand{
					WorkflowID:       before.Workflow.ID,
					ExpectedRevision: before.Workflow.Revision,
					Input:            input,
				}
			}
			operation, err := module.Start(t.Context(), testOwnerID, command)
			if err != nil {
				t.Fatal(err)
			}
			waitForWorkflowOperation(t, module, before.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool { return status.Status == api.StageStatusFailed })
			want := api.ReleaseRef{SourcePath: source, Generation: generation}
			if test.checkpointFailure {
				if len(observed) != 0 {
					t.Fatal("HDR allocated resources without durable cleanup authority")
				}
				return
			}
			if len(observed) == 0 {
				t.Fatal("preparation never entered HDR allocation")
			}
			state, err := repo.Load(t.Context(), testOwnerID, before.Workflow.ID)
			if err != nil || state.Workflow.Revision != before.Workflow.Revision {
				t.Fatalf("failed preparation advanced aggregate authority: revision=%d err=%v", state.Workflow.Revision, err)
			}
			for _, snapshot := range state.Releases {
				if snapshot.Release.Generation == want.Generation {
					t.Fatal("unfinished preparation committed a release snapshot")
				}
			}
			record, err := repo.LoadOperation(t.Context(), testOwnerID, before.Workflow.ID, operation.ID)
			if err != nil || record.HDRCleanupRelease != want {
				t.Fatalf("attempt authority=%v want=%v err=%v", record.HDRCleanupRelease, want, err)
			}
			if err := module.cleanupInterruptedHDR(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, release := range builder.cleaned {
				if release == want {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("cleanup authority absent or duplicated: cleaned=%v want=%v", builder.cleaned, want)
			}
			if !maps.Equal(builder.protected, state.HDRExtractions) {
				t.Fatal("cleanup did not protect complete checkpointed metadata")
			}
			newMetadata := false
			for _, entry := range state.HDRExtractions {
				newMetadata = newMetadata || entry.Release == want
			}
			if newMetadata != test.checkpoint {
				t.Fatalf("new metadata checkpoint=%v want=%v", newMetadata, test.checkpoint)
			}
			if test.checkpoint {
				record.HDRCleanupRelease = api.ReleaseRef{}
				builder.cleaned = nil
				if err := module.cleanupInterruptedHDR(t.Context(), record); err != nil || !slices.Contains(builder.cleaned, want) {
					t.Fatalf("legacy checkpoint recovery cleaned=%v err=%v", builder.cleaned, err)
				}
			}
		})
	}
}

func TestMixedTargetDecodedRetryRetainsSelection(t *testing.T) {
 b := &hdrBuilderFixture{decodedOnly: true, partial: true}
 m, _ := newTestModule(t, testPreparer(), WithHDRAnalysisBuilder(b))
 created := executeCommand(t, m, CreateWorkflowCommand{WorkflowID: "review-mixed"})
 prepared := executeCommand(t, m, PrepareReleaseCommand{
WorkflowID: created.Workflow.ID,
 ExpectedRevision: created.Workflow.Revision,
 Input: api.PrepareInput{SourcePath: "Synthetic.HDR.mkv"},
})
 ids := []string{api.HDRTargetID("first", ""), api.HDRTargetID("second", "")}
 instructions := api.HDRAnalysisInstructions{
Release: api.ReleaseRef{SourcePath: prepared.Release.Release.Source.SourcePath, Generation: prepared.Release.Release.Generation},
 TargetIDs: ids,
 ProfileVersion: api.HDRAnalysisProfileV2,
 Decoded: &api.HDRDecodedOptions{Audio: true},
}
 initial := executeCommand(t, m, AnalyzeHDRCommand{
WorkflowID: prepared.Workflow.ID,
 ExpectedRevision: prepared.Workflow.Revision,
 Instructions: instructions,
 IdempotencyKey: "initial",
})
 if initial.HDRAnalysis.Targets[0].Decoded[0].State != api.HDRDecodedComplete || initial.HDRAnalysis.Targets[1].Decoded[0].State != api.HDRDecodedFailed { t.Fatal("fixture does not have mixed outcomes") }
 instructions.DecodedRetry = &api.HDRDecodedRetry{AnalysisAttemptID: initial.HDRAnalysis.AttemptID, Modes: []api.HDRDecodedMode{api.HDRDecodedAudio}}
 retried := executeCommand(t,m,AnalyzeHDRCommand{
WorkflowID:initial.Workflow.ID,
ExpectedRevision:initial.Workflow.Revision,
Instructions:instructions,
IdempotencyKey:"retry",
})
 if b.builds != 2 || len(retried.HDRAnalysis.Targets)!=2 {t.Fatal("mixed retry did not preserve target selection")}
}
