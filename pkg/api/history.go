// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "time"

type HistoryEntry struct {
	SourcePath         string
	ReleaseTitle       string
	ReleaseSource      string
	ReleaseResolution  string
	MetadataUpdatedAt  time.Time
	LatestUploadStatus string
	LatestUploadAt     time.Time
	RuleFailureCount   int
}

type HistoryOverview struct {
	SourcePath           string
	ReleaseTitle         string
	ReleaseSource        string
	ReleaseResolution    string
	MetadataUpdatedAt    time.Time
	LatestUploadStatus   string
	LatestUploadAt       time.Time
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
