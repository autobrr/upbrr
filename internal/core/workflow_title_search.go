// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

// assessTitleSearchRules gathers tracker-owned whole-title evidence before rule
// readiness gates. Pure validation then decides whether the evidence permits a
// waiver; the search itself never authorizes an upload.
func (b workflowPreflightBuilder) assessTitleSearchRules(
	ctx context.Context,
	subject api.UploadSubject,
	initial api.TrackerReleaseProjectionSet,
	assessedAt time.Time,
) (api.UploadSubject, api.TrackerReleaseProjectionSet, error) {
	initial.Projections = slices.Clone(initial.Projections)
	subject.TrackerTitleSearchEvidence = maps.Clone(subject.TrackerTitleSearchEvidence)
	if subject.TrackerTitleSearchEvidence == nil {
		subject.TrackerTitleSearchEvidence = make(map[string]api.TrackerTitleSearchEvidence)
	}
	for index := range initial.Projections {
		projection := &initial.Projections[index]
		descriptor, ok := b.registry.LookupDescriptor(string(projection.TrackerID))
		if !ok {
			continue
		}
		provider, ok := descriptor.Definition.(trackers.TitleSearchPolicyProvider)
		if !ok {
			continue
		}
		policy := provider.TitleSearchPolicy()
		validation := api.NewTrackerValidationSubject(subject, string(projection.TrackerID))
		if policy.ID == "" || policy.Required == nil || !policy.Required(validation) {
			continue
		}
		evidence, err := b.acquireTitleSearchEvidence(ctx, subject, *projection, descriptor.Definition, policy.ID, assessedAt)
		if err != nil {
			return subject, initial, err
		}
		projection.TitleSearchEvidence = &evidence
		subject.TrackerTitleSearchEvidence[string(projection.TrackerID)] = evidence
		validation.TitleSearchEvidence = evidence
		validation.QuestionnaireAnswers = maps.Clone(projection.QuestionnaireAnswers)
		failures, err := trackers.EvaluateTrackerValidationWithRegistry(ctx, b.registry, string(projection.TrackerID), validation, b.logger)
		if err != nil {
			return subject, initial, fmt.Errorf("tracker preflight: title-evidence rules: %w", err)
		}
		// Input-readiness findings are projected separately from tracker validation.
		// Their authority is unchanged by remote title evidence.
		for _, decision := range projection.PolicyDecisions {
			if strings.HasPrefix(decision.Code, "input.") {
				failures = append(failures, api.RuleFailure{
					Rule:           decision.Code,
					Reason:         decision.Message,
					Disposition:    decision.Disposition,
					EvidenceStatus: decision.EvidenceStatus,
				})
			}
		}
		projection.PolicyDecisions = slices.Clone(projection.PolicyDecisions)
		projection.RequiredActions = slices.Clone(projection.RequiredActions)
		projection.Failures = slices.Clone(projection.Failures)
		// Rule reevaluation can remove an unresolved rule gate, but never a separate
		// naming, questionnaire, or constructibility prerequisite.
		if !hasNonRuleProjectionBlock(*projection) {
			projection.Readiness = api.ReadinessStatusReady
			projection.DupeReady = true
			projection.UploadReady = !slices.ContainsFunc(projection.RequiredActions, func(action api.RequiredAction) bool {
				return action.Kind != api.RequiredActionAuthorizeRules && action.Status != api.RequiredActionStatusResolved
			})
		}
		if err := trackers.ApplyProjectionRuleFailures(
			projection,
			failures,
			initial.ExecutionMode,
			projection.RuleAuthorizationFingerprint,
			b.logger,
		); err != nil {
			return subject, initial, fmt.Errorf("tracker preflight: title-evidence rule authority: %w", err)
		}
	}
	return subject, initial, nil
}

func hasNonRuleProjectionBlock(projection api.TrackerReleaseProjection) bool {
	return slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Blocking && decision.Disposition == ""
	}) || slices.ContainsFunc(projection.Failures, func(failure api.WorkflowFailure) bool {
		return failure.Failure.Code != api.OperationFailureNoEligibleTrackers || failure.Failure.Operation != api.OperationKindDuplicateCheck
	}) || slices.ContainsFunc(projection.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionAnswerQuestionnaire && action.Status != api.RequiredActionStatusResolved
	})
}

func (b workflowPreflightBuilder) acquireTitleSearchEvidence(
	ctx context.Context,
	subject api.UploadSubject,
	projection api.TrackerReleaseProjection,
	definition trackers.Definition,
	policyID string,
	now time.Time,
) (api.TrackerTitleSearchEvidence, error) {
	evidence := api.TrackerTitleSearchEvidence{
		Status:            api.MetadataEvidenceStatusUnavailable,
		ConfigFingerprint: projection.ConfigFingerprint,
		PolicyID:          policyID,
		CheckedAt:         now,
		FreshUntil:        now.Add(workflowPreflightFreshness),
	}
	title, err := api.TitleSearchIdentityFingerprint(subject.Identity)
	if err != nil {
		return evidence, nil
	}
	evidence.TitleFingerprint = title
	factory, ok := definition.(dupe.Factory)
	if !ok {
		return evidence, nil
	}
	b.logger.Infof("tracker preflight: title evidence tracker=%s state=start", projection.TrackerID)
	adapter := dupe.NewAdapter(factory, string(projection.TrackerID), b.config, nil, b.logger, b.registry)
	titleAdapter, ok := adapter.(dupe.TitleSearchAdapter)
	if !ok {
		return evidence, nil
	}
	result := titleAdapter.SearchTitle(ctx, api.DuplicateSubject{
		SourcePath:  subject.SourcePath,
		Identity:    subject.Identity,
		ReleaseName: projection.UploadReleaseName,
		DiscType:    subject.DiscType,
	})
	if err := ctx.Err(); err != nil {
		return evidence, fmt.Errorf("tracker preflight: title search canceled: %w", err)
	}
	search := result.SearchEvidence()
	rows := result.Entries()
	evidence.TorrentCount = len(rows)
	if search.WorkScope != dupe.WorkScopeProviderID {
		evidence.TorrentCount = -1
	}
	if result.Disposition() == dupe.DispositionResolved {
		evidence.Status = api.MetadataEvidenceStatusPartial
		if search.EffectiveComplete() && search.WorkScope == dupe.WorkScopeProviderID && search.WrongWorkCount == 0 {
			evidence.Status = api.MetadataEvidenceStatusComplete
		}
	}
	// Hash sanitized stable row identity before slot evaluation: even a different
	// resolution, season or format counts against title-wide absence.
	rowIdentities := make([]string, 0, len(rows))
	for _, row := range rows {
		rowIdentities = append(rowIdentities, strings.Join([]string{row.ID, row.Name, row.Type, row.Res}, "\x00"))
	}
	slices.Sort(rowIdentities)
	evidence.ResultFingerprint, err = api.CanonicalWorkflowFingerprint(struct {
		Title  api.WorkflowFingerprint
		Config api.WorkflowFingerprint
		Policy string
		Status api.MetadataEvidenceStatus
		Rows   []string
	}{title, projection.ConfigFingerprint, policyID, evidence.Status, rowIdentities})
	if err != nil {
		return evidence, fmt.Errorf("tracker preflight: title evidence fingerprint: %w", err)
	}
	b.logger.Infof("tracker preflight: title evidence tracker=%s state=%s count=%d", projection.TrackerID, evidence.Status, evidence.TorrentCount)
	return evidence, nil
}
