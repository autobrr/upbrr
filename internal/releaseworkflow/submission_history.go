// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"

	"github.com/autobrr/upbrr/pkg/api"
)

// SubmissionHistoryFilter excludes only strong-bound confirmed submissions.
type SubmissionHistoryFilter interface {
	FilterConfirmedSubmissions(context.Context, api.UploadSubject, []api.TrackerID) ([]api.TrackerID, []api.SubmissionExclusion, error)
}

// WithSubmissionHistoryFilter installs the confirmed-submission exclusion boundary.
// Applying the option rejects a nil filter.
func WithSubmissionHistoryFilter(filter SubmissionHistoryFilter) Option {
	return func(module *Module) error {
		if filter == nil {
			return errors.New("release workflow: submission history filter is required")
		}
		module.submissionHistory = filter
		return nil
	}
}

func withoutConfirmedSubmissions(trackers []api.TrackerID, exclusions []api.SubmissionExclusion) []api.TrackerID {
	remaining := make([]api.TrackerID, 0, len(trackers))
	for _, tracker := range trackers {
		excluded := false
		for _, prior := range exclusions {
			if prior.TrackerID == tracker {
				excluded = true
				break
			}
		}
		if !excluded {
			remaining = append(remaining, tracker)
		}
	}
	return remaining
}
