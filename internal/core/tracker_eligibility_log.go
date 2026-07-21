// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

// logTrackerEligibility emits the canonical eligibility decision without
// reconstructing reasons from tracker payload or presentation state.
func logTrackerEligibility(
	ctx context.Context,
	fallback api.Logger,
	operation string,
	eligibility api.TrackerEligibility,
) {
	logger := logging.FromContext(ctx, fallback)
	reasonCounts := make(map[api.TrackerEligibilityReasonCode]int)
	blocked := 0
	for _, tracker := range eligibility.Trackers {
		if tracker.Eligible {
			continue
		}
		blocked++
		for _, reason := range tracker.Reasons {
			reasonCounts[reason.Code]++
		}
	}
	logger.Infof(
		"core: tracker eligibility operation=%s selected=%d eligible=%d excluded=%d reasons=%s",
		logging.SanitizeMessage(strings.TrimSpace(operation)),
		len(eligibility.Trackers),
		len(eligibility.EligibleTrackers),
		blocked,
		formatEligibilityReasonCounts(reasonCounts),
	)
	for _, tracker := range eligibility.Trackers {
		if tracker.Eligible {
			logger.Tracef(
				"core: tracker eligibility operation=%s tracker=%s eligible=true",
				logging.SanitizeMessage(strings.TrimSpace(operation)),
				logging.SanitizeMessage(tracker.Tracker),
			)
			continue
		}
		codes := eligibilityReasonCodes(tracker.Reasons)
		logger.Infof(
			"core: tracker eligibility operation=%s tracker=%s included=false reasons=%s",
			logging.SanitizeMessage(strings.TrimSpace(operation)),
			logging.SanitizeMessage(tracker.Tracker),
			strings.Join(codes, ","),
		)
		logger.Debugf(
			"core: tracker eligibility operation=%s tracker=%s details=%s",
			logging.SanitizeMessage(strings.TrimSpace(operation)),
			logging.SanitizeMessage(tracker.Tracker),
			eligibilityReasonDetails(tracker.Reasons),
		)
	}
}

func formatEligibilityReasonCounts(counts map[api.TrackerEligibilityReasonCode]int) string {
	if len(counts) == 0 {
		return "none"
	}
	codes := make([]string, 0, len(counts))
	for code := range counts {
		codes = append(codes, string(code))
	}
	slices.Sort(codes)
	parts := make([]string, 0, len(codes))
	for _, code := range codes {
		parts = append(parts, fmt.Sprintf("%s:%d", code, counts[api.TrackerEligibilityReasonCode(code)]))
	}
	return strings.Join(parts, ",")
}

func eligibilityReasonCodes(reasons []api.TrackerEligibilityReason) []string {
	codes := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		codes = append(codes, string(reason.Code))
	}
	slices.Sort(codes)
	return codes
}

func eligibilityReasonDetails(reasons []api.TrackerEligibilityReason) string {
	details := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		details = append(details, string(reason.Code)+":"+logging.SanitizeMessage(reason.Message))
	}
	slices.Sort(details)
	return strings.Join(details, ",")
}
