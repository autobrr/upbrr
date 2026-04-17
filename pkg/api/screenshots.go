// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "time"

type ScreenshotPurpose string

const (
	ScreenshotPurposePreview ScreenshotPurpose = "preview"
	ScreenshotPurposeFinal   ScreenshotPurpose = "final"
	ScreenshotPurposeMenu    ScreenshotPurpose = "menu"
)

type ScreenshotSelection struct {
	Index            int
	TimestampSeconds float64
	Frame            int
	Source           string
}

type ScreenshotOverrides struct {
	ManualFrames           []int
	ComparisonPaths        []string
	ComparisonPrimaryIndex *int
	MenuPaths              []string
}

type ScreenshotFinalSelection struct {
	SourcePath string
	ImagePath  string
	Order      int
	Source     string
	SelectedAt time.Time
}

type ScreenshotPlan struct {
	SourcePath                 string
	DiscType                   string
	DurationSeconds            float64
	FrameRate                  float64
	SuggestedSelections        []ScreenshotSelection
	ExistingScreenshots        []ScreenshotImage
	ExistingTrackerScreenshots []ScreenshotImage
	FinalSelections            []ScreenshotImage
	TrackerImageLinks          []ScreenshotLinkedImage
	PreviewImages              []ScreenshotImage
	MetadataTimestamp          string
	RequiresManualFrames       bool
}

type ScreenshotLinkedImage struct {
	Tracker string
	URL     string
	Path    string
	Host    string // Normalized host name (e.g., "imgbb", "ptpimg") or domain name
}

type ScreenshotImage struct {
	Index            int
	TimestampSeconds float64
	Path             string
	Width            int
	Height           int
	SizeBytes        int64
	// Optional upload information (populated when image has been uploaded)
	Host       string    `json:"Host,omitempty"`
	ImgURL     string    `json:"ImgURL,omitempty"`
	RawURL     string    `json:"RawURL,omitempty"`
	WebURL     string    `json:"WebURL,omitempty"`
	UploadedAt time.Time `json:"UploadedAt,omitempty"`
}

type ScreenshotPreview struct {
	TimestampSeconds float64
	ImageBytes       []byte
	Width            int
	Height           int
	SizeBytes        int64
}

type ScreenshotResult struct {
	SourcePath     string
	Purpose        ScreenshotPurpose
	Images         []ScreenshotImage
	Tonemapped     bool
	UsedLibplacebo bool
	Errors         []ScreenshotError
}

type ScreenshotError struct {
	Index   int
	Message string
}
