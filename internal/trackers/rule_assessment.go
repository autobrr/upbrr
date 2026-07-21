// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// RuleAssessment is the complete live-upload decision for one tracker's rule evidence.
type RuleAssessment struct {
	Failures   []api.RuleFailure
	Decisions  []api.RuleDecision
	Advisory   []api.RuleFailure
	Waivable   []api.RuleFailure
	Strict     []api.RuleFailure
	Authorized []api.RuleFailure
	Eligible   bool
}

// AssessRuleFailures applies exact rule keys while retaining all evidence.
func AssessRuleFailures(tracker string, failures []api.RuleFailure, authorizedRules []string) (RuleAssessment, error) {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	known := make(map[string]api.RuleFailure, len(failures))
	assessment := RuleAssessment{
		Failures:  append([]api.RuleFailure(nil), failures...),
		Decisions: make([]api.RuleDecision, 0, len(failures)),
		Eligible:  true,
	}
	for idx, failure := range assessment.Failures {
		key := strings.TrimSpace(failure.Rule)
		if key == "" {
			return RuleAssessment{}, fmt.Errorf("tracker %s contains a rule failure without a key", name)
		}
		failure.Rule = key
		failure.Reason = strings.TrimSpace(failure.Reason)
		failure.Disposition = api.NormalizeRuleDisposition(failure.Disposition)
		assessment.Failures[idx] = failure
		known[key] = failure
	}
	authorized := make(map[string]struct{}, len(authorizedRules))
	for _, raw := range authorizedRules {
		key := strings.TrimSpace(raw)
		failure, ok := known[key]
		if !ok {
			return RuleAssessment{}, fmt.Errorf("tracker %s rule authorization %q does not match a current failure", name, key)
		}
		if !api.IsWaivableRuleFailure(failure) {
			return RuleAssessment{}, fmt.Errorf("tracker %s rule authorization %q is not waivable", name, key)
		}
		authorized[key] = struct{}{}
	}
	for _, failure := range assessment.Failures {
		failure.Disposition = api.NormalizeRuleDisposition(failure.Disposition)
		_, isAuthorized := authorized[failure.Rule]
		decision := api.RuleDecision{
			Rule:        failure.Rule,
			Reason:      failure.Reason,
			Disposition: failure.Disposition,
			Authorized:  isAuthorized,
		}
		assessment.Decisions = append(assessment.Decisions, decision)
		switch failure.Disposition {
		case api.RuleDispositionAdvisory:
			assessment.Advisory = append(assessment.Advisory, failure)
		case api.RuleDispositionWaivable:
			assessment.Waivable = append(assessment.Waivable, failure)
			if isAuthorized {
				assessment.Authorized = append(assessment.Authorized, failure)
			} else {
				assessment.Eligible = false
			}
		case api.RuleDispositionStrict:
			assessment.Strict = append(assessment.Strict, failure)
			assessment.Eligible = false
		}
	}
	return assessment, nil
}

// AuthorizedRulesForTracker returns exact keys supplied for tracker and rejects
// duplicate tracker authorization records.
func AuthorizedRulesForTracker(authorizations []api.RuleAuthorization, tracker string) ([]string, error) {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	var rules []string
	found := false
	for _, authorization := range authorizations {
		if strings.ToUpper(strings.TrimSpace(authorization.Tracker)) != name {
			continue
		}
		if found {
			return nil, fmt.Errorf("tracker %s has duplicate rule authorization records", name)
		}
		found = true
		rules = append([]string(nil), authorization.Rules...)
	}
	return rules, nil
}

// ValidateRuleAuthorizations rejects unknown trackers, unknown keys, and any
// attempt to authorize advisory or strict failures.
func ValidateRuleAuthorizations(
	selected []string,
	failures map[string][]api.RuleFailure,
	authorizations []api.RuleAuthorization,
) error {
	selectedSet := make(map[string]struct{}, len(selected))
	for _, tracker := range selected {
		if name := strings.ToUpper(strings.TrimSpace(tracker)); name != "" {
			selectedSet[name] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(authorizations))
	for _, authorization := range authorizations {
		name := strings.ToUpper(strings.TrimSpace(authorization.Tracker))
		if _, ok := selectedSet[name]; !ok {
			return fmt.Errorf("rule authorization references unselected tracker %s", name)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("tracker %s has duplicate rule authorization records", name)
		}
		seen[name] = struct{}{}
		if _, err := AssessRuleFailures(name, failures[name], authorization.Rules); err != nil {
			return err
		}
	}
	return nil
}
