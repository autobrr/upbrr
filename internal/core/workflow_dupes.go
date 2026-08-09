// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

const workflowDupeFreshness = 30 * time.Minute

type workflowDupeBuilder struct {
	service api.ProjectionDupeService
	logger  api.Logger
}

type workflowDupePrivateEvidence struct {
	Summary    api.DupeCheckSummary
	Assessment api.DupeAssessmentEvidence
}

func (b workflowDupeBuilder) Build(
	ctx context.Context,
	subject api.DuplicateSubject,
	projections api.TrackerReleaseProjectionSet,
	preflight api.TrackerPreflightAssessment,
	checkedAt time.Time,
	skipRemote bool,
) (api.DupeAssessment, any, error) {
	if err := ctx.Err(); err != nil {
		return api.DupeAssessment{}, nil, fmt.Errorf("workflow duplicate check: %w", err)
	}
	if b.service == nil {
		return api.DupeAssessment{}, nil, errors.New("workflow duplicate check: service is required")
	}
	if b.logger == nil {
		b.logger = api.NopLogger{}
	}
	if preflight.Status != api.StageStatusReady || !preflight.ExpiresAt.After(checkedAt) {
		return api.DupeAssessment{}, nil, errors.New("workflow duplicate check: preflight is stale or not ready")
	}
	preflightByTracker := make(map[api.TrackerID]api.TrackerPreflightResult, len(preflight.Results))
	for _, result := range preflight.Results {
		preflightByTracker[result.TrackerID] = result
	}
	inClient := func(trackerID api.TrackerID) bool {
		return slices.ContainsFunc(subject.MatchedTrackers, func(candidate string) bool {
			return strings.EqualFold(strings.TrimSpace(candidate), string(trackerID))
		})
	}
	eligibleProjections := projections
	eligibleProjections.Projections = make([]api.TrackerReleaseProjection, 0, len(projections.Projections))
	for _, projection := range projections.Projections {
		if projection.DupeReady && slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
			return decision.Blocking
		}) {
			return api.DupeAssessment{}, nil, fmt.Errorf(
				"workflow duplicate check: tracker %s is dupe-ready with a blocking policy decision",
				projection.TrackerID,
			)
		}
		result, ok := preflightByTracker[projection.TrackerID]
		if inClient(projection.TrackerID) ||
			(ok && projection.Readiness == api.ReadinessStatusReady && projection.DupeReady && result.State == api.TrackerPreflightStateReady) {
			eligibleProjections.Projections = append(eligibleProjections.Projections, projection)
			continue
		}
		b.logger.Tracef(
			"core: duplicate lane skipped tracker=%s readiness=%s preflight=%s dupe_ready=%t policy_codes=%q failure_codes=%q recoveries=%q required_actions=%q",
			projection.TrackerID,
			projection.Readiness,
			result.State,
			projection.DupeReady,
			blockingPolicyDecisionLogValues(projection.PolicyDecisions),
			workflowFailureLogValues(result.Failures, func(failure api.OperationFailure) string { return string(failure.Code) }),
			workflowFailureLogValues(result.Failures, func(failure api.OperationFailure) string { return string(failure.Recovery) }),
			requiredActionLogValues(result.RequiredActions),
		)
	}
	if len(eligibleProjections.Projections) == 0 {
		return api.DupeAssessment{}, nil, errors.New("workflow duplicate check: no eligible trackers")
	}
	var (
		summary    api.DupeCheckSummary
		assessment api.DupeAssessmentEvidence
		err        error
	)
	summary, assessment, err = b.service.CheckProjectionSet(ctx, subject, eligibleProjections, api.ProjectionDupeCheckOptions{
		SkipRemote:         skipRemote,
		BypassBannedGroups: projections.ExecutionMode == api.WorkflowExecutionModeDebug,
	})
	if err != nil {
		return api.DupeAssessment{}, nil, fmt.Errorf("workflow duplicate check: %w", err)
	}
	resultsByTracker := make(map[api.TrackerID]api.DupeCheckResult, len(summary.Results))
	for _, result := range summary.Results {
		trackerID := api.TrackerID(strings.ToUpper(strings.TrimSpace(result.Tracker)))
		resultsByTracker[trackerID] = result
	}
	freshUntil := checkedAt.Add(workflowDupeFreshness)
	results := make([]api.TrackerDupeAssessment, 0, len(projections.Projections))
	for _, projection := range projections.Projections {
		projectionFingerprint, err := api.CanonicalWorkflowFingerprint(projection)
		if err != nil {
			return api.DupeAssessment{}, nil, fmt.Errorf("workflow duplicate check: fingerprint %s projection: %w", projection.TrackerID, err)
		}
		trackerResult := api.TrackerDupeAssessment{
			TrackerID:             projection.TrackerID,
			UploadReleaseName:     projection.UploadReleaseName,
			ProjectionFingerprint: projectionFingerprint,
			CriteriaFingerprint:   projection.CriteriaFingerprint,
			Criteria:              projection.DuplicateCriteria,
			TargetFingerprint:     projection.DuplicateTargetFingerprint,
			SearchFingerprint:     projection.DuplicateSearchFingerprint,
			PolicyID:              projection.DuplicatePolicyID,
			PolicyFingerprint:     projection.DuplicatePolicyFingerprint,
			CheckedAt:             checkedAt,
			FreshUntil:            freshUntil,
		}
		preflightResult, preflightOK := preflightByTracker[projection.TrackerID]
		if !preflightOK {
			return api.DupeAssessment{}, nil, fmt.Errorf("workflow duplicate check: tracker %s has no preflight result", projection.TrackerID)
		}
		result, resultOK := resultsByTracker[projection.TrackerID]
		if !inClient(projection.TrackerID) &&
			(projection.Readiness != api.ReadinessStatusReady || !projection.DupeReady || preflightResult.State != api.TrackerPreflightStateReady) {
			trackerResult.Decision = api.DupeDecisionSkipped
			trackerResult.Status = api.StageStatusSkipped
			trackerResult.RequiredActions = append([]api.RequiredAction(nil), preflightResult.RequiredActions...)
			trackerResult.Failures = append([]api.WorkflowFailure(nil), preflightResult.Failures...)
			if hasWorkflowDupeExtendedLineage(trackerResult) {
				trackerResult.EvidenceFingerprint, err = api.CanonicalWorkflowFingerprint(struct {
					ProjectionReadiness api.ReadinessStatus
					DupeReady           bool
					Preflight           api.TrackerPreflightResult
				}{
					ProjectionReadiness: projection.Readiness,
					DupeReady:           projection.DupeReady,
					Preflight:           preflightResult,
				})
				if err != nil {
					return api.DupeAssessment{}, nil, fmt.Errorf(
						"workflow duplicate check: fingerprint %s skipped evidence: %w",
						projection.TrackerID,
						err,
					)
				}
			}
			results = append(results, trackerResult)
			continue
		}
		if !resultOK {
			return api.DupeAssessment{}, nil, fmt.Errorf("workflow duplicate check: eligible tracker %s returned no result", projection.TrackerID)
		}
		trackerResult.Matches = publicDupeMatches(result)
		trackerResult.Search = result.Search
		if hasWorkflowDupeExtendedLineage(trackerResult) {
			trackerResult.EvidenceFingerprint, err = duplicateEvidenceFingerprint(result)
			if err != nil {
				return api.DupeAssessment{}, nil, fmt.Errorf("workflow duplicate check: fingerprint %s evidence: %w", projection.TrackerID, err)
			}
		}
		if !result.CheckedAt.IsZero() {
			trackerResult.CheckedAt = result.CheckedAt
		}
		setWorkflowDupeOutcome(&trackerResult, result)
		results = append(results, trackerResult)
	}
	inputFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		ProjectionSet api.TrackerReleaseProjectionSetRef
		Preflight     api.TrackerPreflightAssessmentRef
		SkipRemote    bool
	}{
		ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: projections.ID, Revision: projections.Revision},
		Preflight:     api.TrackerPreflightAssessmentRef{ID: preflight.ID, Revision: preflight.Revision},
		SkipRemote:    skipRemote,
	})
	if err != nil {
		return api.DupeAssessment{}, nil, fmt.Errorf("workflow duplicate check: input fingerprint: %w", err)
	}
	return api.DupeAssessment{
		InputFingerprint: inputFingerprint,
		Results:          results,
		ExpiresAt:        freshUntil,
	}, workflowDupePrivateEvidence{Summary: summary, Assessment: assessment}, nil
}

func blockingPolicyDecisionLogValues(decisions []api.TrackerPolicyDecision) string {
	values := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		if decision.Blocking {
			values = appendUniqueLogValue(values, decision.Code)
		}
	}
	slices.Sort(values)
	return strings.Join(values, ",")
}

func workflowFailureLogValues(failures []api.WorkflowFailure, value func(api.OperationFailure) string) string {
	values := make([]string, 0, len(failures))
	for _, failure := range failures {
		values = appendUniqueLogValue(values, value(failure.Failure))
	}
	slices.Sort(values)
	return strings.Join(values, ",")
}

func requiredActionLogValues(actions []api.RequiredAction) string {
	values := make([]string, 0, len(actions))
	for _, action := range actions {
		values = appendUniqueLogValue(values, string(action.Kind))
	}
	slices.Sort(values)
	return strings.Join(values, ",")
}

func appendUniqueLogValue(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

func hasWorkflowDupeExtendedLineage(result api.TrackerDupeAssessment) bool {
	return result.PolicyID != "" || result.TargetFingerprint != "" ||
		result.SearchFingerprint != "" || result.PolicyFingerprint != ""
}

func duplicateEvidenceFingerprint(result api.DupeCheckResult) (api.WorkflowFingerprint, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Search      api.DupeSearchEvidence
		Evaluations []api.DupeCandidateEvaluation
		HasDupes    bool
		Skipped     bool
		SkipCode    string
		Status      string
		Error       string
	}{
		Search:      result.Search,
		Evaluations: result.Evaluations,
		HasDupes:    result.HasDupes,
		Skipped:     result.Skipped,
		SkipCode:    result.SkipCode,
		Status:      result.Status,
		Error:       result.Error,
	})
	if err != nil {
		return "", fmt.Errorf("canonical duplicate evidence fingerprint: %w", err)
	}
	return fingerprint, nil
}

func publicDupeMatches(result api.DupeCheckResult) []api.DupeMatchProjection {
	matches := make([]api.DupeMatchProjection, 0, len(result.Evaluations))
	for _, evaluation := range result.Evaluations {
		if evaluation.Relation == api.DupeRelationCoexists {
			continue
		}
		reason := ""
		if len(evaluation.Reasons) > 0 {
			reason = evaluation.Reasons[0].Code
		}
		matches = append(matches, api.DupeMatchProjection{
			ID:             strings.TrimSpace(evaluation.ID),
			Name:           strings.TrimSpace(evaluation.Name),
			Link:           strings.TrimSpace(evaluation.Link),
			SizeBytes:      evaluation.SizeBytes,
			Reason:         reason,
			Relation:       evaluation.Relation,
			Reasons:        append([]api.DupeReason(nil), evaluation.Reasons...),
			Flags:          append([]string(nil), evaluation.Flags...),
			HDR:            evaluation.HDR,
			Category:       evaluation.Category,
			Type:           evaluation.Type,
			Resolution:     evaluation.Resolution,
			Source:         evaluation.Source,
			Codec:          evaluation.Codec,
			Container:      evaluation.Container,
			Provider:       evaluation.Provider,
			Group:          evaluation.Group,
			ReleaseOrigin:  evaluation.ReleaseOrigin,
			Edition:        evaluation.Edition,
			Region:         evaluation.Region,
			ThreeD:         evaluation.ThreeD,
			Repack:         evaluation.Repack,
			Season:         evaluation.Season,
			Episode:        evaluation.Episode,
			Date:           evaluation.Date,
			Pack:           evaluation.Pack,
			Internal:       evaluation.Internal,
			Trumpable:      evaluation.Trumpable,
			EvidenceStatus: evaluation.EvidenceStatus,
		})
	}
	if len(matches) == 0 && !result.Search.Complete && !result.Skipped &&
		!strings.EqualFold(strings.TrimSpace(result.Status), "failed") &&
		!strings.EqualFold(strings.TrimSpace(result.Status), "bypassed") && strings.TrimSpace(result.Error) == "" {
		name := strings.TrimSpace(result.UploadReleaseName)
		if name == "" {
			name = strings.TrimSpace(result.CanonicalReleaseName)
		}
		if name == "" {
			name = "Possible duplicate"
		}
		matches = append(matches, api.DupeMatchProjection{
			Name:     name,
			Reason:   "incomplete_search",
			Relation: api.DupeRelationInsufficientEvidence,
			Reasons: []api.DupeReason{{
				Code:    "incomplete_search",
				Message: "Duplicate search was incomplete; confirm tracker policy risk before continuing.",
			}},
		})
	}
	return matches
}

func setWorkflowDupeOutcome(target *api.TrackerDupeAssessment, result api.DupeCheckResult) {
	switch {
	case strings.EqualFold(strings.TrimSpace(result.Status), "failed") || strings.TrimSpace(result.Error) != "":
		target.Decision = api.DupeDecisionSkipped
		target.Status = api.StageStatusFailed
		target.Failures = []api.WorkflowFailure{{
			Failure: api.OperationFailure{
				Code:      api.OperationFailureInternal,
				Operation: api.OperationKindDuplicateCheck,
				Message:   "Duplicate search failed. Retry the assessment.",
				Recovery:  api.OperationRecoveryRetry,
			},
			TrackerID: target.TrackerID,
		}}
	case hasBlockingDupeRelation(result):
		// Duplicate evidence blocks only this tracker by default. The assessment
		// itself is complete, so unrelated trackers and downstream pages remain
		// available. Remote matches can be explicitly ignored later; in-client
		// matches remain strict.
		target.Decision = api.DupeDecisionAccepted
		target.Status = api.StageStatusCompleted
	case strings.EqualFold(strings.TrimSpace(result.Status), "bypassed"):
		target.Decision = api.DupeDecisionBypassed
		target.Status = api.StageStatusCompleted
	case result.Skipped:
		target.Decision = api.DupeDecisionSkipped
		target.Status = api.StageStatusSkipped
	case result.HasDupes || !result.Search.Complete:
		target.Decision = api.DupeDecisionPending
		target.Status = api.StageStatusBlocked
		target.RequiredActions = []api.RequiredAction{{
			Kind:      api.RequiredActionReviewDuplicates,
			Status:    api.RequiredActionStatusPending,
			TrackerID: target.TrackerID,
			Prompt:    "Review incomplete, same-slot, or proposed-trump duplicate evidence and acknowledge tracker policy risk.",
			Options: []api.RequiredActionOption{
				{Value: string(api.DupeDecisionAccepted), Label: "Treat as duplicate"},
				{Value: string(api.DupeDecisionIgnored), Label: "Acknowledge risk and continue"},
			},
		}}
	default:
		target.Decision = api.DupeDecisionNoMatch
		target.Status = api.StageStatusCompleted
	}
}

func hasBlockingDupeRelation(result api.DupeCheckResult) bool {
	return slices.ContainsFunc(result.Evaluations, func(candidate api.DupeCandidateEvaluation) bool {
		return candidate.Relation == api.DupeRelationExactDuplicate || candidate.Relation == api.DupeRelationExistingPreferred
	})
}
