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
	ReleaseNameOverrides ReleaseNameOverrides
	Release              ReleaseRef
	Identity             ExternalIdentity
	Display              PreparedReleaseDisplay
	Bluray               *BlurayMetadata
	Diagnostics          []PreparationDiagnostic
	TrackerData          []TrackerPreview
	// TrackerRuleFailures is keyed by normalized tracker code and contains
	// upload rule failures known at preview time.
	TrackerRuleFailures map[string][]RuleFailure
}

type DescriptionBuilderPreview struct {
	SourcePath      string
	Groups          []DescriptionBuilderGroup
	ContentFailures []TrackerContentFailure
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
	SourcePath      string
	Descriptions    []PreparationDescription
	ContentFailures []TrackerContentFailure
}

type TrackerDryRunPreview struct {
	SourcePath string
	Trackers   []TrackerDryRunEntry
}

type UploadReview struct {
	SourcePath  string
	Trackers    []TrackerReview
	Eligibility TrackerEligibility
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
	Tracker string
	Status  string
	Message string
	// Banned reports whether the normalized release group matched this tracker
	// during dry-run or review evaluation. It is diagnostic state and does not
	// mean the dry-run payload builder was skipped.
	Banned bool
	// BannedReason explains the matched banned group when Banned is true.
	BannedReason string
	// BannedCheckError carries a redacted banned-group refresh or cache error
	// for dry-run diagnostics when the banned state could not be determined.
	BannedCheckError        string
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
	DebugSections  []TrackerDryRunDebugSection
	Questionnaire  *TrackerQuestionnaire
	ImageHost      ImageHostFeedback
	ContentFailure *TrackerContentFailure
	Diagnostics    TrackerDryRunDiagnostics
}

// TrackerDryRunDiagnostics describes live-upload findings without changing
// dry-run payload readiness.
type TrackerDryRunDiagnostics struct {
	RuleDecisions          []RuleDecision
	Duplicate              DupeCheckResult
	LiveEligibilityReasons []TrackerEligibilityReason
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

// ExternalIDInfo is the user-visible provider ID plus the resolver source label
// that produced it.
type ExternalIDInfo struct {
	Provider string
	ID       int
	Source   string
}
