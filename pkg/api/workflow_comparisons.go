// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"fmt"
	"strings"
)

// RequiredActionReviewComparisons stops continuation until captured rows have
// been explicitly reviewed; unattended execution cannot approve them implicitly.
const RequiredActionReviewComparisons RequiredActionKind = "review_comparisons"

// ComparisonVideoSelection supplies a server-local video for an explicit source
// column. Paths are command input only and never enter public comparison plans.
type ComparisonVideoSelection struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// ComparisonInstructions edits, matches, captures or approves a comparison plan.
// Approval uses only ExpectedFingerprint and cannot apply pending source edits.
// An empty configure request removes comparison intent and its own artifacts.
type ComparisonInstructions struct {
	Action              string                     `json:"action"`
	Plan                ComparisonPlan             `json:"plan"`
	VideoSources        []ComparisonVideoSelection `json:"videoSources,omitempty"`
	Attachments         []MediaAttachment          `json:"attachments,omitempty"`
	ExpectedFingerprint WorkflowFingerprint        `json:"expectedFingerprint,omitempty"`
}

// Validate checks operation syntax; complete-row and content authority are
// checked against retained workflow resources before approval or upload.
func (i ComparisonInstructions) Validate() error {
	switch i.Action {
	case "configure", "match", "capture":
	case "approve":
		if i.ExpectedFingerprint == "" {
			return errors.New("comparison approval requires the reviewed fingerprint")
		}
		if len(i.VideoSources) != 0 || len(i.Attachments) != 0 {
			return errors.New("comparison approval cannot change sources or images")
		}
	default:
		return fmt.Errorf("unsupported comparison action %q", i.Action)
	}
	seen := make(map[string]bool, len(i.VideoSources))
	for _, source := range i.VideoSources {
		if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.Path) == "" || len(source.Path) > 32768 {
			return errors.New("comparison video requires a column ID and accessible path")
		}
		if seen[source.ID] {
			return errors.New("comparison video column IDs must be unique")
		}
		seen[source.ID] = true
	}
	return nil
}

// EditReleaseWorkflowComparisonsRequest binds comparison work to the current
// workflow and, when present, its exact retained media revision.
type EditReleaseWorkflowComparisonsRequest struct {
	ReleaseWorkflowCommandContext
	Media        *MediaArtifactSetRef   `json:"media,omitempty"`
	Instructions ComparisonInstructions `json:"instructions"`
}

// Validate checks workflow authority and the requested comparison operation.
func (r EditReleaseWorkflowComparisonsRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if r.Media != nil {
		if err := validateWorkflowMediaRef(*r.Media); err != nil {
			return err
		}
	}
	return r.Instructions.Validate()
}
