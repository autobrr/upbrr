// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"slices"
	"strings"
)

// ReusableDescription is the safe, source-scoped description output that can
// be materialized into a later workflow. It deliberately excludes runtime
// configuration, credentials, and generation instructions.
type ReusableDescription struct {
	CompatibilityFingerprint WorkflowFingerprint        `json:"compatibilityFingerprint"`
	Descriptions             []RenderedDescription      `json:"descriptions"`
	TrackerResults           []DescriptionTrackerResult `json:"trackerResults,omitempty"`
	Overrides                []DescriptionOverrideInput `json:"overrides,omitempty"`
}

// ReusableDescriptionRecord binds safe reusable description output to its
// canonical source. It is an internal persistence handoff, never workflow
// transport state.
type ReusableDescriptionRecord struct {
	SourcePath  string
	Description ReusableDescription
}

// Valid reports whether the record names a source and contains reusable safe
// description output. This checks structure, not compatibility with a later workflow.
func (r ReusableDescriptionRecord) Valid() bool {
	return strings.TrimSpace(r.SourcePath) != "" && r.Description.Valid()
}

// Clone returns a detached reusable-description record.
func (r ReusableDescriptionRecord) Clone() ReusableDescriptionRecord {
	cloned := r
	cloned.Description.Descriptions = slices.Clone(r.Description.Descriptions)
	for index := range cloned.Description.Descriptions {
		cloned.Description.Descriptions[index].TrackerIDs = slices.Clone(r.Description.Descriptions[index].TrackerIDs)
	}
	cloned.Description.TrackerResults = slices.Clone(r.Description.TrackerResults)
	cloned.Description.Overrides = slices.Clone(r.Description.Overrides)
	return cloned
}

// Valid reports whether the record contains reusable public output and
// explicit description text overrides. It validates fingerprints, group/tracker uniqueness,
// and terminal tracker outcomes; it does not sanitize text or recompute content fingerprints.
func (r ReusableDescription) Valid() bool {
	if err := validateWorkflowFingerprint(r.CompatibilityFingerprint); err != nil || len(r.Descriptions) == 0 {
		return false
	}
	seenGroups := make(map[string]struct{}, len(r.Descriptions))
	for _, description := range r.Descriptions {
		groupKey := strings.TrimSpace(description.GroupKey)
		if groupKey == "" {
			return false
		}
		if _, exists := seenGroups[groupKey]; exists {
			return false
		}
		seenGroups[groupKey] = struct{}{}
		seenTrackers := make(map[TrackerID]struct{}, len(description.TrackerIDs))
		for _, trackerID := range description.TrackerIDs {
			if strings.TrimSpace(string(trackerID)) == "" {
				return false
			}
			if _, exists := seenTrackers[trackerID]; exists {
				return false
			}
			seenTrackers[trackerID] = struct{}{}
		}
		if err := validateWorkflowFingerprint(description.ContentFingerprint); err != nil {
			return false
		}
	}
	seenResults := make(map[TrackerID]struct{}, len(r.TrackerResults))
	for _, result := range r.TrackerResults {
		trackerID := normalizeTrackerID(result.TrackerID)
		if trackerID == "" {
			return false
		}
		if _, exists := seenResults[trackerID]; exists {
			return false
		}
		seenResults[trackerID] = struct{}{}
		switch result.Status {
		case StageStatusCompleted:
		case StageStatusSkipped, StageStatusFailed:
			if strings.TrimSpace(result.Message) == "" {
				return false
			}
		case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale, StageStatusPartial,
			StageStatusRunning, StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled, StageStatusUnavailable:
			return false
		default:
			return false
		}
	}
	return (DescriptionInstructions{Overrides: r.Overrides}).Validate() == nil
}

// DescriptionReuseRepository retrieves reusable public descriptions and
// explicit override text. Workflow-state persistence owns their atomic save.
type DescriptionReuseRepository interface {
	LoadReusableDescription(context.Context, string) (ReusableDescription, bool, error)
}
