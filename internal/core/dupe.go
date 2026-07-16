// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/autobrr/upbrr/internal/clientdiscovery"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

// dupeModule owns duplicate-check policy over exact prepared generations.
type dupeModule struct {
	cfg           config.Config
	logger        api.Logger
	services      api.ServiceSet
	registry      *trackers.Registry
	preparedFacts duplicatePreparedFacts
	discovery     *clientdiscovery.Module
}

// duplicatePreparedFacts exposes only preparation operations required by dupe checks.
type duplicatePreparedFacts interface {
	Prepare(context.Context, api.PrepareInput) (api.PrepareResult, error)
	ResolveDuplicateSubject(context.Context, api.DuplicateCheckInput) (api.DuplicateSubject, error)
}

type duplicateAssessmentChecker interface {
	CheckWithAssessment(context.Context, api.DuplicateSubject, []string, dupechecking.CheckOptions) (api.DupeCheckSummary, dupechecking.Assessment, error)
}

// newDupeModule composes duplicate policy with canonical facts and current client discovery.
func newDupeModule(
	cfg config.Config,
	logger api.Logger,
	services api.ServiceSet,
	registry *trackers.Registry,
	preparedFacts duplicatePreparedFacts,
	discovery *clientdiscovery.Module,
) *dupeModule {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &dupeModule{
		cfg:           cfg,
		logger:        logger,
		services:      services,
		registry:      registry,
		preparedFacts: preparedFacts,
		discovery:     discovery,
	}
}

func (m *dupeModule) check(ctx context.Context, req api.Request) (api.DupeCheckSummary, error) {
	req = normalizeExecutionRequest(req)
	if m.preparedFacts == nil {
		return api.DupeCheckSummary{}, errors.New("core: canonical preparation is not configured")
	}
	prepareInput, err := api.MapPreparationRequest(req, api.PreparationIntentDuplicateCheck)
	if err != nil {
		return api.DupeCheckSummary{}, fmt.Errorf("core: map duplicate-check preparation request: %w", err)
	}
	prepared, err := m.preparedFacts.Prepare(ctx, prepareInput)
	if err != nil {
		return api.DupeCheckSummary{}, fmt.Errorf("core: prepare duplicate check: %w", err)
	}
	input := duplicateCheckInputFromRequest(req, api.ReleaseRef{
		SourcePath: prepared.Release.Source.SourcePath,
		Generation: prepared.Release.Generation,
	})
	input.Trackers = trackers.ResolveTrackersWithRegistry(m.cfg, req.Trackers, nil, m.logger, m.registry)
	return m.checkAccepted(ctx, input)
}

// checkAccepted refreshes operation-local client evidence, applies explicit ID
// overrides, performs auth preflight, and assesses the exact prepared generation.
func (m *dupeModule) checkAccepted(ctx context.Context, input api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
	if m.preparedFacts == nil {
		return api.DupeCheckSummary{}, errors.New("core: canonical preparation is not configured")
	}
	if m.services.Dupes == nil {
		return api.DupeCheckSummary{}, errors.New("core: dupe service not configured")
	}
	subject, err := m.preparedFacts.ResolveDuplicateSubject(ctx, input)
	if err != nil {
		return api.DupeCheckSummary{}, fmt.Errorf("core: resolve duplicate-check subject: %w", err)
	}
	var clientEvidence clientdiscovery.Evidence
	if m.discovery != nil {
		evidence, discoveryErr := m.discovery.Discover(ctx, clientdiscovery.SearchInput{
			SourcePath:   subject.SourcePath,
			FileList:     subject.FileList,
			DiscType:     subject.DiscType,
			Policy:       input.ClientSearch,
			ForceRecheck: input.ForceRecheck,
			Debug:        input.Debug,
		})
		if discoveryErr != nil {
			return api.DupeCheckSummary{}, fmt.Errorf("core: discover duplicate-check client state: %w", discoveryErr)
		}
		clientEvidence = evidence
	}
	applyClientEvidenceToDuplicateSubject(&subject, clientEvidence, input.TrackerIDs)
	resolvedTrackers := trackers.ResolveExplicitTrackersWithRegistry(input.Trackers, m.logger, m.registry)
	if len(resolvedTrackers) == 0 {
		return api.DupeCheckSummary{}, noEligibleTrackersError(api.OperationKindDuplicateCheck)
	}
	readyTrackers := resolvedTrackers
	var authBlocked []api.DupeCheckResult
	if input.Interaction == api.InteractionModeInteractive && m.services.TrackerAuth != nil {
		readyTrackers, authBlocked, err = m.preflightGUITrackerAuth(ctx, subject, resolvedTrackers)
		if err != nil {
			return api.DupeCheckSummary{}, err
		}
	}
	summary, _, err := checkDuplicateAssessment(ctx, m.services.Dupes, subject, readyTrackers, dupechecking.CheckOptions{SkipRemote: input.Skip})
	if err != nil {
		return summary, fmt.Errorf("core: %w", err)
	}
	summary.Results = append(summary.Results, authBlocked...)
	sort.Slice(summary.Results, func(i, j int) bool {
		return strings.ToUpper(summary.Results[i].Tracker) < strings.ToUpper(summary.Results[j].Tracker)
	})
	resultByTracker := mapDupeResults(summary.Results)
	assessments := make([]api.TrackerEligibilityAssessment, 0, len(resolvedTrackers))
	for _, tracker := range resolvedTrackers {
		name := normalizeEligibilityTracker(tracker)
		assessments = append(assessments, api.TrackerEligibilityAssessment{
			Tracker:      name,
			Duplicate:    resultByTracker[name],
			RuleFailures: cloneRuleFailures(subject.TrackerRuleFailures[name]),
			PolicyBlocks: append([]api.TrackerBlockReason(nil), subject.BlockedTrackers[name]...),
			Choices: api.TrackerReviewChoices{
				IgnoreDuplicate: containsNormalizedTracker(input.IgnoreFor, name),
			},
		})
	}
	summary.Eligibility = buildTrackerEligibility(api.TrackerEligibilityInput{
		Release:          input.Release,
		SelectedTrackers: resolvedTrackers,
		Assessments:      assessments,
	})
	return summary, nil
}

// applyClientEvidenceToDuplicateSubject replaces stale client-derived state with
// the current snapshot, then overlays explicit tracker-ID overrides.
func applyClientEvidenceToDuplicateSubject(
	subject *api.DuplicateSubject,
	evidence clientdiscovery.Evidence,
	overrides map[string]string,
) {
	if subject == nil {
		return
	}
	subject.TrackerIDs = cloneStringMap(evidence.TrackerIDs)
	if subject.TrackerIDs == nil && len(overrides) > 0 {
		subject.TrackerIDs = make(map[string]string, len(overrides))
	}
	for key, value := range overrides {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			subject.TrackerIDs[key] = value
		}
	}
	subject.MatchedTrackers = normalizeReviewedTrackers(evidence.MatchedTrackers)
}

func containsNormalizedTracker(values []string, target string) bool {
	for _, value := range values {
		if normalizeEligibilityTracker(value) == target {
			return true
		}
	}
	return false
}

func cloneTrackerRuleFailures(input map[string][]api.RuleFailure) map[string][]api.RuleFailure {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string][]api.RuleFailure, len(input))
	for tracker, failures := range input {
		cloned[tracker] = cloneRuleFailures(failures)
	}
	return cloned
}
