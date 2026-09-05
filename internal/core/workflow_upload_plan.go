// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const workflowUploadPlanTTL = 15 * time.Minute

type workflowRetainedUploadService interface {
	PrepareRetainedUploadPlan(context.Context, api.UploadSubject, []api.TrackerReleaseProjection) (workflowRetainedUploadPlan, error)
}

type workflowRetainedUploadPlan interface {
	Preparations() []trackers.RetainedTrackerPreparation
	ResolveAction(context.Context, string, api.RequiredActionKind, bool) (trackers.RetainedTrackerPreparation, error)
	Execute(context.Context) ([]trackers.RetainedTrackerResult, error)
	ExecuteSelected(context.Context, []string) ([]trackers.RetainedTrackerResult, error)
	Release() error
}

type workflowTrackerServiceAdapter struct {
	service *trackers.Service
}

func (a workflowTrackerServiceAdapter) PrepareRetainedUploadPlan(
	ctx context.Context,
	subject api.UploadSubject,
	projections []api.TrackerReleaseProjection,
) (workflowRetainedUploadPlan, error) {
	plan, err := a.service.PrepareRetainedUploadPlan(ctx, subject, projections)
	if err != nil {
		return nil, fmt.Errorf("core: prepare retained workflow upload plan: %w", err)
	}
	return plan, nil
}

type workflowUploadPlanBuilder struct {
	config   config.Config
	resolver workflowDescriptionSubjectResolver
	trackers workflowRetainedUploadService
	torrents api.TorrentService
	clients  api.ClientService
}

type workflowUploadExecution struct {
	plan                workflowRetainedUploadPlan
	clients             api.ClientService
	clientSubject       api.ClientSubject
	noSeed              bool
	dryRunInjected      map[api.TrackerID]struct{}
	registeredArtifacts map[api.TrackerID]api.TorrentResult
	crossSeeds          []api.UploadedTorrent
	projections         map[api.TrackerID]api.TrackerReleaseProjection
	inputFingerprint    api.WorkflowFingerprint
	trackers            []api.UploadPlanTracker
}

func newWorkflowUploadPlanBuilder(
	cfg config.Config,
	resolver workflowDescriptionSubjectResolver,
	service api.TrackerService,
	torrents api.TorrentService,
	clients api.ClientService,
) workflowUploadPlanBuilder {
	retained, _ := service.(workflowRetainedUploadService)
	if concrete, ok := service.(*trackers.Service); ok {
		retained = workflowTrackerServiceAdapter{service: concrete}
	}
	return workflowUploadPlanBuilder{
		config:   cfg,
		resolver: resolver,
		trackers: retained,
		torrents: torrents,
		clients:  clients,
	}
}

func (b workflowUploadPlanBuilder) Fingerprint(
	_ context.Context,
	projections api.TrackerReleaseProjectionSet,
	dupes api.DupeAssessment,
	media api.MediaArtifactSet,
	descriptions api.DescriptionSet,
	options releaseworkflow.UploadPlanBuildOptions,
) (api.WorkflowFingerprint, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		ProjectionSet    api.TrackerReleaseProjectionSetRef
		Projection       api.WorkflowFingerprint
		ProjectionPolicy api.WorkflowFingerprint
		Dupes            api.DupeAssessmentRef
		DupeInput        api.WorkflowFingerprint
		Media            api.MediaArtifactSetRef
		MediaCapture     api.WorkflowFingerprint
		Descriptions     api.DescriptionSetRef
		Description      api.WorkflowFingerprint
		NoSeed           bool
		RehashCooldown   int
		TrackerIDs       []api.TrackerID
		TrackerApproval  *api.TrackerApprovalSnapshotRef
		Authority        api.WorkflowFingerprint
	}{
		ProjectionSet:    api.TrackerReleaseProjectionSetRef{ID: projections.ID, Revision: projections.Revision},
		Projection:       projections.InputFingerprint,
		ProjectionPolicy: projections.PolicyFingerprint,
		Dupes:            api.DupeAssessmentRef{ID: dupes.ID, Revision: dupes.Revision},
		DupeInput:        dupes.InputFingerprint,
		Media:            api.MediaArtifactSetRef{ID: media.ID, Revision: media.Revision},
		MediaCapture:     media.CaptureFingerprint,
		Descriptions:     api.DescriptionSetRef{ID: descriptions.ID, Revision: descriptions.Revision},
		Description:      descriptions.InputFingerprint,
		NoSeed:           options.NoSeed,
		RehashCooldown:   b.config.TorrentCreation.RehashCooldown,
		TrackerIDs:       append([]api.TrackerID(nil), options.TrackerIDs...),
		TrackerApproval:  options.TrackerApproval,
		Authority:        options.AuthorityFingerprint,
	})
	if err != nil {
		return "", fmt.Errorf("workflow upload plan fingerprint: %w", err)
	}
	return fingerprint, nil
}

func (b workflowUploadPlanBuilder) Build(
	ctx context.Context,
	projections api.TrackerReleaseProjectionSet,
	dupes api.DupeAssessment,
	privateDupes any,
	media api.MediaArtifactSet,
	privateMedia any,
	descriptions api.DescriptionSet,
	privateDescriptions any,
	options releaseworkflow.UploadPlanBuildOptions,
	now time.Time,
) (api.UploadPlan, releaseworkflow.RetainedUploadExecution, error) {
	if err := ctx.Err(); err != nil {
		return api.UploadPlan{}, nil, fmt.Errorf("workflow upload plan: %w", err)
	}
	if b.resolver == nil || b.trackers == nil {
		return api.UploadPlan{}, nil, errors.New("workflow upload plan: retained tracker preparation is unavailable")
	}
	dupeEvidence, ok := privateDupes.(workflowDupePrivateEvidence)
	if !ok {
		return api.UploadPlan{}, nil, errors.New("workflow upload plan: duplicate evidence is incompatible")
	}
	exactMedia, err := resolveWorkflowExactMedia(privateMedia, media)
	if err != nil {
		return api.UploadPlan{}, nil, fmt.Errorf("workflow upload plan: %w", err)
	}
	descriptionInstructions, ok := privateDescriptions.(api.DescriptionInstructions)
	if !ok {
		return api.UploadPlan{}, nil, errors.New("workflow upload plan: description inputs are incompatible")
	}
	descriptionInstructions.Options.NoSeed = options.NoSeed
	inputFingerprint, err := b.Fingerprint(ctx, projections, dupes, media, descriptions, options)
	if err != nil {
		return api.UploadPlan{}, nil, err
	}
	dupeByTracker := make(map[api.TrackerID]api.TrackerDupeAssessment, len(dupes.Results))
	for _, result := range dupes.Results {
		dupeByTracker[result.TrackerID] = result
	}
	descriptionByTracker := workflowDescriptionResultsByTracker(descriptions)
	eligible := make([]api.TrackerReleaseProjection, 0, len(projections.Projections))
	planProjections := make([]api.TrackerReleaseProjection, 0, len(projections.Projections))
	trackerStatuses := make(map[api.TrackerID]api.StageStatus, len(projections.Projections))
	trackerReasons := make(map[api.TrackerID]string, len(projections.Projections))
	trackerFailures := make(map[api.TrackerID][]api.WorkflowFailure, len(projections.Projections))
	targets := make(map[api.TrackerID]struct{}, len(options.TrackerIDs))
	for _, trackerID := range options.TrackerIDs {
		targets[api.TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))] = struct{}{}
	}
	for _, projection := range projections.Projections {
		if len(targets) > 0 {
			if _, selected := targets[projection.TrackerID]; !selected {
				continue
			}
		}
		dupe, exists := dupeByTracker[projection.TrackerID]
		if !releaseworkflow.ProjectionEligibleForDownstream(projection, dupe, exists) {
			continue
		}
		if failure, failed := releaseworkflow.TrackerImageHostFailure(media, projection.TrackerID); failed {
			trackerStatuses[projection.TrackerID] = api.StageStatusFailed
			trackerReasons[projection.TrackerID] = strings.TrimSpace(failure.Failure.Message)
			if trackerReasons[projection.TrackerID] == "" {
				trackerReasons[projection.TrackerID] = "Required image hosting failed."
			}
			trackerFailures[projection.TrackerID] = []api.WorkflowFailure{failure}
			planProjections = append(planProjections, projection)
			continue
		}
		if projection.Artifacts.Description {
			result, hasResult := descriptionByTracker[projection.TrackerID]
			switch {
			case !hasResult:
				trackerStatuses[projection.TrackerID] = api.StageStatusBlocked
				trackerReasons[projection.TrackerID] = "Tracker description outcome is unavailable."
				planProjections = append(planProjections, projection)
				continue
			case result.Status == api.StageStatusSkipped:
				continue
			case result.Status != api.StageStatusCompleted:
				trackerStatuses[projection.TrackerID] = api.StageStatusBlocked
				trackerReasons[projection.TrackerID] = "Tracker description could not be prepared."
				planProjections = append(planProjections, projection)
				continue
			}
		}
		eligible = append(eligible, projection)
		planProjections = append(planProjections, projection)
	}
	for _, projection := range planProjections {
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:     "upload_plan",
			ItemID:    string(projection.TrackerID),
			Kind:      "tracker",
			Label:     projection.DisplayName,
			Status:    api.StageStatusQueued,
			Completed: 0,
			Total:     len(planProjections),
			Message:   "Tracker upload-plan preparation queued.",
		})
	}
	plan := api.UploadPlan{
		InputFingerprint: inputFingerprint,
		ProjectionSet:    api.TrackerReleaseProjectionSetRef{ID: projections.ID, Revision: projections.Revision},
		Dupes:            api.DupeAssessmentRef{ID: dupes.ID, Revision: dupes.Revision},
		TrackerApproval:  options.TrackerApproval,
		Media:            &api.MediaArtifactSetRef{ID: media.ID, Revision: media.Revision},
		Descriptions:     &api.DescriptionSetRef{ID: descriptions.ID, Revision: descriptions.Revision},
		Status:           api.StageStatusSkipped,
		ExpiresAt:        now.Add(workflowUploadPlanTTL),
	}
	if len(planProjections) == 0 {
		return plan, &workflowUploadExecution{noSeed: options.NoSeed}, nil
	}
	plan.Status = api.StageStatusBlocked
	descriptionGroups := workflowUploadDescriptionGroups(descriptions, eligible)
	questionnaire := workflowQuestionnaireAnswers(descriptionInstructions.QuestionnaireAnswers)
	subject, err := b.resolver.ResolveUploadSubject(ctx, api.UploadSubjectInput{
		Release:                projections.ReleaseRef,
		Trackers:               workflowProjectionTrackerNames(eligible),
		QuestionnaireAnswers:   questionnaire,
		DescriptionGroups:      descriptionGroups,
		DescriptionGroupsFinal: true,
		TrackerConfigOverrides: descriptionInstructions.TrackerConfig,
		TrackerSiteOverrides:   descriptionInstructions.TrackerSite,
		ClientOverrides:        descriptionInstructions.Client,
		ImageHostOverrides:     descriptionInstructions.ImageHost,
		TorrentOverrides:       descriptionInstructions.Torrent,
		Options:                descriptionInstructions.Options,
	})
	if err != nil {
		return api.UploadPlan{}, nil, fmt.Errorf("workflow upload plan: resolve subject: %w", err)
	}
	subject.ExactMedia = exactMedia
	skipImageUpload := true
	subject.ImageHostOverrides.SkipUpload = &skipImageUpload
	applyWorkflowCrossSeeds(&subject, dupeEvidence, dupes)
	if len(eligible) > 0 && b.torrents != nil && !descriptionInstructions.Options.SkipAutoTorrent {
		torrent, err := b.torrents.Create(ctx, api.TorrentSubject{
			SourcePath:           subject.SourcePath,
			SourceSize:           subject.SourceSize,
			FileList:             append([]string(nil), subject.FileList...),
			DiscType:             subject.DiscType,
			ClientTorrentPath:    subject.ClientTorrentPath,
			Trackers:             workflowProjectionTrackerNames(eligible),
			SkipIfRehashTrackers: workflowSkipIfRehashTrackers(b.config, eligible),
			TorrentOverrides:     descriptionInstructions.Torrent,
		})
		if err != nil {
			return api.UploadPlan{}, nil, fmt.Errorf("workflow upload plan: prepare torrent: %w", err)
		}
		subject.TorrentPath = torrent.Path
		skipped := make(map[api.TrackerID]struct{}, len(torrent.SkippedTrackers))
		for _, tracker := range torrent.SkippedTrackers {
			trackerID := api.TrackerID(strings.ToUpper(strings.TrimSpace(tracker)))
			if trackerID != "" {
				skipped[trackerID] = struct{}{}
			}
		}
		if len(skipped) > 0 {
			remaining := make([]api.TrackerReleaseProjection, 0, len(eligible))
			for _, projection := range eligible {
				if _, ok := skipped[projection.TrackerID]; ok {
					trackerStatuses[projection.TrackerID] = api.StageStatusSkipped
					trackerReasons[projection.TrackerID] = "Tracker skipped because its torrent would require rehashing."
					continue
				}
				remaining = append(remaining, projection)
			}
			eligible = remaining
		}
		subject.Trackers = workflowProjectionTrackerNames(eligible)
		subject.RehashedTrackers = append([]string(nil), torrent.RehashedTrackers...)
	}
	var retained workflowRetainedUploadPlan
	if len(eligible) > 0 {
		retained, err = b.trackers.PrepareRetainedUploadPlan(ctx, subject, eligible)
		if err != nil {
			return api.UploadPlan{}, nil, fmt.Errorf("workflow upload plan: prepare retained operations: %w", err)
		}
	}
	preparationByTracker := make(map[api.TrackerID]trackers.RetainedTrackerPreparation, len(eligible))
	if retained != nil {
		for _, preparation := range retained.Preparations() {
			preparationByTracker[api.TrackerID(strings.ToUpper(strings.TrimSpace(preparation.Tracker)))] = preparation
		}
	}
	clientSubject := api.ClientSubject{
		SourcePath:      subject.SourcePath,
		FileList:        append([]string(nil), subject.FileList...),
		DiscType:        subject.DiscType,
		ClientOverrides: descriptionInstructions.Client,
	}
	injectPreparedTorrent := options.DryRun &&
		api.NormalizeWorkflowExecutionMode(projections.ExecutionMode) == api.WorkflowExecutionModeDebug
	dryRunInjected := make(map[api.TrackerID]struct{})
	readyCount := 0
	skippedCount := 0
	for index, projection := range planProjections {
		preparation, hasPreparation := preparationByTracker[projection.TrackerID]
		tracker := api.UploadPlanTracker{
			TrackerID:         projection.TrackerID,
			DisplayName:       projection.DisplayName,
			UploadReleaseName: projection.UploadReleaseName,
			Taxonomy:          projection.Taxonomy,
			DescriptionGroup:  projection.DescriptionGroup,
			Status:            trackerStatuses[projection.TrackerID],
			Failures:          append([]api.WorkflowFailure(nil), trackerFailures[projection.TrackerID]...),
		}
		switch reason := trackerReasons[projection.TrackerID]; {
		case reason != "":
			tracker.Warnings = []string{reason}
		case !hasPreparation:
			tracker.Status = api.StageStatusFailed
			tracker.Warnings = []string{"Retained tracker preparation returned no operation."}
			tracker.Failures = []api.WorkflowFailure{{
				Failure: api.OperationFailure{
					Code:      api.OperationFailureMissingPreparedTracker,
					Operation: api.OperationKindUploadDryRun,
					Message:   "No exact prepared tracker operation is available. Prepare the tracker again.",
					Recovery:  api.OperationRecoveryReprepare,
				},
				TrackerID: projection.TrackerID,
			}}
		case preparation.Failure != nil && preparation.Failure.Code == trackers.PreparationFailureCodeSkipped:
			tracker.Status = api.StageStatusSkipped
			message := strings.TrimSpace(logging.SanitizeMessage(preparation.Failure.Message))
			if message == "" {
				message = "Tracker skipped during preparation."
			}
			tracker.Warnings = []string{message}
		case preparation.Failure != nil:
			tracker.Status = api.StageStatusFailed
			message := strings.TrimSpace(logging.SanitizeMessage(preparation.Failure.Message))
			if message == "" {
				message = "Tracker operation could not be prepared. Review prerequisites and retry."
			}
			tracker.Warnings = []string{message}
			tracker.Failures = []api.WorkflowFailure{{
				Failure: api.OperationFailure{
					Code:      api.OperationFailureMissingPreparedTracker,
					Operation: api.OperationKindUploadDryRun,
					Message:   message,
					Recovery:  api.OperationRecoveryReprepare,
				},
				TrackerID: projection.TrackerID,
			}}
		default:
			preparedOperationID, torrentArtifactID, torrentFingerprint, identityErr := workflowTrackerArtifactIdentity(
				projection.TrackerID,
				preparation.TorrentPath,
				inputFingerprint,
			)
			tracker.PreparedOperationID = preparedOperationID
			tracker.TorrentArtifactID = torrentArtifactID
			tracker.TorrentFingerprint = torrentFingerprint
			tracker.Endpoint, tracker.Fields, tracker.Files = sanitizeWorkflowUploadPreview(preparation.Preview)
			tracker.RequiredActions = append([]api.RequiredAction(nil), preparation.Preview.RequiredActions...)
			if identityErr != nil || torrentArtifactID == "" {
				markWorkflowUploadTrackerMissingExactTorrent(&tracker, projection.TrackerID)
				break
			}
			tracker.Eligible = true
			tracker.Status = api.StageStatusReady
			readyCount++
			if injectPreparedTorrent {
				switch {
				case options.NoSeed:
					tracker.ClientInjectionStatus = api.StageStatusSkipped
					tracker.ClientInjectionMessage = "Client injection disabled by the skip option."
				default:
					injectionStatus, injectionMessage, failureCode, injected, injectionErr := injectWorkflowDryRunClient(
						ctx,
						b.clients,
						clientSubject,
						projection,
						preparation.TorrentPath,
						index,
						len(planProjections),
					)
					if injectionErr != nil {
						if retained != nil {
							_ = retained.Release()
						}
						return api.UploadPlan{}, nil, injectionErr
					}
					tracker.ClientInjectionStatus = injectionStatus
					tracker.ClientInjectionMessage = injectionMessage
					tracker.ClientFailureCode = failureCode
					if failureCode != "" {
						tracker.Failures = append(
							tracker.Failures,
							workflowDryRunClientFailure(projection.TrackerID, failureCode, injectionMessage),
						)
					}
					if injected {
						dryRunInjected[projection.TrackerID] = struct{}{}
					} else if injectionStatus == api.StageStatusFailed {
						tracker.Eligible = false
						tracker.Status = api.StageStatusFailed
						readyCount--
					}
				}
			}
		}
		if options.DryRun && tracker.ClientInjectionStatus == "" {
			projectDeferredWorkflowClientInjection(&tracker, options.NoSeed)
		}
		semanticFingerprint, err := workflowUploadTrackerSemanticFingerprint(projection, tracker)
		if err != nil {
			if retained != nil {
				_ = retained.Release()
			}
			return api.UploadPlan{}, nil, fmt.Errorf("workflow upload plan: semantic fingerprint %s: %w", projection.TrackerID, err)
		}
		tracker.SemanticFingerprint = semanticFingerprint
		plan.Trackers = append(plan.Trackers, tracker)
		if tracker.Status == api.StageStatusSkipped {
			skippedCount++
		}
		itemStatus := tracker.Status
		message := "Tracker upload operation prepared."
		switch tracker.Status {
		case api.StageStatusSkipped:
			message = "Tracker upload skipped."
		case api.StageStatusBlocked:
			message = "Tracker upload preparation blocked."
		case api.StageStatusFailed:
			message = uploadPreparationProgressMessage(tracker)
		case api.StageStatusReady:
		case api.StageStatusPending, api.StageStatusQueued, api.StageStatusStale, api.StageStatusPartial, api.StageStatusRunning,
			api.StageStatusCompleted, api.StageStatusExecuted, api.StageStatusInterrupted, api.StageStatusCanceled, api.StageStatusUnavailable, "":
		}
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:     "upload_plan",
			ItemID:    string(projection.TrackerID),
			Kind:      "tracker",
			Label:     projection.DisplayName,
			Status:    itemStatus,
			Completed: index + 1,
			Total:     len(planProjections),
			Message:   message,
		})
	}
	if readyCount > 0 {
		plan.Status = api.StageStatusReady
	} else if len(plan.Trackers) > 0 && skippedCount == len(plan.Trackers) {
		plan.Status = api.StageStatusSkipped
	}
	return plan, &workflowUploadExecution{
		plan:             retained,
		clients:          b.clients,
		clientSubject:    clientSubject,
		noSeed:           options.NoSeed,
		dryRunInjected:   dryRunInjected,
		crossSeeds:       append([]api.UploadedTorrent(nil), subject.CrossSeedTorrents...),
		projections:      workflowProjectionMap(planProjections),
		inputFingerprint: inputFingerprint,
		trackers:         append([]api.UploadPlanTracker(nil), plan.Trackers...),
	}, nil
}

func workflowProjectionMap(projections []api.TrackerReleaseProjection) map[api.TrackerID]api.TrackerReleaseProjection {
	result := make(map[api.TrackerID]api.TrackerReleaseProjection, len(projections))
	for _, projection := range projections {
		result[projection.TrackerID] = projection
	}
	return result
}

func workflowUploadTrackerSemanticFingerprint(
	projection api.TrackerReleaseProjection,
	tracker api.UploadPlanTracker,
) (api.WorkflowFingerprint, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Projection          api.TrackerReleaseProjection
		Endpoint            string
		Fields              []api.UploadPlanField
		Files               []api.UploadPlanFile
		Eligible            bool
		Status              api.StageStatus
		Warnings            []string
		RequiredActions     []api.RequiredAction
		PreparedOperationID api.PublicResourceID
		TorrentArtifactID   api.PublicResourceID
		TorrentFingerprint  api.WorkflowFingerprint
	}{
		projection,
		tracker.Endpoint,
		tracker.Fields,
		tracker.Files,
		tracker.Eligible,
		tracker.Status,
		tracker.Warnings,
		tracker.RequiredActions,
		tracker.PreparedOperationID,
		tracker.TorrentArtifactID,
		tracker.TorrentFingerprint,
	})
	if err != nil {
		return "", fmt.Errorf("workflow upload tracker semantic fingerprint: %w", err)
	}
	return fingerprint, nil
}

// workflowSkipIfRehashTrackers returns eligible tracker names whose config
// enables rehash skipping. Exact config keys win over deterministic case-folded aliases.
func workflowSkipIfRehashTrackers(cfg config.Config, projections []api.TrackerReleaseProjection) []string {
	result := make([]string, 0, len(projections))
	for _, projection := range projections {
		name := strings.ToUpper(strings.TrimSpace(string(projection.TrackerID)))
		trackerConfig, ok := cfg.Trackers.Trackers[name]
		if !ok {
			for _, configuredName := range slices.Sorted(maps.Keys(cfg.Trackers.Trackers)) {
				if strings.EqualFold(strings.TrimSpace(configuredName), name) {
					trackerConfig, ok = cfg.Trackers.Trackers[configuredName], true
					break
				}
			}
		}
		if ok && trackerConfig.SkipIfRehash {
			result = append(result, name)
		}
	}
	return result
}

func (b workflowUploadPlanBuilder) RetryClientInjections(
	ctx context.Context,
	authority releaseworkflow.RegisteredArtifactAuthority,
	trackerIDs []api.TrackerID,
) ([]api.UploadTrackerResult, error) {
	results := make([]api.UploadTrackerResult, 0, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		trackerID = api.TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
		torrent, ok := authority.Torrents[trackerID]
		if !ok {
			results = append(results, workflowClientInjectionFailure(
				trackerID,
				api.OperationFailureMissingExactTorrent,
				"Tracker upload completed, but no registered tracker artifact is retained for client injection.",
			))
			continue
		}
		results = append(results, executeWorkflowClientInjection(
			ctx,
			b.clients,
			authority.ClientSubject,
			trackerID,
			torrent,
		))
	}
	return results, nil
}

// workflowDryRunClientFailure preserves client-effect identity when an unknown
// injection outcome requires explicit reconciliation.
func workflowDryRunClientFailure(
	trackerID api.TrackerID,
	code api.OperationFailureCode,
	message string,
) api.WorkflowFailure {
	operation := api.OperationKindUploadDryRun
	recovery := api.OperationRecoveryRetry
	resource := ""
	switch code {
	case api.OperationFailureMissingExactTorrent:
		recovery = api.OperationRecoveryReprepare
	case api.OperationFailureUnknownOutcome:
		operation = api.OperationKindClientInjection
		recovery = api.OperationRecoveryConfirm
		resource = "dry-run:" + string(trackerID)
	case api.OperationFailureInvalidInput,
		api.OperationFailureInvalidSource,
		api.OperationFailureConfirmationRequired,
		api.OperationFailureStaleGeneration,
		api.OperationFailureIncompatibleGeneration,
		api.OperationFailureMissingPrerequisite,
		api.OperationFailureTrackerAuthRequired,
		api.OperationFailureTrackerAuthUnavailable,
		api.OperationFailureNoEligibleTrackers,
		api.OperationFailureStaleReview,
		api.OperationFailureStaleResult,
		api.OperationFailureMissingReview,
		api.OperationFailureMissingPreparedTracker,
		api.OperationFailureDryRunClientInjection,
		api.OperationFailureClientInjection,
		api.OperationFailureImageHostUnavailable,
		api.OperationFailureInternal:
	}
	return api.WorkflowFailure{
		Failure: api.OperationFailure{
			Code:      code,
			Operation: operation,
			Message:   message,
			Recovery:  recovery,
		},
		TrackerID: trackerID,
		Resource:  resource,
	}
}

func uploadPreparationProgressMessage(tracker api.UploadPlanTracker) string {
	for _, warning := range tracker.Warnings {
		if message := strings.TrimSpace(logging.SanitizeMessage(warning)); message != "" {
			return message
		}
	}
	return "Tracker upload preparation failed."
}

func markWorkflowUploadTrackerMissingExactTorrent(tracker *api.UploadPlanTracker, trackerID api.TrackerID) {
	const message = "No exact prepared tracker torrent is available. Prepare the tracker again."

	tracker.Eligible = false
	tracker.Status = api.StageStatusFailed
	tracker.Warnings = []string{message}
	tracker.Failures = []api.WorkflowFailure{{
		Failure: api.OperationFailure{
			Code:      api.OperationFailureMissingExactTorrent,
			Operation: api.OperationKindUploadDryRun,
			Message:   message,
			Recovery:  api.OperationRecoveryReprepare,
		},
		TrackerID: trackerID,
	}}
}

func projectDeferredWorkflowClientInjection(tracker *api.UploadPlanTracker, noSeed bool) {
	tracker.ClientInjectionStatus = api.StageStatusSkipped
	tracker.ClientFailureCode = ""
	switch {
	case noSeed:
		tracker.ClientInjectionMessage = "Client injection disabled by the skip option."
	case tracker.Status == api.StageStatusReady:
		tracker.ClientInjectionMessage = "Client injection deferred until tracker submission completes."
	default:
		tracker.ClientInjectionMessage = "Client injection skipped because the tracker upload was not ready."
	}
}

func workflowTrackerArtifactIdentity(
	trackerID api.TrackerID,
	torrentPath string,
	planFingerprint api.WorkflowFingerprint,
) (api.PublicResourceID, api.PublicResourceID, api.WorkflowFingerprint, error) {
	operationFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		TrackerID api.TrackerID
		Plan      api.WorkflowFingerprint
	}{trackerID, planFingerprint})
	if err != nil {
		return "", "", "", fmt.Errorf("workflow upload plan: fingerprint prepared operation %s: %w", trackerID, err)
	}
	preparedOperationID := api.PublicResourceID("prepared-operation-" + string(operationFingerprint)[:24])
	torrentPath = strings.TrimSpace(torrentPath)
	if torrentPath == "" {
		return preparedOperationID, "", "", nil
	}
	payload, err := os.ReadFile(torrentPath)
	if err != nil {
		return preparedOperationID, "", "", fmt.Errorf("workflow upload plan: read exact tracker torrent %s: %w", trackerID, err)
	}
	sum := sha256.Sum256(payload)
	torrentFingerprint := api.WorkflowFingerprint(hex.EncodeToString(sum[:]))
	artifactFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		TrackerID api.TrackerID
		Plan      api.WorkflowFingerprint
		Torrent   api.WorkflowFingerprint
	}{trackerID, planFingerprint, torrentFingerprint})
	if err != nil {
		return "", "", "", fmt.Errorf("workflow upload plan: fingerprint exact torrent %s: %w", trackerID, err)
	}
	torrentArtifactID := api.PublicResourceID("tracker-torrent-" + string(artifactFingerprint)[:24])
	return preparedOperationID, torrentArtifactID, torrentFingerprint, nil
}

func injectWorkflowDryRunClient(
	ctx context.Context,
	clients api.ClientService,
	subject api.ClientSubject,
	projection api.TrackerReleaseProjection,
	torrentPath string,
	processedBefore int,
	total int,
) (api.StageStatus, string, api.OperationFailureCode, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", "", "", false, fmt.Errorf("workflow dry-run client injection: %w", err)
	}
	trackerID := projection.TrackerID
	total = max(1, total)
	processedBefore = max(0, min(processedBefore, total))
	processedAfter := min(total, processedBefore+1)
	if clients == nil {
		emitWorkflowClientInjectionProgress(
			ctx,
			projection,
			api.StageStatusFailed,
			processedAfter,
			total,
			"Dry-run client injection failed.",
		)
		return api.StageStatusFailed, "Client injection failed because no client service is configured.", api.OperationFailureDryRunClientInjection, false, nil
	}
	torrentPath = strings.TrimSpace(torrentPath)
	if torrentPath == "" {
		emitWorkflowClientInjectionProgress(
			ctx,
			projection,
			api.StageStatusFailed,
			processedAfter,
			total,
			"Dry-run client injection failed.",
		)
		return api.StageStatusFailed, "Client injection failed because no prepared tracker torrent is available.", api.OperationFailureMissingExactTorrent, false, nil
	}
	effectFingerprint, err := workflowClientInjectionFingerprint(trackerID, torrentPath, false)
	if err != nil {
		emitWorkflowClientInjectionProgress(
			ctx,
			projection,
			api.StageStatusFailed,
			processedAfter,
			total,
			"Dry-run client injection preparation failed.",
		)
		return "", "", "", false, err
	}
	effectReceipt, err := api.BeginWorkflowExternalEffect(ctx, api.WorkflowExternalEffect{
		Kind:                api.WorkflowExternalEffectClientInjection,
		ScopeID:             "dry-run:" + string(trackerID),
		SemanticFingerprint: effectFingerprint,
	})
	if err != nil {
		if errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
			emitWorkflowClientInjectionProgress(
				ctx,
				projection,
				api.StageStatusFailed,
				processedAfter,
				total,
				"Dry-run client injection outcome is unknown.",
			)
			return api.StageStatusFailed,
				"Client injection may have succeeded before interruption. Verify the client before retrying.",
				api.OperationFailureUnknownOutcome,
				false,
				nil
		}
		emitWorkflowClientInjectionProgress(
			ctx,
			projection,
			api.StageStatusFailed,
			processedAfter,
			total,
			"Dry-run client injection could not start.",
		)
		return "", "", "", false, fmt.Errorf("workflow dry-run client injection fence: %w", err)
	}
	if effectReceipt.AlreadySucceeded {
		emitWorkflowClientInjectionProgress(
			ctx,
			projection,
			api.StageStatusCompleted,
			processedAfter,
			total,
			"Prior dry-run client injection receipt retained.",
		)
		return api.StageStatusCompleted, "Prior client injection receipt retained.", "", true, nil
	}
	emitWorkflowClientInjectionProgress(
		ctx,
		projection,
		api.StageStatusRunning,
		processedBefore,
		total,
		"Injecting dry-run torrent into the client.",
	)
	injectErr := clients.Inject(ctx, subject, api.TorrentResult{Path: torrentPath, Tracker: string(trackerID)})
	receiptErr := api.CompleteWorkflowExternalEffect(ctx, effectReceipt, injectErr == nil)
	if receiptErr != nil {
		emitWorkflowClientInjectionProgress(
			ctx,
			projection,
			api.StageStatusFailed,
			processedAfter,
			total,
			"Dry-run client injection receipt could not be retained.",
		)
		return api.StageStatusFailed,
			"Client injection may have succeeded, but its terminal receipt could not be retained. Verify the client before retrying.",
			api.OperationFailureUnknownOutcome,
			false,
			nil
	}
	if injectErr != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return "", "", "", false, fmt.Errorf("workflow dry-run client injection: %w", contextErr)
		}
		emitWorkflowClientInjectionProgress(
			ctx,
			projection,
			api.StageStatusFailed,
			processedAfter,
			total,
			"Dry-run client injection failed.",
		)
		return api.StageStatusFailed, "Client injection failed. Review client settings and retry.", api.OperationFailureDryRunClientInjection, false, nil
	}
	emitWorkflowClientInjectionProgress(
		ctx,
		projection,
		api.StageStatusCompleted,
		processedAfter,
		total,
		"Dry-run client injection completed.",
	)
	return api.StageStatusCompleted, "Client injection completed.", "", true, nil
}

func emitWorkflowClientInjectionProgress(
	ctx context.Context,
	projection api.TrackerReleaseProjection,
	status api.StageStatus,
	completed int,
	total int,
	message string,
) {
	total = max(1, total)
	api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
		Phase:     "client_injection",
		ItemID:    string(projection.TrackerID),
		Kind:      "tracker",
		Label:     projection.DisplayName,
		Status:    status,
		Completed: max(0, min(completed, total)),
		Total:     total,
		Message:   message,
	})
}

func workflowClientInjectionFingerprint(
	trackerID api.TrackerID,
	torrentPath string,
	crossSeed bool,
) (api.WorkflowFingerprint, error) {
	payload, err := os.ReadFile(strings.TrimSpace(torrentPath))
	if err != nil {
		return "", fmt.Errorf("workflow client injection read exact torrent: %w", err)
	}
	sum := sha256.Sum256(payload)
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Tracker   api.TrackerID
		Torrent   string
		CrossSeed bool
	}{
		Tracker:   trackerID,
		Torrent:   hex.EncodeToString(sum[:]),
		CrossSeed: crossSeed,
	})
	if err != nil {
		return "", fmt.Errorf("workflow client injection fingerprint: %w", err)
	}
	return fingerprint, nil
}

func (e *workflowUploadExecution) ResolveAction(
	ctx context.Context,
	trackerID api.TrackerID,
	kind api.RequiredActionKind,
	confirmed bool,
) (api.UploadPlanTracker, error) {
	if e == nil || e.plan == nil {
		return api.UploadPlanTracker{}, trackers.ErrPlanNotSubmittable
	}
	trackerID = api.TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
	index := slices.IndexFunc(e.trackers, func(tracker api.UploadPlanTracker) bool {
		return tracker.TrackerID == trackerID
	})
	projection, hasProjection := e.projections[trackerID]
	if index < 0 || !hasProjection {
		return api.UploadPlanTracker{}, fmt.Errorf("workflow upload action: tracker %s is unavailable", trackerID)
	}
	preparation, err := e.plan.ResolveAction(ctx, string(trackerID), kind, confirmed)
	if err != nil {
		return api.UploadPlanTracker{}, fmt.Errorf("workflow upload action: %w", err)
	}
	updated := e.trackers[index]
	previousTorrentFingerprint := updated.TorrentFingerprint
	updated.RequiredActions = nil
	updated.Warnings = nil
	updated.Failures = nil
	if preparation.Failure != nil {
		updated.Endpoint = ""
		updated.Fields = nil
		updated.Files = nil
		updated.Eligible = false
		updated.PreparedOperationID = ""
		updated.TorrentArtifactID = ""
		updated.TorrentFingerprint = ""
		message := strings.TrimSpace(logging.SanitizeMessage(preparation.Failure.Message))
		if message == "" {
			if preparation.Failure.Code == trackers.PreparationFailureCodeSkipped {
				message = "Tracker skipped during preparation."
			} else {
				message = "Tracker operation could not be prepared. Review prerequisites and retry."
			}
		}
		updated.Warnings = []string{message}
		if preparation.Failure.Code == trackers.PreparationFailureCodeSkipped {
			updated.Status = api.StageStatusSkipped
		} else {
			updated.Status = api.StageStatusFailed
			updated.Failures = []api.WorkflowFailure{{
				Failure: api.OperationFailure{
					Code:      api.OperationFailureMissingPreparedTracker,
					Operation: api.OperationKindUploadDryRun,
					Message:   message,
					Recovery:  api.OperationRecoveryReprepare,
				},
				TrackerID: trackerID,
			}}
		}
	} else {
		updated.Endpoint, updated.Fields, updated.Files = sanitizeWorkflowUploadPreview(preparation.Preview)
		updated.RequiredActions = append([]api.RequiredAction(nil), preparation.Preview.RequiredActions...)
		updated.PreparedOperationID, updated.TorrentArtifactID, updated.TorrentFingerprint, err = workflowTrackerArtifactIdentity(
			trackerID,
			preparation.TorrentPath,
			e.inputFingerprint,
		)
		if err != nil {
			return api.UploadPlanTracker{}, err
		}
		if updated.TorrentArtifactID == "" {
			markWorkflowUploadTrackerMissingExactTorrent(&updated, trackerID)
		} else {
			updated.Eligible = true
			updated.Status = api.StageStatusReady
		}
	}
	projectDeferredWorkflowClientInjection(&updated, e.noSeed)
	if updated.TorrentFingerprint != previousTorrentFingerprint {
		delete(e.dryRunInjected, trackerID)
	}
	updated.SemanticFingerprint, err = workflowUploadTrackerSemanticFingerprint(projection, updated)
	if err != nil {
		return api.UploadPlanTracker{}, fmt.Errorf("workflow upload action fingerprint %s: %w", trackerID, err)
	}
	e.trackers[index] = updated
	return updated, nil
}

func (e *workflowUploadExecution) Execute(
	ctx context.Context,
	trackerIDs []api.TrackerID,
) ([]api.UploadTrackerResult, error) {
	if e == nil {
		return nil, nil
	}
	var (
		results []trackers.RetainedTrackerResult
		err     error
	)
	if e.plan != nil {
		if len(trackerIDs) == 0 {
			results, err = e.plan.Execute(ctx)
		} else {
			selected := make([]string, 0, len(trackerIDs))
			for _, trackerID := range trackerIDs {
				selected = append(selected, string(trackerID))
			}
			results, err = e.plan.ExecuteSelected(ctx, selected)
		}
	}
	public := make([]api.UploadTrackerResult, 0, len(results))
	for _, result := range results {
		trackerID := api.TrackerID(strings.ToUpper(strings.TrimSpace(result.Tracker)))
		if result.Failure != nil {
			failureCode := api.OperationFailureInternal
			recovery := api.OperationRecoveryRetry
			message := strings.TrimSpace(logging.SanitizeMessage(result.Failure.Message))
			if message == "" {
				message = "Tracker upload failed. Review the tracker result and retry after correcting the failure."
			}
			submissionStatus := api.StageStatusFailed
			if result.Failure.Code == "unknown_outcome" {
				failureCode = api.OperationFailureUnknownOutcome
				recovery = api.OperationRecoveryConfirm
				message = "Tracker submission may have succeeded, but no terminal receipt was retained. Verify the tracker before continuing."
				submissionStatus = api.StageStatusUnavailable
			}
			public = append(public, api.UploadTrackerResult{
				TrackerID:             trackerID,
				Status:                api.StageStatusFailed,
				SubmissionStatus:      submissionStatus,
				ClientInjectionStatus: api.StageStatusPending,
				Failures: []api.WorkflowFailure{{
					Failure: api.OperationFailure{
						Code:      failureCode,
						Operation: api.OperationKindUploadExecute,
						Message:   message,
						Recovery:  recovery,
					},
					TrackerID: trackerID,
				}},
			})
			continue
		}
		outcome := api.UploadTrackerResult{
			TrackerID:        trackerID,
			SubmissionStatus: api.StageStatusCompleted,
		}
		uploaded, hasUploaded := selectWorkflowRegisteredTorrent(trackerID, result.Summary.UploadedTorrents)
		if hasUploaded {
			outcome.RemoteID = strings.TrimSpace(uploaded.TorrentID)
			outcome.RemoteURL = sanitizeWorkflowRemoteURL(uploaded.TorrentURL)
		}
		_, alreadyInjected := e.dryRunInjected[trackerID]
		switch {
		case result.Summary.PendingPublication:
			outcome.ClientInjectionStatus = api.StageStatusSkipped
			outcome.ClientInjectionMessage = "Client injection skipped while tracker publication is pending."
		case alreadyInjected:
			outcome.ClientInjected = true
			outcome.ClientInjectionStatus = api.StageStatusCompleted
		case e.noSeed:
			outcome.ClientInjectionStatus = api.StageStatusSkipped
		case !hasUploaded:
			outcome = workflowClientInjectionFailure(
				trackerID,
				api.OperationFailureMissingExactTorrent,
				"Tracker upload completed, but no registered tracker artifact was returned for client injection.",
			)
		default:
			torrent, ok := workflowRegisteredTorrentResult(trackerID, uploaded)
			if !ok {
				outcome = workflowClientInjectionFailure(
					trackerID,
					api.OperationFailureMissingExactTorrent,
					"Tracker upload completed, but its registered tracker artifact is unavailable for client injection.",
				)
				outcome.RemoteID = strings.TrimSpace(uploaded.TorrentID)
				outcome.RemoteURL = sanitizeWorkflowRemoteURL(uploaded.TorrentURL)
				break
			}
			if e.registeredArtifacts == nil {
				e.registeredArtifacts = make(map[api.TrackerID]api.TorrentResult)
			}
			e.registeredArtifacts[trackerID] = torrent
			injected := executeWorkflowClientInjection(ctx, e.clients, e.clientSubject, trackerID, torrent)
			injected.RemoteID = strings.TrimSpace(uploaded.TorrentID)
			injected.RemoteURL = sanitizeWorkflowRemoteURL(uploaded.TorrentURL)
			outcome = injected
		}
		outcome.Status = outcome.DerivedStatus()
		public = append(public, outcome)
	}
	if !e.noSeed && e.clients != nil {
		for _, crossSeed := range e.crossSeeds {
			trackerID := api.TrackerID(strings.ToUpper(strings.TrimSpace(crossSeed.Tracker)))
			injected, failureCode, failureMessage, injectErr := injectWorkflowClientWithFence(
				ctx,
				e.clients,
				e.clientSubject,
				api.TorrentResult{
					Path:      strings.TrimSpace(crossSeed.TorrentPath),
					URL:       strings.TrimSpace(crossSeed.DownloadURL),
					Tracker:   string(trackerID),
					CrossSeed: true,
				},
				"cross-seed:"+string(trackerID),
				true,
			)
			if injectErr != nil {
				return public, injectErr
			}
			outcome := api.UploadTrackerResult{
				TrackerID:             trackerID,
				SubmissionStatus:      api.StageStatusSkipped,
				ClientInjectionStatus: api.StageStatusCompleted,
				CrossSeeded:           injected,
			}
			if failureCode != "" {
				outcome.ClientInjectionStatus = api.StageStatusFailed
				outcome.ClientInjectionMessage = failureMessage
				outcome.ClientFailureCode = failureCode
				outcome.Failures = []api.WorkflowFailure{{
					Failure: api.OperationFailure{
						Code:      failureCode,
						Operation: api.OperationKindClientInjection,
						Message:   failureMessage,
						Recovery:  workflowClientFailureRecovery(failureCode),
					},
					TrackerID: trackerID,
					Resource:  "cross-seed:" + string(trackerID),
				}}
			}
			outcome.Status = outcome.DerivedStatus()
			public = append(public, outcome)
		}
	}
	if err != nil {
		return public, fmt.Errorf("workflow upload execution: %w", err)
	}
	return public, nil
}

func (e *workflowUploadExecution) RegisteredArtifactAuthority() releaseworkflow.RegisteredArtifactAuthority {
	if e == nil || len(e.registeredArtifacts) == 0 {
		return releaseworkflow.RegisteredArtifactAuthority{}
	}
	torrents := make(map[api.TrackerID]api.TorrentResult, len(e.registeredArtifacts))
	maps.Copy(torrents, e.registeredArtifacts)
	subject := e.clientSubject
	subject.FileList = append([]string(nil), e.clientSubject.FileList...)
	return releaseworkflow.RegisteredArtifactAuthority{
		ClientSubject: subject,
		Torrents:      torrents,
	}
}

func selectWorkflowRegisteredTorrent(
	trackerID api.TrackerID,
	uploadedTorrents []api.UploadedTorrent,
) (api.UploadedTorrent, bool) {
	for _, uploaded := range uploadedTorrents {
		if strings.EqualFold(strings.TrimSpace(uploaded.Tracker), string(trackerID)) {
			return uploaded, true
		}
	}
	if len(uploadedTorrents) == 1 && strings.TrimSpace(uploadedTorrents[0].Tracker) == "" {
		return uploadedTorrents[0], true
	}
	return api.UploadedTorrent{}, false
}

func workflowRegisteredTorrentResult(
	trackerID api.TrackerID,
	uploaded api.UploadedTorrent,
) (api.TorrentResult, bool) {
	torrent := api.TorrentResult{Tracker: string(trackerID)}
	if torrentPath := strings.TrimSpace(uploaded.TorrentPath); torrentPath != "" {
		if _, err := trackers.ReadRegisteredTorrent(torrentPath); err == nil {
			torrent.Path = torrentPath
			return torrent, true
		}
	}
	if downloadURL := strings.TrimSpace(uploaded.DownloadURL); downloadURL != "" {
		torrent.URL = downloadURL
		return torrent, true
	}
	return api.TorrentResult{}, false
}

func executeWorkflowClientInjection(
	ctx context.Context,
	clients api.ClientService,
	subject api.ClientSubject,
	trackerID api.TrackerID,
	torrent api.TorrentResult,
) api.UploadTrackerResult {
	if clients == nil {
		return workflowClientInjectionFailure(
			trackerID,
			api.OperationFailureClientInjection,
			"Tracker upload completed, but no client service is configured.",
		)
	}
	injected, failureCode, message, err := injectWorkflowClientWithFence(
		ctx,
		clients,
		subject,
		torrent,
		"upload:"+string(trackerID),
		false,
	)
	if err != nil {
		return workflowClientInjectionFailure(
			trackerID,
			api.OperationFailureClientInjection,
			"Tracker upload completed, but client injection could not be started.",
		)
	}
	if failureCode != "" {
		return workflowClientInjectionFailure(trackerID, failureCode, message)
	}
	outcome := api.UploadTrackerResult{
		TrackerID:             trackerID,
		SubmissionStatus:      api.StageStatusCompleted,
		ClientInjectionStatus: api.StageStatusCompleted,
		ClientInjected:        injected,
	}
	outcome.Status = outcome.DerivedStatus()
	return outcome
}

func workflowClientInjectionFailure(
	trackerID api.TrackerID,
	code api.OperationFailureCode,
	message string,
) api.UploadTrackerResult {
	outcome := api.UploadTrackerResult{
		TrackerID:              trackerID,
		SubmissionStatus:       api.StageStatusCompleted,
		ClientInjectionStatus:  api.StageStatusFailed,
		ClientInjectionMessage: message,
		ClientFailureCode:      code,
		Failures: []api.WorkflowFailure{{
			Failure: api.OperationFailure{
				Code:      code,
				Operation: api.OperationKindClientInjection,
				Message:   message,
				Recovery:  workflowClientFailureRecovery(code),
			},
			TrackerID: trackerID,
			Resource:  "upload:" + string(trackerID),
		}},
	}
	outcome.Status = outcome.DerivedStatus()
	return outcome
}

func workflowClientFailureRecovery(code api.OperationFailureCode) api.OperationRecovery {
	switch code {
	case api.OperationFailureClientInjection:
		return api.OperationRecoveryRetry
	case api.OperationFailureUnknownOutcome:
		return api.OperationRecoveryConfirm
	case api.OperationFailureMissingExactTorrent:
		return api.OperationRecoveryNone
	case api.OperationFailureInvalidInput,
		api.OperationFailureInvalidSource,
		api.OperationFailureConfirmationRequired,
		api.OperationFailureStaleGeneration,
		api.OperationFailureIncompatibleGeneration,
		api.OperationFailureMissingPrerequisite,
		api.OperationFailureTrackerAuthRequired,
		api.OperationFailureTrackerAuthUnavailable,
		api.OperationFailureNoEligibleTrackers,
		api.OperationFailureStaleReview,
		api.OperationFailureStaleResult,
		api.OperationFailureMissingReview,
		api.OperationFailureMissingPreparedTracker,
		api.OperationFailureDryRunClientInjection,
		api.OperationFailureImageHostUnavailable,
		api.OperationFailureInternal,
		"":
		return api.OperationRecoveryNone
	}
	return api.OperationRecoveryNone
}

func injectWorkflowClientWithFence(
	ctx context.Context,
	clients api.ClientService,
	subject api.ClientSubject,
	torrent api.TorrentResult,
	scopeID string,
	crossSeed bool,
) (bool, api.OperationFailureCode, string, error) {
	trackerID := api.TrackerID(strings.ToUpper(strings.TrimSpace(torrent.Tracker)))
	fingerprint, err := workflowClientEffectFingerprint(trackerID, torrent, crossSeed)
	if err != nil {
		return false, "", "", err
	}
	receipt, err := api.BeginWorkflowExternalEffect(ctx, api.WorkflowExternalEffect{
		Kind:                api.WorkflowExternalEffectClientInjection,
		ScopeID:             scopeID,
		SemanticFingerprint: fingerprint,
	})
	if err != nil {
		if errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
			return false,
				api.OperationFailureUnknownOutcome,
				"Client injection may have succeeded before interruption. Verify the client before retrying.",
				nil
		}
		return false, "", "", fmt.Errorf("workflow client injection fence: %w", err)
	}
	if receipt.AlreadySucceeded {
		return true, "", "", nil
	}
	injectErr := clients.Inject(ctx, subject, torrent)
	if err := api.CompleteWorkflowExternalEffect(ctx, receipt, injectErr == nil); err != nil {
		return false,
			api.OperationFailureUnknownOutcome,
			"Client injection may have succeeded, but its terminal receipt could not be retained. Verify the client before retrying.",
			nil
	}
	if injectErr != nil {
		return false,
			api.OperationFailureClientInjection,
			"Exact-torrent client injection failed. Review client settings before retrying.",
			nil
	}
	return true, "", "", nil
}

func workflowClientEffectFingerprint(
	trackerID api.TrackerID,
	torrent api.TorrentResult,
	crossSeed bool,
) (api.WorkflowFingerprint, error) {
	torrentIdentity := strings.TrimSpace(torrent.URL)
	if path := strings.TrimSpace(torrent.Path); path != "" {
		payload, err := trackers.ReadRegisteredTorrent(path)
		if err != nil {
			return "", fmt.Errorf("workflow client effect read exact torrent: %w", err)
		}
		sum := sha256.Sum256(payload)
		torrentIdentity = hex.EncodeToString(sum[:])
	}
	if torrentIdentity == "" {
		return "", errors.New("workflow client effect exact torrent identity is unavailable")
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Tracker   api.TrackerID
		Torrent   string
		CrossSeed bool
	}{
		Tracker:   trackerID,
		Torrent:   torrentIdentity,
		CrossSeed: crossSeed,
	})
	if err != nil {
		return "", fmt.Errorf("workflow client effect fingerprint: %w", err)
	}
	return fingerprint, nil
}

func (e *workflowUploadExecution) Release() error {
	if e == nil || e.plan == nil {
		return nil
	}
	if err := e.plan.Release(); err != nil {
		return fmt.Errorf("workflow upload release: %w", err)
	}
	return nil
}

func workflowDescriptionResultsByTracker(descriptions api.DescriptionSet) map[api.TrackerID]api.DescriptionTrackerResult {
	results := make(map[api.TrackerID]api.DescriptionTrackerResult)
	for _, description := range descriptions.Descriptions {
		for _, trackerID := range description.TrackerIDs {
			results[trackerID] = api.DescriptionTrackerResult{
				TrackerID: trackerID,
				Status:    api.StageStatusCompleted,
			}
		}
	}
	for _, failure := range descriptions.Failures {
		if failure.TrackerID == "" {
			continue
		}
		results[failure.TrackerID] = api.DescriptionTrackerResult{
			TrackerID: failure.TrackerID,
			Status:    api.StageStatusFailed,
			Message:   strings.TrimSpace(failure.Failure.Message),
		}
	}
	for _, result := range descriptions.TrackerResults {
		results[result.TrackerID] = result
	}
	return results
}

func workflowUploadDescriptionGroups(
	descriptions api.DescriptionSet,
	projections []api.TrackerReleaseProjection,
) []api.DescriptionBuilderGroup {
	displayByID := make(map[api.TrackerID]string, len(projections))
	for _, projection := range projections {
		displayByID[projection.TrackerID] = string(projection.TrackerID)
	}
	groups := make([]api.DescriptionBuilderGroup, 0, len(descriptions.Descriptions))
	for _, retained := range descriptions.Descriptions {
		trackerNames := make([]string, 0, len(retained.TrackerIDs))
		for _, trackerID := range retained.TrackerIDs {
			if name := displayByID[trackerID]; name != "" {
				trackerNames = append(trackerNames, name)
			}
		}
		groups = append(groups, api.DescriptionBuilderGroup{
			GroupKey:           retained.GroupKey,
			Trackers:           trackerNames,
			Description:        retained.Source,
			DescriptionHTML:    retained.Rendered,
			RawDescription:     retained.Source,
			RawDescriptionHTML: retained.Rendered,
			HasOverride:        true,
		})
	}
	return groups
}

func workflowQuestionnaireAnswers(input map[api.TrackerID]map[string]string) map[string]map[string]string {
	result := make(map[string]map[string]string, len(input))
	for trackerID, answers := range input {
		cloned := make(map[string]string, len(answers))
		maps.Copy(cloned, answers)
		result[string(trackerID)] = cloned
	}
	return result
}

func applyWorkflowCrossSeeds(subject *api.UploadSubject, evidence workflowDupePrivateEvidence, dupes api.DupeAssessment) {
	if subject == nil || evidence.Assessment == nil {
		return
	}
	duplicateSubject := api.DuplicateSubject{}
	evidence.Assessment.Apply(&duplicateSubject)
	accepted := make(map[api.TrackerID]struct{}, len(dupes.Results))
	for _, result := range dupes.Results {
		if result.Decision == api.DupeDecisionAccepted {
			accepted[result.TrackerID] = struct{}{}
		}
	}
	for _, torrent := range duplicateSubject.CrossSeedTorrents {
		trackerID := api.TrackerID(strings.ToUpper(strings.TrimSpace(torrent.Tracker)))
		if _, ok := accepted[trackerID]; ok {
			subject.CrossSeedTorrents = append(subject.CrossSeedTorrents, torrent)
		}
	}
}

func sanitizeWorkflowUploadPreview(preview api.TrackerDryRunEntry) (string, []api.UploadPlanField, []api.UploadPlanFile) {
	endpoint := sanitizeWorkflowRemoteURL(preview.Endpoint)
	keys := make([]string, 0, len(preview.Payload))
	for key := range preview.Payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fields := make([]api.UploadPlanField, 0, len(keys))
	for _, key := range keys {
		fields = append(fields, api.UploadPlanField{Key: key, Value: sanitizeWorkflowFieldValue(key, preview.Payload[key])})
	}
	files := make([]api.UploadPlanFile, 0, len(preview.Files))
	for _, file := range preview.Files {
		files = append(files, api.UploadPlanFile{Field: strings.TrimSpace(file.Field), Present: file.Present})
	}
	slices.SortFunc(files, func(left api.UploadPlanFile, right api.UploadPlanFile) int {
		return strings.Compare(left.Field, right.Field)
	})
	return endpoint, fields, files
}

func sanitizeWorkflowFieldValue(key string, value string) string {
	normalizedKey := strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{"password", "passkey", "api_key", "apikey", "api_token", "token", "cookie", "auth", "secret"} {
		if strings.Contains(normalizedKey, marker) {
			return "[redacted]"
		}
	}
	trimmed := strings.TrimSpace(value)
	if parsed, err := url.Parse(trimmed); err == nil && parsed.IsAbs() && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		parsed.RawQuery = ""
		parsed.Fragment = ""
		parsed.User = nil
		return parsed.String()
	}
	if filepath.IsAbs(trimmed) || strings.Contains(trimmed, ":\\") {
		return "[private path]"
	}
	return value
}

func sanitizeWorkflowRemoteURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
}
