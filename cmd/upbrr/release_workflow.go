// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

const cliWorkflowOwnerID = "cli"

// cliReleaseWorkflowCore is the in-process application seam used by the CLI.
// It deliberately excludes HTTP and every legacy operation-specific entrypoint.
type cliReleaseWorkflowCore interface {
	GetActiveInput(context.Context, string) (api.ActiveInputSnapshot, error)
	ReleaseActiveInput(context.Context, string, api.ReleaseActiveInputRequest) (api.ActiveInputSnapshot, error)
	RecoverLegacyActiveInput(context.Context, string, api.RecoverLegacyActiveInputRequest) (api.ActiveInputSnapshot, error)
	ReconcileActiveInput(context.Context, string, api.ReconcileActiveInputRequest) (api.ActiveInputSnapshot, error)
	LiveTestEnabled() bool
	StartLiveTestReleaseWorkflowUpload(context.Context, string, api.CreateReleaseWorkflowUploadRequest) (releaseworkflow.CommandResult, error)
	ContinueReleaseWorkflow(context.Context, string, api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error)
	StartReleaseWorkflow(context.Context, string, releaseworkflow.Command) (api.WorkflowOperationStatus, error)
	StartReleaseWorkflowUpload(
		context.Context,
		string,
		api.CreateReleaseWorkflowUploadRequest,
	) (releaseworkflow.CommandResult, error)
	SubmitReleaseWorkflowUploadFeedback(
		context.Context,
		string,
		api.WorkflowID,
		api.ReleaseWorkflowUploadFeedback,
	) (releaseworkflow.CommandResult, error)
	CurrentReleaseWorkflow(context.Context, string, api.WorkflowID) (releaseworkflow.CommandResult, error)
	GetInputHistory(context.Context, string) (api.InputHistory, error)
	ReleaseWorkflowOperation(
		context.Context,
		string,
		api.WorkflowID,
		api.WorkflowOperationID,
	) (api.WorkflowOperationStatus, error)
	ReleaseWorkflowOperationEvents(
		context.Context,
		string,
		api.WorkflowID,
		api.WorkflowOperationID,
		uint64,
		int,
	) ([]api.WorkflowEvent, error)
	CancelReleaseWorkflowOperation(
		context.Context,
		string,
		api.WorkflowID,
		api.WorkflowOperationID,
	) (api.WorkflowOperationStatus, error)
	ReleaseWorkflowAudioAnalysisArtifactPath(
		context.Context,
		string,
		api.WorkflowID,
		api.AudioAnalysisRef,
		api.PublicResourceID,
	) (string, error)
}

type cliWorkflowSession struct {
	core               cliReleaseWorkflowCore
	logger             api.Logger
	current            releaseworkflow.CommandResult
	intent             cliWorkflowIntent
	uploadRequest      api.Request
	idempotencyRun     string
	intentSequence     uint64
	printedProgress    map[api.OperationKind]struct{}
	projectionsPrinted bool
	eventLogState      cliWorkflowEventLogState
	progressWriter     io.Writer
	streams            cliIO
	inputBaseline      cliInputSlotBaseline
	inputClaim         cliInputSlotClaim
}

type cliInputSlotBaseline struct {
	captured bool
	state    api.ActiveInputState
	revision uint64
}

type cliInputSlotClaim struct {
	workflowID api.WorkflowID
	revision   uint64
}

func (s *cliWorkflowSession) releaseActiveInput(ctx context.Context) {
	if s == nil || s.inputClaim.workflowID == "" || s.inputClaim.revision == 0 {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	active, err := s.core.GetActiveInput(cleanup, cliWorkflowOwnerID)
	if err == nil && active.State == api.ActiveInputActive && active.Revision == s.inputClaim.revision &&
		active.Current != nil && active.Current.Workflow.ID == s.inputClaim.workflowID {
		_, err = s.core.ReleaseActiveInput(cleanup, cliWorkflowOwnerID, api.ReleaseActiveInputRequest{ExpectedRevision: s.inputClaim.revision})
	}
	if err != nil {
		s.logger.Warnf("workflow: state=cleanup_failed decision=recovery_required")
	}
}

func (s *cliWorkflowSession) captureInputBaseline(ctx context.Context) error {
	if s.inputBaseline.captured {
		return nil
	}
	active, err := s.core.GetActiveInput(ctx, cliWorkflowOwnerID)
	if err != nil {
		return fmt.Errorf("upbrr: inspect initial input: %w", err)
	}
	s.inputBaseline = cliInputSlotBaseline{
		captured: true,
		state:    active.State,
		revision: active.Revision,
	}
	return nil
}

func (s *cliWorkflowSession) captureInputClaim(ctx context.Context, workflowID api.WorkflowID, requireChanged bool) {
	if !s.inputBaseline.captured || (s.inputBaseline.state != api.ActiveInputEmpty && s.inputBaseline.state != api.ActiveInputRecovering) ||
		(s.inputBaseline.state == api.ActiveInputRecovering && requireChanged) {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	active, err := s.core.GetActiveInput(cleanup, cliWorkflowOwnerID)
	if err != nil || active.State != api.ActiveInputActive || active.Current == nil {
		return
	}
	if workflowID == "" {
		workflowID = active.Current.Workflow.ID
	}
	if workflowID == "" || active.Current.Workflow.ID != workflowID || (requireChanged && active.Revision == s.inputBaseline.revision) {
		return
	}
	// Reclaiming a previous-process slot advances its revision twice before an
	// explicit open. Only a successful continuation can prove that the CLI
	// opened this slot rather than merely restoring the previous process's input.
	if s.inputBaseline.state == api.ActiveInputRecovering && active.Revision <= s.inputBaseline.revision+2 {
		return
	}
	s.inputClaim = cliInputSlotClaim{workflowID: workflowID, revision: active.Revision}
}

// cliWorkflowIntent is the detached CLI adapter input retained after the
// initial flag-to-workflow mapping. Later stages consume shared workflow DTOs,
// not the broad legacy api.Request shape.
type cliWorkflowIntent struct {
	sourcePath    string
	trackerConfig api.TrackerConfigOverrides
	trackerSite   api.TrackerSiteOverrides
	interaction   api.InteractionMode
	noSeed        bool
}

func (s *cliWorkflowSession) nextIdempotencyKey(label string) string {
	if s.idempotencyRun == "" {
		s.idempotencyRun = strings.ToLower(rand.Text())
	}
	s.intentSequence++
	return fmt.Sprintf("cli-%s-%s-%d", s.idempotencyRun, strings.TrimSpace(label), s.intentSequence)
}

func (s *cliWorkflowSession) executeContinuation(
	ctx context.Context,
	request api.ContinueReleaseWorkflowRequest,
) error {
	// The first continuation can create a workflow through an active input.
	// Bind the CLI's post-duplicate approval policy before that creation; later
	// continuations retain the persisted mode.
	ctx = releaseworkflow.WithTrackerDecisionMode(ctx, releaseworkflow.TrackerDecisionModePostDupeGate)
	initial := request.Authority == nil
	if initial {
		if err := s.captureInputBaseline(ctx); err != nil {
			return err
		}
	}
	current, err := s.core.ContinueReleaseWorkflow(ctx, cliWorkflowOwnerID, request)
	if err != nil {
		if initial {
			// OpenInput may have committed the slot before a later continuation
			// stage fails. Re-read the exact owned snapshot for deferred cleanup.
			if current.Workflow.ID != "" {
				s.captureInputClaim(ctx, current.Workflow.ID, false)
			} else {
				s.captureInputClaim(ctx, "", true)
			}
		}
		return fmt.Errorf("upbrr: continue release workflow: %w", err)
	}
	if initial {
		s.captureInputClaim(ctx, current.Workflow.ID, false)
	}
	// Retain ownership before polling so canceled waits can close the exact input.
	s.current = current
	if current.Operation != nil && !isTerminalCLIWorkflowOperation(current.Operation.Status) {
		completed, waitErr := s.waitForOperation(ctx, *current.Operation)
		if waitErr != nil {
			return waitErr
		}
		current, err = s.core.CurrentReleaseWorkflow(ctx, cliWorkflowOwnerID, completed.WorkflowID)
		if err != nil {
			return fmt.Errorf("upbrr: load continued release workflow: %w", err)
		}
		current.Operation = &completed
	}
	s.current = current
	return nil
}

func (s *cliWorkflowSession) continueUntilStable(
	ctx context.Context,
	request api.ContinueReleaseWorkflowRequest,
) error {
	for range 32 {
		priorWorkflowID := s.current.Workflow.ID
		priorRevision := s.current.Workflow.Revision
		if priorWorkflowID == "" {
			request.Authority = nil
		} else {
			request.Authority = &api.WorkflowAuthority{
				WorkflowID:       priorWorkflowID,
				ExpectedRevision: priorRevision,
			}
		}
		if err := s.executeContinuation(ctx, request); err != nil {
			return err
		}
		if slices.ContainsFunc(s.current.Workflow.RequiredActions, func(action api.RequiredAction) bool {
			return action.Status == api.RequiredActionStatusPending
		}) {
			return nil
		}
		if s.current.Workflow.ID == priorWorkflowID && s.current.Workflow.Revision == priorRevision {
			return nil
		}
	}
	return errors.New("upbrr: release workflow continuation exceeded the transition limit")
}

func isTerminalCLIWorkflowOperation(status api.StageStatus) bool {
	switch status {
	case api.StageStatusBlocked, api.StageStatusStale, api.StageStatusFailed, api.StageStatusPartial, api.StageStatusSkipped,
		api.StageStatusCompleted, api.StageStatusExecuted, api.StageStatusInterrupted, api.StageStatusCanceled,
		api.StageStatusUnavailable:
		return true
	case api.StageStatusPending, api.StageStatusQueued, api.StageStatusReady, api.StageStatusRunning:
		return false
	}
	return false
}

func (s *cliWorkflowSession) waitForOperation(
	ctx context.Context,
	operation api.WorkflowOperationStatus,
) (api.WorkflowOperationStatus, error) {
	if _, printed := s.printedProgress[operation.Operation]; !printed {
		if s.printedProgress == nil {
			s.printedProgress = make(map[api.OperationKind]struct{})
		}
		s.printedProgress[operation.Operation] = struct{}{}
		printCLIWorkflowProgress(s.progressWriter, operation.Operation)
	}
	if err := s.logNewOperationEvents(ctx, operation, &s.eventLogState); err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	for operation.Status == api.StageStatusQueued || operation.Status == api.StageStatusRunning {
		select {
		case <-ctx.Done():
			cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_, cancelErr := s.core.CancelReleaseWorkflowOperation(
				cancelCtx,
				cliWorkflowOwnerID,
				operation.WorkflowID,
				operation.ID,
			)
			cancel()
			if cancelErr != nil {
				return api.WorkflowOperationStatus{}, fmt.Errorf(
					"upbrr: wait for release workflow command: %w (cancel operation: %w)",
					ctx.Err(),
					cancelErr,
				)
			}
			return api.WorkflowOperationStatus{}, fmt.Errorf("upbrr: wait for release workflow command: %w", ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
		current, err := s.core.ReleaseWorkflowOperation(
			ctx,
			cliWorkflowOwnerID,
			operation.WorkflowID,
			operation.ID,
		)
		if err != nil {
			return api.WorkflowOperationStatus{}, fmt.Errorf("upbrr: poll release workflow command: %w", err)
		}
		operation = current
		if err := s.logNewOperationEvents(ctx, operation, &s.eventLogState); err != nil {
			return api.WorkflowOperationStatus{}, err
		}
	}
	if operation.Status == api.StageStatusCompleted ||
		operation.Status == api.StageStatusBlocked ||
		operation.Status == api.StageStatusPartial ||
		operation.Status == api.StageStatusExecuted {
		return operation, nil
	}
	if operation.Operation == api.OperationKindAudioAnalysis && operation.Status == api.StageStatusFailed && operation.Result != nil {
		return operation, nil
	}
	if len(operation.Failures) > 0 {
		return api.WorkflowOperationStatus{}, fmt.Errorf(
			"upbrr: release workflow command: %s",
			operation.Failures[0].Failure.Message,
		)
	}
	return api.WorkflowOperationStatus{}, fmt.Errorf(
		"upbrr: release workflow command ended with status %s",
		operation.Status,
	)
}

func (s *cliWorkflowSession) logNewOperationEvents(
	ctx context.Context,
	operation api.WorkflowOperationStatus,
	state *cliWorkflowEventLogState,
) error {
	const eventPageSize = 1000
	switch {
	case state.workflowID != operation.WorkflowID:
		state.workflowID = operation.WorkflowID
		state.operationID = operation.ID
		state.lastSequence = 0
		state.loggedEvents = nil
	case state.operationID != operation.ID:
		state.operationID = operation.ID
		state.loggedEvents = nil
	}
	for {
		events, err := s.core.ReleaseWorkflowOperationEvents(
			ctx,
			cliWorkflowOwnerID,
			operation.WorkflowID,
			operation.ID,
			state.lastSequence,
			eventPageSize,
		)
		if err != nil {
			return fmt.Errorf("upbrr: poll release workflow events: %w", err)
		}
		if len(events) == 0 {
			return nil
		}
		logCLIWorkflowEvents(s.logger, events, state)
		if len(events) < eventPageSize {
			return nil
		}
	}
}

func newCLIWorkflowSession(
	ctx context.Context,
	coreSvc cliReleaseWorkflowCore,
	request api.Request,
	intent api.PreparationIntent,
	reader *bufio.Reader,
	cfg config.Config,
	streams cliIO,
	logger api.Logger,
) (*cliWorkflowSession, error) {
	streams = streams.normalized()
	input, err := api.MapPreparationRequest(request, intent)
	if err != nil {
		return nil, fmt.Errorf("upbrr: map workflow preparation: %w", err)
	}
	session := &cliWorkflowSession{
		core:           coreSvc,
		logger:         logger,
		intent:         mapCLIWorkflowIntent(request),
		uploadRequest:  request,
		idempotencyRun: strings.ToLower(rand.Text()),
		progressWriter: streams.out,
		streams:        streams,
	}
	ready := false
	defer func() {
		if !ready {
			session.releaseActiveInput(ctx)
		}
	}()
	if err := session.reconcileLegacyInputs(ctx, reader); err != nil {
		return nil, err
	}
	if err := session.continueUntilStable(ctx, api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: session.nextIdempotencyKey("prepare"),
		Goal:           api.WorkflowGoalPrepared,
		Intent: api.WorkflowIntent{
			FactInstructions: &input.Instructions,
			Preparation:      &input,
			TrackerIDs:       normalizeCLIWorkflowTrackerIDs(request.Trackers),
		},
	}); err != nil {
		return nil, err
	}
	if err := session.resolvePlaylistAction(ctx, input, session.intent.interaction, reader, cfg); err != nil {
		return nil, err
	}
	if session.current.Release == nil {
		if pendingCLIWorkflowAction(session.current.Workflow.RequiredActions, api.RequiredActionConfirmCorrections) != nil {
			ready = true
			return session, nil
		}
		return nil, errors.New("upbrr: release workflow produced no canonical release")
	}
	session.intent.sourcePath = session.current.Release.Release.Source.SourcePath
	ready = true
	return session, nil
}

func mapCLIWorkflowIntent(request api.Request) cliWorkflowIntent {
	return cliWorkflowIntent{
		sourcePath:    strings.TrimSpace(request.SourcePath),
		trackerConfig: request.TrackerConfigOverrides,
		trackerSite:   request.TrackerSiteOverrides,
		interaction:   request.Options.InteractionMode,
		noSeed:        request.Options.NoSeed,
	}
}

func (s *cliWorkflowSession) resolvePlaylistAction(
	ctx context.Context,
	input api.PrepareInput,
	interaction api.InteractionMode,
	reader *bufio.Reader,
	cfg config.Config,
) error {
	action := pendingCLIWorkflowAction(s.current.Workflow.RequiredActions, api.RequiredActionSelectPlaylist)
	if action == nil {
		return nil
	}
	if interaction == api.InteractionModeUnattended && !cfg.Metadata.UseLargestPlaylist {
		return errors.New("upbrr: unattended Blu-ray preparation requires playlist selection; use --unattended_confirm/--uac to allow the prompt")
	}
	selected, err := selectCLIWorkflowPlaylists(reader, s.streams.out, *action, cfg.Metadata.UseLargestPlaylist)
	if err != nil {
		return err
	}
	instructions := input.Instructions
	instructions.Playlist = api.PlaylistInstruction{Set: true, Selected: selected}
	input.Instructions = instructions
	// The selection is a user instruction accepted while preparing the current
	// workflow. Retain that raw instruction for the following composite request;
	// reconstructing it from provider-resolved facts could change other fields.
	s.uploadRequest.PlaylistInstruction = cloneCLIPlaylistInstruction(instructions.Playlist)
	return s.continueUntilStable(ctx, api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: s.nextIdempotencyKey("prepare-playlist"),
		Goal:           api.WorkflowGoalPrepared,
		Intent: api.WorkflowIntent{
			FactInstructions: &instructions,
			Preparation:      &input,
		},
	})
}

func pendingCLIWorkflowAction(actions []api.RequiredAction, kind api.RequiredActionKind) *api.RequiredAction {
	for index := range actions {
		if actions[index].Kind == kind && actions[index].Status == api.RequiredActionStatusPending {
			return &actions[index]
		}
	}
	return nil
}

func selectCLIWorkflowPlaylists(reader *bufio.Reader, output io.Writer, action api.RequiredAction, useLargest bool) ([]string, error) {
	if len(action.Options) == 0 {
		return nil, errors.New("upbrr: Blu-ray playlist action has no candidates")
	}
	if useLargest {
		selected := make([]string, 0)
		seenDiscs := make(map[string]struct{})
		for _, option := range action.Options {
			discID := playlistOptionDiscID(option)
			if _, ok := seenDiscs[discID]; ok {
				continue
			}
			seenDiscs[discID] = struct{}{}
			selected = append(selected, option.Value)
		}
		return selected, nil
	}
	if reader == nil {
		return nil, errors.New("upbrr: Blu-ray playlist selection requires input")
	}
	fmt.Fprintln(output)
	fmt.Fprintln(output, action.Prompt)
	for index, option := range action.Options {
		fmt.Fprintf(output, "%d. %s\n", index+1, option.Label)
	}
	answer, err := promptLine(reader, output, "Playlist number(s), comma-separated: ")
	if err != nil {
		return nil, err
	}
	selected := make([]string, 0)
	seen := make(map[string]struct{})
	selectedDiscs := make(map[string]struct{})
	for _, token := range strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' }) {
		for index, option := range action.Options {
			if token != strconv.Itoa(index+1) && token != option.Value {
				continue
			}
			if _, ok := seen[option.Value]; !ok {
				seen[option.Value] = struct{}{}
				selected = append(selected, option.Value)
				selectedDiscs[playlistOptionDiscID(option)] = struct{}{}
			}
			break
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("upbrr: no valid Blu-ray playlist selected")
	}
	for _, option := range action.Options {
		if _, ok := selectedDiscs[playlistOptionDiscID(option)]; !ok {
			return nil, errors.New("upbrr: select at least one Blu-ray playlist for each disc")
		}
	}
	return selected, nil
}

func playlistOptionDiscID(option api.RequiredActionOption) string {
	if option.Playlist == nil || strings.TrimSpace(option.Playlist.DiscID) == "" {
		return "single-disc"
	}
	return strings.TrimSpace(option.Playlist.DiscID)
}

func runCLIWorkflowInteractive(
	ctx context.Context,
	coreSvc cliReleaseWorkflowCore,
	baseArgs []string,
	opts cliOptions,
	visited map[string]bool,
	sourcePath string,
	playlist api.PlaylistInstruction,
	screens int,
	cfg config.Config,
	streams cliIO,
	logger api.Logger,
) error {
	streams = streams.normalized()
	reader := bufio.NewReader(streams.in)
	currentArgs := append([]string(nil), baseArgs...)
	currentOpts := opts
	currentVisited := copyVisited(visited)
	var (
		request     api.Request
		session     *cliWorkflowSession
		inputTracks []api.MediaTrackFacts
		err         error
	)
	defer func() { session.releaseActiveInput(ctx) }()
	for {
		if len(currentOpts.TrackLanguages) > 0 && inputTracks == nil {
			history, historyErr := coreSvc.GetInputHistory(ctx, sourcePath)
			if historyErr != nil || len(history.Tracks) == 0 {
				if historyErr != nil {
					return fmt.Errorf("upbrr: track-languages requires retained input history; run --input-only first: %w", historyErr)
				}
				return errors.New("upbrr: track-languages requires retained input history; run --input-only first")
			}
			inputTracks = history.Tracks
		}
		request, err = buildCLIRequest(currentOpts, currentVisited, []string{sourcePath}, screens)
		if err != nil {
			return err
		}
		request.PlaylistInstruction = cloneCLIPlaylistInstruction(playlist)
		replacement, err := newCLIWorkflowSession(ctx, coreSvc, request, api.PreparationIntentPreview, reader, cfg, streams, logger)
		if err != nil {
			return err
		}
		if session != nil && replacement.inputClaim.workflowID == "" {
			replacement.inputClaim = session.inputClaim
		}
		session = replacement
		if err := applyCLIInputCorrections(ctx, session, currentOpts, currentVisited, inputTracks); err != nil {
			return err
		}
		if session.current.Release != nil {
			session.intent.sourcePath = session.current.Release.Release.Source.SourcePath
		}
		if err := applyCLITrackerInput(ctx, session, currentOpts.TrackerInput); err != nil {
			return err
		}
		if action := pendingCLIWorkflowAction(session.current.Workflow.RequiredActions, api.RequiredActionConfirmCorrections); action != nil {
			fmt.Fprintln(streams.out, action.Prompt)
			if details := action.CorrectionConfirmation; details != nil {
				fmt.Fprintf(
					streams.out,
					"Current identity: category=%s tmdb=%d imdb=%s\n",
					details.CurrentBinding.Category,
					details.CurrentBinding.ProviderIDs.TMDBID,
					providerid.IMDb(details.CurrentBinding.ProviderIDs.IMDBID).Prefixed(),
				)
				for _, field := range details.Fields {
					fmt.Fprintf(streams.out, "Confirm with --confirm-input %s or reset with --reset-input %s\n", field, field)
				}
			}
			return exitError(2, errors.New("saved input corrections require confirmation"))
		}
		preview := cliWorkflowMetadataPreview(session.current)
		printMetadataPreview(streams.out, preview, currentOpts.Debug)
		if currentOpts.InputOnly || currentOpts.interactionMode() == api.InteractionModeUnattended {
			break
		}
		confirmed, promptErr := promptYesNo(reader, streams.out, "Metadata correct? [Y/n]: ", true)
		if promptErr != nil {
			return promptErr
		}
		if confirmed {
			break
		}
		editArgs, promptErr := promptLine(reader, streams.out, "Input args that need correction, or 'continue': ")
		if promptErr != nil {
			return promptErr
		}
		if strings.EqualFold(strings.TrimSpace(editArgs), "continue") {
			break
		}
		editTokens, splitErr := splitInteractiveCLIArgs(editArgs)
		if splitErr != nil {
			fmt.Fprintf(streams.out, "Invalid override args: %v\n", splitErr)
			continue
		}
		nextArgs, mergeErr := mergeCLIInputEditArgs(currentArgs, editTokens)
		if mergeErr != nil {
			fmt.Fprintf(streams.out, "Invalid override args: %v\n", mergeErr)
			continue
		}
		nextOpts, nextVisited, _, parseErr := parseCLIOptions(nextArgs)
		if parseErr != nil {
			fmt.Fprintf(streams.out, "Invalid override args: %v\n", parseErr)
			continue
		}
		currentArgs, currentOpts, currentVisited = nextArgs, nextOpts, nextVisited
	}
	if currentOpts.InputOnly {
		return completeCLIInputOnly(ctx, session, streams.out)
	}
	_, err = session.complete(ctx, currentOpts.Debug, reader, cfg, logger)
	return err
}

func applyCLIInputCorrections(
	ctx context.Context,
	session *cliWorkflowSession,
	opts cliOptions,
	visited map[string]bool,
	tracks []api.MediaTrackFacts,
) error {
	patch, err := buildCLIInputCorrectionPatch(opts, visited, tracks)
	if err != nil || patch == nil {
		return err
	}
	if session.current.Corrections == nil && session.current.FactInstructions == nil {
		return errors.New("upbrr: release workflow has no correction revision")
	}
	var revision uint64
	if session.current.Corrections != nil {
		revision = session.current.Corrections.Revision
	} else {
		revision = session.current.FactInstructions.CorrectionRevision
	}
	if len(patch.ConfirmFields) > 0 {
		action := pendingCLIWorkflowAction(session.current.Workflow.RequiredActions, api.RequiredActionConfirmCorrections)
		if action == nil || action.CorrectionConfirmation == nil || action.WorkflowRevision != session.current.Workflow.Revision ||
			session.current.Corrections == nil || action.CorrectionConfirmation.Revision != revision {
			return errors.New("upbrr: confirm-input requires the current saved input correction action")
		}
		for _, field := range patch.ConfirmFields {
			if !slices.Contains(action.CorrectionConfirmation.Fields, field.Field) {
				return fmt.Errorf("upbrr: confirm-input field %s is not pending confirmation", field.Field)
			}
		}
	}
	patch.ExpectedRevision = &revision
	return session.continueUntilStable(ctx, api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: session.nextIdempotencyKey("input-corrections"),
		Goal:           api.WorkflowGoalPrepared,
		Intent: api.WorkflowIntent{
			CorrectionPatch: patch,
		},
	})
}

func applyCLITrackerInput(ctx context.Context, session *cliWorkflowSession, values []string) error {
	if len(values) == 0 {
		return nil
	}
	answers, err := buildCLITrackerInput(values)
	if err != nil {
		return err
	}
	patch := make(map[api.TrackerID]map[string]*string, len(answers))
	for tracker, fields := range answers {
		patch[api.TrackerID(tracker)] = make(map[string]*string, len(fields))
		for field, value := range fields {
			if value == "auto" {
				patch[api.TrackerID(tracker)][field] = nil
			} else {
				patch[api.TrackerID(tracker)][field] = new(value)
			}
		}
	}
	return session.continueUntilStable(ctx, api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: session.nextIdempotencyKey("tracker-input"),
		Goal:           api.WorkflowGoalInputReady,
		Intent:         api.WorkflowIntent{TrackerIDs: normalizeCLIWorkflowTrackerIDs(session.uploadRequest.Trackers), TrackerInputAnswers: patch},
	})
}

func completeCLIInputOnly(ctx context.Context, session *cliWorkflowSession, output io.Writer) error {
	if err := session.continueUntilStable(ctx, api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: session.nextIdempotencyKey("input-ready"),
		Goal:           api.WorkflowGoalInputReady,
		Intent:         api.WorkflowIntent{TrackerIDs: normalizeCLIWorkflowTrackerIDs(session.uploadRequest.Trackers)},
	}); err != nil {
		return err
	}
	if session.current.Release != nil {
		release := session.current.Release.Release
		facts, err := json.Marshal(struct {
			Identity api.ExternalIdentity `json:"identity"`
			Naming   api.NamingFacts      `json:"naming"`
			Episode  api.EpisodeFacts     `json:"episode"`
			Media    api.MediaFacts       `json:"media"`
		}{
			Identity: release.Identity,
			Naming:   release.Naming,
			Episode:  release.Episode,
			Media:    release.Media,
		})
		if err != nil {
			return fmt.Errorf("upbrr: encode input facts: %w", err)
		}
		fmt.Fprintf(output, "Input facts: %s\n", facts)
		for _, track := range release.Media.Tracks {
			fmt.Fprintf(
				output,
				"Track %s: kind=%s languages=%s manifest=%s\n",
				track.ID,
				track.Kind,
				strings.Join(track.Languages, ","),
				track.ManifestFingerprint,
			)
		}
	}
	if session.current.InputReadiness == nil {
		return errors.New("upbrr: input readiness was not produced")
	}
	ready := true
	for _, field := range session.current.InputReadiness.Fields {
		fmt.Fprintf(output, "Input %s: %s\n", field.Key, field.Status)
		if field.Status != api.InputReadinessFieldReady && api.NormalizeRuleDisposition(field.Disposition) != api.RuleDispositionAdvisory {
			ready = false
		}
	}
	if !ready {
		return exitError(2, errors.New("required input is missing or invalid"))
	}
	return nil
}

func cliWorkflowMetadataPreview(current releaseworkflow.CommandResult) api.MetadataPreview {
	if current.Release == nil {
		return api.MetadataPreview{}
	}
	release := current.Release.Release
	preview := api.MetadataPreview{
		SourcePath:  release.Source.SourcePath,
		ReleaseName: release.Naming.ReleaseName,
		Release: api.ReleaseRef{
			SourcePath: release.Source.SourcePath,
			Generation: release.Generation,
		},
		Identity:    release.Identity,
		Display:     current.Release.Display,
		Bluray:      release.ProviderMetadata.Bluray,
		Diagnostics: append([]api.PreparationDiagnostic(nil), current.Release.Diagnostics...),
	}
	if current.FactInstructions != nil {
		preview.ReleaseNameOverrides = current.FactInstructions.Instructions.ReleaseName
	}
	return preview
}

func (s *cliWorkflowSession) complete(
	ctx context.Context,
	debug bool,
	reader *bufio.Reader,
	cfg config.Config,
	logger api.Logger,
) (int, error) {
	s.streams = s.streams.normalized()
	if s.progressWriter == nil {
		s.progressWriter = s.streams.out
	}
	if strings.TrimSpace(s.uploadRequest.SourcePath) == "" {
		return 0, errors.New("upbrr: composite upload source is unavailable")
	}
	if s.uploadRequest.Options.AudioAnalysis {
		if err := s.completeAudioAnalysis(ctx); err != nil {
			return 0, err
		}
	}
	return s.completeComposite(ctx, debug, reader, cfg, logger)
}

func (s *cliWorkflowSession) completeAudioAnalysis(ctx context.Context) error {
	if s.current.Release == nil {
		return errors.New("upbrr: audio analysis requires a prepared release")
	}
	release := s.current.Release.Release
	audioTracks := make([]api.MediaTrackFacts, 0)
	for _, track := range release.Media.Tracks {
		if track.Kind == api.MediaTrackAudio {
			audioTracks = append(audioTracks, track)
		}
	}
	if len(audioTracks) == 0 {
		return errors.New("upbrr: requested audio analysis but the prepared source has no audio tracks")
	}
	selection, ordinals, err := parseCLIAudioTrackSelection(s.uploadRequest.Options.AudioTracks)
	if err != nil {
		return err
	}
	selected := make([]api.MediaTrackFacts, 0, len(audioTracks))
	switch selection {
	case api.AudioAnalysisSelectionPrimary:
		for _, track := range audioTracks {
			if track.ID == release.Media.PrimaryAudioTrackID {
				selected = append(selected, track)
				break
			}
		}
		if len(selected) == 0 {
			return errors.New("upbrr: prepared primary audio track is unavailable or ambiguous")
		}
	case api.AudioAnalysisSelectionAll:
		selected = append(selected, audioTracks...)
	case api.AudioAnalysisSelectionSelected:
		wanted := make(map[int]struct{}, len(ordinals))
		for _, ordinal := range ordinals {
			wanted[ordinal] = struct{}{}
		}
		for _, track := range audioTracks {
			if _, ok := wanted[track.Ordinal]; ok {
				selected = append(selected, track)
				delete(wanted, track.Ordinal)
			}
		}
		if len(wanted) != 0 {
			return errors.New("upbrr: audio-tracks contains an ordinal not present in the prepared source")
		}
	}
	resourceID := selected[0].ResourceID
	trackIDs := make([]string, len(selected))
	for index, track := range selected {
		if track.ResourceID != resourceID {
			return errors.New("upbrr: selected audio tracks do not belong to one decodable prepared resource")
		}
		trackIDs[index] = track.ID
	}
	variants, err := parseCLIAudioVariants(s.uploadRequest.Options.AudioImages)
	if err != nil {
		return err
	}
	variants = append(variants, api.AudioAnalysisStats)
	request := api.AnalyzeReleaseWorkflowAudioRequest{
		WorkflowID:       s.current.Workflow.ID,
		ExpectedRevision: s.current.Workflow.Revision,
		IdempotencyKey:   s.nextIdempotencyKey("audio-analysis"),
		Instructions: api.AudioAnalysisInstructions{
			Release:        api.ReleaseRef{SourcePath: release.Source.SourcePath, Generation: release.Generation},
			ResourceID:     resourceID,
			Selection:      selection,
			TrackIDs:       trackIDs,
			Variants:       variants,
			ProfileVersion: api.AudioAnalysisProfileVersion,
		},
	}
	command, err := releaseworkflow.CommandFromRequest(request)
	if err != nil {
		return fmt.Errorf("upbrr: prepare audio analysis command: %w", err)
	}
	operation, err := s.core.StartReleaseWorkflow(ctx, cliWorkflowOwnerID, command)
	if err != nil {
		return fmt.Errorf("upbrr: start audio analysis: %w", err)
	}
	operation, err = s.waitForOperation(ctx, operation)
	if err != nil {
		return err
	}
	current, err := s.core.CurrentReleaseWorkflow(ctx, cliWorkflowOwnerID, operation.WorkflowID)
	if err != nil {
		return fmt.Errorf("upbrr: load audio analysis result: %w", err)
	}
	s.current = current
	if current.AudioAnalysis == nil || current.Workflow.AudioAnalysis == nil {
		return errors.New("upbrr: audio analysis produced no retained result")
	}
	analysis := current.AudioAnalysis
	analysisRef := *current.Workflow.AudioAnalysis
	for _, track := range analysis.Tracks {
		for _, artifact := range track.Artifacts {
			if artifact.Status != api.StageStatusCompleted {
				continue
			}
			pathValue, pathErr := s.core.ReleaseWorkflowAudioAnalysisArtifactPath(
				ctx, cliWorkflowOwnerID, current.Workflow.ID, analysisRef, artifact.ID,
			)
			if pathErr != nil {
				return fmt.Errorf("upbrr: resolve audio analysis artifact: %w", pathErr)
			}
			fmt.Fprintf(
				s.streams.out,
				"Audio analysis resource 1 track %d %s: %s\n",
				track.Ordinal,
				artifact.Variant,
				//logpolicy:allow local CLI output intentionally exposes an owner-authorized retained artifact path
				pathValue,
			)
		}
		if track.Failure != nil {
			fmt.Fprintf(s.streams.errOut, "Audio track %d failed: %s\n", track.Ordinal, logging.SanitizeMessage(track.Failure.Message))
		}
		for _, artifact := range track.Artifacts {
			if artifact.Failure != nil {
				fmt.Fprintf(
					s.streams.errOut,
					"Audio track %d %s failed: %s\n",
					track.Ordinal,
					artifact.Variant,
					logging.SanitizeMessage(artifact.Failure.Message),
				)
			}
		}
	}
	fmt.Fprintf(s.streams.out, "Audio analysis artifacts are retained locally until %s.\n", analysis.ExpiresAt.Local().Format(time.RFC3339))
	if analysis.Status == api.StageStatusPartial || analysis.Status == api.StageStatusFailed {
		return fmt.Errorf("upbrr: audio analysis completed with status %s", analysis.Status)
	}
	return nil
}

func cliProjectionInstructions(request api.Request) map[api.TrackerID]api.TrackerProjectionInstructions {
	instructions := make(map[api.TrackerID]api.TrackerProjectionInstructions)
	for tracker, answers := range request.TrackerQuestionnaireAnswers {
		trackerID := api.TrackerID(strings.ToUpper(strings.TrimSpace(tracker)))
		if trackerID == "" {
			continue
		}
		questionnaire := make(map[string]*string, len(answers))
		for key, answer := range answers {
			value := answer
			questionnaire[key] = &value
		}
		instructions[trackerID] = api.TrackerProjectionInstructions{
			Questionnaire: questionnaire,
			TrackerConfig: request.TrackerConfigOverrides,
			TrackerSite:   request.TrackerSiteOverrides,
		}
	}
	for _, trackerID := range normalizeCLIWorkflowTrackerIDs(request.Trackers) {
		instruction := instructions[trackerID]
		instruction.TrackerConfig = request.TrackerConfigOverrides
		instruction.TrackerSite = request.TrackerSiteOverrides
		instructions[trackerID] = instruction
	}
	return instructions
}

func normalizeCLIWorkflowTrackerIDs(trackers []string) []api.TrackerID {
	ids := make([]api.TrackerID, 0, len(trackers))
	for _, tracker := range trackers {
		id := api.TrackerID(strings.ToUpper(strings.TrimSpace(tracker)))
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

func ensureCLIWorkflowProjectionOverrides(
	instructions map[api.TrackerID]api.TrackerProjectionInstructions,
	trackerIDs []api.TrackerID,
	trackerConfig api.TrackerConfigOverrides,
	trackerSite api.TrackerSiteOverrides,
) bool {
	changed := false
	for _, trackerID := range trackerIDs {
		instruction := instructions[trackerID]
		if instruction.TrackerConfig != trackerConfig || instruction.TrackerSite != trackerSite {
			instruction.TrackerConfig = trackerConfig
			instruction.TrackerSite = trackerSite
			instructions[trackerID] = instruction
			changed = true
		}
	}
	return changed
}

func collectCLIWorkflowQuestionnaires(
	reader *bufio.Reader,
	output io.Writer,
	interaction api.InteractionMode,
	projections *api.TrackerReleaseProjectionSet,
	instructions map[api.TrackerID]api.TrackerProjectionInstructions,
) (bool, error) {
	if projections == nil {
		return false, errors.New("upbrr: tracker workflow produced no projections")
	}
	changed := false
	for _, projection := range projections.Projections {
		instruction := instructions[projection.TrackerID]
		for _, field := range projection.Questionnaire {
			if !field.Required || instruction.Questionnaire[field.Key] != nil {
				continue
			}
			if interaction == api.InteractionModeUnattended {
				continue
			}
			if instruction.Questionnaire == nil {
				instruction.Questionnaire = make(map[string]*string)
			}
			label := strings.TrimSpace(field.Label)
			if label == "" {
				label = field.Key
			}
			if len(field.Options) > 0 {
				label += " [" + strings.Join(field.Options, "/") + "]"
			}
			answer, err := promptLine(reader, output, fmt.Sprintf("%s %s: ", projection.TrackerID, label))
			if err != nil {
				return false, err
			}
			if strings.TrimSpace(answer) == "" {
				return false, fmt.Errorf("upbrr: tracker input %s for %s is required", field.Key, projection.TrackerID)
			}
			value := answer
			instruction.Questionnaire[field.Key] = &value
			instructions[projection.TrackerID] = instruction
			changed = true
		}
	}
	return changed, nil
}

func printCLIWorkflowProjections(
	output io.Writer,
	projections *api.TrackerReleaseProjectionSet,
	dupes *api.DupeAssessment,
	includePolicyDetails bool,
) {
	if projections == nil {
		return
	}
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Tracker projections")
	blocked := make([]string, 0)
	if !includePolicyDetails {
		for _, projection := range projections.Projections {
			readiness := cliWorkflowProjectionReadiness(projection, dupes)
			if readiness != api.ReadinessStatusBlocked && readiness != api.ReadinessStatusIneligible {
				continue
			}
			name := strings.TrimSpace(projection.DisplayName)
			if name == "" {
				name = string(projection.TrackerID)
			}
			blocked = append(blocked, name)
		}
	}
	if len(blocked) > 0 {
		fmt.Fprintf(output, "Blocked/ineligible: %s\n", strings.Join(blocked, ", "))
	}
	printed := len(blocked) > 0
	for index, projection := range projections.Projections {
		readiness := cliWorkflowProjectionReadiness(projection, dupes)
		for _, decision := range projection.PolicyDecisions {
			if strings.HasPrefix(decision.Code, "release_name_override") && decision.Message != "" {
				fmt.Fprintf(output, "  %s naming: %s\n", projection.DisplayName, decision.Message)
			}
		}
		if !includePolicyDetails && (readiness == api.ReadinessStatusBlocked || readiness == api.ReadinessStatusIneligible) {
			continue
		}
		if printed || index > 0 {
			fmt.Fprintln(output)
		}
		printed = true
		if cliWorkflowTrackerNameChanged(projection.CanonicalReleaseName, projection.UploadReleaseName) {
			fmt.Fprintf(output, "- %s: RENAMED (readiness=%s)\n", projection.DisplayName, readiness)
			fmt.Fprintf(output, "  original: %s\n", projection.CanonicalReleaseName)
			fmt.Fprintf(output, "  upload:   %s\n", projection.UploadReleaseName)
		} else {
			fmt.Fprintf(output, "- %s: %s (readiness=%s)\n", projection.DisplayName, projection.UploadReleaseName, readiness)
		}
		if !includePolicyDetails {
			continue
		}
		for _, decision := range projection.PolicyDecisions {
			if !auditableProjectionPolicyDecision(decision) {
				continue
			}
			reason := strings.TrimSpace(decision.Message)
			if reason == "" {
				reason = "none"
			}
			disposition := strings.TrimSpace(string(decision.Disposition))
			if disposition == "" {
				disposition = "unspecified"
			}
			evidenceStatus := strings.TrimSpace(string(decision.EvidenceStatus))
			if evidenceStatus == "" {
				evidenceStatus = "unspecified"
			}
			fmt.Fprintf(
				output,
				"  policy: code=%s decision=%s blocking=%t disposition=%s evidence=%s reason=%s\n",
				strings.TrimSpace(decision.Code),
				strings.TrimSpace(decision.Decision),
				decision.Blocking,
				disposition,
				evidenceStatus,
				reason,
			)
		}
	}
}

func cliWorkflowProjectionReadiness(
	projection api.TrackerReleaseProjection,
	dupes *api.DupeAssessment,
) api.ReadinessStatus {
	if dupes == nil {
		return projection.Readiness
	}
	for _, result := range dupes.Results {
		if result.TrackerID != projection.TrackerID {
			continue
		}
		if slices.ContainsFunc(result.Matches, func(match api.DupeMatchProjection) bool {
			return strings.EqualFold(strings.TrimSpace(match.Reason), "in_client")
		}) {
			return api.ReadinessStatusBlocked
		}
		break
	}
	return projection.Readiness
}

func cliWorkflowTrackerNameChanged(canonical string, upload string) bool {
	canonical = strings.TrimSpace(canonical)
	upload = strings.TrimSpace(upload)
	return canonical != "" && upload != "" && canonical != upload
}

func cliWorkflowProjectionForTracker(
	projections *api.TrackerReleaseProjectionSet,
	trackerID api.TrackerID,
) *api.TrackerReleaseProjection {
	if projections == nil {
		return nil
	}
	index := slices.IndexFunc(projections.Projections, func(projection api.TrackerReleaseProjection) bool {
		return projection.TrackerID == trackerID
	})
	if index < 0 {
		return nil
	}
	return &projections.Projections[index]
}

// auditableProjectionPolicyDecision keeps explicit rule outcomes and legacy
// blocking decisions while suppressing non-diagnostic provenance entries.
func auditableProjectionPolicyDecision(decision api.TrackerPolicyDecision) bool {
	switch decision.Disposition {
	case api.RuleDispositionStrict, api.RuleDispositionWaivable, api.RuleDispositionAdvisory:
		return true
	}
	return decision.Blocking ||
		strings.EqualFold(strings.TrimSpace(decision.Decision), "ineligible") ||
		strings.EqualFold(strings.TrimSpace(decision.Decision), "bypassed")
}

func (s *cliWorkflowSession) collectContinuationActionAnswers(
	_ context.Context,
	reader *bufio.Reader,
	_ config.Config,
	_ api.Logger,
) ([]api.RequiredActionAnswer, bool, error) {
	answers := make([]api.RequiredActionAnswer, 0)
	for _, action := range s.current.Continuation.RequiredActions {
		if action.Status != api.RequiredActionStatusPending {
			continue
		}
		switch action.Kind {
		case legacyTrackerAuthActionKind, legacyTrackerTwoFactorActionKind:
			return nil, false, errors.New(
				"upbrr: tracker authentication must be resolved outside the upload workflow; start a fresh attempt",
			)
		case api.RequiredActionAuthorizeRules:
			if s.intent.interaction == api.InteractionModeUnattended {
				continue
			}
			confirmed, err := promptYesNo(reader, s.streams.out, action.Prompt+" [y/N]: ", false)
			if err != nil {
				return nil, false, err
			}
			if !confirmed {
				return nil, true, nil
			}
			answers = append(answers, api.RequiredActionAnswer{
				ActionID:         action.ID,
				WorkflowRevision: s.current.Workflow.Revision,
				Confirmed:        &confirmed,
			})
		case api.RequiredActionResolveTrackerPreparation:
			if s.intent.interaction == api.InteractionModeUnattended {
				continue
			}
			confirmed, err := promptYesNo(reader, s.streams.out, action.Prompt+" [y/N]: ", false)
			if err != nil {
				return nil, false, err
			}
			answers = append(answers, api.RequiredActionAnswer{
				ActionID:         action.ID,
				WorkflowRevision: s.current.Workflow.Revision,
				Confirmed:        &confirmed,
			})
		case api.RequiredActionReconcileSubmission:
			if s.intent.interaction == api.InteractionModeUnattended {
				return nil, false, errors.New("upbrr: unattended external-effect reconciliation requires manual confirmation")
			}
			confirmed, err := promptYesNo(reader, s.streams.out, action.Prompt+" Confirm it did not complete? [y/N]: ", false)
			if err != nil {
				return nil, false, err
			}
			if !confirmed {
				return nil, true, nil
			}
			answers = append(answers, api.RequiredActionAnswer{
				ActionID:         action.ID,
				WorkflowRevision: s.current.Workflow.Revision,
				SelectedValues:   []string{api.RequiredActionReconcileNotCompleted},
			})
		case api.RequiredActionProvideTrackerInput, api.RequiredActionAnswerQuestionnaire, api.RequiredActionReviewDuplicates,
			api.RequiredActionApproveUpload: //nolint:staticcheck // Retained v1 actions are resolved only by legacy authority.
			// Desired intent or exact upload approval resolves these actions.
		case api.RequiredActionApproveTrackers:
			return nil, false, errors.New("upbrr: post-dupe tracker approval requires the composite upload flow")
		case api.RequiredActionSelectPlaylist, api.RequiredActionSelectMetadata, api.RequiredActionConfirmRescan, api.RequiredActionReprepare,
			api.RequiredActionConfirmCorrections:
			return nil, false, fmt.Errorf("upbrr: release workflow requires action %s: %s", action.Kind, action.Prompt)
		}
	}
	return answers, false, nil
}

func cliWorkflowContinuationError(current releaseworkflow.CommandResult, interaction api.InteractionMode) error {
	mode := strings.TrimSpace(string(interaction))
	if mode == "" {
		mode = string(api.InteractionModeInteractive)
	}
	for _, lane := range current.Continuation.TrackerOutcomes {
		if len(lane.Failures) > 0 {
			return fmt.Errorf(
				"upbrr: release workflow interaction=%s tracker %s: %s",
				mode,
				lane.TrackerID,
				lane.Failures[0].Failure.Message,
			)
		}
	}
	if len(current.Workflow.Failures) > 0 {
		return fmt.Errorf(
			"upbrr: release workflow interaction=%s: %s",
			mode,
			current.Workflow.Failures[0].Failure.Message,
		)
	}
	if len(current.Continuation.RequiredActions) > 0 {
		action := current.Continuation.RequiredActions[0]
		return fmt.Errorf(
			"upbrr: release workflow interaction=%s requires action %s: %s",
			mode,
			action.Kind,
			action.Prompt,
		)
	}
	return fmt.Errorf(
		"upbrr: release workflow interaction=%s made no progress toward the requested goal (lifecycle=%s disposition=%s)",
		mode,
		current.Continuation.Lifecycle,
		current.Continuation.Disposition,
	)
}

func cliWorkflowMediaInstructions(request api.Request) api.MediaCaptureInstructions {
	count := max(request.Options.Screens, 0)
	return api.MediaCaptureInstructions{
		ScreenshotCount: count,
		Purpose:         api.ScreenshotPurposeFinal,
		ManualFrames:    append([]int(nil), request.ScreenshotOverrides.ManualFrames...),
		CaptureDVDMenus: request.Options.CaptureDVDMenus,
	}
}

func printCLIWorkflowDryRun(
	output io.Writer,
	result api.UploadDryRunResult,
	noSeed bool,
	projections *api.TrackerReleaseProjectionSet,
	liveTest ...bool,
) {
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Upload dry run")
	switch {
	case len(liveTest) > 0 && liveTest[0]:
		fmt.Fprintln(output, "Live testing: tracker submissions and client mutations are disabled.")
	case noSeed:
		fmt.Fprintln(output, "Debug mode: tracker uploads and client injection are disabled.")
	default:
		fmt.Fprintln(output, "Debug mode: tracker uploads are disabled; client injection was attempted for each ready tracker.")
	}
	for index, report := range result.Reports {
		if index > 0 {
			fmt.Fprintln(output)
		}
		projection := cliWorkflowProjectionForTracker(projections, report.TrackerID)
		if projection != nil && cliWorkflowTrackerNameChanged(projection.CanonicalReleaseName, report.UploadReleaseName) {
			fmt.Fprintf(output, "- %s: RENAMED status=%s\n", report.DisplayName, report.Status)
			fmt.Fprintf(output, "  original: %s\n", projection.CanonicalReleaseName)
			fmt.Fprintf(output, "  upload:   %s\n", report.UploadReleaseName)
		} else {
			fmt.Fprintf(output, "- %s: %s status=%s\n", report.DisplayName, report.UploadReleaseName, report.Status)
		}
		if report.ClientInjection.Status != "" {
			fmt.Fprintf(
				output,
				"  client injection: %s: %s\n",
				report.ClientInjection.Status,
				logging.SanitizeMessage(report.ClientInjection.Message),
			)
		}
		for _, warning := range report.Warnings {
			fmt.Fprintf(output, "  warning: %s\n", logging.SanitizeMessage(warning))
		}
	}
	fmt.Fprintf(
		output,
		"Dry run complete: %d succeeded, %d failed, %d skipped; status=%s.\n",
		result.SucceededCount,
		result.FailedCount,
		result.SkippedCount,
		result.Status,
	)
}

func printCLIWorkflowUploadResult(output io.Writer, result *api.UploadResult) (int, error) {
	if result == nil {
		return 0, errors.New("upbrr: upload workflow produced no result")
	}
	uploaded := 0
	failed := 0
	clientFailed := 0
	for _, tracker := range result.Results {
		submissionStatus := tracker.EffectiveSubmissionStatus()
		clientStatus := tracker.EffectiveClientInjectionStatus()
		fmt.Fprintf(output, "Upload %s: submission=%s client-injection=%s\n", tracker.TrackerID, submissionStatus, clientStatus)
		clientMessage := strings.TrimSpace(logging.SanitizeMessage(tracker.ClientInjectionMessage))
		if clientMessage != "" {
			fmt.Fprintf(output, "  client injection: %s\n", clientMessage)
		}
		for _, failure := range tracker.Failures {
			message := strings.TrimSpace(logging.SanitizeMessage(failure.Failure.Message))
			if message != "" && message != clientMessage {
				fmt.Fprintf(output, "  failure: %s\n", message)
			}
		}
		switch submissionStatus {
		case api.StageStatusCompleted:
			uploaded++
		case api.StageStatusFailed, api.StageStatusUnavailable:
			failed++
		case api.StageStatusPending,
			api.StageStatusQueued,
			api.StageStatusReady,
			api.StageStatusBlocked,
			api.StageStatusStale,
			api.StageStatusPartial,
			api.StageStatusSkipped,
			api.StageStatusRunning,
			api.StageStatusExecuted,
			api.StageStatusInterrupted,
			api.StageStatusCanceled:
		}
		if clientStatus == api.StageStatusFailed {
			clientFailed++
		}
	}
	fmt.Fprintf(output, "Upload complete: %d tracker upload(s).\n", uploaded)
	if clientFailed > 0 {
		fmt.Fprintf(output, "Client injection incomplete: %d tracker artifact(s); retry client injection without resubmitting.\n", clientFailed)
	}
	if failed > 0 {
		return uploaded, fmt.Errorf("upbrr: %d tracker upload(s) failed", failed)
	}
	return uploaded, nil
}

func runCLIWorkflowUploadOnly(
	ctx context.Context,
	coreSvc cliReleaseWorkflowCore,
	batch cliPreparationBatch,
	debug bool,
	queueMode bool,
	cfg config.Config,
	streams cliIO,
	logger api.Logger,
) error {
	streams = streams.normalized()
	reader := bufio.NewReader(streams.in)
	uploaded := 0
	return processCLIPreparationItems(ctx, batch, queueMode, cliItemTimeout, logger, func(itemCtx context.Context, item cliPreparationItem) error {
		request := batch.defaults
		request.SourcePath = item.originalPath
		request.ExternalIDOverrides = item.externalIDs
		request.PlaylistInstruction = item.playlistInstruction
		session, err := newCLIWorkflowSession(itemCtx, coreSvc, request, api.PreparationIntentUpload, reader, cfg, streams, logger)
		if err != nil {
			return err
		}
		defer session.releaseActiveInput(itemCtx)
		count, err := session.complete(itemCtx, debug, reader, cfg, logger)
		uploaded += count
		return err
	})
}

func runCLIWorkflowSiteCheck(
	ctx context.Context,
	coreSvc cliReleaseWorkflowCore,
	opts cliOptions,
	visited map[string]bool,
	item cliPreparationItem,
	screens int,
	cfg config.Config,
	streams cliIO,
	logger api.Logger,
) error {
	request, err := buildCLIRequest(opts, visited, []string{item.originalPath}, screens)
	if err != nil {
		return err
	}
	request.PlaylistInstruction = item.playlistInstruction
	streams = streams.normalized()
	reader := bufio.NewReader(streams.in)
	session, err := newCLIWorkflowSession(ctx, coreSvc, request, api.PreparationIntentDryRun, reader, cfg, streams, logger)
	if err != nil {
		return err
	}
	defer session.releaseActiveInput(ctx)
	fmt.Fprintf(streams.out, "\n[Site Check] %s\n", formatPathLabel(item.originalPath))
	_, err = session.complete(ctx, true, reader, cfg, logger)
	return err
}
