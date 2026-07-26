// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	workflowPreflightFreshness      = 15 * time.Minute
	dupeSkipCodeTrackerAuthNotReady = "tracker_auth_not_ready"
	authBlockedPreflightMessage     = "Tracker authentication is not ready for this attempt. Resolve authentication outside the upload workflow, then restart it."
)

// workflowPreflightBuilder adapts live auth validation to the workflow's
// immutable preflight/finalized-projection boundary. Other live prerequisites
// are already resolved by the current registry-owned projector pass.
type workflowPreflightBuilder struct {
	auth     api.TrackerAuthService
	config   config.Config
	registry *trackers.Registry
	logger   api.Logger
	banned   *trackers.BannedGroupChecker
}

func (b workflowPreflightBuilder) Build(
	ctx context.Context,
	subject api.UploadSubject,
	catalog api.TrackerCatalogSnapshot,
	runtime api.TrackerRuntimeSnapshot,
	initial api.TrackerReleaseProjectionSet,
	assessedAt time.Time,
) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
	if err := ctx.Err(); err != nil {
		return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("tracker preflight: %w", err)
	}
	if b.auth == nil {
		return api.TrackerPreflightAssessment{}, nil, errors.New("tracker preflight: auth service is required")
	}
	if b.registry == nil {
		return api.TrackerPreflightAssessment{}, nil, errors.New("tracker preflight: tracker registry is required")
	}
	if b.logger == nil {
		b.logger = api.NopLogger{}
	}
	if b.banned == nil {
		b.banned = trackers.NewBannedGroupCheckerWithRegistry(b.config.MainSettings.DBPath, b.registry)
	}
	executionMode := api.NormalizeWorkflowExecutionMode(initial.ExecutionMode)
	debugMode := executionMode == api.WorkflowExecutionModeDebug
	catalogIDs := make(map[api.TrackerID]struct{}, len(catalog.Trackers))
	for _, descriptor := range catalog.Trackers {
		catalogIDs[descriptor.TrackerID] = struct{}{}
	}
	for _, projection := range initial.Projections {
		if _, ok := catalogIDs[projection.TrackerID]; !ok {
			return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("tracker preflight: tracker %s is absent from catalog", projection.TrackerID)
		}
	}
	checkedSubject, localResourcesChanged := subjectWithAvailablePreparedResources(subject)
	if localResourcesChanged {
		initial.Projections = append([]api.TrackerReleaseProjection(nil), initial.Projections...)
		for index := range initial.Projections {
			projection := &initial.Projections[index]
			if projection.Readiness != api.ReadinessStatusReady || !projection.DupeReady {
				continue
			}
			validationSubject := api.NewTrackerValidationSubject(checkedSubject, string(projection.TrackerID))
			checkedFingerprint := api.WorkflowFingerprint(validationSubject.PreparedResourceFingerprint)
			if projection.PreparedResourceFingerprint == checkedFingerprint {
				continue
			}
			failures, err := trackers.EvaluateTrackerValidationWithRegistry(
				ctx,
				b.registry,
				string(projection.TrackerID),
				validationSubject,
				b.logger,
			)
			if err != nil {
				return api.TrackerPreflightAssessment{}, nil, fmt.Errorf(
					"tracker preflight: validate local resources for %s: %w",
					projection.TrackerID,
					err,
				)
			}
			projection.PreparedResourceFingerprint = checkedFingerprint
			trackers.ApplyProjectionRuleFailures(
				projection,
				newProjectionRuleFailures(*projection, failures),
				executionMode,
			)
		}
	}
	descriptorByID := make(map[api.TrackerID]api.TrackerCatalogDescriptor, len(catalog.Trackers))
	for _, descriptor := range catalog.Trackers {
		descriptorByID[descriptor.TrackerID] = descriptor
	}
	runtimeConfigured := make(map[api.TrackerID]bool, len(runtime.Trackers))
	for _, entry := range runtime.Trackers {
		runtimeConfigured[entry.TrackerID] = entry.Configured
	}

	hasReadyProjection := false
	for _, projection := range initial.Projections {
		if projection.Readiness == api.ReadinessStatusReady && projection.DupeReady {
			hasReadyProjection = true
			break
		}
	}
	var capabilities []api.TrackerAuthCapability
	var capabilityErr error
	if hasReadyProjection {
		capabilities, capabilityErr = b.auth.Capabilities(ctx)
		if err := ctx.Err(); err != nil {
			return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("tracker preflight: auth capabilities: %w", err)
		}
	}
	knownCapabilities := make(map[api.TrackerID]api.TrackerAuthCapability, len(capabilities))
	managed := make(map[api.TrackerID]struct{}, len(capabilities))
	for _, projection := range initial.Projections {
		capability, ok := b.registry.LookupAuthCapability(string(projection.TrackerID))
		if !ok {
			continue
		}
		knownCapabilities[projection.TrackerID] = capability
		if trackerauth.IsManagedCapability(capability) {
			managed[projection.TrackerID] = struct{}{}
		}
	}
	for _, capability := range capabilities {
		trackerID := api.TrackerID(strings.ToUpper(strings.TrimSpace(capability.TrackerID)))
		knownCapabilities[trackerID] = capability
		if trackerauth.IsManagedCapability(capability) {
			managed[trackerID] = struct{}{}
		}
	}
	statuses := make(map[api.TrackerID]api.TrackerAuthStatus, len(managed))
	var validationErr error
	if capabilityErr == nil && len(managed) > 0 {
		ids := make([]string, 0, len(initial.Projections))
		for _, projection := range initial.Projections {
			if _, ok := managed[projection.TrackerID]; ok && projection.Readiness == api.ReadinessStatusReady && projection.DupeReady {
				ids = append(ids, string(projection.TrackerID))
			}
		}
		if len(ids) > 0 {
			values, err := b.auth.ValidateMany(ctx, ids)
			validationErr = err
			if ctxErr := ctx.Err(); ctxErr != nil {
				return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("tracker preflight: auth validation: %w", ctxErr)
			}
			if err == nil && len(values) != len(ids) {
				validationErr = fmt.Errorf("auth validation returned %d statuses for %d trackers", len(values), len(ids))
			}
			if validationErr == nil {
				for index, id := range ids {
					statuses[api.TrackerID(id)] = values[index]
				}
			}
		}
	}
	selectedIDs := make([]string, 0, len(initial.Projections))
	for _, projection := range initial.Projections {
		if projection.Readiness == api.ReadinessStatusReady && projection.DupeReady {
			selectedIDs = append(selectedIDs, string(projection.TrackerID))
		}
	}
	var bannedRefreshErr error
	audioBlocked := make(map[string][]string)
	audioWarned := make(map[string][]string)
	if !debugMode {
		bannedRefreshErr = b.banned.RefreshDynamic(ctx, b.config, selectedIDs, b.logger)
		audioBlocked, audioWarned = metadata.EvaluateAudioBloatPolicy(subject, selectedIDs, b.registry)
	}

	freshUntil := assessedAt.Add(workflowPreflightFreshness)
	results := make([]api.TrackerPreflightResult, 0, len(initial.Projections))
	finalized := make([]api.TrackerReleaseProjection, 0, len(initial.Projections))
	for _, projection := range initial.Projections {
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:   "preflight",
			ItemID:  string(projection.TrackerID),
			Kind:    "tracker",
			Label:   string(projection.TrackerID),
			Status:  api.StageStatusQueued,
			Total:   len(initial.Projections),
			Message: "Tracker preflight queued.",
		})
	}
	for index, projection := range initial.Projections {
		projectionFingerprint, err := api.CanonicalWorkflowFingerprint(projection)
		if err != nil {
			return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("tracker preflight: fingerprint %s projection: %w", projection.TrackerID, err)
		}
		result := api.TrackerPreflightResult{
			TrackerID:             projection.TrackerID,
			State:                 api.TrackerPreflightStateReady,
			AuthReady:             true,
			ClaimsReady:           true,
			BannedGroupsReady:     true,
			RemoteMetadataReady:   true,
			ConfigFingerprint:     projection.ConfigFingerprint,
			ProjectionFingerprint: projectionFingerprint,
			AssessedAt:            assessedAt,
			FreshUntil:            freshUntil,
		}
		if projection.Readiness != api.ReadinessStatusReady || !projection.DupeReady {
			result.State = api.TrackerPreflightStateFailed
			result.RequiredActions = append([]api.RequiredAction(nil), projection.RequiredActions...)
			result.Failures = append([]api.WorkflowFailure(nil), projection.Failures...)
			if len(result.RequiredActions) > 0 {
				result.State = api.TrackerPreflightStateActionRequired
			}
			if len(result.RequiredActions) == 0 && len(result.Failures) == 0 {
				result.Failures = []api.WorkflowFailure{preflightFailure(
					projection.TrackerID,
					api.OperationFailureMissingPrerequisite,
					"Tracker projection prerequisites are incomplete.",
					api.OperationRecoveryCompletePrerequisite,
				)}
			}
		} else if _, hasCapability := knownCapabilities[projection.TrackerID]; hasCapability && capabilityErr != nil {
			setAuthBlockedPreflight(&result)
			b.logAuthBlocked(projection.TrackerID, "capability_unavailable")
		} else if _, ok := managed[projection.TrackerID]; ok {
			if validationErr != nil {
				setAuthBlockedPreflight(&result)
				b.logAuthBlocked(projection.TrackerID, "validation_unavailable")
			} else if status := statuses[projection.TrackerID]; !trackerauth.IsReadyStatus(status) {
				setAuthBlockedPreflight(&result)
				b.logAuthBlocked(projection.TrackerID, status.State)
			}
		} else if _, hasCapability := knownCapabilities[projection.TrackerID]; hasCapability && !runtimeConfigured[projection.TrackerID] {
			setAuthBlockedPreflight(&result)
			b.logAuthBlocked(projection.TrackerID, trackerauth.StateNotConfigured)
		}
		trackerName := string(projection.TrackerID)
		if result.State == api.TrackerPreflightStateReady {
			imageHostReady, err := trackers.ImageHostPolicySatisfiedWithRegistry(
				b.registry,
				b.config,
				trackerName,
				subject.ImageHostOverrides,
			)
			if err != nil {
				setMissingImageHostPreflight(&result, "Configured image host does not satisfy this tracker's image-host policy.")
			} else if !imageHostReady {
				setMissingImageHostPreflight(
					&result,
					"Required image host is not selected. Configure a compatible host in Image Hosting or tracker settings.",
				)
			}
		}
		descriptor := descriptorByID[projection.TrackerID]
		if !debugMode && result.State == api.TrackerPreflightStateReady && descriptor.Capabilities.DynamicBannedGroups && bannedRefreshErr != nil {
			setRetryablePreflight(&result, "Tracker banned-group data could not be refreshed. Retry preflight.")
			result.BannedGroupsReady = false
		}
		if !debugMode && result.State == api.TrackerPreflightStateReady {
			banned, err := b.banned.IsBanned(string(projection.TrackerID), subject.Tag)
			if err != nil {
				setRetryablePreflight(&result, "Tracker banned-group data could not be checked. Retry preflight.")
				result.BannedGroupsReady = false
			} else if banned {
				setPolicyBlockedPreflight(&result, "Release group is banned by this tracker.")
			}
		}
		if !debugMode && result.State == api.TrackerPreflightStateReady && descriptor.Capabilities.Claims {
			if factory, ok := b.registry.LookupClaimCheckerFactory(string(projection.TrackerID)); ok {
				checker := factory.NewClaimChecker(b.config, b.logger)
				claimed, err := checker.HasClaim(ctx, subject)
				if err != nil {
					setRetryablePreflight(&result, "Tracker claim data could not be checked. Retry preflight.")
					result.ClaimsReady = false
				} else if claimed {
					setPolicyBlockedPreflight(&result, "Tracker claim policy blocks this release.")
				}
			}
		}
		if !debugMode && result.State == api.TrackerPreflightStateReady && len(audioBlocked[trackerName]) > 0 {
			setPolicyBlockedPreflight(
				&result,
				fmt.Sprintf("Audio languages %s are not allowed for this tracker on bloated releases.", strings.Join(audioBlocked[trackerName], ", ")),
			)
		}
		if languages := audioWarned[trackerName]; len(languages) > 0 {
			b.logger.Warnf("core: tracker preflight audio bloat tracker=%s languages=%s", trackerName, strings.Join(languages, ","))
		}
		finalProjection := projection
		if debugMode && result.State == api.TrackerPreflightStateReady {
			if descriptor.Capabilities.StaticBannedGroups || descriptor.Capabilities.DynamicBannedGroups {
				appendBypassedRuntimeDecision(
					&finalProjection,
					"banned_group",
					"Debug mode bypassed tracker banned-group policy.",
				)
			}
			if descriptor.Capabilities.Claims {
				appendBypassedRuntimeDecision(
					&finalProjection,
					"claim_policy",
					"Debug mode bypassed tracker claim policy.",
				)
			}
			if registered, ok := b.registry.LookupDescriptor(string(projection.TrackerID)); ok && registered.AudioPolicy != nil {
				appendBypassedRuntimeDecision(
					&finalProjection,
					"audio_policy",
					"Debug mode bypassed tracker audio policy.",
				)
			}
		}
		finalProjection.RequiredActions = append([]api.RequiredAction(nil), result.RequiredActions...)
		finalProjection.Failures = append([]api.WorkflowFailure(nil), result.Failures...)
		if result.State == api.TrackerPreflightStateReady {
			finalProjection.Readiness = api.ReadinessStatusReady
			finalProjection.DupeReady = true
		} else {
			finalProjection.Readiness = api.ReadinessStatusBlocked
			if result.State == api.TrackerPreflightStateFailed {
				finalProjection.Readiness = api.ReadinessStatusIneligible
			}
			finalProjection.DupeReady = false
			finalProjection.UploadReady = false
		}
		results = append(results, result)
		finalized = append(finalized, finalProjection)
		itemStatus := api.StageStatusCompleted
		message := trackerPreflightProgressMessage(finalProjection, "Tracker is ready.")
		if result.State != api.TrackerPreflightStateReady {
			itemStatus = api.StageStatusSkipped
			message = "Tracker preflight did not pass."
		}
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:     "preflight",
			ItemID:    string(projection.TrackerID),
			Kind:      "tracker",
			Label:     string(projection.TrackerID),
			Status:    itemStatus,
			Completed: index + 1,
			Total:     len(initial.Projections),
			Message:   message,
		})
	}
	inputFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		ProjectionSet api.TrackerReleaseProjectionSetRef
		Runtime       api.WorkflowFingerprint
		ExecutionMode api.WorkflowExecutionMode
	}{
		ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: initial.ID, Revision: initial.Revision},
		Runtime:       runtime.Fingerprint,
		ExecutionMode: executionMode,
	})
	if err != nil {
		return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("tracker preflight: input fingerprint: %w", err)
	}
	return api.TrackerPreflightAssessment{
		InputFingerprint: inputFingerprint,
		ExecutionMode:    executionMode,
		Results:          results,
		ExpiresAt:        freshUntil,
	}, finalized, nil
}

func trackerPreflightProgressMessage(projection api.TrackerReleaseProjection, fallback string) string {
	for _, decision := range projection.PolicyDecisions {
		if !strings.EqualFold(strings.TrimSpace(decision.Decision), "bypassed") {
			continue
		}
		code := strings.TrimSpace(decision.Code)
		reason := strings.TrimSpace(decision.Message)
		if code == "" || reason == "" {
			continue
		}
		return fmt.Sprintf("%s policy_code=%s decision=bypassed reason=%s", fallback, code, reason)
	}
	return fallback
}

func subjectWithAvailablePreparedResources(subject api.UploadSubject) (api.UploadSubject, bool) {
	checked := subject
	changed := false
	clearUnavailable := func(path string, clearPath func()) {
		if strings.TrimSpace(path) == "" {
			return
		}
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return
		}
		clearPath()
		changed = true
	}
	clearUnavailable(checked.MediaInfoJSONPath, func() { checked.MediaInfoJSONPath = "" })
	clearUnavailable(checked.MediaInfoTextPath, func() { checked.MediaInfoTextPath = "" })
	clearUnavailable(checked.SceneNFOPath, func() { checked.SceneNFOPath = "" })
	return checked, changed
}

func newProjectionRuleFailures(projection api.TrackerReleaseProjection, failures []api.RuleFailure) []api.RuleFailure {
	existing := make(map[string]struct{}, len(projection.PolicyDecisions))
	for _, decision := range projection.PolicyDecisions {
		existing[strings.TrimSpace(decision.Code)] = struct{}{}
	}
	result := make([]api.RuleFailure, 0, len(failures))
	for _, failure := range failures {
		if _, ok := existing[strings.TrimSpace(failure.Rule)]; ok {
			continue
		}
		result = append(result, failure)
	}
	return result
}

func appendBypassedRuntimeDecision(projection *api.TrackerReleaseProjection, code string, message string) {
	projection.PolicyDecisions = append(projection.PolicyDecisions, api.TrackerPolicyDecision{
		Code:     code,
		Decision: "bypassed",
		Blocking: false,
		Message:  message,
	})
}

func setRetryablePreflight(result *api.TrackerPreflightResult, message string) {
	result.State = api.TrackerPreflightStateRetryable
	result.AuthReady = false
	result.Failures = []api.WorkflowFailure{preflightFailure(
		result.TrackerID,
		api.OperationFailureInternal,
		message,
		api.OperationRecoveryRetry,
	)}
}

func setAuthBlockedPreflight(result *api.TrackerPreflightResult) {
	result.State = api.TrackerPreflightStateRetryable
	result.AuthReady = false
	result.RequiredActions = nil
	result.Failures = []api.WorkflowFailure{preflightFailure(
		result.TrackerID,
		api.OperationFailureTrackerAuthRequired,
		authBlockedPreflightMessage,
		api.OperationRecoveryAuthenticateTrackers,
	)}
}

func (b workflowPreflightBuilder) logAuthBlocked(trackerID api.TrackerID, state string) {
	state = strings.ToLower(strings.TrimSpace(redaction.RedactValue(state, nil)))
	if state == "" {
		state = "unknown"
	}
	b.logger.Warnf(
		"core: tracker auth blocked tracker=%s state=%s decision=blocked",
		trackerID,
		state,
	)
}

func setPolicyBlockedPreflight(result *api.TrackerPreflightResult, message string) {
	result.State = api.TrackerPreflightStateFailed
	result.Failures = []api.WorkflowFailure{preflightFailure(
		result.TrackerID,
		api.OperationFailureNoEligibleTrackers,
		message,
		api.OperationRecoverySelectTrackers,
	)}
}

func setMissingImageHostPreflight(result *api.TrackerPreflightResult, message string) {
	result.State = api.TrackerPreflightStateFailed
	result.Failures = []api.WorkflowFailure{{
		Failure: api.OperationFailure{
			Code:      api.OperationFailureMissingPrerequisite,
			Operation: api.OperationKindImageHosting,
			Message:   message,
			Recovery:  api.OperationRecoveryCompletePrerequisite,
		},
		TrackerID: result.TrackerID,
	}}
}

func preflightFailure(
	trackerID api.TrackerID,
	code api.OperationFailureCode,
	message string,
	recovery api.OperationRecovery,
) api.WorkflowFailure {
	return api.WorkflowFailure{
		Failure: api.OperationFailure{
			Code:      code,
			Operation: api.OperationKindDuplicateCheck,
			Message:   message,
			Recovery:  recovery,
		},
		TrackerID: trackerID,
	}
}
