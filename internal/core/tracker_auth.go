// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackerauth"
	"github.com/autobrr/upbrr/pkg/api"
)

// dupeSkipCodeTrackerAuthNotReady identifies auth-preflight skips for clients.
const dupeSkipCodeTrackerAuthNotReady = "tracker_auth_not_ready"

// checkGUIDupesWithAuth validates managed tracker auth before GUI and
// embedded-web duplicate searches. Trackers needing user action are returned as
// skipped results while ready and unmanaged trackers continue normally.
func (c *Core) checkGUIDupesWithAuth(ctx context.Context, mode api.Mode, meta api.PreparedMetadata, trackerIDs []string) (api.DupeCheckSummary, error) {
	if mode != api.ModeGUI {
		summary, err := c.services.Dupes.Check(ctx, meta, trackerIDs)
		if err != nil {
			return api.DupeCheckSummary{}, fmt.Errorf("dupe service: %w", err)
		}
		return summary, nil
	}

	ready, blocked, err := c.preflightGUITrackerAuth(ctx, meta, trackerIDs)
	if err != nil {
		return api.DupeCheckSummary{}, err
	}

	summary := api.DupeCheckSummary{SourcePath: meta.SourcePath}
	if len(ready) > 0 || len(blocked) == 0 {
		summary, err = c.services.Dupes.Check(ctx, meta, ready)
		if err != nil {
			return api.DupeCheckSummary{}, fmt.Errorf("dupe service: %w", err)
		}
	}
	summary.Results = append(summary.Results, blocked...)
	sort.Slice(summary.Results, func(i, j int) bool {
		return summary.Results[i].Tracker < summary.Results[j].Tracker
	})
	return summary, nil
}

// preflightGUITrackerAuth partitions trackers into auth-ready IDs and blocked
// dupe results. Unmanaged trackers and trackers already destined for a rule
// failure remain ready so the dupe service can produce their normal results.
func (c *Core) preflightGUITrackerAuth(ctx context.Context, meta api.PreparedMetadata, trackerIDs []string) ([]string, []api.DupeCheckResult, error) {
	if c.services.TrackerAuth == nil {
		return nil, nil, errors.New("core: tracker auth service not configured")
	}

	capabilities, err := c.services.TrackerAuth.Capabilities(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("core: tracker auth capabilities: %w", err)
	}
	capabilityByTracker := make(map[string]api.TrackerAuthCapability, len(capabilities))
	for _, capability := range capabilities {
		name := strings.ToUpper(strings.TrimSpace(capability.TrackerID))
		if name != "" {
			capabilityByTracker[name] = capability
		}
	}

	readySet := make(map[string]struct{}, len(trackerIDs))
	managed := make([]string, 0, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		name := strings.ToUpper(strings.TrimSpace(trackerID))
		if name == "" {
			continue
		}
		capability, ok := capabilityByTracker[name]
		if !ok || !trackerauth.IsManagedCapability(capability) {
			readySet[name] = struct{}{}
			continue
		}
		if !meta.IgnoreTrackerRuleFailures && len(ruleFailuresForTracker(meta.TrackerRuleFailures, name)) > 0 {
			readySet[name] = struct{}{}
			continue
		}
		managed = append(managed, name)
	}
	if len(managed) == 0 {
		return orderedReadyTrackers(trackerIDs, readySet), nil, nil
	}

	c.logger.Infof("core: tracker auth preflight start trackers=%d", len(managed))
	for _, trackerID := range managed {
		api.EmitDupeProgress(ctx, api.DupeProgressUpdate{
			SourcePath: meta.SourcePath,
			Tracker:    trackerID,
			Status:     "running",
			Message:    "checking tracker auth",
			Total:      len(trackerIDs),
		})
	}
	statuses, err := c.services.TrackerAuth.ValidateMany(ctx, managed)
	if err != nil {
		return nil, nil, fmt.Errorf("core: tracker auth validation: %w", err)
	}
	if len(statuses) != len(managed) {
		return nil, nil, fmt.Errorf("core: tracker auth validation returned %d statuses for %d trackers", len(statuses), len(managed))
	}

	blocked := make([]api.DupeCheckResult, 0, len(managed))
	for i, trackerID := range managed {
		status := statuses[i]
		if trackerauth.IsReadyStatus(status) {
			readySet[trackerID] = struct{}{}
			api.EmitDupeProgress(ctx, api.DupeProgressUpdate{
				SourcePath: meta.SourcePath,
				Tracker:    trackerID,
				Status:     "queued",
				Message:    "tracker auth ready; queued",
				Total:      len(trackerIDs),
			})
			continue
		}

		reason := guiTrackerAuthSkipReason(status)
		result := api.DupeCheckResult{
			Tracker:    trackerID,
			Skipped:    true,
			SkipReason: reason,
			SkipCode:   dupeSkipCodeTrackerAuthNotReady,
			Status:     "skipped",
			CheckedAt:  time.Now().UTC(),
		}
		blocked = append(blocked, result)
		c.logger.Warnf("core: tracker auth preflight blocked tracker=%s state=%s reason=%s", trackerID, redaction.RedactValue(status.State, nil), reason)
		api.EmitDupeProgress(ctx, api.DupeProgressUpdate{
			SourcePath: meta.SourcePath,
			Tracker:    trackerID,
			Status:     "skipped",
			Message:    reason,
			Total:      len(trackerIDs),
			Result:     result,
		})
	}
	ready := orderedReadyTrackers(trackerIDs, readySet)
	c.logger.Infof("core: tracker auth preflight complete ready=%d skipped=%d", len(ready), len(blocked))
	return ready, blocked, nil
}

// orderedReadyTrackers filters and normalizes tracker IDs without changing
// their input order.
func orderedReadyTrackers(trackerIDs []string, readySet map[string]struct{}) []string {
	ready := make([]string, 0, len(readySet))
	for _, trackerID := range trackerIDs {
		name := strings.ToUpper(strings.TrimSpace(trackerID))
		if _, ok := readySet[name]; ok {
			ready = append(ready, name)
		}
	}
	return ready
}

const guiTrackerAuthRetryAction = "configure tracker auth and retry"

// guiTrackerAuthSkipReason builds a redacted user-facing reason, preferring the
// stable status message before error detail, 2FA state, and raw state. Every
// blocker includes the remediation action, even when diagnostics are present.
func guiTrackerAuthSkipReason(status api.TrackerAuthStatus) string {
	message := strings.TrimSpace(redaction.RedactValue(status.Message, nil))
	detail := strings.TrimSpace(redaction.RedactValue(status.LastError, nil))
	reason := "tracker auth not ready"
	if message != "" && detail != "" && !strings.EqualFold(message, detail) {
		reason += ": " + message + ": " + detail
	} else if message != "" {
		reason += ": " + message
	} else if detail != "" {
		reason += ": " + detail
	} else if status.Needs2FA {
		reason += ": manual 2FA required"
	} else if state := strings.TrimSpace(redaction.RedactValue(status.State, nil)); state != "" {
		reason += ": " + state
	}
	if strings.Contains(strings.ToLower(reason), guiTrackerAuthRetryAction) {
		return reason
	}
	return reason + "; " + guiTrackerAuthRetryAction
}
