// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import "context"

// TrackerDecisionMode is trusted persisted policy for downstream tracker authority.
type TrackerDecisionMode string

const (
	// TrackerDecisionModePostDupeGate requires explicit tracker approval after duplicate checking.
	TrackerDecisionModePostDupeGate TrackerDecisionMode = "post_dupe_gate"
	// TrackerDecisionModeWebUIControls uses the app's existing stage-specific tracker controls.
	TrackerDecisionModeWebUIControls TrackerDecisionMode = "webui_controls"
)

type trackerDecisionModeContextKey struct{}

// WithTrackerDecisionMode supplies trusted workflow-creation policy to an adapter.
// Existing workflows always use their persisted mode.
func WithTrackerDecisionMode(ctx context.Context, mode TrackerDecisionMode) context.Context {
	return context.WithValue(ctx, trackerDecisionModeContextKey{}, normalizeTrackerDecisionMode(mode))
}

func trackerDecisionModeFromContext(ctx context.Context, fallback TrackerDecisionMode) TrackerDecisionMode {
	if ctx != nil {
		if mode, ok := ctx.Value(trackerDecisionModeContextKey{}).(TrackerDecisionMode); ok {
			return normalizeTrackerDecisionMode(mode)
		}
	}
	return normalizeTrackerDecisionMode(fallback)
}

func normalizeTrackerDecisionMode(mode TrackerDecisionMode) TrackerDecisionMode {
	switch mode {
	case TrackerDecisionModePostDupeGate:
		return TrackerDecisionModePostDupeGate
	case TrackerDecisionModeWebUIControls, "":
		return TrackerDecisionModeWebUIControls
	}
	return TrackerDecisionModePostDupeGate
}
