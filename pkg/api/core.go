// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
)

// Mode identifies the entrypoint building a core request.
type Mode string

const (
	// ModeCLI marks requests started from the command-line entrypoint.
	ModeCLI Mode = "cli"
	// ModeGUI marks requests started from GUI or embedded web entrypoints.
	ModeGUI Mode = "gui"
)

// Request carries one core operation across CLI, Wails, and embedded web.
// Paths and path-keyed maps use host filesystem source paths as keys.
type Request struct {
	Paths                        []string
	Mode                         Mode
	Options                      UploadOptions
	Execution                    ExecutionOptions
	DescriptionGroups            []DescriptionBuilderGroup
	Trackers                     []string
	TrackersRemove               []string
	IgnoreTrackerRuleFailures    bool
	IgnoreTrackerRuleFailuresFor []string
	IgnoreDupesFor               []string
	SkipDupeCheck                bool
	SkipDupeAsActual             bool
	DoubleDupeCheck              bool
	SourceLookupURL              string
	DescriptionOverrideRaw       string
	DescriptionOverrideURL       string
	DescriptionOverrideGroup     string
	MetadataOverrides            MetadataOverrides
	TrackerConfigOverrides       TrackerConfigOverrides
	TrackerSiteOverrides         TrackerSiteOverrides
	ClientOverrides              ClientOverrides
	ImageHostOverrides           ImageHostOverrides
	ScreenshotOverrides          ScreenshotOverrides
	TorrentOverrides             TorrentOverrides
	TrackerIDOverrides           map[string]string
	ExternalIDOverrides          ExternalIDOverrides
	ExternalIDSelections         map[string]ExternalIDSelection // keyed by source path, value is selected external IDs for that path
	ReleaseNameOverrides         ReleaseNameOverrides
	TrackerQuestionnaireAnswers  map[string]map[string]string // keyed by tracker, then questionnaire field key
	PlaylistSelections           map[string][]string          // keyed by source path, value is selected playlist files
	PlaylistSelectionsUseAll     map[string]bool              // keyed by source path, value is use-all flag
	ConfirmBDMVRescan            bool
}

// ExecutionOptions controls queued and site-check execution behavior.
type ExecutionOptions struct {
	QueueName         string
	QueueLimit        int
	SiteCheck         bool
	SiteUploadTracker string
}

// ExternalIDSelection records the external metadata IDs chosen for one source path.
type ExternalIDSelection struct {
	TMDBID   *int
	IMDBID   *int
	TVDBID   *int
	TVmazeID *int
}

// UploadOptions contains per-run upload and preview behavior flags.
type UploadOptions struct {
	Debug           bool
	DryRun          bool
	RunLogLevel     string
	Screens         int
	NoSeed          bool
	SkipAutoTorrent bool
	OnlyID          bool
	KeepFolder      bool
	KeepImages      bool
	InteractionMode InteractionMode
}

// TrackerConfigOverrides supplies optional per-request tracker setting overrides.
type TrackerConfigOverrides struct {
	Anon    *bool
	Draft   *bool
	ModQ    *bool
	Channel *string
}

// TrackerSiteOverrides groups tracker-specific site override payloads.
type TrackerSiteOverrides struct {
	TIK TIKOverrides
}

// TIKOverrides carries TIK-specific upload flags selected outside static config.
type TIKOverrides struct {
	Foreign  *bool
	Opera    *bool
	Asian    *bool
	DiscType *string
}

// Result summarizes a completed core upload run.
type Result struct {
	UploadedCount int
}

// Core defines the shared upload, preview, history, screenshot, and description
// operations used by CLI, Wails, and embedded web entrypoints.
type Core interface {
	RunUpload(ctx context.Context, req Request) (Result, error)
	RunUploadPrepared(ctx context.Context, req Request) (Result, error)
	FetchMetadataPreview(ctx context.Context, req Request) (MetadataPreview, error)
	// FetchDescriptionBuilderPreview returns editable description groups for one
	// source path. Non-empty request trackers limit group generation to that set.
	FetchDescriptionBuilderPreview(ctx context.Context, req Request) (DescriptionBuilderPreview, error)
	// FetchDescriptionBuilderGroupPreview rebuilds one editable description group
	// for the requested source path and selected tracker set.
	FetchDescriptionBuilderGroupPreview(ctx context.Context, req Request) (DescriptionBuilderGroup, error)
	// FetchPreparationPreview returns tracker preparation details for one source
	// path. Non-empty request trackers limit preparation to that set.
	FetchPreparationPreview(ctx context.Context, req Request) (PreparationPreview, error)
	FetchTrackerDryRunPreview(ctx context.Context, req Request) (TrackerDryRunPreview, error)
	CheckDupes(ctx context.Context, req Request) (DupeCheckSummary, error)
	BuildUploadReview(ctx context.Context, req Request) (UploadReview, error)
	FetchScreenshotPlan(ctx context.Context, req Request) (ScreenshotPlan, error)
	GenerateScreenshots(ctx context.Context, req Request, selections []ScreenshotSelection, purpose ScreenshotPurpose) (ScreenshotResult, error)
	PreviewScreenshotFrame(ctx context.Context, req Request, timestampSeconds float64) (ScreenshotPreview, error)
	DeleteScreenshot(ctx context.Context, req Request, imagePath string) error
	DeleteTrackerImageURL(ctx context.Context, req Request, url string) error
	SaveFinalScreenshotSelections(ctx context.Context, req Request, images []ScreenshotImage) error
	ListUploadCandidates(ctx context.Context, req Request) ([]ScreenshotImage, error)
	ListUploadedImages(ctx context.Context, req Request) ([]UploadedImageLink, error)
	UploadImages(ctx context.Context, req Request, host string, images []ScreenshotImage) (UploadImagesResult, error)
	DeleteUploadedImage(ctx context.Context, req Request, imagePath string, host string) error
	ImportMenuImages(ctx context.Context, req Request, paths []string) error
	DiscoverPlaylists(ctx context.Context, sourcePath string) ([]PlaylistInfo, error)
	SavePlaylistSelection(ctx context.Context, sourcePath string, playlists []string, useAll bool) error
	LoadPlaylistSelection(ctx context.Context, sourcePath string) (PlaylistSelection, error)
	ListHistory(ctx context.Context) ([]HistoryEntry, error)
	GetHistoryOverview(ctx context.Context, sourcePath string) (HistoryOverview, error)
	DeleteHistoryRelease(ctx context.Context, sourcePath string) error
	DeleteAllHistoryReleases(ctx context.Context) (int, error)
	RenderDescription(ctx context.Context, raw string) (string, error)
	SaveDescriptionOverride(ctx context.Context, req Request, raw string) (DescriptionBuilderGroup, error)
	Close() error
}

// Config defines the minimum application configuration contract required by core wiring.
// Keeping this in pkg/api avoids leaking internal package types into exported APIs.
type Config interface {
	Validate() error
}

// CoreDependencies supplies the externally owned services used to construct a Core.
type CoreDependencies struct {
	Config              Config
	Logger              Logger
	Services            ServiceSet
	Repository          MetadataRepository
	SkipCookieMigration bool
}

// CoreImpl is the exported concrete-core contract used by packages that accept
// any current core implementation.
type CoreImpl interface {
	Core
}
