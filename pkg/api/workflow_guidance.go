// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "context"

type trackerGuidanceHistoryKey struct{}

// WithTrackerGuidanceHistory supplies immutable, secret-free preflight projections
// that presented advice in the current upload attempt. An empty history explicitly
// excludes advice retained from earlier attempts.
func WithTrackerGuidanceHistory(ctx context.Context, history []TrackerReleaseProjection) context.Context {
	return context.WithValue(ctx, trackerGuidanceHistoryKey{}, history)
}

// TrackerGuidanceWasPresented reports whether this attempt already presented the
// same tracker advice. Only callers without coordinated history use the fallback
// projection; a known empty history takes precedence over cached advice.
func TrackerGuidanceWasPresented(ctx context.Context, tracker TrackerID, code, message string, fallback *TrackerReleaseProjection) bool {
	history, known := ctx.Value(trackerGuidanceHistoryKey{}).([]TrackerReleaseProjection)
	if !known && fallback != nil {
		history = []TrackerReleaseProjection{*fallback}
	}
	for _, projection := range history {
		if projection.TrackerID != tracker {
			continue
		}
		for _, decision := range projection.PolicyDecisions {
			if decision.Code == code && decision.Message == message {
				return true
			}
		}
	}
	return false
}
