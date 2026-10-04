// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCompositeUploadAppliesRunLogLevel(t *testing.T) {
	t.Parallel()
	for _, level := range []string{"trace", "debug", "info", "warn", "error", ""} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			module, _, _ := newCompositeUploadTestModule(t)
			root := installRunLogLevelProbe(t, module)
			request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "run-log-level")
			request.Execution.RunLogLevel = &level
			started, err := module.StartUpload(t.Context(), testOwnerID, request)
			if err != nil {
				t.Fatal(err)
			}
			waitCompositeUploadTestOperation(t, module, started)
			assertRunLogLevelEntries(t, root, level)
		})
	}
}

func TestCompositeUploadRetainsRunLogLevelOnFeedbackResume(t *testing.T) {
	t.Parallel()
	module, _, _ := newCompositeUploadTestModule(t)
	root := installRunLogLevelProbe(t, module)
	media := module.mediaBuilder
	module.mediaBuilder = mediaArtifactBuilderFunc(func(
		ctx context.Context,
		release api.ReleaseRef,
		projections api.TrackerReleaseProjectionSet,
		instructions api.MediaCaptureInstructions,
		now time.Time,
	) (api.MediaArtifactSet, any, error) {
		logging.FromContext(ctx, root).Tracef("synthetic resumed trace")
		return media.Build(ctx, release, projections, instructions, now)
	})
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "resume-log-level")
	level := "trace"
	request.Execution.RunLogLevel = &level
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	completed := approveCompositeUploadTrackers(t, module, blocked, []api.TrackerID{"ALPHA", "BETA"}, "resume")
	if completed.DryRun == nil {
		t.Fatal("resumed upload did not reach its dry run")
	}
	if !slices.ContainsFunc(root.Recent(100), func(entry logging.Entry) bool { return entry.Message == "synthetic resumed trace" }) {
		t.Fatal("resumed worker lost the requested trace level")
	}
}

func TestRunLogLevelRejectsInvalidRequestsBeforeAdmission(t *testing.T) {
	t.Parallel()
	module, repository, _ := newCompositeUploadTestModule(t)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "invalid-log-level")
	level := "invalid"
	request.Execution.RunLogLevel = &level
	if _, err := module.StartUpload(t.Context(), testOwnerID, request); err == nil {
		t.Fatal("composite upload accepted an invalid log level")
	}
	if _, err := module.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "invalid-log-level",
		Goal:           api.WorkflowGoalPrepared,
		Intent: api.WorkflowIntent{
			Preparation:  &api.PrepareInput{SourcePath: "Example.Release.2026.1080p-GRP"},
			Descriptions: &api.DescriptionInstructions{Options: api.UploadOptions{RunLogLevel: level}},
		},
	}); err == nil {
		t.Fatal("continuation accepted an invalid log level")
	}
	if len(repository.states) != 0 || len(repository.intents) != 0 || len(repository.operations) != 0 {
		t.Fatal("invalid logging input created workflow state")
	}
}

func TestConcurrentUploadLogLevelsStayIsolated(t *testing.T) {
	t.Parallel()
	first, _, _ := newCompositeUploadTestModule(t)
	second, _, _ := newCompositeUploadTestModule(t)
	root := installRunLogLevelProbe(t, first)
	second.logger = root
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	releaseWorkers := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseWorkers)
	for _, module := range []*Module{first, second} {
		preparer := testPreparer()
		prepare := preparer.PrepareFunc
		preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return api.PrepareResult{}, ctx.Err()
			}
			logger := logging.FromContext(ctx, root)
			logger.Tracef("synthetic concurrent trace %s", input.SourcePath)
			logger.Infof("synthetic concurrent info %s", input.SourcePath)
			return prepare(ctx, input)
		}
		module.preparer = preparer
	}
	started := make([]CommandResult, 2)
	for index, module := range []*Module{first, second} {
		level := []string{"trace", "warn"}[index]
		request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "concurrent-log-level")
		request.Source.Path = "example_" + level
		request.Execution.RunLogLevel = &level
		var err error
		started[index], err = module.StartUpload(t.Context(), testOwnerID, request)
		if err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent operations did not reach the probe")
		}
	}
	releaseWorkers()
	waitCompositeUploadTestOperation(t, first, started[0])
	waitCompositeUploadTestOperation(t, second, started[1])
	root.Tracef("synthetic root trace")
	root.Infof("synthetic root info")
	entries := root.Recent(100)
	for message, want := range map[string]bool{
		"synthetic concurrent trace example_trace": true,
		"synthetic concurrent info example_trace":  true,
		"synthetic concurrent trace example_warn":  false,
		"synthetic concurrent info example_warn":   false,
		"synthetic root trace":                     false,
		"synthetic root info":                      true,
	} {
		if got := slices.ContainsFunc(entries, func(entry logging.Entry) bool { return entry.Message == message }); got != want {
			t.Errorf("message %q present = %t, want %t", message, got, want)
		}
	}
}

func TestCancelUploadPreservesRunLogLevelAndRootThreshold(t *testing.T) {
	t.Parallel()
	module, _, _ := newCompositeUploadTestModule(t)
	root := installRunLogLevelProbe(t, module)
	entered := make(chan struct{})
	preparer := testPreparer()
	preparer.PrepareFunc = func(ctx context.Context, _ api.PrepareInput) (api.PrepareResult, error) {
		close(entered)
		<-ctx.Done()
		logging.FromContext(ctx, root).Tracef("synthetic cancellation cleanup")
		return api.PrepareResult{}, ctx.Err()
	}
	module.preparer = preparer
	level := "trace"
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "cancel-log-level")
	request.Execution.RunLogLevel = &level
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not reach cancellation probe")
	}
	if _, err := module.CancelOperation(t.Context(), testOwnerID, started.Workflow.ID, started.Operation.ID); err != nil {
		t.Fatal(err)
	}
	completed := waitCompositeUploadTestOperation(t, module, started)
	if completed.Operation.Status != api.StageStatusCanceled {
		t.Fatalf("operation status = %s, want canceled", completed.Operation.Status)
	}
	entries := root.Recent(100)
	if !slices.ContainsFunc(entries, func(entry logging.Entry) bool { return entry.Message == "synthetic cancellation cleanup" }) {
		t.Fatal("cancellation cleanup lost the operation logger")
	}
	root.Tracef("synthetic canceled scope leak")
	if len(root.Recent(100)) != len(entries) {
		t.Fatal("canceled operation changed the root log level")
	}
}

func TestRetainedUploadLogLevelPreservesExplicitAndQuietContexts(t *testing.T) {
	t.Parallel()
	module, _, _ := newCompositeUploadTestModule(t)
	root := installRunLogLevelProbe(t, module)
	state := State{Workflow: api.ReleaseWorkflow{
		ID:           "retained-log-level",
		Descriptions: &api.DescriptionSetRef{ID: "descriptions", Revision: 1},
	}}
	if err := module.private.Put(testOwnerID, state.Workflow.ID, descriptionPrivateResourceID(state.Workflow.Descriptions.ID),
		api.DescriptionInstructions{Options: api.UploadOptions{RunLogLevel: "trace"}}, module.clock.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, command := range []mutation{DryRunUploadsCommand{}, ExecuteUploadsCommand{}, RetryFailedUploadsCommand{}, RetryClientInjectionsCommand{}} {
		ctx, err := module.withCommandRunLogLevel(t.Context(), testOwnerID, state, command)
		if err != nil {
			t.Fatal(err)
		}
		logging.FromContext(ctx, root).Tracef("synthetic retained %s", command.commandName())
	}
	if len(root.Recent(100)) != 4 {
		t.Fatal("upload or retry did not inherit its retained run level")
	}
	explicit, err := module.withRunLogLevel(t.Context(), "warn")
	if err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{explicit, logging.WithOperationLogger(explicit, api.NopLogger{})} {
		scoped, err := module.withCommandRunLogLevel(ctx, testOwnerID, state, RetryFailedUploadsCommand{})
		if err != nil {
			t.Fatal(err)
		}
		logging.FromContext(scoped, root).Tracef("synthetic retained override leak")
	}
	quiet := logging.WithOperationLogger(explicit, api.NopLogger{})
	quiet, err = module.withRunLogLevel(quiet, " ")
	if err != nil {
		t.Fatal(err)
	}
	logging.FromContext(quiet, root).Errorf("synthetic quiet view leak")
	if len(root.Recent(100)) != 4 {
		t.Fatal("retained settings or blank override replaced the caller's verbosity or quiet view")
	}
}

func TestContinuationAppliesRunLogLevel(t *testing.T) {
	t.Parallel()
	module, _ := newTestModule(t, testPreparer())
	root := installRunLogLevelProbe(t, module)
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "run-log-level",
		Goal:           api.WorkflowGoalPrepared,
		Intent: api.WorkflowIntent{
			Preparation:  &api.PrepareInput{SourcePath: "Example.Release.2026.1080p-GRP"},
			Descriptions: &api.DescriptionInstructions{Options: api.UploadOptions{RunLogLevel: "trace"}},
		},
	}
	created, err := module.Continue(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Authority = &api.WorkflowAuthority{WorkflowID: created.Workflow.ID, ExpectedRevision: created.Workflow.Revision}
	started, err := module.Continue(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	if started.Operation == nil {
		t.Fatal("continuation did not start preparation")
	}
	waitForWorkflowOperation(t, module, created.Workflow.ID, started.Operation.ID, func(status api.WorkflowOperationStatus) bool {
		return isTerminalProgressStatus(status.Status)
	})
	assertRunLogLevelEntries(t, root, "trace")
}

func installRunLogLevelProbe(t *testing.T, module *Module) *logging.Logger {
	t.Helper()
	root, err := logging.New(config.LoggingConfig{Level: "info"}, "")
	if err != nil {
		t.Fatal(err)
	}
	root.SetConsoleOutput(io.Discard, io.Discard)
	t.Cleanup(func() { _ = root.Close() })
	module.logger = root
	preparer := testPreparer()
	prepare := preparer.PrepareFunc
	preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
		logger := logging.FromContext(ctx, root)
		logger.Tracef("synthetic run trace")
		logger.Debugf("synthetic run debug")
		logger.Infof("synthetic run info")
		root.Tracef("synthetic unrelated trace")
		root.Infof("synthetic unrelated info")
		return prepare(ctx, input)
	}
	module.preparer = preparer
	return root
}

func assertRunLogLevelEntries(t *testing.T, root *logging.Logger, level string) {
	t.Helper()
	entries := root.Recent(100)
	for message, want := range map[string]bool{
		"synthetic run trace":       level == "trace",
		"synthetic run debug":       level == "trace" || level == "debug",
		"synthetic run info":        level != "warn" && level != "error",
		"synthetic unrelated trace": false,
		"synthetic unrelated info":  true,
	} {
		got := slices.ContainsFunc(entries, func(entry logging.Entry) bool { return entry.Message == message })
		if got != want {
			t.Errorf("message %q present = %t, want %t for run level %q", message, got, want, level)
		}
	}
}
