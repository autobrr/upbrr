// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"strings"
	"time"
)

type HistoryEntry struct {
	SourcePath         string
	ReleaseTitle       string
	ReleaseSource      string
	ReleaseResolution  string
	MetadataUpdatedAt  time.Time `ts_type:"string"`
	LatestUploadStatus string
	LatestUploadAt     time.Time `ts_type:"string"`
	RuleFailureCount   int
	// RuleWarningCount excludes blocking, legacy, and unrecognized rule results.
	RuleWarningCount int
}

type HistoryOverview struct {
	SourcePath           string
	ReleaseTitle         string
	ReleaseSource        string
	ReleaseResolution    string
	MetadataUpdatedAt    time.Time `ts_type:"string"`
	LatestUploadStatus   string
	LatestUploadAt       time.Time `ts_type:"string"`
	StatusLabel          string
	Metadata             FileMetadata
	ExternalIDs          ExternalIDs
	ExternalMetadata     ExternalMetadata
	ReleaseNameOverrides ReleaseNameOverrides
	DescriptionOverride  DescriptionOverride
	DescriptionOverrides []DescriptionOverride
	PlaylistSelection    PlaylistSelection
	TrackerMetadata      []TrackerMetadata
	TrackerRuleFailures  []TrackerRuleFailure
	Screenshots          []Screenshot
	FinalSelections      []ScreenshotFinalSelection
	UploadedImages       []UploadedImageLink
	UploadHistory        []UploadRecord
}

// HistoryStatusLabel returns the display label for a persisted upload status,
// falling back to rule-issue or stored state when no status is available.
func HistoryStatusLabel(rawStatus string, ruleFailureCount int) string {
	status := strings.TrimSpace(strings.ToLower(rawStatus))
	switch status {
	case "pending":
		return "Pending"
	case "pending-internal":
		return "Pending Internal"
	case "uploaded", "success", "completed":
		return "Uploaded"
	case "failed", "error":
		return "Failed"
	}
	if status != "" {
		normalized := strings.ReplaceAll(status, "-", " ")
		words := strings.Fields(normalized)
		for idx, word := range words {
			if word == "" {
				continue
			}
			words[idx] = strings.ToUpper(word[:1]) + word[1:]
		}
		return strings.Join(words, " ")
	}
	if ruleFailureCount > 0 {
		return "Rule Issues"
	}
	return "Stored"
}
