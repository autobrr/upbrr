// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// Request carries one core operation across CLI and WebUI.
type Request struct {
	SourcePath                   string
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
	ReleaseNameOverrides         ReleaseNameOverrides
	TrackerQuestionnaireAnswers  map[string]map[string]string // keyed by tracker, then questionnaire field key
	PlaylistInstruction          PlaylistInstruction
	ConfirmBDMVRescan            bool
}

// ExecutionOptions controls queued and site-check execution behavior.
type ExecutionOptions struct {
	QueueName         string
	QueueLimit        int
	SiteCheck         bool
	SiteUploadTracker string
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
	// CaptureDVDMenus requests automatic menu capture for DVD inputs before review or upload.
	CaptureDVDMenus bool
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

// Config defines the minimum application configuration contract required by core wiring.
// Keeping this in pkg/api avoids leaking internal package types into exported APIs.
type Config interface {
	Validate() error
}

// RepositoryOwner owns repository lifecycle separately from borrowed
// capabilities. Core never closes an externally supplied owner.
type RepositoryOwner interface {
	Close() error
}

// CoreDependencies supplies the externally owned services used to construct the core runtime.
type CoreDependencies struct {
	// Config is validated before the core is created.
	Config Config
	// Logger receives core initialization and runtime messages. Nil uses NopLogger.
	Logger Logger
	// Services supplies optional service overrides; zero values use the core defaults.
	Services ServiceSet
	// Repository supplies immutable borrowed persistence capabilities. Its zero
	// value opens and owns a SQLite repository from Config.
	Repository RepositoryCapabilities
	// RepositoryOwner identifies the externally owned adapter used to build
	// Repository. It remains borrowed and is used only for owner-specific setup.
	RepositoryOwner RepositoryOwner
	// SkipCookieMigration skips legacy cookie migration for callers that already
	// synchronize cookie encryption state with the shared repository.
	SkipCookieMigration bool
}
