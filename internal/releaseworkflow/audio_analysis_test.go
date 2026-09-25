// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type audioAnalysisBuilderFake struct {
	mu                sync.Mutex
	builds            int
	incrementalBuilds int
	block             <-chan struct{}
	started           chan struct{}
	startOnce         sync.Once
	resource          RetainedAudioAnalysisResource
	retainOnCancel    bool
	interrupt         bool
	failAfterBlock    bool
}

func (b *audioAnalysisBuilderFake) Build(
	ctx context.Context,
	release api.ReleaseRef,
	instructions api.AudioAnalysisInstructions,
	attemptID string,
	now time.Time,
	prior *api.AudioAnalysisResult,
	priorResource RetainedAudioAnalysisResource,
) (api.AudioAnalysisResult, RetainedAudioAnalysisResource, error) {
	if b.started != nil {
		b.startOnce.Do(func() { close(b.started) })
	}
	if prior != nil {
		b.mu.Lock()
		b.incrementalBuilds++
		b.mu.Unlock()
		if prior.Status != api.StageStatusPartial || priorResource == nil {
			return api.AudioAnalysisResult{}, nil, ErrInvalidTransition
		}
		result := partialAudioAnalysisForTest(release, instructions, attemptID, now)
		result.Status = api.StageStatusCompleted
		result.Tracks[0].Status = api.StageStatusCompleted
		result.Tracks[0].Artifacts[1] = api.AudioAnalysisArtifact{
			ID:      "spectrogram-retry",
			Variant: api.AudioAnalysisSpectrogram,
			Status:  api.StageStatusCompleted,
			Width:   3141,
			Height:  1105,
		}
		return result, b.retainedResource(), nil
	}
	b.mu.Lock()
	b.builds++
	b.mu.Unlock()
	if b.block != nil {
		select {
		case <-ctx.Done():
			if b.retainOnCancel {
				result := partialAudioAnalysisForTest(release, instructions, attemptID, now)
				failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureCanceled, Message: "audio analysis was canceled"}
				result.Status = api.StageStatusCanceled
				result.Tracks[0].Failure = &failure
				result.Tracks[0].Artifacts[1].Failure = &failure
				return result, b.retainedResource(), nil
			}
			return api.AudioAnalysisResult{}, nil, fmt.Errorf("fake audio analysis canceled: %w", ctx.Err())
		case <-b.block:
			if b.failAfterBlock {
				return api.AudioAnalysisResult{}, nil, errors.New("synthetic abandoned audio analysis")
			}
		}
	}
	if b.interrupt {
		result := partialAudioAnalysisForTest(release, instructions, attemptID, now)
		failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureInterrupted, Message: "audio analysis timed out"}
		result.Status = api.StageStatusInterrupted
		result.Tracks[0].Failure = &failure
		result.Tracks[0].Artifacts[1].Failure = &failure
		return result, b.retainedResource(), nil
	}
	return partialAudioAnalysisForTest(release, instructions, attemptID, now), b.retainedResource(), nil
}

func (b *audioAnalysisBuilderFake) retainedResource() RetainedAudioAnalysisResource {
	if b.resource != nil {
		return b.resource
	}
	return audioAnalysisResourceFake{}
}

func (b *audioAnalysisBuilderFake) counts() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.builds, b.incrementalBuilds
}

type audioAnalysisResourceFake struct{}

func (audioAnalysisResourceFake) OpenArtifact(
	context.Context,
	api.AudioAnalysisResult,
	api.PublicResourceID,
) (MediaArtifactContent, error) {
	return MediaArtifactContent{Body: io.NopCloser(strings.NewReader("png")), ContentType: "image/png"}, nil
}

type audioAnalysisReleaseProbe struct {
	mu       sync.Mutex
	releases int
}

func (*audioAnalysisReleaseProbe) OpenArtifact(
	context.Context,
	api.AudioAnalysisResult,
	api.PublicResourceID,
) (MediaArtifactContent, error) {
	return MediaArtifactContent{Body: io.NopCloser(strings.NewReader("png")), ContentType: "image/png"}, nil
}

func (p *audioAnalysisReleaseProbe) Release() error {
	p.mu.Lock()
	p.releases++
	p.mu.Unlock()
	return nil
}

func (p *audioAnalysisReleaseProbe) releaseCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.releases
}

func TestAudioAnalysisSettingInvalidatesDescriptions(t *testing.T) {
	t.Parallel()
	state := &State{Workflow: api.ReleaseWorkflow{
		AudioAnalysisEnabled: true,
		AudioAnalysis:        &api.AudioAnalysisRef{ID: "analysis-1", Revision: 1},
		Descriptions:         &api.DescriptionSetRef{ID: "descriptions-1", Revision: 2},
		DryRun:               &api.UploadDryRunResultRef{ID: "dry-run-1", Revision: 3},
	}}
	(&Module{}).setAudioAnalysisEnabled(state, SetAudioAnalysisEnabledCommand{Enabled: false})
	if state.Workflow.AudioAnalysis != nil || state.Workflow.Descriptions != nil || state.Workflow.DryRun != nil {
		t.Fatalf("disabled audio analysis retained downstream descriptions: %#v", state.Workflow)
	}
}

func TestAudioAnalysisRetryRequiresCurrentProfile(t *testing.T) {
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1}
	instructions := api.AudioAnalysisInstructions{
		ResourceID:     "resource-1",
		Selection:      api.AudioAnalysisSelectionPrimary,
		TrackIDs:       []string{"track-1"},
		Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
		ProfileVersion: api.AudioAnalysisProfileVersion,
	}
	prior := api.AudioAnalysisResult{
		Release:        release,
		ResourceID:     instructions.ResourceID,
		Selection:      instructions.Selection,
		TrackIDs:       instructions.TrackIDs,
		Variants:       instructions.Variants,
		ProfileVersion: instructions.ProfileVersion,
	}
	if !audioAnalysisRetryCompatible(prior, instructions, release) {
		t.Fatal("current-profile artifact was not reusable")
	}
	prior.ProfileVersion = "audio-analysis-v2"
	if audioAnalysisRetryCompatible(prior, instructions, release) {
		t.Fatal("old-profile waveform artifact was reusable")
	}
}

func TestModuleAudioAnalysisRetriesIncrementallyAndPersistsDisabledIntent(t *testing.T) {
	t.Parallel()

	builder := &audioAnalysisBuilderFake{}
	module, repository := newTestModule(t, audioAnalysisPreparerForTest(), WithAudioAnalysisBuilder(builder))
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-audio"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.mkv"},
	})
	instructions := audioAnalysisInstructionsForTest(*prepared.Release)
	firstCommand := AnalyzeAudioCommand{
		WorkflowID:       prepared.Workflow.ID,
		ExpectedRevision: prepared.Workflow.Revision,
		Instructions:     instructions,
		IdempotencyKey:   "analyze-audio-1",
	}
	first := executeCommand(t, module, firstCommand)
	if first.AudioAnalysis == nil || first.AudioAnalysis.Status != api.StageStatusPartial ||
		!first.Workflow.AudioAnalysisEnabled || first.Workflow.AudioAnalysis == nil {
		t.Fatalf("first analysis = %#v workflow=%#v", first.AudioAnalysis, first.Workflow)
	}
	replayed := executeCommand(t, module, firstCommand)
	if replayed.AudioAnalysis == nil || replayed.AudioAnalysis.ID != first.AudioAnalysis.ID {
		t.Fatalf("idempotent replay = %#v", replayed.AudioAnalysis)
	}
	builds, retries := builder.counts()
	if builds != 1 || retries != 0 {
		t.Fatalf("builder counts after replay = builds:%d retries:%d", builds, retries)
	}
	module.clock = fixedClock{now: first.AudioAnalysis.CreatedAt.Add(48 * time.Hour)}
	content, err := module.AudioAnalysisArtifact(t.Context(), testOwnerID, first.Workflow.ID,
		*first.Workflow.AudioAnalysis, "waveform-first")
	if err != nil {
		t.Fatalf("open retained audio artifact after 48 hours: %v", err)
	}
	if err := content.Body.Close(); err != nil {
		t.Fatal(err)
	}

	retry := executeCommand(t, module, AnalyzeAudioCommand{
		WorkflowID:       first.Workflow.ID,
		ExpectedRevision: first.Workflow.Revision,
		Instructions:     instructions,
		IdempotencyKey:   "analyze-audio-2",
	})
	if retry.AudioAnalysis == nil || retry.AudioAnalysis.Status != api.StageStatusCompleted ||
		retry.AudioAnalysis.Tracks[0].Artifacts[0].ID != "waveform-first" ||
		retry.AudioAnalysis.Tracks[0].Artifacts[1].ID != "spectrogram-retry" {
		t.Fatalf("incremental retry = %#v", retry.AudioAnalysis)
	}
	builds, retries = builder.counts()
	if builds != 1 || retries != 1 {
		t.Fatalf("builder counts after retry = builds:%d retries:%d", builds, retries)
	}

	disabled := executeCommand(t, module, SetAudioAnalysisEnabledCommand{
		WorkflowID:       retry.Workflow.ID,
		ExpectedRevision: retry.Workflow.Revision,
		Enabled:          false,
		IdempotencyKey:   "disable-audio",
	})
	if disabled.Workflow.AudioAnalysisEnabled || disabled.Workflow.AudioAnalysis != nil || disabled.AudioAnalysis != nil {
		t.Fatalf("disabled workflow = %#v current=%#v", disabled.Workflow, disabled.AudioAnalysis)
	}
	stored, err := repository.Load(t.Context(), testOwnerID, disabled.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.AudioAnalyses) != 2 {
		t.Fatalf("retained analyses = %d, want 2", len(stored.AudioAnalyses))
	}
}

func TestModuleAudioAnalysisReplacesDisabledAttemptResource(t *testing.T) {
	t.Parallel()

	oldResource := &audioAnalysisReleaseProbe{}
	newResource := &audioAnalysisReleaseProbe{}
	builder := &audioAnalysisBuilderFake{resource: oldResource}
	module, _ := newTestModule(t, audioAnalysisPreparerForTest(), WithAudioAnalysisBuilder(builder))
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-audio-disabled-replacement"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.mkv"},
	})
	instructions := audioAnalysisInstructionsForTest(*prepared.Release)
	first := executeCommand(t, module, AnalyzeAudioCommand{
		WorkflowID:       prepared.Workflow.ID,
		ExpectedRevision: prepared.Workflow.Revision,
		Instructions:     instructions,
		IdempotencyKey:   "analyze-before-disable",
	})
	disabled := executeCommand(t, module, SetAudioAnalysisEnabledCommand{
		WorkflowID:       first.Workflow.ID,
		ExpectedRevision: first.Workflow.Revision,
		Enabled:          false,
		IdempotencyKey:   "disable-before-replacement",
	})
	if oldResource.releaseCount() != 0 {
		t.Fatal("disabling audio removed its retained artifact")
	}
	builder.resource = newResource
	replaced := executeCommand(t, module, AnalyzeAudioCommand{
		WorkflowID:       disabled.Workflow.ID,
		ExpectedRevision: disabled.Workflow.Revision,
		Instructions:     instructions,
		IdempotencyKey:   "analyze-after-disable",
	})
	if replaced.AudioAnalysis == nil || replaced.AudioAnalysis.AttemptID == first.AudioAnalysis.AttemptID {
		t.Fatalf("replacement audio result = %#v", replaced.AudioAnalysis)
	}
	if oldResource.releaseCount() != 1 || newResource.releaseCount() != 0 {
		t.Fatalf("artifact releases after replacement old=%d new=%d", oldResource.releaseCount(), newResource.releaseCount())
	}
}

func TestModuleTrackerProjectionPreservesAudioArtifactsUntilReleaseChanges(t *testing.T) {
	t.Parallel()

	resource := &audioAnalysisReleaseProbe{}
	projector := trackerProjectionBuilderFunc(func(
		_ context.Context,
		_ api.ReleaseSnapshot,
		_ api.UploadSubject,
		trackerIDs []api.TrackerID,
		_ map[api.TrackerID]api.TrackerProjectionInstructions,
		_ map[api.TrackerID]api.WorkflowFingerprint,
		_ api.WorkflowExecutionMode,
	) (
		api.TrackerCatalogSnapshot,
		api.TrackerRuntimeSnapshot,
		api.TrackerSelection,
		api.TrackerReleaseProjectionSet,
		error,
	) {
		return testCatalog(t), testRuntime(t), api.TrackerSelection{TrackerIDs: trackerIDs}, testProjectionSet(t), nil
	})
	preparer := audioAnalysisPreparerForTest()
	preparer.SubjectFunc = testPreparer().SubjectFunc
	module, _ := newTestModule(
		t,
		preparer,
		WithAudioAnalysisBuilder(&audioAnalysisBuilderFake{resource: resource}),
		WithTrackerProjectionBuilder(projector),
	)
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-audio-tracker-retention"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.mkv"},
	})
	analyzed := executeCommand(t, module, AnalyzeAudioCommand{
		WorkflowID:       prepared.Workflow.ID,
		ExpectedRevision: prepared.Workflow.Revision,
		Instructions:     audioAnalysisInstructionsForTest(*prepared.Release),
		IdempotencyKey:   "analyze-before-project",
	})
	analysisRef := *analyzed.Workflow.AudioAnalysis
	artifactID := analyzed.AudioAnalysis.Tracks[0].Artifacts[0].ID
	projected := executeCommand(t, module, ProjectTrackersCommand{
		WorkflowID:       analyzed.Workflow.ID,
		ExpectedRevision: analyzed.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"ALPHA", "BETA"},
		IdempotencyKey:   "project-after-analysis",
	})
	content, err := module.AudioAnalysisArtifact(t.Context(), testOwnerID, projected.Workflow.ID, analysisRef, artifactID)
	if err != nil {
		t.Fatalf("open audio artifact after tracker projection: %v", err)
	}
	if err := content.Body.Close(); err != nil {
		t.Fatalf("close retained audio artifact: %v", err)
	}
	if resource.releaseCount() != 0 || projected.Workflow.AudioAnalysis == nil || !projected.Workflow.AudioAnalysisEnabled {
		t.Fatalf("tracker projection invalidated audio resource: workflow=%#v releases=%d", projected.Workflow, resource.releaseCount())
	}

	reprepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       projected.Workflow.ID,
		ExpectedRevision: projected.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Changed.Release.2026.mkv"},
		IdempotencyKey:   "reprepare-after-analysis",
	})
	if reprepared.Workflow.AudioAnalysis != nil || reprepared.Workflow.AudioAnalysisEnabled || resource.releaseCount() != 1 {
		t.Fatalf("changed release retained stale audio resource: workflow=%#v releases=%d", reprepared.Workflow, resource.releaseCount())
	}
}

func TestModuleRecoveredAudioAnalysisDeletesUncommittedAttemptResource(t *testing.T) {
	now := time.Date(2026, time.September, 21, 6, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	privateStore := NewMemoryPrivateResourceStore()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstBuilder := &audioAnalysisBuilderFake{
		block:          releaseFirst,
		started:        firstStarted,
		failAfterBlock: true,
	}
	moduleA, err := New(
		repository,
		privateStore,
		audioAnalysisPreparerForTest(),
		WithAudioAnalysisBuilder(firstBuilder),
		WithClock(fixedClock{now: now}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithProcessEpoch("audio-epoch-a"),
	)
	if err != nil {
		t.Fatalf("new first audio module: %v", err)
	}
	created := executeCommand(t, moduleA, CreateWorkflowCommand{WorkflowID: "workflow-audio-recovery"})
	prepared := executeCommand(t, moduleA, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.mkv"},
	})
	operation, err := moduleA.Start(context.Background(), testOwnerID, AnalyzeAudioCommand{
		WorkflowID:       prepared.Workflow.ID,
		ExpectedRevision: prepared.Workflow.Revision,
		Instructions:     audioAnalysisInstructionsForTest(*prepared.Release),
		IdempotencyKey:   "recover-audio-attempt",
	})
	if err != nil {
		t.Fatalf("start first audio operation: %v", err)
	}
	select {
	case <-firstStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("first audio operation did not start")
	}
	moduleA.operationWorkersMu.Lock()
	firstWorker := moduleA.operationWorkers[operation.ID]
	moduleA.operationWorkersMu.Unlock()

	orphan := &audioAnalysisReleaseProbe{}
	if err := privateStore.Put(
		testOwnerID,
		prepared.Workflow.ID,
		audioAnalysisPrivateResourceID(string(operation.ID)),
		orphan,
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf("retain orphaned audio attempt: %v", err)
	}
	resumedBuilder := &audioAnalysisBuilderFake{}
	moduleB, err := New(
		repository,
		privateStore,
		audioAnalysisPreparerForTest(),
		WithAudioAnalysisBuilder(resumedBuilder),
		WithClock(fixedClock{now: now.Add(2 * workflowWorkLeaseTTL)}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithProcessEpoch("audio-epoch-b"),
	)
	if err != nil {
		t.Fatalf("new restarted audio module: %v", err)
	}
	if _, err := moduleB.Current(context.Background(), testOwnerID, prepared.Workflow.ID); err != nil {
		t.Fatalf("trigger audio operation recovery: %v", err)
	}
	resumed := waitForWorkflowOperation(t, moduleB, prepared.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
		return isTerminalProgressStatus(status.Status)
	})
	if resumed.Status != api.StageStatusPartial || orphan.releaseCount() != 1 {
		t.Fatalf("recovered audio operation = %#v orphan releases=%d", resumed, orphan.releaseCount())
	}
	builds, retries := resumedBuilder.counts()
	if builds != 1 || retries != 0 {
		t.Fatalf("resumed builder counts = builds:%d retries:%d", builds, retries)
	}

	close(releaseFirst)
	if firstWorker.done != nil {
		select {
		case <-firstWorker.done:
		case <-time.After(10 * time.Second):
			t.Fatal("first audio operation worker did not exit")
		}
	}
}

func TestModuleCancelAudioAnalysisRetainsCompletedResult(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	builder := &audioAnalysisBuilderFake{
		block:          block,
		started:        make(chan struct{}),
		retainOnCancel: true,
	}
	module, _ := newTestModule(t, audioAnalysisPreparerForTest(), WithAudioAnalysisBuilder(builder))
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-audio-cancel"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.mkv"},
	})
	operation, err := module.Start(t.Context(), testOwnerID, AnalyzeAudioCommand{
		WorkflowID:       prepared.Workflow.ID,
		ExpectedRevision: prepared.Workflow.Revision,
		Instructions:     audioAnalysisInstructionsForTest(*prepared.Release),
		IdempotencyKey:   "cancel-audio",
	})
	if err != nil {
		t.Fatal(err)
	}
	operation = waitForWorkflowOperation(t, module, prepared.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
		return status.Status == api.StageStatusRunning
	})
	select {
	case <-builder.started:
	case <-time.After(10 * time.Second):
		t.Fatal("audio analysis builder did not start")
	}
	canceled, err := module.CancelOperation(t.Context(), testOwnerID, operation.WorkflowID, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status == api.StageStatusRunning {
		canceled = waitForWorkflowOperation(t, module, prepared.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
			return status.Status == api.StageStatusCanceled
		})
	}
	if canceled.Status != api.StageStatusCanceled || canceled.Message != "Operation canceled." {
		t.Fatalf("canceled operation = %#v", canceled)
	}
	current, err := module.Current(t.Context(), testOwnerID, prepared.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.AudioAnalysis == nil || current.AudioAnalysis.Status != api.StageStatusCanceled ||
		current.AudioAnalysis.Tracks[0].Artifacts[0].Status != api.StageStatusCompleted ||
		current.AudioAnalysis.Tracks[0].Artifacts[1].Failure == nil ||
		current.AudioAnalysis.Tracks[0].Artifacts[1].Failure.Code != api.AudioAnalysisFailureCanceled ||
		current.Workflow.AudioAnalysis == nil || !current.Workflow.AudioAnalysisEnabled {
		t.Fatalf("current after cancellation = %#v", current)
	}
}

func TestModuleAudioAnalysisPersistsInterruptedResultAndOperation(t *testing.T) {
	t.Parallel()

	builder := &audioAnalysisBuilderFake{interrupt: true}
	module, _ := newTestModule(t, audioAnalysisPreparerForTest(), WithAudioAnalysisBuilder(builder))
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-audio-interrupted"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.mkv"},
	})
	operation, err := module.Start(t.Context(), testOwnerID, AnalyzeAudioCommand{
		WorkflowID:       prepared.Workflow.ID,
		ExpectedRevision: prepared.Workflow.Revision,
		Instructions:     audioAnalysisInstructionsForTest(*prepared.Release),
		IdempotencyKey:   "interrupt-audio",
	})
	if err != nil {
		t.Fatal(err)
	}
	operation = waitForWorkflowOperation(t, module, prepared.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
		return isTerminalProgressStatus(status.Status)
	})
	if operation.Status != api.StageStatusInterrupted || operation.Message != "Operation interrupted." || operation.Result == nil ||
		operation.Result.Kind != api.WorkflowOperationResultAudioAnalysis {
		t.Fatalf("interrupted operation = %#v", operation)
	}
	current, err := module.Current(t.Context(), testOwnerID, prepared.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.AudioAnalysis == nil || current.AudioAnalysis.Status != api.StageStatusInterrupted ||
		current.AudioAnalysis.Tracks[0].Failure == nil ||
		current.AudioAnalysis.Tracks[0].Failure.Code != api.AudioAnalysisFailureInterrupted {
		t.Fatalf("current after interruption = %#v", current)
	}
}

func TestLegacyWorkflowWithoutAudioAnalysisFieldsDefaultsDisabled(t *testing.T) {
	t.Parallel()

	module, repository := newTestModule(t, audioAnalysisPreparerForTest())
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-audio-legacy"})
	state, err := repository.Load(t.Context(), testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	record, err := workflowStateRecord(testOwnerID, state)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(record.Payload, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "AudioAnalyses")
	workflow, ok := legacy["Workflow"].(map[string]any)
	if !ok {
		t.Fatalf("legacy workflow payload = %#v", legacy["Workflow"])
	}
	delete(workflow, "audioAnalysis")
	delete(workflow, "audioAnalysisEnabled")
	record.Payload, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := decodeWorkflowState(record)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Workflow.AudioAnalysisEnabled || loaded.Workflow.AudioAnalysis != nil || loaded.AudioAnalyses == nil || len(loaded.AudioAnalyses) != 0 {
		t.Fatalf("legacy audio analysis state = enabled:%v ref:%#v results:%#v", loaded.Workflow.AudioAnalysisEnabled, loaded.Workflow.AudioAnalysis, loaded.AudioAnalyses)
	}
}

func partialAudioAnalysisForTest(
	release api.ReleaseRef,
	instructions api.AudioAnalysisInstructions,
	attemptID string,
	now time.Time,
) api.AudioAnalysisResult {
	completed := now.Add(time.Second)
	failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureOutput, Message: "could not publish analysis image"}
	return api.AudioAnalysisResult{
		Release:             release,
		ResourceID:          instructions.ResourceID,
		ManifestFingerprint: "manifest-audio",
		AttemptID:           attemptID,
		Selection:           instructions.Selection,
		TrackIDs:            append([]string(nil), instructions.TrackIDs...),
		Variants:            append([]api.AudioAnalysisVariant(nil), instructions.Variants...),
		ProfileVersion:      instructions.ProfileVersion,
		ResourceLimits:      instructions.ResourceLimits,
		Status:              api.StageStatusPartial,
		Tracks: []api.AudioAnalysisTrackResult{{
			TrackID:      "audio-track-1",
			Ordinal:      1,
			Channels:     2,
			SampleRate:   48_000,
			SampleFrames: 96_000,
			Duration:     2,
			Status:       api.StageStatusPartial,
			Artifacts: []api.AudioAnalysisArtifact{
				{
					ID:      "waveform-first",
					Variant: api.AudioAnalysisWaveform,
					Status:  api.StageStatusCompleted,
					Width:   1812,
					Height:  340,
				},
				{
					Variant: api.AudioAnalysisSpectrogram,
					Status:  api.StageStatusFailed,
					Failure: &failure,
				},
			},
		}},
		CreatedAt:   now,
		CompletedAt: &completed,
	}
}

func audioAnalysisPreparerForTest() ReleasePreparerFunc {
	return ReleasePreparerFunc{
		PrepareFunc: func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			return api.PrepareResult{Release: api.PreparedRelease{
				Generation: 1,
				Source:     api.SourceManifest{SourcePath: input.SourcePath},
				Naming:     api.NamingFacts{ReleaseName: "Example.Release.2026-GRP"},
				Media: api.MediaFacts{
					PrimaryAudioTrackID:   "audio-track-1",
					TrackCoverageComplete: true,
					Tracks: []api.MediaTrackFacts{{
						ID:                  "audio-track-1",
						Kind:                api.MediaTrackAudio,
						ResourceID:          "resource-1",
						ManifestFingerprint: "manifest-audio",
						Ordinal:             1,
						Codec:               "FLAC",
						ChannelLayout:       "stereo",
						Channels:            2,
						SampleRate:          48_000,
					}},
				},
			}}, nil
		},
		DisplayFunc: func(_ context.Context, _ api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
			return api.PreparedReleaseDisplay{ReleaseName: "Example.Release.2026-GRP"}, nil
		},
	}
}

func audioAnalysisInstructionsForTest(release api.ReleaseSnapshot) api.AudioAnalysisInstructions {
	return api.AudioAnalysisInstructions{
		Release:        api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation},
		ResourceID:     "resource-1",
		Selection:      api.AudioAnalysisSelectionPrimary,
		TrackIDs:       []string{"audio-track-1"},
		Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform, api.AudioAnalysisSpectrogram},
		ProfileVersion: api.AudioAnalysisProfileVersion,
	}
}
