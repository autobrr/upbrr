// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strings"
	"time"
)

type Category string

const (
	CategoryUnknown Category = ""
	CategoryMovie   Category = "MOVIE"
	CategoryTV      Category = "TV"
)

func NormalizeCategory(value string) Category {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return CategoryUnknown
	}

	upper := strings.ToUpper(trimmed)
	switch upper {
	case string(CategoryMovie), "FILM":
		return CategoryMovie
	case string(CategoryTV), "SHOW", "SERIES", "TVSHOW", "TV-SHOW", "EPISODE":
		return CategoryTV
	}
	if strings.Contains(upper, "MOVIE") {
		return CategoryMovie
	}
	if strings.Contains(upper, "TV") || strings.Contains(upper, "SERIES") || strings.Contains(upper, "EPISODE") {
		return CategoryTV
	}
	return Category(trimmed)
}

func (c Category) Canonical() Category {
	return NormalizeCategory(string(c))
}

func (c Category) IsValid() bool {
	switch c.Canonical() {
	case CategoryMovie, CategoryTV:
		return true
	default:
		return false
	}
}

func (c Category) Value() (driver.Value, error) {
	canonical := c.Canonical()
	if canonical.IsValid() {
		return string(canonical), nil
	}
	return strings.TrimSpace(string(c)), nil
}

func (c *Category) Scan(src any) error {
	if c == nil {
		return fmt.Errorf("api: scan category: nil destination")
	}
	if src == nil {
		*c = CategoryUnknown
		return nil
	}

	switch value := src.(type) {
	case string:
		*c = Category(strings.TrimSpace(value))
		return nil
	case []byte:
		*c = Category(strings.TrimSpace(string(value)))
		return nil
	default:
		return fmt.Errorf("api: scan category: unsupported type %T", src)
	}
}

type FileMetadata struct {
	Path       string
	InfoHash   string
	UpdatedAt  time.Time
	DiscType   string
	VideoPath  string
	FileList   []string
	SourceSize int64
	Scene      bool
	SceneName  string
	SceneIMDB  int
	// Category is the normalized movie/TV content category that drives upload
	// logic. It is seeded from release parsing but should be overridden by a
	// supported TrackerMetadata.Category when that value is available, since a
	// site-reported movie/TV category is the authoritative classification for
	// the upload.
	Category   Category
	Type       string
	Artist     string
	Title      string
	Subtitle   string
	Alt        string
	Year       int
	Month      int
	Day        int
	Source     string
	Resolution string
	Codec      []string
	Audio      []string
	HDR        []string
	Ext        string
	Language   []string
	Site       string
	Genre      string
	Channels   string
	Collection string
	Region     string
	Size       string
	Group      string
	Disc       string
	Edition    []string
	Other      []string
}

type TrackerMetadata struct {
	SourcePath string
	Tracker    string
	TrackerID  string
	InfoHash   string
	TMDBID     int
	IMDBID     int
	TVDBID     int
	MALID      int
	// Category is the site-reported movie/TV content category from the tracker
	// API. Supported values take precedence over MediaInfoCategory and
	// Release.Category when resolving ExternalIDs.Category for upload
	// classification; unsupported categories are ignored.
	Category    Category
	Description string
	ImageURLs   []string
	Filename    string
	Matched     bool
	UpdatedAt   time.Time
}

type TrackerTimestamp struct {
	Tracker   string
	UpdatedAt time.Time
}

type UploadRecord struct {
	Tracker    string
	Status     string
	CreatedAt  time.Time
	SourcePath string
}

type TrackerRuleFailure struct {
	SourcePath string
	Tracker    string
	Rule       string
	Reason     string
	CreatedAt  time.Time
}

type DescriptionOverride struct {
	SourcePath  string
	GroupKey    string
	Description string
	UpdatedAt   time.Time
}

type PlaylistSelection struct {
	SourcePath        string
	SelectedPlaylists []string
	UseAll            bool
	UpdatedAt         time.Time
}

type Screenshot struct {
	SourcePath  string
	ImagePath   string
	Timestamp   float64
	FrameNumber int
	Width       int
	Height      int
	Purpose     ScreenshotPurpose
	CapturedAt  time.Time
}

type DVDMediaInfo struct {
	SourcePath      string
	IFOPath         string
	VOBPath         string
	VOBSet          string
	Width           int
	Height          int
	FrameRate       string
	ScanType        string
	Resolution      string
	HighFrameRate   bool
	MediaInfoJSON   string
	MediaInfoText   string
	VOBMediaInfoRaw string
	UpdatedAt       time.Time
}

type MetadataRepository interface {
	GetByPath(ctx context.Context, path string) (FileMetadata, error)
	Save(ctx context.Context, metadata FileMetadata) error
	GetExternalIDs(ctx context.Context, path string) (ExternalIDs, error)
	SaveExternalIDs(ctx context.Context, ids ExternalIDs) error
	GetExternalMetadata(ctx context.Context, path string) (ExternalMetadata, error)
	SaveExternalMetadata(ctx context.Context, metadata ExternalMetadata) error
	GetDVDMediaInfo(ctx context.Context, path string) (DVDMediaInfo, error)
	SaveDVDMediaInfo(ctx context.Context, info DVDMediaInfo) error
	GetReleaseNameOverrides(ctx context.Context, path string) (ReleaseNameOverrides, error)
	SaveReleaseNameOverrides(ctx context.Context, path string, overrides ReleaseNameOverrides) error
	DeleteReleaseNameOverrides(ctx context.Context, path string) error
	GetDescriptionOverride(ctx context.Context, path string, groupKey string) (DescriptionOverride, error)
	ListDescriptionOverridesByPath(ctx context.Context, path string) ([]DescriptionOverride, error)
	SaveDescriptionOverride(ctx context.Context, override DescriptionOverride) error
	DeleteDescriptionOverride(ctx context.Context, path string, groupKey string) error
	GetPlaylistSelection(ctx context.Context, sourcePath string) (PlaylistSelection, error)
	SavePlaylistSelection(ctx context.Context, sourcePath string, playlists []string, useAll bool) error
	DeletePlaylistSelection(ctx context.Context, sourcePath string) error
	ListHistoryEntries(ctx context.Context) ([]HistoryEntry, error)
	ListUploadHistoryByPath(ctx context.Context, sourcePath string) ([]UploadRecord, error)
	ListPendingUploads(ctx context.Context) ([]UploadRecord, error)
	CreateUploadRecord(ctx context.Context, record UploadRecord) error
	UpdateLatestUploadRecordStatus(ctx context.Context, sourcePath string, tracker string, status string) error
	SaveTrackerRuleFailures(ctx context.Context, sourcePath string, tracker string, failures []TrackerRuleFailure) error
	ListTrackerRuleFailuresByPath(ctx context.Context, path string) ([]TrackerRuleFailure, error)
	GetTrackerTimestamp(ctx context.Context, tracker string) (time.Time, error)
	SaveTrackerTimestamp(ctx context.Context, timestamp TrackerTimestamp) error
	SaveTrackerMetadata(ctx context.Context, metadata TrackerMetadata) error
	ListTrackerMetadataByPath(ctx context.Context, path string) ([]TrackerMetadata, error)
	SaveScreenshot(ctx context.Context, screenshot Screenshot) error
	ListScreenshotsByPath(ctx context.Context, path string) ([]Screenshot, error)
	DeleteScreenshot(ctx context.Context, imagePath string) error
	SaveFinalSelections(ctx context.Context, path string, selections []ScreenshotFinalSelection) error
	ListFinalSelections(ctx context.Context, path string) ([]ScreenshotFinalSelection, error)
	DeleteFinalSelection(ctx context.Context, imagePath string) error
	ReplaceScreenshotSlots(ctx context.Context, path string, slots []ScreenshotSlot) error
	ListScreenshotSlotsByPath(ctx context.Context, path string) ([]ScreenshotSlot, error)
	UpsertScreenshotSlotVariants(ctx context.Context, path string, variants []ScreenshotSlotVariant) error
	SaveUploadedImages(ctx context.Context, path string, host string, images []UploadedImageLink) error
	ListUploadedImagesByPath(ctx context.Context, path string) ([]UploadedImageLink, error)
	DeleteUploadedImage(ctx context.Context, path string, imagePath string, host string) error
	ListStoredReleasePaths(ctx context.Context) ([]string, error)
	PurgeContentData(ctx context.Context, path string) error
}
