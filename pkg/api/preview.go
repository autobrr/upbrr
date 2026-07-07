// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

type InteractionMode string

const (
	InteractionModeInteractive       InteractionMode = "interactive"
	InteractionModeUnattended        InteractionMode = "unattended"
	InteractionModeUnattendedConfirm InteractionMode = "unattended_confirm"
)

type MetadataPreview struct {
	SourcePath           string
	TrackerName          string
	ReleaseName          string
	Warnings             []string
	ReleaseNameOverrides ReleaseNameOverrides
	ExternalIDs          ExternalIDs
	ExternalIDCandidates ExternalIDCandidates
	ExternalIDInfo       []ExternalIDInfo
	ExternalPreview      []ExternalPreview
	Bluray               *BlurayMetadata
	TrackerData          []TrackerPreview
	// TrackerRuleFailures is keyed by normalized tracker code and contains
	// upload rule failures known at preview time.
	TrackerRuleFailures map[string][]RuleFailure
}

type DescriptionBuilderPreview struct {
	SourcePath string
	Groups     []DescriptionBuilderGroup
}

type DescriptionBuilderGroup struct {
	GroupKey           string
	Trackers           []string
	Description        string
	DescriptionHTML    string
	RawDescription     string
	RawDescriptionHTML string
	HasOverride        bool
	ImageHost          ImageHostFeedback
}

type PreparationPreview struct {
	SourcePath   string
	Descriptions []PreparationDescription
}

type TrackerDryRunPreview struct {
	SourcePath string
	Trackers   []TrackerDryRunEntry
}

type UploadReview struct {
	SourcePath string
	Trackers   []TrackerReview
}

type TrackerReview struct {
	Tracker       string
	Banned        bool
	BannedReason  string
	RuleFailures  []RuleFailure
	DupeCheck     DupeCheckResult
	DryRun        TrackerDryRunEntry
	Questionnaire *TrackerQuestionnaire
}

type TrackerDryRunEntry struct {
	Tracker                 string
	Status                  string
	Message                 string
	ReleaseName             string
	OriginalReleaseName     string
	UploadReleaseName       string
	ReleaseNameChanged      bool
	ReleaseNameChangeReason string
	DescriptionGroup        string
	Description             string
	Endpoint                string
	Payload                 map[string]string
	Files                   []TrackerDryRunFile
	// DebugSections carries optional staged diagnostics for trackers whose dry-run
	// preview needs to show more than one request or derived payload.
	DebugSections []TrackerDryRunDebugSection
	Questionnaire *TrackerQuestionnaire
	ImageHost     ImageHostFeedback
}

// TrackerDryRunDebugSection describes one named diagnostic payload inside a
// dry-run preview. Payload and files use the same redaction and path-display
// rules as the top-level dry-run entry.
type TrackerDryRunDebugSection struct {
	Title    string
	Endpoint string
	Payload  map[string]string
	Files    []TrackerDryRunFile
}

type TrackerDryRunFile struct {
	Field   string
	Path    string
	Present bool
}

type PreparationDescription struct {
	GroupKey           string
	Trackers           []string
	RawDescription     string
	RawDescriptionHTML string
	Description        string
	DescriptionHTML    string
	HasOverride        bool
	ImageHost          ImageHostFeedback
}

type ImageHostFeedback struct {
	Status       string
	SelectedHost string
	AllowedHosts []string
	Warnings     []ImageHostWarning
	Reuploaded   bool
	Message      string
}

type ImageHostWarning struct {
	Host    string
	Message string
}

type DescriptionImageHostStatus struct {
	Trackers  []string
	ImageHost ImageHostFeedback
}

type TrackerPreview struct {
	Tracker         string
	TrackerID       string
	TorrentURL      string
	InfoHash        string
	TMDBID          int
	IMDBID          int
	TVDBID          int
	MALID           int
	Category        string
	Description     string
	DescriptionHTML string
	ImageURLs       []string
	Filename        string
	Matched         bool
	UpdatedAt       string
}

type ExternalIDInfo struct {
	Provider string
	ID       int
	Source   string
}

type ExternalPreview struct {
	Provider         string
	ID               int
	Source           string
	Title            string
	Year             int
	Overview         string
	PosterURL        string
	BackdropURL      string
	Category         string
	OriginalTitle    string
	ReleaseDate      string
	FirstAirDate     string
	LastAirDate      string
	OriginalLanguage string
	TMDBType         string
	Runtime          int
	Genres           string
	Keywords         string
	YouTube          string
	IMDBType         string
	Rating           float64
	RatingCount      int
	RuntimeMinutes   int
	Country          string
	Premiered        string
	IMDBID           int
	TVDBID           int
	TMDB             *TMDBMetadata
	IMDB             *IMDBMetadata
	TVDB             *TVDBMetadata
	TVmaze           *TVmazeMetadata
}
