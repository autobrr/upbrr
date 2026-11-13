// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"encoding/hex"
	"fmt"
	"html"
	"math"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// Validate returns corrections needed before comparison review or image hosting.
// Explicitly excluded rows need not be complete. It never trusts diagnostics or
// an existing approval, and the hosting owner must separately verify image bytes.
func Validate(plan api.ComparisonPlan) []api.ComparisonProblem {
	var problems []api.ComparisonProblem
	add := func(rowID, sourceID, message string) {
		problems = append(problems, api.ComparisonProblem{
			RowID:    rowID,
			SourceID: sourceID,
			Message:  message,
		})
	}
	sources := make(map[string]api.ComparisonSource, len(plan.Sources))
	for _, source := range plan.Sources {
		if _, exists := sources[source.ID]; exists || strings.TrimSpace(source.ID) == "" {
			add("", source.ID, "Each source column needs a unique nonempty identity.")
		}
		sources[source.ID] = source
		if strings.TrimSpace(source.Label) == "" {
			add("", source.ID, "Choose a label for this source column.")
		}
		if strings.ContainsAny(html.UnescapeString(source.Label), ",[]\r\n") {
			add("", source.ID, "Use a single-line source label without commas or BBCode brackets.")
		}
		if source.Overlay && (len(source.Label) > 80 || strings.IndexFunc(source.Label, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune(" ._-", r)
		}) >= 0) {
			add("", source.ID, "Overlay labels support up to 80 ASCII letters, digits, spaces and ._-; change the label or disable its overlay.")
		}
		if slices.ContainsFunc(source.AllowedFrameTypes, func(value string) bool { return value != "I" && value != "P" && value != "B" }) {
			add("", source.ID, "Allowed picture types must contain only I, P, or B.")
		}
		if source.Imported && (source.DurationSeconds != 0 || source.FrameRate != 0 || source.Width != 0 || source.Height != 0) {
			add("", source.ID, "Imported columns cannot claim video timing or dimensions.")
		}
		if plan.Imported && !source.Imported {
			add("", source.ID, "Imported comparison columns must retain their external provenance.")
		}
	}
	if len(sources) < 2 {
		add("", "", "Comparisons require at least two source columns.")
	}
	if _, exists := sources[plan.MainSourceID]; !exists || plan.MainSourceID == "" {
		add("", "", "Select the source column belonging to the main release input.")
	}
	rows := make(map[string]bool, len(plan.Rows))
	activeRows := 0
	for _, row := range plan.Rows {
		if rows[row.ID] || strings.TrimSpace(row.ID) == "" {
			add(row.ID, "", "Each comparison row needs a unique nonempty identity.")
		}
		rows[row.ID] = true
		if row.Excluded {
			continue
		}
		activeRows++
		cells := make(map[string]bool, len(row.Cells))
		for _, cell := range row.Cells {
			source, exists := sources[cell.SourceID]
			if !exists {
				add(row.ID, cell.SourceID, "Map this image to an existing source column.")
			}
			if cells[cell.SourceID] {
				add(row.ID, cell.SourceID, "A row can contain only one image from each source column.")
			}
			cells[cell.SourceID] = true
			decoded, err := hex.DecodeString(cell.ContentSHA256)
			if strings.TrimSpace(string(cell.ArtifactID)) == "" || err != nil || len(decoded) != 32 {
				add(row.ID, cell.SourceID, "Retain a readable image with its verified content identity for this cell.")
			}
			if (plan.Imported || source.Imported) &&
				(cell.TimestampSeconds != nil || cell.FrameNumber != nil || cell.PictureType != "" || cell.Confidence != nil ||
					cell.ObservedPTS != nil || cell.TimeBaseNumerator != 0 || cell.TimeBaseDenominator != 0) {
				add(row.ID, cell.SourceID, "Imported image timing, picture type and matching confidence must remain unknown.")
			}
		}
		for _, source := range plan.Sources {
			if !cells[source.ID] {
				add(row.ID, source.ID, "Supply the missing source image or explicitly exclude this row.")
			}
		}
	}
	if activeRows == 0 {
		add("", "", "Retain at least one complete comparison row.")
	}
	if plan.Preferences.Count < 0 || plan.Preferences.MotionPercent < 0 || plan.Preferences.MotionPercent > 100 ||
		plan.Preferences.DarkPercent < 0 || plan.Preferences.DarkPercent > 100 {
		add("", "", "Scene count must be nonnegative and scene percentages must be between 0 and 100.")
	}
	for _, anchor := range plan.Anchors {
		_, exists := sources[anchor.SourceID]
		if !exists || anchor.MainSeconds < 0 || anchor.SourceSeconds < 0 || math.IsNaN(anchor.MainSeconds) || math.IsNaN(anchor.SourceSeconds) ||
			math.IsInf(anchor.MainSeconds, 0) || math.IsInf(anchor.SourceSeconds, 0) {
			add("", anchor.SourceID, "Anchors need an existing source and finite nonnegative timestamps.")
		}
	}
	return problems
}

// ReviewFingerprint binds retained sources, rows, content digests and all review
// choices without hashing derived approval state or plan-level diagnostics.
func ReviewFingerprint(plan api.ComparisonPlan) (api.WorkflowFingerprint, error) {
	plan.Fingerprint = ""
	plan.ApprovedFingerprint = ""
	plan.Problems = nil
	plan.Warnings = nil
	fingerprint, err := api.CanonicalWorkflowFingerprint(plan)
	if err != nil {
		return "", fmt.Errorf("comparison review fingerprint: %w", err)
	}
	return fingerprint, nil
}
