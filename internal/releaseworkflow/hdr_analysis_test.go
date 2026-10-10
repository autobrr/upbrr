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
	builds        int
	partial       bool
	failRender    bool
	unavailable   bool
	pngOnly       bool
	checkpoint    func(HDRExtractionRecord)
	prior         *api.HDRAnalysisResult
	priorResource RetainedHDRAnalysisResource
}

func (b *hdrBuilderFixture) Build(
	_ context.Context,
	release api.ReleaseRef,
	instructions api.HDRAnalysisInstructions,
	attempt string,
	now time.Time,
	prior *api.HDRAnalysisResult,
	priorResource RetainedHDRAnalysisResource,
	entries map[api.HDRExtractionID]HDRExtractionRecord,
	_ map[api.HDRExtractionID]RetainedHDRExtractionResource,
	publish func(HDRExtractionRecord, RetainedHDRExtractionResource) error,
) (api.HDRAnalysisResult, RetainedHDRAnalysisResource, error) {
	b.builds++
	b.prior, b.priorResource = prior, priorResource
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
		Release:             release,
		AttemptID:           attempt,
		ManifestFingerprint: "manifest",
		TargetIDs:           instructions.TargetIDs,
		PeakSource:          instructions.PeakSource,
		ProfileVersion:      instructions.ProfileVersion,
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
		result.Targets = append(result.Targets, item)
	}
	if b.unavailable {
		return result, nil, nil
	}
	return result, hdrResourceFixture{}, nil
}

type hdrResourceFixture struct{}

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
				Instructions: api.HDRAnalysisInstructions{Release: api.ReleaseRef{SourcePath: "Synthetic.HDR.mkv", Generation: 1},
					TargetIDs: []string{api.HDRTargetID("first", ""), api.HDRTargetID("second", "")}},
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
		Release:   api.ReleaseRef{SourcePath: prepared.Release.Release.Source.SourcePath, Generation: 1},
		TargetIDs: []string{api.HDRTargetID("source", "")},
	}
	builder.checkpoint = func(record HDRExtractionRecord) {
		stored, err := repo.Load(t.Context(), testOwnerID, prepared.Workflow.ID)
		if err != nil || stored.HDRExtractions[record.ID].SHA256 != record.SHA256 || stored.Workflow.Revision != prepared.Workflow.Revision {
			t.Fatalf("metadata not checkpointed before render: %#v %v", stored, err)
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
	builder.failRender = false
	command.IdempotencyKey = "retry"
	retry := executeCommand(t, module, command)
	if retry.HDRAnalysis == nil || !retry.Workflow.HDRAnalysisEnabled {
		t.Fatalf("retry=%#v", retry)
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
		Instructions:     api.HDRAnalysisInstructions{Release: api.ReleaseRef{SourcePath: "Synthetic.HDR.mkv", Generation: 1}, TargetIDs: ids},
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
	if err != nil || !requestedHDRMatches(analysis, instructions) {
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
		Release:   api.ReleaseRef{SourcePath: "Synthetic.HDR.mkv", Generation: 1},
		TargetIDs: []string{api.HDRTargetID("source", "")},
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
	release api.ReleaseRef,
	instructions api.HDRAnalysisInstructions,
	attempt string,
	now time.Time,
	prior *api.HDRAnalysisResult,
	priorResource RetainedHDRAnalysisResource,
	entries map[api.HDRExtractionID]HDRExtractionRecord,
	resources map[api.HDRExtractionID]RetainedHDRExtractionResource,
	publish func(HDRExtractionRecord, RetainedHDRExtractionResource) error,
) (api.HDRAnalysisResult, RetainedHDRAnalysisResource, error) {
	if b.beforeAllocation != nil {
		b.beforeAllocation(ctx, release)
	}
	if b.stopBeforeCheckpoint {
		return api.HDRAnalysisResult{}, nil, errors.New("synthetic interruption before metadata checkpoint")
	}
	return b.hdrBuilderFixture.Build(ctx, release, instructions, attempt, now, prior, priorResource, entries, resources, publish)
}

func (b *hdrCleanupBuilderFixture) CloneExtractions(
	ctx context.Context,
	release api.ReleaseRef,
	entries map[api.HDRExtractionID]HDRExtractionRecord,
	resources map[api.HDRExtractionID]RetainedHDRExtractionResource,
	attempt string,
) (map[api.HDRExtractionID]HDRExtractionRecord, map[api.HDRExtractionID]RetainedHDRExtractionResource, error) {
	if b.beforeAllocation != nil {
		b.beforeAllocation(ctx, release)
	}
	if b.stopBeforeCheckpoint {
		return nil, nil, errors.New("synthetic interruption before rebind checkpoint")
	}
	return b.hdrInputRestorerFixture.CloneExtractions(ctx, release, entries, resources, attempt)
}

func (b *hdrCleanupBuilderFixture) CleanupAttempt(release api.ReleaseRef, _ string, entries map[api.HDRExtractionID]HDRExtractionRecord) error {
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
name: "reset after checkpoint",
 reset: true,
 checkpoint: true,
},
		{
name: "rebind before checkpoint",
 reset: true,
 rebind: true,
},
		{
name: "rebind after checkpoint",
 reset: true,
 rebind: true,
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
					ID: target,
 Label: "Disc 1",
 Supported: true,
 SelectionPolicy: "primary_hevc_angle_zero",
				}}}, nil
			}
			module, repo := newTestModule(t, preparer, WithHDRAnalysisBuilder(builder))
			before := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "hdr-preparation-recovery"})
			if test.reset {
				before = executeCommand(t, module, PrepareReleaseCommand{
					WorkflowID: before.Workflow.ID,
 ExpectedRevision: before.Workflow.Revision,
 Input: api.PrepareInput{SourcePath: source},
				})
			}
			if test.rebind {
				before = executeCommand(t, module, AnalyzeHDRCommand{
					WorkflowID: before.Workflow.ID,
 ExpectedRevision: before.Workflow.Revision,
					Instructions: api.HDRAnalysisInstructions{Release: api.ReleaseRef{SourcePath: source, Generation: generation}, TargetIDs: []string{target}},
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
WorkflowID: before.Workflow.ID,
 ExpectedRevision: before.Workflow.Revision,
 Input: input,
}
			if test.reset {
				command = ResetReleaseCommand{
WorkflowID: before.Workflow.ID,
 ExpectedRevision: before.Workflow.Revision,
 Input: input,
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
