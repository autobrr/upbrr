// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// buildTrackerEligibility is the only tracker eligibility decision mapper.
// Callers provide current exact-generation assessments; presentation layers
// consume the ordered projection without reconstructing policy.
func buildTrackerEligibility(input api.TrackerEligibilityInput) api.TrackerEligibility {
	selected := normalizeEligibilityTrackers(input.SelectedTrackers)
	assessments := make(map[string]api.TrackerEligibilityAssessment, len(input.Assessments))
	for _, assessment := range input.Assessments {
		name := normalizeEligibilityTracker(assessment.Tracker)
		if name != "" {
			assessment.Tracker = name
			assessments[name] = assessment
		}
	}
	result := api.TrackerEligibility{
		Release:  input.Release,
		Trackers: make([]api.TrackerEligibilityState, 0, len(selected)),
	}
	for _, tracker := range selected {
		assessment, found := assessments[tracker]
		state := api.TrackerEligibilityState{Tracker: tracker}
		if !found {
			state.Reasons = append(state.Reasons, api.TrackerEligibilityReason{
				Code:    api.TrackerEligibilityAssessmentFailed,
				Message: "Tracker assessment is unavailable.",
			})
		} else {
			state.Reasons = trackerEligibilityReasons(assessment)
		}
		state.Eligible = len(state.Reasons) == 0
		result.Trackers = append(result.Trackers, state)
		if state.Eligible {
			result.EligibleTrackers = append(result.EligibleTrackers, tracker)
		}
	}
	return result
}

func trackerEligibilityReasons(assessment api.TrackerEligibilityAssessment) []api.TrackerEligibilityReason {
	reasons := make([]api.TrackerEligibilityReason, 0, 4)
	appendReason := func(code api.TrackerEligibilityReasonCode, message string) {
		for _, existing := range reasons {
			if existing.Code == code {
				return
			}
		}
		reasons = append(reasons, api.TrackerEligibilityReason{Code: code, Message: message})
	}
	if assessment.AuthRequired || assessment.Duplicate.SkipCode == dupeSkipCodeTrackerAuthNotReady {
		appendReason(api.TrackerEligibilityAuthRequired, "Tracker authentication is required.")
	}
	if assessment.Banned {
		message := "Release group is banned by the tracker."
		if value := strings.TrimSpace(assessment.BannedReason); value != "" {
			message = value
		}
		appendReason(api.TrackerEligibilityBannedGroup, message)
	}
	if len(assessment.PolicyBlocks) > 0 {
		appendReason(api.TrackerEligibilityPolicy, "Tracker policy blocks this release.")
	}
	if api.HasBlockingRuleFailures(assessment.RuleFailures) && !assessment.Choices.IgnoreRuleFailures {
		appendReason(api.TrackerEligibilityBlockingRule, "Blocking tracker rules must be resolved or explicitly authorized.")
	}
	duplicate := assessment.Duplicate
	if duplicate.HasDupes && !assessment.Choices.IgnoreDuplicate {
		appendReason(api.TrackerEligibilityDuplicate, "A blocking duplicate was found.")
	}
	status := strings.ToLower(strings.TrimSpace(duplicate.Status))
	if status == "failed" || strings.TrimSpace(duplicate.Error) != "" {
		appendReason(api.TrackerEligibilityAssessmentFailed, "Duplicate assessment failed.")
	} else if duplicate.Skipped && duplicate.SkipCode != dupeSkipCodeTrackerAuthNotReady {
		appendReason(api.TrackerEligibilityAssessmentSkipped, "Duplicate assessment was skipped.")
	}
	return reasons
}

func normalizeEligibilityTrackers(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		name := normalizeEligibilityTracker(value)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func normalizeEligibilityTracker(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func noEligibleTrackersError(operation api.OperationKind) error {
	return api.NewOperationError(api.OperationFailure{
		Code:      api.OperationFailureNoEligibleTrackers,
		Operation: operation,
		Message:   "No selected trackers are eligible.",
		Recovery:  api.OperationRecoverySelectTrackers,
	}, errNoEligibleTrackers)
}

var errNoEligibleTrackers = &trackerEligibilityError{}

type trackerEligibilityError struct{}

func (*trackerEligibilityError) Error() string { return "no eligible trackers" }
