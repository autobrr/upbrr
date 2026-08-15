// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/uploadinput"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	legacyTrackerAuthActionKind        = api.RequiredActionKind("authenticate_tracker")
	legacyTrackerTwoFactorActionKind   = api.RequiredActionKind("provide_two_factor")
	legacyTrackerAuthFeedbackKind      = api.ReleaseWorkflowUploadFeedbackKind("trackerAuthentication")
	legacyTrackerTwoFactorFeedbackKind = api.ReleaseWorkflowUploadFeedbackKind("twoFactor")
)

func (s *cliWorkflowSession) completeComposite(
	ctx context.Context,
	debug bool,
	reader *bufio.Reader,
	cfg config.Config,
	logger api.Logger,
) (int, error) {
	request, err := mapCLICompositeUploadRequest(s.uploadRequest, debug, s.nextIdempotencyKey("upload"))
	if err != nil {
		return 0, err
	}
	if s.current.FactInstructions != nil {
		request.Preparation.Facts = compositeCLIFacts(s.current.FactInstructions.Instructions)
	}
	ctx, request, err = resolveCLICompositeUploadInputs(ctx, request)
	if err != nil {
		return 0, err
	}
	current, err := s.core.StartReleaseWorkflowUpload(ctx, cliWorkflowOwnerID, request)
	if err != nil {
		return 0, fmt.Errorf("upbrr: start composite upload: %w", err)
	}
	current, err = s.waitForCompositeUpload(ctx, current)
	if err != nil {
		return 0, err
	}
	s.current = current

	printProjections := cliWorkflowProjectionOutputEnabled(
		cfg.Logging.Level,
		s.uploadRequest.Options.RunLogLevel,
		debug,
	)
	var projectionRevision api.WorkflowRevision
	for range 64 {
		if printProjections && s.current.Projections != nil && s.current.Dupes != nil &&
			s.current.Projections.Revision != projectionRevision {
			projectionRevision = s.current.Projections.Revision
			printCLIWorkflowProjections(s.current.Projections, s.current.Dupes)
		}
		if debug && s.current.DryRun != nil {
			printCLIWorkflowDryRun(*s.current.DryRun, s.intent.noSeed, s.current.Projections)
			return 0, nil
		}
		if !debug && s.current.UploadResult != nil {
			return printCLIWorkflowUploadResult(s.current.UploadResult)
		}
		if s.current.Selection != nil && len(s.current.Selection.TrackerIDs) == 0 {
			fmt.Printf("No trackers configured for %s\n", formatPathLabel(s.intent.sourcePath))
			return 0, nil
		}

		action := firstPendingCLICompositeAction(s.current.Continuation.RequiredActions)
		if action == nil {
			return 0, cliWorkflowContinuationError(s.current, s.intent.interaction)
		}
		feedback, declined, feedbackErr := s.collectCompositeUploadFeedback(ctx, reader, cfg, logger, *action)
		if feedbackErr != nil {
			return 0, feedbackErr
		}
		if declined {
			return 0, nil
		}
		next, submitErr := s.core.SubmitReleaseWorkflowUploadFeedback(
			ctx,
			cliWorkflowOwnerID,
			s.current.Workflow.ID,
			feedback,
		)
		if submitErr != nil {
			return 0, fmt.Errorf("upbrr: submit composite upload feedback: %w", submitErr)
		}
		next, err = s.waitForCompositeUpload(ctx, next)
		if err != nil {
			return 0, err
		}
		s.current = next
	}
	return 0, errors.New("upbrr: composite upload exceeded the transition limit")
}

func cliWorkflowProjectionOutputEnabled(configured string, runOverride string, debug bool) bool {
	level, err := logging.ParseLevel(logging.ResolveEffectiveLevel(configured, runOverride, debug))
	return err == nil && level >= logging.LevelDebug
}

func resolveCLICompositeUploadInputs(
	ctx context.Context,
	request api.CreateReleaseWorkflowUploadRequest,
) (context.Context, api.CreateReleaseWorkflowUploadRequest, error) {
	resolvedContext, resolved, err := uploadinput.Resolve(ctx, request)
	if err != nil {
		return ctx, api.CreateReleaseWorkflowUploadRequest{}, fmt.Errorf("upbrr: resolve composite upload inputs: %w", err)
	}
	return resolvedContext, resolved, nil
}

func (s *cliWorkflowSession) waitForCompositeUpload(
	ctx context.Context,
	current releaseworkflow.CommandResult,
) (releaseworkflow.CommandResult, error) {
	if current.Operation == nil || isTerminalCLIWorkflowOperation(current.Operation.Status) {
		return current, nil
	}
	completed, err := s.waitForOperation(ctx, *current.Operation)
	if err != nil {
		return releaseworkflow.CommandResult{}, err
	}
	latest, err := s.core.CurrentReleaseWorkflow(ctx, cliWorkflowOwnerID, completed.WorkflowID)
	if err != nil {
		return releaseworkflow.CommandResult{}, fmt.Errorf("upbrr: load composite upload workflow: %w", err)
	}
	latest.Operation = &completed
	return latest, nil
}

func firstPendingCLICompositeAction(actions []api.RequiredAction) *api.RequiredAction {
	for index := range actions {
		if actions[index].Status == api.RequiredActionStatusPending {
			return &actions[index]
		}
	}
	return nil
}

func (s *cliWorkflowSession) collectCompositeUploadFeedback(
	_ context.Context,
	reader *bufio.Reader,
	cfg config.Config,
	logger api.Logger,
	action api.RequiredAction,
) (api.ReleaseWorkflowUploadFeedback, bool, error) {
	feedback := api.ReleaseWorkflowUploadFeedback{
		Action: api.ReleaseWorkflowUploadActionIdentity{
			ID:               action.ID,
			WorkflowRevision: action.WorkflowRevision,
		},
		IdempotencyKey: s.nextIdempotencyKey("feedback-" + string(action.Kind)),
	}
	if action.Kind == legacyTrackerAuthActionKind || action.Kind == legacyTrackerTwoFactorActionKind {
		return feedback, false, errors.New(
			"upbrr: tracker authentication must be resolved outside the upload workflow; start a fresh attempt",
		)
	}
	if s.intent.interaction == api.InteractionModeUnattended &&
		action.Kind != api.RequiredActionAuthorizeRules && action.Kind != api.RequiredActionResolveTrackerPreparation {
		return feedback, false, fmt.Errorf("upbrr: strict unattended upload requires global action %s: %s", action.Kind, action.Prompt)
	}

	switch action.Kind {
	case legacyTrackerAuthActionKind, legacyTrackerTwoFactorActionKind:
		return feedback, false, errors.New(
			"upbrr: tracker authentication must be resolved outside the upload workflow; start a fresh attempt",
		)
	case api.RequiredActionSelectPlaylist:
		selected, err := selectCLIWorkflowPlaylists(reader, action, cfg.Metadata.UseLargestPlaylist)
		if err != nil {
			return feedback, false, err
		}
		feedback.Response = api.ReleaseWorkflowUploadFeedbackResponse{
			Kind: api.ReleaseWorkflowUploadFeedbackPlaylistSelection,
			PlaylistSelection: &api.ReleaseWorkflowUploadPlaylistSelection{
				Selected: selected,
			},
		}
	case api.RequiredActionSelectMetadata:
		selected, err := selectCLICompositeOption(reader, action, "Metadata option")
		if err != nil {
			return feedback, false, err
		}
		feedback.Response = api.ReleaseWorkflowUploadFeedbackResponse{
			Kind: api.ReleaseWorkflowUploadFeedbackMetadataSelection,
			MetadataSelection: &api.ReleaseWorkflowUploadMetadataSelection{
				SelectedValues: []string{selected},
			},
		}
	case api.RequiredActionConfirmRescan:
		return compositeCLIConfirmationFeedback(
			reader,
			action,
			feedback,
			api.ReleaseWorkflowUploadFeedbackRescanConfirmation,
		)
	case api.RequiredActionProvideTrackerInput, api.RequiredActionAnswerQuestionnaire:
		return s.collectCompositeTrackerFeedback(reader, action, feedback)
	case api.RequiredActionAuthorizeRules:
		return s.collectCompositeRuleOverrideFeedback(reader, action, feedback)
	case api.RequiredActionResolveTrackerPreparation:
		confirmed := false
		if s.intent.interaction != api.InteractionModeUnattended {
			var err error
			confirmed, err = promptYesNo(reader, action.Prompt+" [y/N]: ", false)
			if err != nil {
				return feedback, false, err
			}
		}
		feedback.Response = api.ReleaseWorkflowUploadFeedbackResponse{
			Kind: api.ReleaseWorkflowUploadFeedbackTrackerPreparation,
			TrackerPreparation: &api.ReleaseWorkflowUploadConfirmation{
				Confirmed: confirmed,
			},
		}
	case api.RequiredActionReviewDuplicates:
		return s.collectCompositeDuplicateFeedback(reader, action, feedback)
	case api.RequiredActionApproveTrackers:
		return s.collectCompositeTrackerApproval(reader, logger, action, feedback)
	case api.RequiredActionApproveUpload: //nolint:staticcheck // Reject retained v1 actions explicitly.
		return feedback, false, errors.New("upbrr: obsolete final upload approval requires a fresh workflow continuation")
	case api.RequiredActionReprepare:
		return compositeCLIConfirmationFeedback(
			reader,
			action,
			feedback,
			api.ReleaseWorkflowUploadFeedbackReprepare,
		)
	case api.RequiredActionReconcileSubmission:
		confirmed, err := promptYesNo(reader, action.Prompt+" Confirm it did not complete? [y/N]: ", false)
		if err != nil {
			return feedback, false, err
		}
		if !confirmed {
			return feedback, true, nil
		}
		feedback.Response = api.ReleaseWorkflowUploadFeedbackResponse{
			Kind: api.ReleaseWorkflowUploadFeedbackReconciliation,
			Reconciliation: &api.ReleaseWorkflowUploadReconciliation{
				Selection: api.RequiredActionReconcileNotCompleted,
			},
		}
	default:
		return feedback, false, fmt.Errorf("upbrr: unsupported composite upload action %s", action.Kind)
	}
	return feedback, false, nil
}

func (s *cliWorkflowSession) collectCompositeRuleOverrideFeedback(
	reader *bufio.Reader,
	action api.RequiredAction,
	feedback api.ReleaseWorkflowUploadFeedback,
) (api.ReleaseWorkflowUploadFeedback, bool, error) {
	confirmed := false
	if s.intent.interaction != api.InteractionModeUnattended {
		var err error
		confirmed, err = promptYesNo(reader, action.Prompt+" [y/N]: ", false)
		if err != nil {
			return feedback, false, err
		}
	}
	feedback.Response = api.ReleaseWorkflowUploadFeedbackResponse{
		Kind: api.ReleaseWorkflowUploadFeedbackRuleAuthorization,
		RuleAuthorization: &api.ReleaseWorkflowUploadConfirmation{
			Confirmed: confirmed,
		},
	}
	if confirmed {
		return feedback, false, nil
	}
	trackerID := api.TrackerID(strings.ToUpper(strings.TrimSpace(string(action.TrackerID))))
	if !s.hasTrackerAfterRuleSkip(trackerID) {
		return feedback, true, nil
	}
	return feedback, false, nil
}

func (s *cliWorkflowSession) hasTrackerAfterRuleSkip(skipped api.TrackerID) bool {
	if s.current.Projections == nil {
		return false
	}
	return slices.ContainsFunc(s.current.Projections.Projections, func(projection api.TrackerReleaseProjection) bool {
		return projection.TrackerID != skipped &&
			projection.Readiness != api.ReadinessStatusIneligible
	})
}

func compositeCLIConfirmationFeedback(
	reader *bufio.Reader,
	action api.RequiredAction,
	feedback api.ReleaseWorkflowUploadFeedback,
	kind api.ReleaseWorkflowUploadFeedbackKind,
) (api.ReleaseWorkflowUploadFeedback, bool, error) {
	confirmed, err := promptYesNo(reader, action.Prompt+" [y/N]: ", false)
	if err != nil {
		return feedback, false, err
	}
	if !confirmed {
		return feedback, true, nil
	}
	confirmation := &api.ReleaseWorkflowUploadConfirmation{Confirmed: true}
	feedback.Response.Kind = kind
	switch kind {
	case api.ReleaseWorkflowUploadFeedbackRescanConfirmation:
		feedback.Response.RescanConfirmation = confirmation
	case api.ReleaseWorkflowUploadFeedbackReprepare:
		feedback.Response.Reprepare = &api.ReleaseWorkflowUploadReprepare{Confirmed: true}
	case api.ReleaseWorkflowUploadFeedbackPlaylistSelection,
		api.ReleaseWorkflowUploadFeedbackMetadataSelection,
		legacyTrackerAuthFeedbackKind,
		legacyTrackerTwoFactorFeedbackKind,
		api.ReleaseWorkflowUploadFeedbackTrackerInput,
		api.ReleaseWorkflowUploadFeedbackQuestionnaire,
		api.ReleaseWorkflowUploadFeedbackRuleAuthorization,
		api.ReleaseWorkflowUploadFeedbackTrackerPreparation,
		api.ReleaseWorkflowUploadFeedbackDuplicateReview,
		api.ReleaseWorkflowUploadFeedbackTrackerApproval,
		api.ReleaseWorkflowUploadFeedbackUploadApproval, //nolint:staticcheck // Reject retained v1 feedback explicitly.
		api.ReleaseWorkflowUploadFeedbackReconciliation:
		return feedback, false, fmt.Errorf("upbrr: unsupported confirmation feedback %s", kind)
	}
	return feedback, false, nil
}

func (s *cliWorkflowSession) collectCompositeTrackerApproval(
	reader *bufio.Reader,
	logger api.Logger,
	action api.RequiredAction,
	feedback api.ReleaseWorkflowUploadFeedback,
) (api.ReleaseWorkflowUploadFeedback, bool, error) {
	if s.current.Projections == nil || s.current.Dupes == nil {
		return feedback, false, errors.New("upbrr: post-dupe tracker review is unavailable")
	}
	projectionByTracker := make(map[api.TrackerID]api.TrackerReleaseProjection, len(s.current.Projections.Projections))
	for _, projection := range s.current.Projections.Projections {
		projectionByTracker[projection.TrackerID] = projection
	}
	dupeByTracker := make(map[api.TrackerID]api.TrackerDupeAssessment, len(s.current.Dupes.Results))
	for _, result := range s.current.Dupes.Results {
		dupeByTracker[result.TrackerID] = result
	}
	approved := make([]api.TrackerID, 0, len(action.Options))
	for index, option := range action.Options {
		trackerID := api.TrackerID(strings.ToUpper(strings.TrimSpace(option.Value)))
		projection, ok := projectionByTracker[trackerID]
		if !ok {
			return feedback, false, fmt.Errorf("upbrr: tracker approval candidate %s has no current projection", trackerID)
		}
		if index > 0 {
			fmt.Println()
		}
		tracker := strings.TrimSpace(projection.DisplayName)
		if tracker == "" {
			tracker = string(trackerID)
		}
		dupe := dupeByTracker[trackerID]
		fmt.Printf("Tracker: %s (%s)\n", tracker, trackerID)
		fmt.Printf("Upload name: %q\n", projection.UploadReleaseName)
		fmt.Printf(
			"Duplicate check: decision=%s candidates=%d search_complete=%t policy=%s\n",
			dupe.Decision,
			len(dupe.Matches),
			dupe.Search.Complete,
			emptyCLIValue(dupe.PolicyID),
		)
		fmt.Printf(
			"Requirements: screenshots=%d dvd_menus=%d image_hosting=%t descriptions=%t\n",
			projection.Artifacts.ScreenshotCount,
			projection.Artifacts.DVDMenuCount,
			projection.Artifacts.ImageHosting,
			projection.Artifacts.Description,
		)
		if logger != nil {
			logger.Debugf(
				"tracker approval: tracker=%s screenshots=%d dvd_menus=%d image_hosting=%t descriptions=%t",
				trackerID,
				projection.Artifacts.ScreenshotCount,
				projection.Artifacts.DVDMenuCount,
				projection.Artifacts.ImageHosting,
				projection.Artifacts.Description,
			)
		}
		prompt := fmt.Sprintf("Use %s as %q? [y/N]: ", tracker, projection.UploadReleaseName)
		confirmed, err := promptYesNo(reader, prompt, false)
		if err != nil {
			return feedback, false, err
		}
		if confirmed {
			approved = append(approved, trackerID)
		}
	}
	if len(action.Options) == 0 {
		return feedback, false, errors.New("upbrr: post-dupe tracker review contains no candidates")
	}
	if len(approved) == 0 {
		return feedback, true, nil
	}
	if logger != nil {
		logger.Infof("tracker approval: decision=approve count=%d candidates=%d", len(approved), len(action.Options))
	}
	feedback.Response = api.ReleaseWorkflowUploadFeedbackResponse{
		Kind: api.ReleaseWorkflowUploadFeedbackTrackerApproval,
		TrackerApproval: &api.ReleaseWorkflowUploadTrackerApproval{
			Confirmed:  true,
			TrackerIDs: approved,
		},
	}
	return feedback, false, nil
}

func selectCLICompositeOption(
	reader *bufio.Reader,
	action api.RequiredAction,
	label string,
) (string, error) {
	if len(action.Options) == 0 {
		return "", fmt.Errorf("upbrr: action %s has no options", action.Kind)
	}
	fmt.Println()
	fmt.Println(action.Prompt)
	for index, option := range action.Options {
		fmt.Printf("%d. %s\n", index+1, option.Label)
	}
	answer, err := promptLine(reader, label+" number: ")
	if err != nil {
		return "", err
	}
	for index, option := range action.Options {
		if answer == strconv.Itoa(index+1) || strings.EqualFold(strings.TrimSpace(answer), option.Value) {
			return option.Value, nil
		}
	}
	return "", fmt.Errorf("upbrr: no valid option selected for %s", action.Kind)
}

func (s *cliWorkflowSession) collectCompositeTrackerFeedback(
	reader *bufio.Reader,
	action api.RequiredAction,
	feedback api.ReleaseWorkflowUploadFeedback,
) (api.ReleaseWorkflowUploadFeedback, bool, error) {
	if action.TrackerID == "" {
		return feedback, false, fmt.Errorf("upbrr: action %s requires media or input outside tracker projection feedback", action.Kind)
	}
	instructions := cliProjectionInstructions(s.uploadRequest)
	ensureCLIWorkflowProjectionOverrides(
		instructions,
		[]api.TrackerID{action.TrackerID},
		s.intent.trackerConfig,
		s.intent.trackerSite,
	)
	_, err := collectCLIWorkflowQuestionnaires(reader, s.intent.interaction, s.current.Projections, instructions)
	if err != nil {
		return feedback, false, err
	}
	instruction := instructions[action.TrackerID]
	if action.Kind == api.RequiredActionProvideTrackerInput && action.AllowsFreeText && len(action.Options) > 0 {
		proposed := strings.TrimSpace(action.Options[0].Value)
		if proposed == "" {
			return feedback, false, fmt.Errorf("upbrr: release-name confirmation for %s has no proposed name", action.TrackerID)
		}
		fmt.Println()
		fmt.Println(action.Prompt)
		answer, promptErr := promptLine(reader, fmt.Sprintf("Release name [%s]: ", proposed))
		if promptErr != nil {
			return feedback, false, promptErr
		}
		if answer == "" {
			answer = proposed
		}
		instruction.UploadReleaseName = api.WorkflowPatch[string]{Present: true, Value: answer}
		instructions[action.TrackerID] = instruction
	}
	projection := compositeCLIProjectionFromInstruction(instruction)
	if action.Kind == api.RequiredActionProvideTrackerInput {
		feedback.Response = api.ReleaseWorkflowUploadFeedbackResponse{
			Kind: api.ReleaseWorkflowUploadFeedbackTrackerInput,
			TrackerInput: &api.ReleaseWorkflowUploadTrackerInput{
				TrackerID:  action.TrackerID,
				Projection: projection,
			},
		}
		return feedback, false, nil
	}
	if len(instruction.Questionnaire) == 0 {
		return feedback, false, fmt.Errorf("upbrr: questionnaire answers for %s remain incomplete", action.TrackerID)
	}
	feedback.Response = api.ReleaseWorkflowUploadFeedbackResponse{
		Kind: api.ReleaseWorkflowUploadFeedbackQuestionnaire,
		Questionnaire: &api.ReleaseWorkflowUploadQuestionnaire{
			TrackerID: action.TrackerID,
			Answers:   cloneCLIStringPointerMap(instruction.Questionnaire),
		},
	}
	return feedback, false, nil
}

func (s *cliWorkflowSession) collectCompositeDuplicateFeedback(
	reader *bufio.Reader,
	action api.RequiredAction,
	feedback api.ReleaseWorkflowUploadFeedback,
) (api.ReleaseWorkflowUploadFeedback, bool, error) {
	if s.current.Dupes == nil {
		return feedback, false, errors.New("upbrr: duplicate workflow produced no assessment")
	}
	resultIndex := slices.IndexFunc(s.current.Dupes.Results, func(result api.TrackerDupeAssessment) bool {
		return result.TrackerID == action.TrackerID
	})
	if resultIndex < 0 {
		return feedback, false, fmt.Errorf("upbrr: duplicate assessment for %s is unavailable", action.TrackerID)
	}
	result := s.current.Dupes.Results[resultIndex]
	fmt.Printf(
		"Dupe check %s: upload_name=%s candidates=%d decision=%s search_complete=%t pages=%d policy=%s\n",
		result.TrackerID,
		result.UploadReleaseName,
		len(result.Matches),
		result.Decision,
		result.Search.Complete,
		result.Search.Pages,
		emptyCLIValue(result.PolicyID),
	)
	printCLICompositeDupeMatches(result.Matches)
	fmt.Println()
	prompt := fmt.Sprintf("Upload to %s despite duplicate evidence? [y/N]: ", result.TrackerID)
	if cliDupeRequiresRiskAcknowledgement(result) {
		prompt = fmt.Sprintf("Acknowledge incomplete/manual policy evidence and upload to %s? [y/N]: ", result.TrackerID)
	}
	allow, err := promptYesNo(reader, prompt, false)
	if err != nil {
		return feedback, false, err
	}
	fmt.Println()
	decision := api.DupeDecisionAccepted
	if allow {
		decision = api.DupeDecisionIgnored
	}
	feedback.Response = api.ReleaseWorkflowUploadFeedbackResponse{
		Kind: api.ReleaseWorkflowUploadFeedbackDuplicateReview,
		DuplicateReview: &api.ReleaseWorkflowUploadDuplicateReview{
			TrackerID: result.TrackerID,
			Decision:  decision,
		},
	}
	return feedback, false, nil
}

func printCLICompositeDupeMatches(matches []api.DupeMatchProjection) {
	if len(matches) == 0 {
		return
	}
	fmt.Println("Duplicate candidates:")
	for index, match := range matches {
		fmt.Printf("  %d. %s\n", index+1, strings.TrimSpace(match.Name))
		fmt.Printf(
			"     Relation: %s  Evidence: %s/%s\n",
			emptyCLIValue(string(match.Relation)),
			emptyCLIValue(string(match.EvidenceStatus)),
			emptyCLIValue(string(match.HDR.Origin)),
		)
		if len(match.Reasons) > 0 {
			reasons := make([]string, 0, len(match.Reasons))
			for _, reason := range match.Reasons {
				if code := strings.TrimSpace(reason.Code); code != "" {
					reasons = append(reasons, code)
				}
			}
			if len(reasons) > 0 {
				fmt.Printf("     Reasons: %s\n", strings.Join(reasons, ","))
			}
		} else if reason := strings.TrimSpace(match.Reason); reason != "" {
			fmt.Printf("     Reason: %s\n", reason)
		}
		if len(match.HDR.Formats) > 0 {
			formats := make([]string, len(match.HDR.Formats))
			for index, format := range match.HDR.Formats {
				formats[index] = string(format)
			}
			fmt.Printf("     HDR: %s\n", strings.Join(formats, "+"))
		}
		if link := strings.TrimSpace(match.Link); link != "" {
			fmt.Printf("     Link: %s\n", link)
		}
	}
}

func cliDupeRequiresRiskAcknowledgement(result api.TrackerDupeAssessment) bool {
	if result.Search.Pages > 0 && !result.Search.Complete {
		return true
	}
	return slices.ContainsFunc(result.Matches, func(match api.DupeMatchProjection) bool {
		switch match.Relation {
		case api.DupeRelationSameSlot, api.DupeRelationProposedTrumps, api.DupeRelationManualReview,
			api.DupeRelationInsufficientEvidence:
			return true
		case "", api.DupeRelationExactDuplicate, api.DupeRelationExistingPreferred, api.DupeRelationCoexists:
			return false
		}
		return false
	})
}

func emptyCLIValue(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "none"
}

func compositeCLIProjectionFromInstruction(
	instruction api.TrackerProjectionInstructions,
) api.ReleaseWorkflowUploadTrackerProjection {
	return api.ReleaseWorkflowUploadTrackerProjection{
		UploadReleaseName: instruction.UploadReleaseName,
		AdditionalNames:   cloneCLIStringPointerMap(instruction.AdditionalNames),
		Questionnaire:     cloneCLIStringPointerMap(instruction.Questionnaire),
		Config: api.ReleaseWorkflowUploadTrackerConfig{
			Anon:    cloneCLIBoolPointer(instruction.TrackerConfig.Anon),
			Draft:   cloneCLIBoolPointer(instruction.TrackerConfig.Draft),
			ModQ:    cloneCLIBoolPointer(instruction.TrackerConfig.ModQ),
			Channel: cloneCLIStringPointer(instruction.TrackerConfig.Channel),
		},
		Site: api.ReleaseWorkflowUploadTrackerSite{
			TIK: api.ReleaseWorkflowUploadTIKOptions{
				Foreign:  cloneCLIBoolPointer(instruction.TrackerSite.TIK.Foreign),
				Opera:    cloneCLIBoolPointer(instruction.TrackerSite.TIK.Opera),
				Asian:    cloneCLIBoolPointer(instruction.TrackerSite.TIK.Asian),
				DiscType: cloneCLIStringPointer(instruction.TrackerSite.TIK.DiscType),
			},
		},
	}
}

func mapCLICompositeUploadRequest(
	request api.Request,
	debug bool,
	idempotencyKey string,
) (api.CreateReleaseWorkflowUploadRequest, error) {
	preparation, err := api.MapPreparationRequest(request, api.PreparationIntentUpload)
	if err != nil {
		return api.CreateReleaseWorkflowUploadRequest{}, fmt.Errorf("upbrr: map composite upload preparation: %w", err)
	}
	mode := api.ReleaseWorkflowUploadModeUpload
	if debug || request.Execution.SiteCheck {
		mode = api.ReleaseWorkflowUploadModeDebug
	}
	confirm := request.Options.InteractionMode != api.InteractionModeUnattended
	sourceIDs := make(map[api.TrackerID]string, len(preparation.Instructions.TrackerIDs))
	for trackerID, sourceID := range preparation.Instructions.TrackerIDs {
		normalized := api.TrackerID(strings.ToUpper(strings.TrimSpace(trackerID)))
		if normalized != "" {
			sourceIDs[normalized] = strings.TrimSpace(sourceID)
		}
	}
	projections := make(map[api.TrackerID]api.ReleaseWorkflowUploadTrackerProjection)
	for trackerID, instruction := range cliProjectionInstructions(request) {
		projections[trackerID] = api.ReleaseWorkflowUploadTrackerProjection{
			UploadReleaseName: instruction.UploadReleaseName,
			AdditionalNames:   cloneCLIStringPointerMap(instruction.AdditionalNames),
			Questionnaire:     cloneCLIStringPointerMap(instruction.Questionnaire),
		}
	}
	defaultProjection := &api.ReleaseWorkflowUploadTrackerProjection{
		Config: api.ReleaseWorkflowUploadTrackerConfig{
			Anon:    cloneCLIBoolPointer(request.TrackerConfigOverrides.Anon),
			Draft:   cloneCLIBoolPointer(request.TrackerConfigOverrides.Draft),
			ModQ:    cloneCLIBoolPointer(request.TrackerConfigOverrides.ModQ),
			Channel: cloneCLIStringPointer(request.TrackerConfigOverrides.Channel),
		},
		Site: api.ReleaseWorkflowUploadTrackerSite{
			TIK: api.ReleaseWorkflowUploadTIKOptions{
				Foreign:  cloneCLIBoolPointer(request.TrackerSiteOverrides.TIK.Foreign),
				Opera:    cloneCLIBoolPointer(request.TrackerSiteOverrides.TIK.Opera),
				Asian:    cloneCLIBoolPointer(request.TrackerSiteOverrides.TIK.Asian),
				DiscType: cloneCLIStringPointer(request.TrackerSiteOverrides.TIK.DiscType),
			},
		},
	}
	if compositeCLIProjectionEmpty(*defaultProjection) {
		defaultProjection = nil
	}
	descriptions := make([]api.ReleaseWorkflowUploadDescriptionOverride, 0, len(request.DescriptionGroups)+1)
	for _, group := range request.DescriptionGroups {
		source := group.Source()
		if strings.TrimSpace(group.GroupKey) != "" && strings.TrimSpace(source) != "" {
			descriptions = append(descriptions, api.ReleaseWorkflowUploadDescriptionOverride{
				GroupKey: strings.TrimSpace(group.GroupKey),
				Inline:   &source,
			})
		}
	}
	if source := request.DescriptionOverrideRaw; strings.TrimSpace(source) != "" {
		group := strings.TrimSpace(request.DescriptionOverrideGroup)
		if group == "" {
			group = "default"
		}
		descriptions = append(descriptions, api.ReleaseWorkflowUploadDescriptionOverride{GroupKey: group, Inline: &source})
	} else if sourceURL := strings.TrimSpace(request.DescriptionOverrideURL); sourceURL != "" {
		group := strings.TrimSpace(request.DescriptionOverrideGroup)
		if group == "" {
			group = "default"
		}
		descriptions = append(descriptions, api.ReleaseWorkflowUploadDescriptionOverride{GroupKey: group, URL: &sourceURL})
	}
	checkCount := 1
	if request.DoubleDupeCheck {
		checkCount = 2
	}
	remoteCheck := !request.SkipDupeCheck
	onEvidence := api.ReleaseWorkflowDuplicateDisposition("")
	if request.SkipDupeAsActual {
		onEvidence = api.ReleaseWorkflowDuplicateUpload
	}
	screenshotCount := request.Options.Screens
	captureMenus := request.Options.CaptureDVDMenus
	return api.CreateReleaseWorkflowUploadRequest{
		Source: api.ReleaseWorkflowUploadSource{Path: preparation.SourcePath},
		Unattended: &api.ReleaseWorkflowUploadUnattended{
			Confirm: confirm,
		},
		Execution: api.ReleaseWorkflowUploadExecution{
			Mode:            mode,
			PreparedRelease: api.ReleaseWorkflowPreparedReleaseRequire,
			RunLogLevel:     stringPointerWhenSet(request.Options.RunLogLevel),
		},
		Trackers: api.ReleaseWorkflowUploadTrackers{
			Include:           normalizeCLIWorkflowTrackerIDs(request.Trackers),
			Remove:            normalizeCLIWorkflowTrackerIDs(request.TrackersRemove),
			SourceIDs:         sourceIDs,
			DefaultProjection: defaultProjection,
			Projection:        projections,
		},
		Preparation: api.ReleaseWorkflowUploadPreparation{
			Facts:         compositeCLIFacts(preparation.Instructions),
			Policy:        compositeCLIPolicy(preparation.Policy),
			ClientSearch:  compositeCLIClientSearch(preparation.Search),
			Force:         preparation.Force,
			ConfirmRescan: preparation.Controls.ConfirmBDMVRescan,
		},
		Duplicates: api.ReleaseWorkflowUploadDuplicates{
			RemoteCheck: &remoteCheck,
			CheckCount:  &checkCount,
			OnEvidence:  onEvidence,
			AllowUpload: normalizeCLIWorkflowTrackerIDs(request.IgnoreDupesFor),
		},
		Media: api.ReleaseWorkflowUploadMedia{
			Screenshots: api.ReleaseWorkflowUploadScreenshots{
				Count:                  &screenshotCount,
				Frames:                 append([]int(nil), request.ScreenshotOverrides.ManualFrames...),
				ComparisonPaths:        append([]string(nil), request.ScreenshotOverrides.ComparisonPaths...),
				ComparisonPrimaryIndex: cloneCLIIntPointer(request.ScreenshotOverrides.ComparisonPrimaryIndex),
			},
			DVDMenus: api.ReleaseWorkflowUploadDVDMenus{
				Capture:   &captureMenus,
				MenuPaths: append([]string(nil), request.ScreenshotOverrides.MenuPaths...),
			},
		},
		Descriptions: api.ReleaseWorkflowUploadDescriptions{Overrides: descriptions},
		ImageHosting: api.ReleaseWorkflowUploadImageHosting{
			PreferredHost: cloneCLIStringPointer(request.ImageHostOverrides.PreferredHost),
			SkipUpload:    cloneCLIBoolPointer(request.ImageHostOverrides.SkipUpload),
		},
		Client: api.ReleaseWorkflowUploadClient{
			NoSeed:            new(request.Options.NoSeed),
			SkipAutoDiscovery: new(request.Options.SkipAutoTorrent),
			Selected:          cloneCLIStringPointer(request.ClientOverrides.Client),
			QbitTag:           cloneCLIStringPointer(request.ClientOverrides.QbitTag),
			QbitCategory:      cloneCLIStringPointer(request.ClientOverrides.QbitCategory),
			ForceRecheck:      cloneCLIBoolPointer(request.ClientOverrides.ForceRecheck),
		},
		Torrent: api.ReleaseWorkflowUploadTorrent{
			InfoHash:        cloneCLIStringPointer(request.TorrentOverrides.InfoHash),
			MaxPieceSizeMiB: cloneCLIIntPointer(request.TorrentOverrides.MaxPieceSizeMiB),
			NoHash:          cloneCLIBoolPointer(request.TorrentOverrides.NoHash),
			Rehash:          cloneCLIBoolPointer(request.TorrentOverrides.Rehash),
		},
		IdempotencyKey: idempotencyKey,
	}, nil
}

func compositeCLIFacts(instructions api.ReleaseFactInstructions) api.ReleaseWorkflowUploadFacts {
	facts := api.ReleaseWorkflowUploadFacts{
		ReleaseName: api.ReleaseWorkflowUploadReleaseName{
			Category:         cloneCLIStringPointer(instructions.ReleaseName.Category),
			Type:             cloneCLIStringPointer(instructions.ReleaseName.Type),
			Source:           cloneCLIStringPointer(instructions.ReleaseName.Source),
			Resolution:       cloneCLIStringPointer(instructions.ReleaseName.Resolution),
			Tag:              cloneCLIStringPointer(instructions.ReleaseName.Tag),
			Service:          cloneCLIStringPointer(instructions.ReleaseName.Service),
			Edition:          cloneCLIStringPointer(instructions.ReleaseName.Edition),
			Season:           cloneCLIStringPointer(instructions.ReleaseName.Season),
			Episode:          cloneCLIStringPointer(instructions.ReleaseName.Episode),
			EpisodeTitle:     cloneCLIStringPointer(instructions.ReleaseName.EpisodeTitle),
			ManualYear:       cloneCLIIntPointer(instructions.ReleaseName.ManualYear),
			Daily:            cloneCLIStringPointer(instructions.ReleaseName.ManualDate),
			UseSeasonEpisode: cloneCLIBoolPointer(instructions.ReleaseName.UseSeasonEpisode),
			NoSeason:         cloneCLIBoolPointer(instructions.ReleaseName.NoSeason),
			NoYear:           cloneCLIBoolPointer(instructions.ReleaseName.NoYear),
			NoAKA:            cloneCLIBoolPointer(instructions.ReleaseName.NoAKA),
			NoTag:            cloneCLIBoolPointer(instructions.ReleaseName.NoTag),
			NoEpisodeTitle:   cloneCLIBoolPointer(instructions.ReleaseName.NoEpisodeTitle),
			NoDistributor:    cloneCLIBoolPointer(instructions.ReleaseName.NoDistributor),
			NoEdition:        cloneCLIBoolPointer(instructions.ReleaseName.NoEdition),
			NoDub:            cloneCLIBoolPointer(instructions.ReleaseName.NoDub),
			NoDual:           cloneCLIBoolPointer(instructions.ReleaseName.NoDual),
			DualAudio:        cloneCLIBoolPointer(instructions.ReleaseName.DualAudio),
			Region:           cloneCLIStringPointer(instructions.ReleaseName.Region),
		},
		Metadata: api.ReleaseWorkflowUploadMetadata{
			Distributor:      cloneCLIStringPointer(instructions.Metadata.Distributor),
			OriginalLanguage: cloneCLIStringPointer(instructions.Metadata.OriginalLanguage),
			PersonalRelease:  cloneCLIBoolPointer(instructions.Metadata.PersonalRelease),
			Commentary:       cloneCLIBoolPointer(instructions.Metadata.Commentary),
			WebDV:            cloneCLIBoolPointer(instructions.Metadata.WebDV),
			StreamOptimized:  cloneCLIBoolPointer(instructions.Metadata.StreamOptimized),
			Anime:            cloneCLIBoolPointer(instructions.Metadata.Anime),
		},
		Category: cloneCLICategoryPointer(instructions.Category),
	}
	if instructions.SourceLookup != "" {
		facts.SourceLookup = cloneCLIStringPointer(&instructions.SourceLookup)
	}
	if instructions.Playlist.Set {
		facts.Playlist = &api.ReleaseWorkflowUploadPlaylist{
			Selected: append([]string(nil), instructions.Playlist.Selected...),
			UseAll:   instructions.Playlist.UseAll,
		}
	}
	facts.ExternalIDs = compositeCLIExternalIDs(instructions.Identity)
	return facts
}

func compositeCLIExternalIDs(ids api.ExternalIDOverrides) api.ReleaseWorkflowUploadExternalIDs {
	result := api.ReleaseWorkflowUploadExternalIDs{
		TMDB:   compositeCLINumericID(ids.TMDBID),
		TVDB:   compositeCLINumericID(ids.TVDBID),
		TVmaze: compositeCLINumericID(ids.TVmazeID),
		MAL:    compositeCLINumericID(ids.MALID),
	}
	if ids.IMDBID != nil {
		value := ""
		if *ids.IMDBID > 0 {
			value = providerid.IMDb(*ids.IMDBID).Prefixed()
		}
		result.IMDB = &api.ReleaseWorkflowUploadStringID{Value: &value}
	}
	return result
}

func compositeCLINumericID(value *int) *api.ReleaseWorkflowUploadNumericID {
	if value == nil {
		return nil
	}
	return &api.ReleaseWorkflowUploadNumericID{Value: cloneCLIIntPointer(value)}
}

func compositeCLIPolicy(policy api.PreparationPolicy) api.ReleaseWorkflowUploadPolicy {
	return api.ReleaseWorkflowUploadPolicy{
		KeepFolder: new(policy.KeepFolder),
		KeepImages: new(policy.KeepImages),
		OnlyID:     new(policy.OnlyID),
	}
}

func compositeCLIClientSearch(search api.ClientSearchPolicy) api.ReleaseWorkflowUploadClientSearch {
	return api.ReleaseWorkflowUploadClientSearch{
		Skip:   new(search.Skip),
		Client: cloneCLIStringPointer(search.Client),
	}
}

func compositeCLIProjectionEmpty(value api.ReleaseWorkflowUploadTrackerProjection) bool {
	return value.Config.Anon == nil && value.Config.Draft == nil && value.Config.ModQ == nil && value.Config.Channel == nil &&
		value.Site.TIK.Foreign == nil && value.Site.TIK.Opera == nil && value.Site.TIK.Asian == nil && value.Site.TIK.DiscType == nil
}

func stringPointerWhenSet(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func cloneCLIStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneCLIBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneCLIIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneCLICategoryPointer(value *api.CanonicalCategory) *api.CanonicalCategory {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneCLIStringPointerMap(value map[string]*string) map[string]*string {
	if value == nil {
		return nil
	}
	cloned := make(map[string]*string, len(value))
	for key, item := range value {
		cloned[key] = cloneCLIStringPointer(item)
	}
	return cloned
}
