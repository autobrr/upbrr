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
	ID        string                        `json:"id"`
	Path      string                        `json:"path"`
	Programme *ComparisonProgrammeSelection `json:"programme,omitempty"`
}

// ComparisonProgrammeSelection binds one server-discovered programme to its
// structural generation. Candidate IDs never name files or decoder arguments.
type ComparisonProgrammeSelection struct {
	Kind                 string `json:"kind"`
	DiscID               string `json:"discId"`
	CandidateID          string `json:"candidateId"`
	DiscoveryFingerprint string `json:"discoveryFingerprint"`
	Angle                int    `json:"angle,omitempty"`
}

// ComparisonProgrammeCandidate is a path-free authored programme choice.
type ComparisonProgrammeCandidate struct {
	Limitation      string  `json:"limitation,omitempty"`
	ID              string  `json:"id"`
	Kind            string  `json:"kind"`
	DiscID          string  `json:"discId"`
	DiscName        string  `json:"discName"`
	Label           string  `json:"label"`
	DurationSeconds float64 `json:"durationSeconds"`
	Rank            int     `json:"rank"`
	Supported       bool    `json:"supported"`
	Reason          string  `json:"reason,omitempty"`
}

// ComparisonSourceDiscovery contains facts only; it never applies a selection.
type ComparisonSourceDiscovery struct {
	SourceID    string                         `json:"sourceId"`
	Fingerprint string                         `json:"fingerprint"`
	Candidates  []ComparisonProgrammeCandidate `json:"candidates"`
}

// DiscoverReleaseWorkflowComparisonSourceRequest is a synchronized read of the
// current source context. It does not advance workflow or media revisions.
type DiscoverReleaseWorkflowComparisonSourceRequest struct {
	ReleaseWorkflowCommandContext
	Media    *MediaArtifactSetRef `json:"media,omitempty"`
	SourceID string               `json:"sourceId"`
	Path     string               `json:"path"`
}

// Validate checks authority and bounded local query input.
func (r DiscoverReleaseWorkflowComparisonSourceRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if r.Media != nil {
		if err := validateWorkflowMediaRef(*r.Media); err != nil {
			return err
		}
	}
	if strings.TrimSpace(r.SourceID) == "" || len(r.SourceID) > 128 || len(r.Path) > 32768 {
		return errors.New("comparison discovery requires a column and bounded path")
	}
	return nil
}

// ComparisonInstructions restores, edits, matches, captures or approves a comparison plan.
// Approval uses only ExpectedFingerprint and cannot apply pending source edits.
// An empty configure request removes comparison intent and its own artifacts.
type ComparisonInstructions struct {
	// CaptureAfterMatch defaults to true. False retains selected witnesses for
	// inspection/restart before the separate capture operation.
	CaptureAfterMatch   *bool                      `json:"captureAfterMatch,omitempty"`
	FrameSelections     []ComparisonFrameSelection `json:"frameSelections,omitempty"`
	Action              string                     `json:"action"`
	Plan                ComparisonPlan             `json:"plan"`
	VideoSources        []ComparisonVideoSelection `json:"videoSources,omitempty"`
	Attachments         []MediaAttachment          `json:"attachments,omitempty"`
	ExpectedFingerprint WorkflowFingerprint        `json:"expectedFingerprint,omitempty"`
}

// ComparisonFrameSelection applies an owner-checked observed preview to a cell.
type ComparisonFrameSelection struct {
	RowID     string           `json:"rowId"`
	SourceID  string           `json:"sourceId"`
	PreviewID PublicResourceID `json:"previewId"`
}

// Validate checks operation syntax; complete-row and content authority are
// checked against retained workflow resources before approval or upload.
func (i ComparisonInstructions) Validate() error {
	if i.Action != "restore" && i.Action != "approve" && !i.Plan.Imported {
		if err := i.Plan.Preferences.Validate(); err != nil {
			return err
		}
	}
	if len(i.FrameSelections) > 0 && i.Action != "configure" {
		return errors.New("comparison frame selections require configuration")
	}
	if len(i.FrameSelections) > 256 {
		return errors.New("too many comparison frame selections")
	}
	frames := map[string]bool{}
	for _, selection := range i.FrameSelections {
		key := selection.RowID + "\x00" + selection.SourceID
		if selection.RowID == "" || selection.SourceID == "" || selection.PreviewID == "" || frames[key] {
			return errors.New("comparison frame selections require unique row/column cells and preview IDs")
		}
		frames[key] = true
	}
	switch i.Action {
	case "configure", "match", "capture":
	case "restore":
		if len(i.VideoSources) != 0 || len(i.Attachments) != 0 || len(i.Plan.Sources) != 0 || len(i.Plan.Rows) != 0 {
			return errors.New("comparison restoration uses only retained workflow intent")
		}
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
		if strings.TrimSpace(source.ID) == "" || (strings.TrimSpace(source.Path) == "" && source.Programme == nil) || len(source.Path) > 32768 {
			return errors.New("comparison video requires a column ID and accessible path")
		}
		if seen[source.ID] {
			return errors.New("comparison video column IDs must be unique")
		}
		seen[source.ID] = true
		if p := source.Programme; p != nil {
			if (p.Kind != "dvd" && p.Kind != "bluray") || p.DiscID == "" || len(p.DiscID) > 128 || p.CandidateID == "" || len(p.CandidateID) > 128 ||
				p.DiscoveryFingerprint == "" ||
				len(p.DiscoveryFingerprint) > 128 ||
				p.Angle < 0 ||
				p.Angle > 1 {
				return errors.New("comparison programme requires a discovered default-angle title or playlist")
			}
		}
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
