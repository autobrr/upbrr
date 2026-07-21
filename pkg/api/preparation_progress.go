// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"strings"
)

// PreparationProgressPhase identifies one stable stage in canonical release
// preparation. It is presentation telemetry, not prepared-release evidence.
type PreparationProgressPhase string

const (
	// PreparationPhaseSourceInspection validates and classifies the requested source.
	PreparationPhaseSourceInspection PreparationProgressPhase = "source_inspection"
	// PreparationPhasePreparedCache checks whether an existing generation can be reused.
	PreparationPhasePreparedCache PreparationProgressPhase = "prepared_cache"
	// PreparationPhaseSourceEvidence collects filesystem and media-source facts.
	PreparationPhaseSourceEvidence PreparationProgressPhase = "source_evidence"
	// PreparationPhaseBDInfo analyzes selected Blu-ray playlists.
	PreparationPhaseBDInfo PreparationProgressPhase = "bdinfo"
	// PreparationPhaseClientDiscovery searches configured torrent clients.
	PreparationPhaseClientDiscovery PreparationProgressPhase = "client_discovery"
	// PreparationPhaseTrackerEvidence collects source metadata from trackers.
	PreparationPhaseTrackerEvidence PreparationProgressPhase = "tracker_evidence"
	// PreparationPhaseMediaInfoIdentity reads identifiers exposed by MediaInfo.
	PreparationPhaseMediaInfoIdentity PreparationProgressPhase = "mediainfo_identity"
	// PreparationPhaseArrIdentity queries configured Sonarr and Radarr services.
	PreparationPhaseArrIdentity PreparationProgressPhase = "arr_identity"
	// PreparationPhaseExternalIdentity resolves the canonical provider identity.
	PreparationPhaseExternalIdentity PreparationProgressPhase = "external_identity"
	// PreparationPhaseMediaFacts derives normalized release media facts.
	PreparationPhaseMediaFacts PreparationProgressPhase = "media_facts"
	// PreparationPhaseCanonicalIdentity finalizes canonical release identity.
	PreparationPhaseCanonicalIdentity PreparationProgressPhase = "canonical_identity"
	// PreparationPhaseGenerationCommit persists the prepared generation.
	PreparationPhaseGenerationCommit PreparationProgressPhase = "generation_commit"
	// PreparationPhasePreviewProjection builds the transport metadata preview.
	PreparationPhasePreviewProjection PreparationProgressPhase = "preview_projection"
	// PreparationPhaseResetCleanup removes reusable metadata before a forced reset.
	PreparationPhaseResetCleanup PreparationProgressPhase = "reset_cleanup"
	// PreparationPhaseCandidateSelection applies a selected Blu-ray candidate.
	PreparationPhaseCandidateSelection PreparationProgressPhase = "candidate_selection"
)

// PreparationProgressStatus describes the latest advisory state of one stage.
type PreparationProgressStatus string

const (
	// PreparationProgressRunning marks a stage that has started but is not terminal.
	PreparationProgressRunning PreparationProgressStatus = "running"
	// PreparationProgressCompleted marks a successfully completed stage.
	PreparationProgressCompleted PreparationProgressStatus = "completed"
	// PreparationProgressSkipped marks an intentional cache or policy bypass.
	PreparationProgressSkipped PreparationProgressStatus = "skipped"
	// PreparationProgressFailed marks a stage whose owning operation failed.
	PreparationProgressFailed PreparationProgressStatus = "failed"
)

// PreparationProgressUpdate is one frontend-safe advisory preparation event.
// CorrelationID and Timestamp are injected by the WebUI transport boundary.
type PreparationProgressUpdate struct {
	// CorrelationID binds the update to one frontend preparation command.
	CorrelationID string `json:"correlationID"`
	// Phase identifies the stable preparation stage.
	Phase PreparationProgressPhase `json:"phase"`
	// Order is the canonical presentation order, not a completion percentage.
	Order int `json:"order"`
	// Label is the user-facing stage name.
	Label string `json:"label"`
	// Message describes the current stage transition.
	Message string `json:"message"`
	// Status is the latest advisory state for Phase.
	Status PreparationProgressStatus `json:"status"`
	// Timestamp is an RFC3339 timestamp injected by the WebUI boundary.
	Timestamp string `json:"timestamp"`
}

// PreparationProgressReporter receives advisory preparation progress.
type PreparationProgressReporter func(update PreparationProgressUpdate)

// preparationProgressReporterKey prevents collisions with caller context values.
type preparationProgressReporterKey struct{}

// WithPreparationProgressReporter attaches an optional reporter to ctx.
func WithPreparationProgressReporter(ctx context.Context, reporter PreparationProgressReporter) context.Context {
	if ctx == nil || reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, preparationProgressReporterKey{}, reporter)
}

// EmitPreparationProgress reports one update when a reporter is installed.
func EmitPreparationProgress(ctx context.Context, update PreparationProgressUpdate) {
	if ctx == nil {
		return
	}
	reporter, _ := ctx.Value(preparationProgressReporterKey{}).(PreparationProgressReporter)
	if reporter != nil {
		reporter(update)
	}
}

// NewPreparationProgressUpdate supplies the canonical order and label for a
// phase. Unknown phases remain renderable with a normalized fallback label.
func NewPreparationProgressUpdate(
	phase PreparationProgressPhase,
	status PreparationProgressStatus,
	message string,
) PreparationProgressUpdate {
	order, label := preparationProgressPresentation(phase)
	return PreparationProgressUpdate{
		Phase:   phase,
		Order:   order,
		Label:   label,
		Message: strings.TrimSpace(message),
		Status:  status,
	}
}

// BeginPreparationProgress emits a running event and returns a completion
// callback. The callback never replaces or modifies the operation error.
func BeginPreparationProgress(
	ctx context.Context,
	phase PreparationProgressPhase,
	message string,
) func(error) {
	EmitPreparationProgress(ctx, NewPreparationProgressUpdate(phase, PreparationProgressRunning, message))
	return func(err error) {
		if err != nil {
			EmitPreparationProgress(ctx, NewPreparationProgressUpdate(phase, PreparationProgressFailed, "Stage failed."))
			return
		}
		EmitPreparationProgress(ctx, NewPreparationProgressUpdate(phase, PreparationProgressCompleted, "Stage complete."))
	}
}

// SkipPreparationProgress records an intentional cache or policy bypass.
func SkipPreparationProgress(ctx context.Context, phase PreparationProgressPhase, message string) {
	EmitPreparationProgress(ctx, NewPreparationProgressUpdate(phase, PreparationProgressSkipped, message))
}

// preparationProgressPresentation returns stable sorting and display metadata
// while keeping unknown phases visible after mixed-version deployments.
func preparationProgressPresentation(phase PreparationProgressPhase) (int, string) {
	switch phase {
	case PreparationPhaseSourceInspection:
		return 100, "Inspect source"
	case PreparationPhasePreparedCache:
		return 200, "Check prepared cache"
	case PreparationPhaseResetCleanup:
		return 250, "Reset previous metadata"
	case PreparationPhaseCandidateSelection:
		return 275, "Select Blu-ray candidate"
	case PreparationPhaseSourceEvidence:
		return 300, "Collect source evidence"
	case PreparationPhaseBDInfo:
		return 350, "Analyze Blu-ray playlists"
	case PreparationPhaseClientDiscovery:
		return 400, "Search torrent clients"
	case PreparationPhaseTrackerEvidence:
		return 500, "Collect tracker evidence"
	case PreparationPhaseMediaInfoIdentity:
		return 600, "Read MediaInfo identity"
	case PreparationPhaseArrIdentity:
		return 700, "Query Sonarr and Radarr"
	case PreparationPhaseExternalIdentity:
		return 800, "Resolve external identity"
	case PreparationPhaseMediaFacts:
		return 900, "Derive media facts"
	case PreparationPhaseCanonicalIdentity:
		return 1000, "Resolve canonical identity"
	case PreparationPhaseGenerationCommit:
		return 1100, "Commit prepared generation"
	case PreparationPhasePreviewProjection:
		return 1200, "Build metadata preview"
	}
	label := strings.TrimSpace(strings.ReplaceAll(string(phase), "_", " "))
	if label == "" {
		label = "Preparation"
	}
	return 10000, label
}
