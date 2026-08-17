// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"strings"
	"time"
)

// ScreenshotPurpose classifies an image within preview, final-selection, and
// disc-menu workflows.
type ScreenshotPurpose string

// Screenshot purpose and selection-source values shared across persistence and
// frontend workflows.
const (
	// ScreenshotPurposePreview identifies transient frame previews.
	ScreenshotPurposePreview ScreenshotPurpose = "preview"
	// ScreenshotPurposeFinal identifies normal final screenshot selections.
	ScreenshotPurposeFinal ScreenshotPurpose = "final"
	// ScreenshotPurposeMenu identifies manual or automatic disc-menu images.
	ScreenshotPurposeMenu ScreenshotPurpose = "menu"

	// ScreenshotSelectionSourceMenu identifies manually imported disc-menu selections.
	ScreenshotSelectionSourceMenu = "menu"
	// ScreenshotSelectionSourceDVDMenu identifies automatically captured DVD-menu selections.
	ScreenshotSelectionSourceDVDMenu = "dvd_menu"
)

// IsDiscMenuSelectionSource reports whether a final-selection source belongs
// to either manually imported or automatically captured disc menus.
func IsDiscMenuSelectionSource(source string) bool {
	switch strings.TrimSpace(source) {
	case ScreenshotSelectionSourceMenu, ScreenshotSelectionSourceDVDMenu:
		return true
	default:
		return false
	}
}

type ScreenshotSelection struct {
	DiscID           string
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

// ScreenshotSubject contains only source facts required to plan and manage
// screenshots. Workflow modules build it from an exact prepared generation.
type ScreenshotSubject struct {
	MediaBinding          PreparedMediaBinding
	SourcePath            string
	DiscType              string
	VideoPath             string
	MediaInfoJSONPath     string
	MediaCategory         string
	HDR                   string
	TVPack                bool
	Episode               int
	Release               ReleaseInfo
	SelectedBDMVPlaylists []PlaylistInfo
	DefaultCount          int
	ManualFrames          []int
	Discs                 []ScreenshotDiscSubject
}

// ScreenshotDiscSubject contains private capture inputs for one ordered disc.
type ScreenshotDiscSubject struct {
	ID                    string
	Name                  string
	Type                  string
	VideoPath             string
	MediaInfoJSONPath     string
	SelectedBDMVPlaylists []PlaylistInfo
}

// DVDMenuSubject contains the stable source facts required by DVD-menu
// capture and lifecycle operations.
type DVDMenuSubject struct {
	MediaBinding PreparedMediaBinding
	SourcePath   string
	DiscType     string
	Discs        []DVDMenuDiscSubject
}

// DVDMenuDiscSubject identifies one private DVD root eligible for menu capture.
type DVDMenuDiscSubject struct {
	ID   string
	Name string
	Root string
}

// ImageHostingSubject scopes image-host operations to one prepared source.
// GalleryName is the already-resolved display name used by batch hosts.
type ImageHostingSubject struct {
	MediaBinding PreparedMediaBinding
	SourcePath   string
	GalleryName  string
}

type ScreenshotFinalSelection struct {
	SourcePath               string
	PreparedMediaFingerprint string
	PreparedGeneration       PreparedGeneration
	DiscID                   string
	ImagePath                string
	Order                    int
	Source                   string
	SelectedAt               time.Time `ts_type:"string"`
}

type ScreenshotPlan struct {
	MediaBinding               PreparedMediaBinding
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
	Discs                      []ScreenshotDiscPlan
}

// ScreenshotDiscPlan is one ordered disc's timing and selection plan.
type ScreenshotDiscPlan struct {
	DiscID              string
	DiscName            string
	DurationSeconds     float64
	FrameRate           float64
	SuggestedSelections []ScreenshotSelection
}

type ScreenshotLinkedImage struct {
	Tracker string
	URL     string
	Path    string
	Host    string // Normalized host name (e.g., "imgbb", "pixhost") or domain name
}

type ScreenshotImage struct {
	DiscID           string
	DiscName         string
	Index            int
	TimestampSeconds float64
	Path             string
	// Purpose distinguishes preview, normal final, and disc-menu images.
	Purpose   ScreenshotPurpose
	Width     int
	Height    int
	SizeBytes int64
	// Optional upload information (populated when image has been uploaded)
	Host       string    `json:"Host,omitempty"`
	ImgURL     string    `json:"ImgURL,omitempty"`
	RawURL     string    `json:"RawURL,omitempty"`
	WebURL     string    `json:"WebURL,omitempty"`
	UploadedAt time.Time `json:"UploadedAt,omitempty" ts_type:"string"`
}

type ScreenshotPreview struct {
	DiscID           string
	DiscName         string
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
	DiscID  string
	Index   int
	Message string
}
