// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
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
	case CategoryUnknown:
		return false
	default:
		return false
	}
}

func (c Category) Value() (driver.Value, error) {
	canonical := c.Canonical()
	switch canonical {
	case CategoryMovie, CategoryTV:
		return string(canonical), nil
	case CategoryUnknown:
		return strings.TrimSpace(string(c)), nil
	default:
		return strings.TrimSpace(string(c)), nil
	}
}

func (c *Category) Scan(src any) error {
	if c == nil {
		return errors.New("api: scan category: nil destination")
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
	UpdatedAt  time.Time `ts_type:"string"`
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
	// Category is site-reported movie/TV evidence consumed only by canonical
	// external-identity resolution; unsupported values are ignored.
	Category    Category
	Description string
	ImageURLs   []string
	Filename    string
	Matched     bool
	UpdatedAt   time.Time `ts_type:"string"`
}

type TrackerTimestamp struct {
	Tracker   string
	UpdatedAt time.Time `ts_type:"string"`
}

type UploadRecord struct {
	Tracker    string
	Status     string
	CreatedAt  time.Time `ts_type:"string"`
	SourcePath string
}

type TrackerRuleFailure struct {
	SourcePath string
	Tracker    string
	Rule       string
	Reason     string
	// Disposition is normalized from legacy severity values during migration/readback.
	Disposition RuleDisposition
	// Authorized records whether the exact waivable result was accepted for the stored operation.
	Authorized bool
	CreatedAt  time.Time `ts_type:"string"`
}

type DescriptionOverride struct {
	SourcePath  string
	GroupKey    string
	Description string
	UpdatedAt   time.Time `ts_type:"string"`
}

type PlaylistSelection struct {
	SourcePath        string
	SelectedPlaylists []string
	UseAll            bool
	UpdatedAt         time.Time `ts_type:"string"`
}

type Screenshot struct {
	SourcePath  string
	ImagePath   string
	Timestamp   float64
	FrameNumber int
	Width       int
	Height      int
	Purpose     ScreenshotPurpose
	CapturedAt  time.Time `ts_type:"string"`
}

// DiscMenuDeleteResult describes local references removed by one atomic menu
// deletion and retains the records needed for transactional compensation.
type DiscMenuDeleteResult struct {
	// Selection is the deleted manual or automatic menu selection.
	Selection ScreenshotFinalSelection
	// Screenshot is the deleted local screenshot record when one existed.
	Screenshot *Screenshot
	// UploadedImages are deleted local upload records. Remote assets are unchanged.
	UploadedImages []UploadedImageLink
	// ScreenshotSlots are deleted slot records whose selected image was removed.
	ScreenshotSlots []ScreenshotSlot
	// ScreenshotSlotVariants are variants deleted with a slot or because they
	// referenced the removed image.
	ScreenshotSlotVariants []ScreenshotSlotVariant
	// UploadedLinks counts local upload records removed with the selection.
	UploadedLinks int
}

// ScreenshotLifecycleRepository owns category-aware screenshot mutations that
// must stay atomic without expanding the general metadata repository contract.
type ScreenshotLifecycleRepository interface {
	// ReplaceNormalFinalSelections replaces non-menu selections while preserving disc menus.
	ReplaceNormalFinalSelections(ctx context.Context, path string, selections []ScreenshotFinalSelection) error
	// AppendManualMenuScreenshots atomically appends manual menu records and selections.
	AppendManualMenuScreenshots(ctx context.Context, path string, screenshots []Screenshot, selections []ScreenshotFinalSelection) error
	// ReplaceDVDMenuScreenshots atomically replaces automatic captures and returns their old local paths.
	ReplaceDVDMenuScreenshots(ctx context.Context, path string, screenshots []Screenshot, selections []ScreenshotFinalSelection) ([]string, error)
	// DeleteDiscMenuScreenshot atomically removes one manual or automatic menu
	// selection and returns the local records needed to compensate the deletion.
	DeleteDiscMenuScreenshot(ctx context.Context, path string, imagePath string) (DiscMenuDeleteResult, error)
	// RestoreDiscMenuScreenshot atomically restores a result returned by
	// DeleteDiscMenuScreenshot for the same source path.
	RestoreDiscMenuScreenshot(ctx context.Context, path string, deleted DiscMenuDeleteResult) error
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
	UpdatedAt       time.Time `ts_type:"string"`
}

// ReleaseStateRepository persists the state used to prepare one release.
// Implementations preserve path validation, UTC timestamps, and ErrNotFound
// identity for missing optional records.
type ReleaseStateRepository interface {
	GetByPath(ctx context.Context, path string) (FileMetadata, error)
	Save(ctx context.Context, metadata FileMetadata) error
	GetExternalIdentity(ctx context.Context, path string) (ExternalIdentity, error)
	SaveExternalIdentity(ctx context.Context, ids ExternalIdentity) error
	GetExternalMetadata(ctx context.Context, path string) (SourceScopedMetadata, error)
	SaveExternalMetadata(ctx context.Context, metadata SourceScopedMetadata) error
	SaveDVDMediaInfo(ctx context.Context, info DVDMediaInfo) error
	GetReleaseNameOverrides(ctx context.Context, path string) (ReleaseNameOverrides, error)
	SaveReleaseNameOverrides(ctx context.Context, path string, overrides ReleaseNameOverrides) error
}

// PreparedReleaseRepository owns whole-generation prepared facts. Commits and
// purges include canonical identity and source-scoped provider metadata in one
// transaction.
type PreparedReleaseRepository interface {
	LoadPreparedRelease(ctx context.Context, sourcePath string) (PreparedRelease, error)
	CommitPreparedRelease(ctx context.Context, release PreparedRelease) error
	PurgePreparedRelease(ctx context.Context, sourcePath string) error
}

// ReleaseSelectionRepository persists user-selected description and playlist
// state. Explicit empty selections are distinct from missing records.
type ReleaseSelectionRepository interface {
	GetDescriptionOverride(ctx context.Context, path string, groupKey string) (DescriptionOverride, error)
	ListDescriptionOverridesByPath(ctx context.Context, path string) ([]DescriptionOverride, error)
	SaveDescriptionOverride(ctx context.Context, override DescriptionOverride) error
	DeleteDescriptionOverride(ctx context.Context, path string, groupKey string) error
	GetPlaylistSelection(ctx context.Context, sourcePath string) (PlaylistSelection, error)
	SavePlaylistSelection(ctx context.Context, sourcePath string, playlists []string, useAll bool) error
}

// HistoryCleanupSnapshot contains persisted local paths needed by Core's
// filesystem cleanup policy. ArtifactPaths is an isolated caller-owned slice.
type HistoryCleanupSnapshot struct {
	Metadata      *FileMetadata
	ArtifactPaths []string
}

// HistoryRepository owns persisted history projection, cleanup discovery, and
// atomic release-state purge. Filesystem deletion remains outside this seam.
type HistoryRepository interface {
	ListHistoryEntries(ctx context.Context) ([]HistoryEntry, error)
	LoadHistoryRecord(ctx context.Context, sourcePath string) (HistoryRecord, error)
	LoadHistoryCleanupSnapshot(ctx context.Context, sourcePath string) (HistoryCleanupSnapshot, error)
	ListStoredReleasePaths(ctx context.Context) ([]string, error)
	PurgeContentData(ctx context.Context, path string) error
}

// UploadLedgerRepository owns upload-attempt creation, latest-record status
// transitions, and canonical newest-first history queries.
type UploadLedgerRepository interface {
	ListUploadHistoryByPath(ctx context.Context, sourcePath string) ([]UploadRecord, error)
	CreateUploadRecord(ctx context.Context, record UploadRecord) error
	UpdateLatestUploadRecordStatus(ctx context.Context, sourcePath string, tracker string, status string) error
}

// TrackerStateRepository persists tracker-derived metadata, refresh times, and
// atomic replacement of rule-failure sets.
type TrackerStateRepository interface {
	SaveTrackerRuleFailures(ctx context.Context, sourcePath string, tracker string, failures []TrackerRuleFailure) error
	ListTrackerRuleFailuresByPath(ctx context.Context, path string) ([]TrackerRuleFailure, error)
	GetTrackerTimestamp(ctx context.Context, tracker string) (time.Time, error)
	SaveTrackerTimestamp(ctx context.Context, timestamp TrackerTimestamp) error
	SaveTrackerMetadata(ctx context.Context, metadata TrackerMetadata) error
	ListTrackerMetadataByPath(ctx context.Context, path string) ([]TrackerMetadata, error)
}

// MediaAssetSnapshot is a coherent caller-owned view of persisted media
// records for one release. Returned slices and nested variant slices may be
// mutated by callers without changing repository state.
type MediaAssetSnapshot struct {
	Screenshots     []Screenshot
	FinalSelections []ScreenshotFinalSelection
	ScreenshotSlots []ScreenshotSlot
	UploadedImages  []UploadedImageLink
}

// MediaAssetRepository persists screenshot, selection, slot, variant, and
// uploaded-image records. Screenshot lifecycle mutations are atomic.
type MediaAssetRepository interface {
	ScreenshotLifecycleRepository
	LoadMediaAssetSnapshot(ctx context.Context, path string) (MediaAssetSnapshot, error)
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
}

var (
	// ErrMissingReleaseStateRepository indicates incomplete repository composition.
	ErrMissingReleaseStateRepository = errors.New("api: release state repository is required")
	// ErrMissingPreparedReleaseRepository indicates incomplete prepared-generation persistence.
	ErrMissingPreparedReleaseRepository = errors.New("api: prepared release repository is required")
	// ErrMissingReleaseSelectionRepository indicates incomplete repository composition.
	ErrMissingReleaseSelectionRepository = errors.New("api: release selection repository is required")
	// ErrMissingHistoryRepository indicates incomplete repository composition.
	ErrMissingHistoryRepository = errors.New("api: history repository is required")
	// ErrMissingUploadLedgerRepository indicates incomplete repository composition.
	ErrMissingUploadLedgerRepository = errors.New("api: upload ledger repository is required")
	// ErrMissingTrackerStateRepository indicates incomplete repository composition.
	ErrMissingTrackerStateRepository = errors.New("api: tracker state repository is required")
	// ErrMissingMediaAssetRepository indicates incomplete repository composition.
	ErrMissingMediaAssetRepository = errors.New("api: media asset repository is required")
)

// RepositoryCapabilities is an immutable set of borrowed persistence
// capabilities. Construct it from one adapter so production capabilities share
// connection, retry, transaction, and lifecycle ownership.
type RepositoryCapabilities struct {
	releaseState ReleaseStateRepository
	prepared     PreparedReleaseRepository
	selections   ReleaseSelectionRepository
	history      HistoryRepository
	uploads      UploadLedgerRepository
	trackers     TrackerStateRepository
	media        MediaAssetRepository
}

// RepositoryCapabilitiesFrom projects one adapter onto every repository seam.
// Call [RepositoryCapabilities.Validate] before installing the result.
func RepositoryCapabilitiesFrom(adapter any) RepositoryCapabilities {
	releaseState, _ := adapter.(ReleaseStateRepository)
	prepared, _ := adapter.(PreparedReleaseRepository)
	selections, _ := adapter.(ReleaseSelectionRepository)
	history, _ := adapter.(HistoryRepository)
	uploads, _ := adapter.(UploadLedgerRepository)
	trackers, _ := adapter.(TrackerStateRepository)
	media, _ := adapter.(MediaAssetRepository)
	return RepositoryCapabilities{
		releaseState: releaseState,
		prepared:     prepared,
		selections:   selections,
		history:      history,
		uploads:      uploads,
		trackers:     trackers,
		media:        media,
	}
}

// NewRepositoryCapabilities constructs and validates borrowed capabilities
// projected from one adapter.
func NewRepositoryCapabilities(adapter any) (RepositoryCapabilities, error) {
	capabilities := RepositoryCapabilitiesFrom(adapter)
	if err := capabilities.Validate(); err != nil {
		return RepositoryCapabilities{}, err
	}
	return capabilities, nil
}

// Validate rejects missing and typed-nil capabilities before runtime use.
func (c RepositoryCapabilities) Validate() error {
	checks := []struct {
		value any
		err   error
	}{
		{value: c.releaseState, err: ErrMissingReleaseStateRepository},
		{value: c.prepared, err: ErrMissingPreparedReleaseRepository},
		{value: c.selections, err: ErrMissingReleaseSelectionRepository},
		{value: c.history, err: ErrMissingHistoryRepository},
		{value: c.uploads, err: ErrMissingUploadLedgerRepository},
		{value: c.trackers, err: ErrMissingTrackerStateRepository},
		{value: c.media, err: ErrMissingMediaAssetRepository},
	}
	for _, check := range checks {
		if isNilRepositoryCapability(check.value) {
			return check.err
		}
	}
	return nil
}

// IsZero reports whether no repository capabilities were supplied.
func (c RepositoryCapabilities) IsZero() bool {
	return c.releaseState == nil && c.prepared == nil && c.selections == nil && c.history == nil && c.uploads == nil && c.trackers == nil && c.media == nil
}

// ReleaseState returns the borrowed release-state capability.
func (c RepositoryCapabilities) ReleaseState() ReleaseStateRepository { return c.releaseState }

// Prepared returns the borrowed whole-generation prepared-release capability.
func (c RepositoryCapabilities) Prepared() PreparedReleaseRepository { return c.prepared }

// Selections returns the borrowed release-selection capability.
func (c RepositoryCapabilities) Selections() ReleaseSelectionRepository { return c.selections }

// History returns the borrowed history capability.
func (c RepositoryCapabilities) History() HistoryRepository { return c.history }

// Uploads returns the borrowed upload-ledger capability.
func (c RepositoryCapabilities) Uploads() UploadLedgerRepository { return c.uploads }

// Trackers returns the borrowed tracker-state capability.
func (c RepositoryCapabilities) Trackers() TrackerStateRepository { return c.trackers }

// Media returns the borrowed media-asset capability.
func (c RepositoryCapabilities) Media() MediaAssetRepository { return c.media }

func isNilRepositoryCapability(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	kind := reflected.Kind()
	if kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface || kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Slice {
		return reflected.IsNil()
	}
	return false
}
