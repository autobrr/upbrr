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
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

const cliWorkflowOwnerID = "cli"

// cliReleaseWorkflowCore is the in-process application seam used by the CLI.
// It deliberately excludes HTTP and every legacy operation-specific entrypoint.
type cliReleaseWorkflowCore interface {
	ContinueReleaseWorkflow(context.Context, string, api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error)
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
	core           cliReleaseWorkflowCore
	logger         api.Logger
	current        releaseworkflow.CommandResult
	intent         cliWorkflowIntent
	idempotencyRun string
	intentSequence uint64
}

// cliWorkflowIntent is the detached CLI adapter input retained after the
// initial flag-to-workflow mapping. Later stages consume shared workflow DTOs,
// not the broad legacy api.Request shape.
type cliWorkflowIntent struct {
	sourcePath               string
	trackerIDs               []api.TrackerID
	projectionInstructions   map[api.TrackerID]api.TrackerProjectionInstructions
	trackerConfig            api.TrackerConfigOverrides
	trackerSite              api.TrackerSiteOverrides
	interaction              api.InteractionMode
	doubleDupeCheck          bool
	skipRemoteDupeCheck      bool
	ignoreDupesFor           map[api.TrackerID]struct{}
	hasManualDVDMenuPaths    bool
	media                    api.MediaCaptureInstructions
	description              api.DescriptionInstructions
	descriptionOverrideURL   string
	descriptionOverrideRaw   string
	descriptionOverrideGroup string
	noSeed                   bool
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
	var eventLogState cliWorkflowEventLogState
	if err := s.logNewOperationEvents(ctx, operation, &eventLogState); err != nil {
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
		if err := s.logNewOperationEvents(ctx, operation, &eventLogState); err != nil {
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
	logger api.Logger,
) (*cliWorkflowSession, error) {
	input, err := api.MapPreparationRequest(request, intent)
	if err != nil {
		return nil, fmt.Errorf("upbrr: map workflow preparation: %w", err)
	}
	session := &cliWorkflowSession{
		core:           coreSvc,
		logger:         logger,
		intent:         mapCLIWorkflowIntent(request),
		idempotencyRun: strings.ToLower(rand.Text()),
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
	ignoreDupesFor := make(map[api.TrackerID]struct{}, len(request.IgnoreDupesFor))
	for _, tracker := range request.IgnoreDupesFor {
		trackerID := api.TrackerID(strings.ToUpper(strings.TrimSpace(tracker)))
		if trackerID != "" {
			ignoreDupesFor[trackerID] = struct{}{}
		}
	}
	overrides := make([]api.DescriptionOverrideInput, 0, len(request.DescriptionGroups))
	for _, group := range request.DescriptionGroups {
		if source := strings.TrimSpace(group.RawDescription); source != "" {
			overrides = append(overrides, api.DescriptionOverrideInput{GroupKey: group.GroupKey, Source: source})
		}
	}
	return cliWorkflowIntent{
		sourcePath:             strings.TrimSpace(request.SourcePath),
		trackerIDs:             normalizeCLIWorkflowTrackerIDs(request.Trackers),
		projectionInstructions: cliProjectionInstructions(request),
		trackerConfig:          request.TrackerConfigOverrides,
		trackerSite:            request.TrackerSiteOverrides,
		interaction:            request.Options.InteractionMode,
		doubleDupeCheck:        request.DoubleDupeCheck,
		skipRemoteDupeCheck:    request.SkipDupeCheck,
		ignoreDupesFor:         ignoreDupesFor,
		hasManualDVDMenuPaths:  len(request.ScreenshotOverrides.MenuPaths) > 0,
		media:                  cliWorkflowMediaInstructions(request),
		description: api.DescriptionInstructions{
			Overrides:     overrides,
			Options:       request.Options,
			ImageHost:     request.ImageHostOverrides,
			TrackerConfig: request.TrackerConfigOverrides,
			TrackerSite:   request.TrackerSiteOverrides,
			Client:        request.ClientOverrides,
			Torrent:       request.TorrentOverrides,
		},
		descriptionOverrideURL:   strings.TrimSpace(request.DescriptionOverrideURL),
		descriptionOverrideRaw:   strings.TrimSpace(request.DescriptionOverrideRaw),
		descriptionOverrideGroup: strings.TrimSpace(request.DescriptionOverrideGroup),
		noSeed:                   request.Options.NoSeed,
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
	selected, err := selectCLIWorkflowPlaylists(reader, *action, cfg.Metadata.UseLargestPlaylist)
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

func selectCLIWorkflowPlaylists(reader *bufio.Reader, action api.RequiredAction, useLargest bool) ([]string, error) {
	if len(action.Options) == 0 {
		return nil, errors.New("upbrr: Blu-ray playlist action has no candidates")
	}
	if useLargest {
		return []string{action.Options[0].Value}, nil
	}
	if reader == nil {
		return nil, errors.New("upbrr: Blu-ray playlist selection requires input")
	}
	fmt.Println()
	fmt.Println(action.Prompt)
	for index, option := range action.Options {
		fmt.Printf("%d. %s\n", index+1, option.Label)
	}
	answer, err := promptLine(reader, "Playlist number(s), comma-separated: ")
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
	stdin io.Reader,
	logger api.Logger,
) error {
	reader := bufio.NewReader(stdin)
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
		session, err = newCLIWorkflowSession(ctx, coreSvc, request, api.PreparationIntentPreview, reader, cfg, logger)
		if err != nil {
			return err
		}
		preview := cliWorkflowMetadataPreview(session.current)
		printMetadataPreview(preview, currentOpts.Debug)
		if currentOpts.interactionMode() == api.InteractionModeUnattended {
			break
		}
		confirmed, promptErr := promptYesNo(reader, "Metadata correct? [Y/n]: ", true)
		if promptErr != nil {
			return promptErr
		}
		if confirmed {
			break
		}
		editArgs, promptErr := promptLine(reader, "Input args that need correction, or 'continue': ")
		if promptErr != nil {
			return promptErr
		}
		if strings.EqualFold(strings.TrimSpace(editArgs), "continue") {
			break
		}
		editTokens, splitErr := splitInteractiveCLIArgs(editArgs)
		if splitErr != nil {
			fmt.Printf("Invalid override args: %v\n", splitErr)
			continue
		}
		nextArgs := append(append([]string(nil), currentArgs...), editTokens...)
		nextOpts, nextVisited, _, parseErr := parseCLIOptions(nextArgs)
		if parseErr != nil {
			fmt.Printf("Invalid override args: %v\n", parseErr)
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
	if s.intent.hasManualDVDMenuPaths {
		return 0, errors.New("upbrr: manual DVD menu paths are not accepted by the retained workflow; use automatic menu capture")
	}
	instructions := s.intent.projectionInstructions
	if instructions == nil {
		instructions = make(map[api.TrackerID]api.TrackerProjectionInstructions)
	}
	duplicateCheckCount := uint8(1)
	if s.intent.doubleDupeCheck {
		duplicateCheckCount = 2
	}
	goal := api.WorkflowGoalUploaded
	if debug {
		goal = api.WorkflowGoalDryRun
	}
	executionMode := api.WorkflowExecutionModeNormal
	if debug {
		executionMode = api.WorkflowExecutionModeDebug
	}
	request := api.ContinueReleaseWorkflowRequest{
		Authority: &api.WorkflowAuthority{
			WorkflowID:       s.current.Workflow.ID,
			ExpectedRevision: s.current.Workflow.Revision,
		},
		IdempotencyKey: s.nextIdempotencyKey("complete"),
		Goal:           goal,
		Intent: api.WorkflowIntent{
			Interaction:            s.intent.interaction,
			ExecutionMode:          executionMode,
			TrackerIDs:             append([]api.TrackerID(nil), s.intent.trackerIDs...),
			ProjectionInstructions: instructions,
			SkipRemoteDuplicates:   s.intent.skipRemoteDupeCheck,
			DuplicateCheckCount:    duplicateCheckCount,
			DuplicateDecisions:     make(map[api.TrackerID]api.DupeDecision),
			Media:                  &s.intent.media,
			NoSeed:                 s.intent.noSeed,
		},
	}
	var (
		projectionRevision api.WorkflowRevision
		dupeRevision       api.WorkflowRevision
	)
	for range 64 {
		if debug && s.current.DryRun != nil {
			printCLIWorkflowDryRun(*s.current.DryRun, s.intent.noSeed)
			return 0, nil
		}
		if !debug && s.current.UploadResult != nil {
			return printCLIWorkflowUploadResult(s.current.UploadResult)
		}
		if s.current.Selection != nil && len(s.current.Selection.TrackerIDs) == 0 {
			fmt.Printf("No trackers configured for %s\n", formatPathLabel(s.intent.sourcePath))
			return 0, nil
		}

		intentChanged := false
		if s.current.Projections != nil && s.current.Projections.Revision != projectionRevision {
			projectionRevision = s.current.Projections.Revision
			if s.current.Projections.Preflight != nil {
				printCLIWorkflowProjections(s.current.Projections)
			}
			if s.current.Selection != nil {
				intentChanged = ensureCLIWorkflowProjectionOverrides(
					instructions,
					s.current.Selection.TrackerIDs,
					s.intent.trackerConfig,
					s.intent.trackerSite,
				)
			}
			questionnaireChanged, err := collectCLIWorkflowQuestionnaires(
				reader,
				s.intent.interaction,
				s.current.Projections,
				instructions,
			)
			if err != nil {
				return 0, err
			}
			intentChanged = intentChanged || questionnaireChanged
			request.Intent.ProjectionInstructions = instructions
			descriptionInstructions, err := cliWorkflowDescriptionInstructions(s.intent, instructions, s.current.Projections)
			if err != nil {
				return 0, err
			}
			descriptionChanged, err := setCLIWorkflowDescriptionIntent(&request, descriptionInstructions)
			if err != nil {
				return 0, err
			}
			intentChanged = intentChanged || descriptionChanged
		}

		if s.current.Dupes != nil && s.current.Dupes.Revision != dupeRevision {
			dupeRevision = s.current.Dupes.Revision
			decisions, err := s.collectDuplicateDecisions(reader, debug)
			if err != nil {
				return 0, err
			}
			for trackerID, decision := range decisions {
				if request.Intent.DuplicateDecisions[trackerID] != decision {
					request.Intent.DuplicateDecisions[trackerID] = decision
					intentChanged = true
				}
			}
		}

		answers, declined, err := s.collectContinuationActionAnswers(ctx, reader, cfg, logger)
		if err != nil {
			return 0, err
		}
		if declined {
			return 0, nil
		}
		request.Answers = answers
		if len(answers) > 0 {
			intentChanged = true
		}
		if !debug && request.Approval == nil {
			approval, declined, err := s.collectUploadApproval(reader)
			if err != nil {
				return 0, err
			}
			if declined {
				return 0, nil
			}
			if approval != nil {
				request.Approval = approval
				intentChanged = true
			}
		}
		if intentChanged {
			request.IdempotencyKey = s.nextIdempotencyKey("continue")
		}

		priorRevision := s.current.Workflow.Revision
		request.Authority = &api.WorkflowAuthority{
			WorkflowID:       s.current.Workflow.ID,
			ExpectedRevision: priorRevision,
		}
		if err := s.executeContinuation(ctx, request); err != nil {
			return 0, err
		}
		if s.current.Workflow.Revision == priorRevision {
			if cliWorkflowNoUploadCandidates(s.current.Continuation) {
				fmt.Printf("No trackers selected for %s\n", formatPathLabel(s.intent.sourcePath))
				return 0, nil
			}
			return 0, cliWorkflowContinuationError(s.current)
		}
	}
	return 0, errors.New("upbrr: release workflow continuation exceeded the transition limit")
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
			answer, err := promptLine(reader, fmt.Sprintf("%s %s: ", projection.TrackerID, label))
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

func printCLIWorkflowProjections(projections *api.TrackerReleaseProjectionSet) {
	if projections == nil {
		return
	}
	fmt.Println()
	fmt.Println("Tracker projections")
	for _, projection := range projections.Projections {
		fmt.Printf("- %s: %s (readiness=%s)\n", projection.DisplayName, projection.UploadReleaseName, projection.Readiness)
		for _, decision := range projection.PolicyDecisions {
			if !auditableProjectionPolicyDecision(decision) {
				continue
			}
			reason := strings.TrimSpace(decision.Message)
			if reason == "" {
				reason = "none"
			}
			fmt.Printf(
				"  policy: code=%s decision=%s blocking=%t reason=%s\n",
				strings.TrimSpace(decision.Code),
				strings.TrimSpace(decision.Decision),
				decision.Blocking,
				reason,
			)
		}
	}
}

func auditableProjectionPolicyDecision(decision api.TrackerPolicyDecision) bool {
	return decision.Blocking ||
		strings.EqualFold(strings.TrimSpace(decision.Decision), "ineligible") ||
		strings.EqualFold(strings.TrimSpace(decision.Decision), "bypassed")
}

func setCLIWorkflowDescriptionIntent(
	request *api.ContinueReleaseWorkflowRequest,
	instructions api.DescriptionInstructions,
) (bool, error) {
	if request.Intent.Descriptions != nil {
		currentFingerprint, err := api.CanonicalWorkflowFingerprint(*request.Intent.Descriptions)
		if err != nil {
			return false, fmt.Errorf("upbrr: fingerprint current description intent: %w", err)
		}
		nextFingerprint, err := api.CanonicalWorkflowFingerprint(instructions)
		if err != nil {
			return false, fmt.Errorf("upbrr: fingerprint next description intent: %w", err)
		}
		if currentFingerprint == nextFingerprint {
			return false, nil
		}
	}
	request.Intent.Descriptions = &instructions
	return true, nil
}

func (s *cliWorkflowSession) collectContinuationActionAnswers(
	ctx context.Context,
	reader *bufio.Reader,
	cfg config.Config,
	logger api.Logger,
) ([]api.RequiredActionAnswer, bool, error) {
	authTrackers := make([]string, 0)
	for _, action := range s.current.Continuation.RequiredActions {
		if action.Status == api.RequiredActionStatusPending &&
			(action.Kind == api.RequiredActionAuthenticateTracker || action.Kind == api.RequiredActionProvideTwoFactor) &&
			!slices.Contains(authTrackers, string(action.TrackerID)) {
			authTrackers = append(authTrackers, string(action.TrackerID))
		}
	}
	if len(authTrackers) > 0 && s.intent.interaction != api.InteractionModeUnattended {
		ready, err := ensureCLITrackerAuthBeforeDupeCheckWithLogger(
			ctx,
			reader,
			cfg,
			s.intent.interaction,
			authTrackers,
			cliWorkflowMetadataPreview(s.current),
			logger,
		)
		if err != nil {
			return nil, false, err
		}
		if len(ready) != len(authTrackers) {
			return nil, false, errors.New("upbrr: tracker authentication remains incomplete")
		}
	}

	answers := make([]api.RequiredActionAnswer, 0)
	for _, action := range s.current.Continuation.RequiredActions {
		if action.Status != api.RequiredActionStatusPending {
			continue
		}
		switch action.Kind {
		case api.RequiredActionAuthenticateTracker, api.RequiredActionProvideTwoFactor:
			if s.intent.interaction == api.InteractionModeUnattended {
				continue
			}
			confirmed := true
			answers = append(answers, api.RequiredActionAnswer{
				ActionID:         action.ID,
				WorkflowRevision: s.current.Workflow.Revision,
				Confirmed:        &confirmed,
			})
		case api.RequiredActionAuthorizeRules:
			if s.intent.interaction == api.InteractionModeUnattended {
				continue
			}
			confirmed, err := promptYesNo(reader, action.Prompt+" [y/N]: ", false)
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
		case api.RequiredActionReconcileSubmission:
			if s.intent.interaction == api.InteractionModeUnattended {
				return nil, false, errors.New("upbrr: unattended external-effect reconciliation requires manual confirmation")
			}
			confirmed, err := promptYesNo(reader, action.Prompt+" Confirm it did not complete? [y/N]: ", false)
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
			api.RequiredActionApproveUpload:
			// Desired intent or exact upload approval resolves these actions.
		case api.RequiredActionSelectPlaylist, api.RequiredActionSelectMetadata, api.RequiredActionConfirmRescan, api.RequiredActionReprepare:
			return nil, false, fmt.Errorf("upbrr: release workflow requires action %s: %s", action.Kind, action.Prompt)
		}
	}
	return answers, false, nil
}

func (s *cliWorkflowSession) collectDuplicateDecisions(
	reader *bufio.Reader,
	debug bool,
) (map[api.TrackerID]api.DupeDecision, error) {
	if s.current.Dupes == nil {
		return nil, errors.New("upbrr: duplicate workflow produced no assessment")
	}
	decisions := make(map[api.TrackerID]api.DupeDecision)
	for _, result := range s.current.Dupes.Results {
		searchName := strings.TrimSpace(result.Criteria.Name)
		if searchName != "" && searchName != strings.TrimSpace(result.UploadReleaseName) {
			fmt.Printf(
				"Dupe check %s: upload_name=%s search_name=%s matches=%d decision=%s\n",
				result.TrackerID,
				result.UploadReleaseName,
				searchName,
				len(result.Matches),
				result.Decision,
			)
		} else {
			fmt.Printf(
				"Dupe check %s: upload_name=%s matches=%d decision=%s\n",
				result.TrackerID,
				result.UploadReleaseName,
				len(result.Matches),
				result.Decision,
			)
		}
		if result.Decision != api.DupeDecisionPending {
			continue
		}
		if _, ok := s.intent.ignoreDupesFor[result.TrackerID]; ok {
			decisions[result.TrackerID] = api.DupeDecisionIgnored
			continue
		}
		if debug || s.intent.interaction == api.InteractionModeUnattended {
			decisions[result.TrackerID] = api.DupeDecisionAccepted
			continue
		}
		allow, err := promptYesNo(reader, fmt.Sprintf("Upload to %s despite duplicate evidence? [y/N]: ", result.TrackerID), false)
		if err != nil {
			return nil, err
		}
		if allow {
			decisions[result.TrackerID] = api.DupeDecisionIgnored
		} else {
			decisions[result.TrackerID] = api.DupeDecisionAccepted
		}
	}
	return decisions, nil
}

func (s *cliWorkflowSession) collectUploadApproval(
	reader *bufio.Reader,
) (*api.UploadApproval, bool, error) {
	if s.current.DryRun == nil {
		return nil, false, nil
	}
	action := pendingCLIWorkflowAction(s.current.Continuation.RequiredActions, api.RequiredActionApproveUpload)
	if action == nil {
		return nil, false, nil
	}
	if s.intent.interaction != api.InteractionModeUnattended {
		confirmed, err := promptYesNo(reader, "Execute tracker uploads? [y/N]: ", false)
		if err != nil {
			return nil, false, err
		}
		if !confirmed {
			return nil, true, nil
		}
	}
	return &api.UploadApproval{
		ActionID: action.ID,
		DryRun: api.UploadDryRunResultRef{
			ID:       s.current.DryRun.ID,
			Revision: s.current.DryRun.Revision,
		},
		InputFingerprint: s.current.DryRun.InputFingerprint,
	}, false, nil
}

func cliWorkflowNoUploadCandidates(continuation api.WorkflowContinuation) bool {
	if len(continuation.TrackerOutcomes) == 0 {
		return false
	}
	for _, lane := range continuation.TrackerOutcomes {
		if lane.Goal != api.WorkflowGoalTrackersAssessed ||
			(lane.Disposition != api.WorkflowDispositionFailed && lane.Disposition != api.WorkflowDispositionCanceled) {
			return false
		}
	}
	return true
}

func cliWorkflowContinuationError(current releaseworkflow.CommandResult) error {
	for _, lane := range current.Continuation.TrackerOutcomes {
		if len(lane.Failures) > 0 {
			return fmt.Errorf(
				"upbrr: release workflow tracker %s: %s",
				lane.TrackerID,
				lane.Failures[0].Failure.Message,
			)
		}
	}
	if len(current.Workflow.Failures) > 0 {
		return fmt.Errorf("upbrr: release workflow: %s", current.Workflow.Failures[0].Failure.Message)
	}
	if len(current.Continuation.RequiredActions) > 0 {
		action := current.Continuation.RequiredActions[0]
		return fmt.Errorf("upbrr: release workflow requires action %s: %s", action.Kind, action.Prompt)
	}
	return fmt.Errorf(
		"upbrr: release workflow made no progress toward the requested goal (lifecycle=%s disposition=%s)",
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

func cliWorkflowDescriptionInstructions(
	intent cliWorkflowIntent,
	projectionInstructions map[api.TrackerID]api.TrackerProjectionInstructions,
	projections *api.TrackerReleaseProjectionSet,
) (api.DescriptionInstructions, error) {
	if intent.descriptionOverrideURL != "" && intent.descriptionOverrideRaw == "" {
		return api.DescriptionInstructions{}, errors.New("upbrr: URL-only description overrides must be resolved before workflow generation")
	}
	instructions := intent.description
	instructions.Overrides = append([]api.DescriptionOverrideInput(nil), instructions.Overrides...)
	if source := intent.descriptionOverrideRaw; source != "" {
		groupKey := intent.descriptionOverrideGroup
		if groupKey == "" && projections != nil {
			for _, projection := range projections.Projections {
				if projection.DescriptionGroup != "" {
					groupKey = projection.DescriptionGroup
					break
				}
			}
		}
		if groupKey == "" {
			groupKey = "default"
		}
		instructions.Overrides = append(instructions.Overrides, api.DescriptionOverrideInput{GroupKey: groupKey, Source: source})
	}
	questionnaires := make(map[api.TrackerID]map[string]string, len(projectionInstructions))
	for trackerID, projectionInstruction := range projectionInstructions {
		answers := make(map[string]string, len(projectionInstruction.Questionnaire))
		for key, answer := range projectionInstruction.Questionnaire {
			if answer != nil {
				answers[key] = *answer
			}
		}
		if len(answers) > 0 {
			questionnaires[trackerID] = answers
		}
	}
	instructions.QuestionnaireAnswers = questionnaires
	return instructions, nil
}

func printCLIWorkflowDryRun(result api.UploadDryRunResult, noSeed bool) {
	fmt.Println()
	fmt.Println("Upload dry run")
	if noSeed {
		fmt.Println("Debug mode: tracker uploads and client injection are disabled.")
	} else {
		fmt.Println("Debug mode: tracker uploads are disabled; client injection was attempted for each ready tracker.")
	}
	for _, report := range result.Reports {
		fmt.Printf("- %s: %s status=%s\n", report.DisplayName, report.UploadReleaseName, report.Status)
		if report.ClientInjection.Status != "" {
			fmt.Printf("  client injection: %s: %s\n", report.ClientInjection.Status, report.ClientInjection.Message)
		}
		for _, warning := range report.Warnings {
			fmt.Printf("  warning: %s\n", warning)
		}
	}
	fmt.Printf(
		"Dry run complete: %d succeeded, %d failed, %d skipped; status=%s.\n",
		result.SucceededCount,
		result.FailedCount,
		result.SkippedCount,
		result.Status,
	)
}

func printCLIWorkflowUploadResult(result *api.UploadResult) (int, error) {
	if result == nil {
		return 0, errors.New("upbrr: upload workflow produced no result")
	}
	uploaded := 0
	failed := 0
	for _, tracker := range result.Results {
		fmt.Printf("Upload %s: %s\n", tracker.TrackerID, tracker.Status)
		switch tracker.Status {
		case api.StageStatusCompleted:
			uploaded++
		case api.StageStatusFailed:
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
			api.StageStatusCanceled,
			api.StageStatusUnavailable:
		}
	}
	fmt.Printf("Upload complete: %d tracker upload(s).\n", uploaded)
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
	stdin io.Reader,
	logger api.Logger,
) error {
	reader := bufio.NewReader(stdin)
	uploaded := 0
	return processCLIPreparationItems(ctx, batch, queueMode, cliItemTimeout, logger, func(itemCtx context.Context, item cliPreparationItem) error {
		request := batch.defaults
		request.SourcePath = item.originalPath
		request.ExternalIDOverrides = item.externalIDs
		request.PlaylistInstruction = item.playlistInstruction
		session, err := newCLIWorkflowSession(itemCtx, coreSvc, request, api.PreparationIntentUpload, reader, cfg, logger)
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
	stdin io.Reader,
	logger api.Logger,
) error {
	request, err := buildCLIRequest(opts, visited, []string{item.originalPath}, screens)
	if err != nil {
		return err
	}
	request.PlaylistInstruction = item.playlistInstruction
	reader := bufio.NewReader(stdin)
	session, err := newCLIWorkflowSession(ctx, coreSvc, request, api.PreparationIntentDryRun, reader, cfg, logger)
	if err != nil {
		return err
	}
	fmt.Printf("\n[Site Check] %s\n", formatPathLabel(item.originalPath))
	_, err = session.complete(ctx, true, reader, cfg, logger)
	return err
}
