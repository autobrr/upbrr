// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"
	"strings"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func cloneTrackerRuleFailures(input map[string][]api.RuleFailure) map[string][]api.RuleFailure {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string][]api.RuleFailure, len(input))
	for tracker, failures := range input {
		cloned[tracker] = append([]api.RuleFailure(nil), failures...)
	}
	return cloned
}

// EvaluateUploadPolicy evaluates request-scoped audio, rule, and claim policy
// against an operation-owned subject. Returned state contains only workflow
// outcomes; canonical prepared facts remain unchanged.
func (s *Service) EvaluateUploadPolicy(
	ctx context.Context,
	subject api.UploadSubject,
	trackerNames []string,
) (api.UploadReviewOutcome, error) {
	resolved := uniqueUpperTrackers(trackerNames)
	outcome := api.UploadReviewOutcome{ResolvedTrackers: append([]string(nil), resolved...)}
	if len(resolved) == 0 {
		return outcome, nil
	}

	audioEvidence := preparationstate.State{
		DiscType:         subject.DiscType,
		AudioLanguages:   append([]string(nil), subject.AudioLanguages...),
		ProviderMetadata: subject.ProviderMetadata,
	}
	blockedAudio, _ := resolveAudioBloatPolicyWithRegistry(audioEvidence, resolved, s.registry)
	for tracker, languages := range blockedAudio {
		outcome.BlockedTrackers = addMetadataTrackerBlockReason(outcome.BlockedTrackers, tracker, api.TrackerBlockReasonAudio)
		outcome.TrackerRuleFailures = addMetadataTrackerRuleFailure(outcome.TrackerRuleFailures, tracker, api.RuleFailure{
			Rule:   "audio_bloat",
			Reason: audioBloatReason(languages, true),
		})
	}

	for _, tracker := range resolved {
		if err := ctx.Err(); err != nil {
			return api.UploadReviewOutcome{}, fmt.Errorf("metadata: evaluate upload policy: %w", err)
		}
		name := strings.ToUpper(strings.TrimSpace(tracker))
		failures := trackers.EvaluateRulesWithRegistry(ctx, s.registry, name, api.NewRuleSubject(subject), s.logger)
		if len(failures) > 0 {
			if outcome.TrackerRuleFailures == nil {
				outcome.TrackerRuleFailures = make(map[string][]api.RuleFailure)
			}
			outcome.TrackerRuleFailures[name] = append(outcome.TrackerRuleFailures[name], failures...)
		}
	}

	subject.Trackers = append([]string(nil), resolved...)
	subject.BlockedTrackers = cloneTrackerBlocks(outcome.BlockedTrackers)
	subject.TrackerRuleFailures = cloneTrackerRuleFailures(outcome.TrackerRuleFailures)
	subject, err := s.evaluateTrackerClaims(ctx, subject)
	if err != nil {
		return api.UploadReviewOutcome{}, err
	}
	outcome.BlockedTrackers = cloneTrackerBlocks(subject.BlockedTrackers)
	outcome.TrackerRuleFailures = cloneTrackerRuleFailures(subject.TrackerRuleFailures)
	return outcome, nil
}

func cloneTrackerBlocks(source map[string][]api.TrackerBlockReason) map[string][]api.TrackerBlockReason {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string][]api.TrackerBlockReason, len(source))
	for tracker, reasons := range source {
		cloned[tracker] = append([]api.TrackerBlockReason(nil), reasons...)
	}
	return cloned
}
