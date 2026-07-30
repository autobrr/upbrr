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

	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const workflowDupeFreshness = 30 * time.Minute

type projectionDupeService interface {
	CheckProjectionSet(
		context.Context,
		api.DuplicateSubject,
		api.TrackerReleaseProjectionSet,
		dupechecking.CheckOptions,
	) (api.DupeCheckSummary, dupechecking.Assessment, error)
}

type workflowDupeBuilder struct {
	service api.DupeService
	logger  api.Logger
}

type workflowDupePrivateEvidence struct {
	Summary    api.DupeCheckSummary
	Assessment dupechecking.Assessment
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
		if ok && projection.Readiness == api.ReadinessStatusReady && projection.DupeReady && result.State == api.TrackerPreflightStateReady {
			eligibleProjections.Projections = append(eligibleProjections.Projections, projection)
			continue
		}
		b.logger.Tracef(
			"core: duplicate lane skipped tracker=%s readiness=%s preflight=%s dupe_ready=%t",
			projection.TrackerID,
			projection.Readiness,
			result.State,
			projection.DupeReady,
		)
	}
	if len(eligibleProjections.Projections) == 0 {
		return api.DupeAssessment{}, nil, errors.New("workflow duplicate check: no eligible trackers")
	}
	var (
		summary    api.DupeCheckSummary
		assessment dupechecking.Assessment
		err        error
	)
	if service, ok := b.service.(projectionDupeService); ok {
		summary, assessment, err = service.CheckProjectionSet(ctx, subject, eligibleProjections, dupechecking.CheckOptions{
			SkipRemote:         skipRemote,
			BypassBannedGroups: projections.ExecutionMode == api.WorkflowExecutionModeDebug,
		})
	} else {
		summary, err = checkProjectionFallback(ctx, b.service, subject, eligibleProjections)
	}
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
			CheckedAt:             checkedAt,
			FreshUntil:            freshUntil,
		}
		preflightResult, preflightOK := preflightByTracker[projection.TrackerID]
		if !preflightOK {
			return api.DupeAssessment{}, nil, fmt.Errorf("workflow duplicate check: tracker %s has no preflight result", projection.TrackerID)
		}
		if projection.Readiness != api.ReadinessStatusReady || !projection.DupeReady || preflightResult.State != api.TrackerPreflightStateReady {
			trackerResult.Decision = api.DupeDecisionSkipped
			trackerResult.Status = api.StageStatusSkipped
			trackerResult.RequiredActions = append([]api.RequiredAction(nil), preflightResult.RequiredActions...)
			trackerResult.Failures = append([]api.WorkflowFailure(nil), preflightResult.Failures...)
			results = append(results, trackerResult)
			continue
		}
		result, ok := resultsByTracker[projection.TrackerID]
		if !ok {
			return api.DupeAssessment{}, nil, fmt.Errorf("workflow duplicate check: eligible tracker %s returned no result", projection.TrackerID)
		}
		trackerResult.Matches = publicDupeMatches(result)
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

func checkProjectionFallback(
	ctx context.Context,
	service api.DupeService,
	subject api.DuplicateSubject,
	projections api.TrackerReleaseProjectionSet,
) (api.DupeCheckSummary, error) {
	combined := api.DupeCheckSummary{SourcePath: subject.SourcePath}
	for _, projection := range projections.Projections {
		trackerSubject := subject
		trackerSubject.Projection = &projection
		trackerSubject.ReleaseName = projection.DuplicateCriteria.Name
		summary, err := service.Check(ctx, trackerSubject, []string{string(projection.TrackerID)})
		if err != nil {
			return combined, fmt.Errorf("tracker %s: %w", projection.TrackerID, err)
		}
		combined.Results = append(combined.Results, summary.Results...)
		combined.Notes = append(combined.Notes, summary.Notes...)
	}
	return combined, nil
}

func publicDupeMatches(result api.DupeCheckResult) []api.DupeMatchProjection {
	matches := make([]api.DupeMatchProjection, 0, len(result.Filtered))
	for _, entry := range result.Filtered {
		matches = append(matches, api.DupeMatchProjection{
			ID:        strings.TrimSpace(entry.ID),
			Name:      strings.TrimSpace(entry.Name),
			Link:      strings.TrimSpace(entry.Link),
			SizeBytes: entry.SizeBytes,
			Flags:     append([]string(nil), entry.Flags...),
			Reason:    strings.TrimSpace(result.Match.MatchedReason),
		})
	}
	if len(matches) == 0 && (result.HasDupes || strings.TrimSpace(result.Match.MatchedReason) != "") {
		name := strings.TrimSpace(result.Match.MatchedName)
		if name == "" {
			name = strings.TrimSpace(result.UploadReleaseName)
		}
		if name == "" {
			name = strings.TrimSpace(result.CanonicalReleaseName)
		}
		if name == "" {
			name = "Existing duplicate"
		}
		matches = append(matches, api.DupeMatchProjection{
			ID:     strings.TrimSpace(result.Match.MatchedID),
			Name:   name,
			Link:   strings.TrimSpace(result.Match.MatchedLink),
			Reason: strings.TrimSpace(result.Match.MatchedReason),
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
	case result.HasDupes || strings.EqualFold(strings.TrimSpace(result.Match.MatchedReason), "in_client"):
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
	default:
		target.Decision = api.DupeDecisionNoMatch
		target.Status = api.StageStatusCompleted
	}
}
