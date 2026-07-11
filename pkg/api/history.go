// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "time"

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
