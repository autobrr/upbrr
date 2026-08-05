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

// ReleaseWorkflowStateRecord is one owner-scoped durable public workflow
// state. Payload is application-owned safe JSON; repository adapters must not
// interpret or augment it with credentials or process-local authority.
type ReleaseWorkflowStateRecord struct {
	OwnerID             string
	WorkflowID          WorkflowID
	Revision            WorkflowRevision
	Status              WorkflowStatus
	CreationKey         string
	CreationFingerprint WorkflowFingerprint
	Payload             []byte
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// ReleaseWorkflowStateRepository persists safe public workflow state with
// owner scoping, durable create idempotency, and optimistic revision updates.
type ReleaseWorkflowStateRepository interface {
	CreateReleaseWorkflowState(context.Context, ReleaseWorkflowStateRecord) (ReleaseWorkflowStateRecord, bool, error)
	LoadReleaseWorkflowState(context.Context, string, WorkflowID) (ReleaseWorkflowStateRecord, error)
	SaveReleaseWorkflowState(context.Context, WorkflowRevision, ReleaseWorkflowStateRecord) error
	DeleteReleaseWorkflowState(context.Context, string, WorkflowID) error
	DeleteTerminalReleaseWorkflowStatesBefore(context.Context, time.Time) (int64, error)
}

// ReleaseWorkflowOperationRecord binds durable pollable progress to one exact
// accepted workflow command without embedding progress in aggregate state.
type ReleaseWorkflowOperationRecord struct {
	OwnerID            string
	WorkflowID         WorkflowID
	OperationID        WorkflowOperationID
	ExpectedRevision   WorkflowRevision
	IdempotencyKey     string
	CommandFingerprint WorkflowFingerprint
	ProcessEpoch       string
	Status             WorkflowOperationStatus
}

// ReleaseWorkflowOperationRepository persists operation lifecycle and progress
// independently from workflow aggregate revisions.
type ReleaseWorkflowOperationRepository interface {
	CreateReleaseWorkflowOperation(context.Context, ReleaseWorkflowOperationRecord) (ReleaseWorkflowOperationRecord, bool, error)
	LoadReleaseWorkflowOperation(context.Context, string, WorkflowID, WorkflowOperationID) (ReleaseWorkflowOperationRecord, error)
	LoadReleaseWorkflowOperationByIdempotency(
		context.Context,
		string,
		WorkflowID,
		string,
		string,
	) (ReleaseWorkflowOperationRecord, error)
	LoadLatestReleaseWorkflowOperation(context.Context, string, WorkflowID) (ReleaseWorkflowOperationRecord, error)
	SaveReleaseWorkflowOperation(context.Context, uint64, ReleaseWorkflowOperationRecord) error
	ListActiveReleaseWorkflowOperations(context.Context) ([]ReleaseWorkflowOperationRecord, error)
	DeleteTerminalReleaseWorkflowOperationsBefore(context.Context, time.Time) (int64, error)
}

// ReleaseWorkflowIntentRecord is one accepted Continue request. IntentPayload
// contains the exact application-owned request JSON and must never contain
// process-local execution authority.
type ReleaseWorkflowIntentRecord struct {
	OwnerID            string
	WorkflowID         WorkflowID
	IdempotencyKey     string
	RequestFingerprint WorkflowFingerprint
	Goal               WorkflowGoal
	IntentPayload      []byte
	AcceptedAt         time.Time
}

// ReleaseWorkflowContinuationRecord is the latest materialized continuation
// projection for one exact workflow revision.
type ReleaseWorkflowContinuationRecord struct {
	OwnerID    string
	WorkflowID WorkflowID
	Revision   WorkflowRevision
	Payload    []byte
	UpdatedAt  time.Time
}

// WorkflowEffectStatus is the durable lifecycle of one external side effect.
type WorkflowEffectStatus string

const (
	WorkflowEffectStatusStarted   WorkflowEffectStatus = "started"
	WorkflowEffectStatusSucceeded WorkflowEffectStatus = "succeeded"
	WorkflowEffectStatusFailed    WorkflowEffectStatus = "failed"
	WorkflowEffectStatusUnknown   WorkflowEffectStatus = "unknown"
)

// ReleaseWorkflowEffectRecord fences one exact tracker, client, or image-host
// attempt. It contains only safe identity/fingerprint metadata.
type ReleaseWorkflowEffectRecord struct {
	OwnerID             string
	WorkflowID          WorkflowID
	OperationID         WorkflowOperationID
	EffectID            string
	Kind                string
	ScopeID             string
	SemanticFingerprint WorkflowFingerprint
	Status              WorkflowEffectStatus
	StartedAt           time.Time
	UpdatedAt           time.Time
	CompletedAt         *time.Time
}

// ReleaseWorkflowWorkRecord is one durable operation lease and latest
// checkpoint. Checkpoint payload contains only safe operation state.
type ReleaseWorkflowWorkRecord struct {
	OwnerID        string
	WorkflowID     WorkflowID
	OperationID    WorkflowOperationID
	LeaseOwner     string
	LeaseExpiresAt time.Time
	Checkpoint     []byte
	UpdatedAt      time.Time
	CompletedAt    *time.Time
}

// ReleaseWorkflowDurabilityRepository persists accepted intent, immutable
// events, materialized continuation views, and fenced external attempts.
type ReleaseWorkflowDurabilityRepository interface {
	AcceptReleaseWorkflowIntent(context.Context, ReleaseWorkflowIntentRecord) (ReleaseWorkflowIntentRecord, bool, error)
	SaveReleaseWorkflowContinuation(context.Context, ReleaseWorkflowContinuationRecord) error
	AppendReleaseWorkflowEvents(context.Context, string, WorkflowID, []WorkflowEvent) ([]WorkflowEvent, error)
	LoadReleaseWorkflowEvents(context.Context, string, WorkflowID, uint64, int) ([]WorkflowEvent, error)
	BeginReleaseWorkflowEffect(context.Context, ReleaseWorkflowEffectRecord) (ReleaseWorkflowEffectRecord, bool, error)
	CompleteReleaseWorkflowEffect(context.Context, WorkflowEffectStatus, ReleaseWorkflowEffectRecord) error
	MarkReleaseWorkflowOperationEffectsUnknown(context.Context, string, WorkflowID, WorkflowOperationID, time.Time) error
	ResolveReleaseWorkflowEffectUnknown(context.Context, string, WorkflowID, WorkflowExternalEffectKind, string, time.Time) error
	LoadReleaseWorkflowWork(context.Context, string, WorkflowID, WorkflowOperationID) (ReleaseWorkflowWorkRecord, error)
	ClaimReleaseWorkflowWork(context.Context, ReleaseWorkflowWorkRecord) error
	RenewReleaseWorkflowWork(context.Context, ReleaseWorkflowWorkRecord) error
	CheckpointReleaseWorkflowWork(context.Context, ReleaseWorkflowWorkRecord) error
	CompleteReleaseWorkflowWork(context.Context, ReleaseWorkflowWorkRecord) error
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
	// ErrMissingReleaseWorkflowStateRepository indicates incomplete workflow persistence.
	ErrMissingReleaseWorkflowStateRepository = errors.New("api: release workflow state repository is required")
	// ErrReleaseWorkflowStateNotFound hides absent and foreign-owner workflow rows.
	ErrReleaseWorkflowStateNotFound = errors.New("api: release workflow state not found")
	// ErrReleaseWorkflowRevisionConflict reports an optimistic workflow update conflict.
	ErrReleaseWorkflowRevisionConflict = errors.New("api: release workflow revision conflict")
	// ErrReleaseWorkflowIdempotencyConflict reports reuse of a creation key with different input.
	ErrReleaseWorkflowIdempotencyConflict = errors.New("api: release workflow idempotency conflict")
	// ErrReleaseWorkflowOperationNotFound hides absent and foreign-owner operation rows.
	ErrReleaseWorkflowOperationNotFound = errors.New("api: release workflow operation not found")
	// ErrReleaseWorkflowOperationConflict reports active-operation or operation idempotency conflicts.
	ErrReleaseWorkflowOperationConflict = errors.New("api: release workflow operation conflict")
	// ErrReleaseWorkflowOperationSequenceConflict reports a concurrent progress update.
	ErrReleaseWorkflowOperationSequenceConflict = errors.New("api: release workflow operation sequence conflict")
	// ErrReleaseWorkflowEffectOutcomeUnknown reports a prior started effect with no terminal receipt.
	ErrReleaseWorkflowEffectOutcomeUnknown = errors.New("api: release workflow external effect outcome is unknown")
	// ErrReleaseWorkflowEffectAlreadySucceeded reports a completed semantic effect that must not be replayed.
	ErrReleaseWorkflowEffectAlreadySucceeded = errors.New("api: release workflow external effect already succeeded")
	// ErrReleaseWorkflowEffectConflict reports invalid effect identity or lifecycle reuse.
	ErrReleaseWorkflowEffectConflict = errors.New("api: release workflow external effect conflict")
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
	workflows    ReleaseWorkflowStateRepository
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
	workflows, _ := adapter.(ReleaseWorkflowStateRepository)
	return RepositoryCapabilities{
		releaseState: releaseState,
		prepared:     prepared,
		selections:   selections,
		history:      history,
		uploads:      uploads,
		trackers:     trackers,
		media:        media,
		workflows:    workflows,
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
		{value: c.workflows, err: ErrMissingReleaseWorkflowStateRepository},
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
	return c.releaseState == nil && c.prepared == nil && c.selections == nil && c.history == nil && c.uploads == nil &&
		c.trackers == nil && c.media == nil && c.workflows == nil
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

// Workflows returns the borrowed durable public workflow-state capability.
func (c RepositoryCapabilities) Workflows() ReleaseWorkflowStateRepository { return c.workflows }

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
