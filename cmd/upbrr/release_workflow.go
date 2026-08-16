// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

const cliWorkflowOwnerID = "cli"

// cliReleaseWorkflowCore is the in-process application seam used by the CLI.
// It deliberately excludes HTTP and every legacy operation-specific entrypoint.
type cliReleaseWorkflowCore interface {
	ContinueReleaseWorkflow(context.Context, string, api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error)
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
	current, err := s.core.ContinueReleaseWorkflow(ctx, cliWorkflowOwnerID, request)
	if err != nil {
		return fmt.Errorf("upbrr: continue release workflow: %w", err)
	}
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
	if err := session.continueUntilStable(ctx, api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: session.nextIdempotencyKey("prepare"),
		Goal:           api.WorkflowGoalPrepared,
		Intent: api.WorkflowIntent{
			FactInstructions: &input.Instructions,
			Preparation:      &input,
		},
	}); err != nil {
		return nil, err
	}
	if err := session.resolvePlaylistAction(ctx, input, session.intent.interaction, reader, cfg); err != nil {
		return nil, err
	}
	if session.current.Release == nil {
		return nil, errors.New("upbrr: release workflow produced no canonical release")
	}
	session.intent.sourcePath = session.current.Release.Release.Source.SourcePath
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
		return []string{action.Options[0].Value}, nil
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
	for _, token := range strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' }) {
		for index, option := range action.Options {
			if token != strconv.Itoa(index+1) && !strings.EqualFold(token, option.Value) {
				continue
			}
			if _, ok := seen[option.Value]; !ok {
				seen[option.Value] = struct{}{}
				selected = append(selected, option.Value)
			}
			break
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("upbrr: no valid Blu-ray playlist selected")
	}
	return selected, nil
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
		request api.Request
		session *cliWorkflowSession
		err     error
	)
	for {
		request, err = buildCLIRequest(currentOpts, currentVisited, []string{sourcePath}, screens)
		if err != nil {
			return err
		}
		request.PlaylistInstruction = cloneCLIPlaylistInstruction(playlist)
		session, err = newCLIWorkflowSession(ctx, coreSvc, request, api.PreparationIntentPreview, reader, cfg, streams, logger)
		if err != nil {
			return err
		}
		preview := cliWorkflowMetadataPreview(session.current)
		printMetadataPreview(streams.out, preview, currentOpts.Debug)
		if currentOpts.interactionMode() == api.InteractionModeUnattended {
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
		nextArgs := append(append([]string(nil), currentArgs...), editTokens...)
		nextOpts, nextVisited, _, parseErr := parseCLIOptions(nextArgs)
		if parseErr != nil {
			fmt.Fprintf(streams.out, "Invalid override args: %v\n", parseErr)
			continue
		}
		currentArgs, currentOpts, currentVisited = nextArgs, nextOpts, nextVisited
	}
	_, err = session.complete(ctx, currentOpts.Debug, reader, cfg, logger)
	return err
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
	return s.completeComposite(ctx, debug, reader, cfg, logger)
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
		case api.RequiredActionSelectPlaylist, api.RequiredActionSelectMetadata, api.RequiredActionConfirmRescan, api.RequiredActionReprepare:
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
	var selections []api.ScreenshotSelection
	if len(request.ScreenshotOverrides.ManualFrames) > 0 {
		selections = make([]api.ScreenshotSelection, 0, len(request.ScreenshotOverrides.ManualFrames))
	}
	for index, frame := range request.ScreenshotOverrides.ManualFrames {
		selections = append(selections, api.ScreenshotSelection{
			Index:  index,
			Frame:  frame,
			Source: "manual",
		})
	}
	return api.MediaCaptureInstructions{
		ScreenshotCount: count,
		Purpose:         api.ScreenshotPurposeFinal,
		Selections:      selections,
		CaptureDVDMenus: request.Options.CaptureDVDMenus,
	}
}

func printCLIWorkflowDryRun(
	output io.Writer,
	result api.UploadDryRunResult,
	noSeed bool,
	projections *api.TrackerReleaseProjectionSet,
) {
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Upload dry run")
	if noSeed {
		fmt.Fprintln(output, "Debug mode: tracker uploads and client injection are disabled.")
	} else {
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
	fmt.Fprintf(streams.out, "\n[Site Check] %s\n", formatPathLabel(item.originalPath))
	_, err = session.complete(ctx, true, reader, cfg, logger)
	return err
}
