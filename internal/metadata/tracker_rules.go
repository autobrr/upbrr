// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"
	"strings"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func (s *Service) applyTrackerRules(ctx context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	if s == nil {
		return meta, nil
	}

	resolved := trackers.ResolveTrackersWithDefaults(s.cfg, meta.Trackers, meta.TrackersRemove, s.logger)
	if len(resolved) == 0 {
		return meta, nil
	}

	ruleFailures := cloneTrackerRuleFailures(meta.TrackerRuleFailures)
	for _, tracker := range resolved {
		select {
		case <-ctx.Done():
			return api.PreparedMetadata{}, ctx.Err()
		default:
		}

		name := strings.ToUpper(strings.TrimSpace(tracker))
		failures := trackers.EvaluateRules(ctx, tracker, meta, s.logger)
		if failures == nil {
			if combined := ruleFailures[name]; len(combined) > 0 && s.repo != nil {
				if err := s.persistRuleFailures(ctx, meta.SourcePath, tracker, combined); err != nil {
					return api.PreparedMetadata{}, err
				}
			}
			continue
		}
		combined := mergeRuleFailures(ruleFailures[name], failures)
		if len(failures) > 0 {
			ruleFailures[name] = combined
			if s.logger != nil {
				for _, failure := range failures {
					s.logger.Warnf("metadata: tracker rule failed tracker=%s rule=%s reason=%s", name, failure.Rule, failure.Reason)
				}
			}
		} else {
			if len(combined) > 0 {
				ruleFailures[name] = combined
			} else {
				delete(ruleFailures, name)
			}
			if s.logger != nil {
				s.logger.Debugf("metadata: tracker rules ok for %s", tracker)
			}
		}

		if len(failures) == 0 && len(combined) > 0 && s.logger != nil {
			s.logger.Debugf("metadata: tracker retained metadata rule failures for %s", tracker)
		}

		if len(failures) == 0 && len(combined) == 0 && s.logger != nil {
			s.logger.Debugf("metadata: tracker rules ok for %s", tracker)
		}

		if s.repo != nil {
			if err := s.persistRuleFailures(ctx, meta.SourcePath, tracker, combined); err != nil {
				return api.PreparedMetadata{}, err
			}
		}
	}

	if len(ruleFailures) > 0 {
		meta.TrackerRuleFailures = ruleFailures
	} else {
		meta.TrackerRuleFailures = nil
	}
	return meta, nil
}

func cloneTrackerRuleFailures(input map[string][]api.RuleFailure) map[string][]api.RuleFailure {
	if len(input) == 0 {
		return make(map[string][]api.RuleFailure)
	}
	cloned := make(map[string][]api.RuleFailure, len(input))
	for tracker, failures := range input {
		cloned[tracker] = append([]api.RuleFailure{}, failures...)
	}
	return cloned
}

func mergeRuleFailures(existing []api.RuleFailure, current []api.RuleFailure) []api.RuleFailure {
	merged := append([]api.RuleFailure{}, existing...)
	for _, failure := range current {
		duplicate := false
		for _, prior := range merged {
			if strings.EqualFold(strings.TrimSpace(prior.Rule), strings.TrimSpace(failure.Rule)) && strings.EqualFold(strings.TrimSpace(prior.Reason), strings.TrimSpace(failure.Reason)) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		merged = append(merged, failure)
	}
	return merged
}

func (s *Service) persistRuleFailures(ctx context.Context, sourcePath string, tracker string, failures []api.RuleFailure) error {
	if s.repo == nil {
		return nil
	}
	trimmedPath := strings.TrimSpace(sourcePath)
	trimmedTracker := strings.TrimSpace(tracker)
	if trimmedPath == "" || trimmedTracker == "" {
		return fmt.Errorf("metadata: tracker rules: %w", internalerrors.ErrInvalidInput)
	}

	records := make([]api.TrackerRuleFailure, 0, len(failures))
	for _, failure := range failures {
		records = append(records, api.TrackerRuleFailure{
			SourcePath: trimmedPath,
			Tracker:    strings.ToUpper(trimmedTracker),
			Rule:       strings.TrimSpace(failure.Rule),
			Reason:     strings.TrimSpace(failure.Reason),
			CreatedAt:  time.Now().UTC(),
		})
	}

	if err := s.repo.SaveTrackerRuleFailures(ctx, trimmedPath, trimmedTracker, records); err != nil {
		return fmt.Errorf("metadata: tracker rule persist: %w", err)
	}
	return nil
}
