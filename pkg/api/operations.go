// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// DuplicateCheckInput contains duplicate-check choices for one exact prepared
// generation. It does not carry prepared facts.
type DuplicateCheckInput struct {
	Release        ReleaseRef
	Trackers       []string
	Interaction    InteractionMode
	IgnoreFor      []string
	Skip           bool
	SkipAsActual   bool
	DoubleCheck    bool
	Authorizations []string
	// TrackerIDs contains explicit tracker IDs that override discovered client evidence.
	TrackerIDs map[string]string
	// ClientSearch controls the operation-time refresh of local client evidence.
	ClientSearch ClientSearchPolicy
	// ForceRecheck forwards an explicit torrent-client hash recheck choice.
	ForceRecheck *bool
}

// UploadReviewInput contains review-time workflow choices for one exact
// prepared generation.
type UploadReviewInput struct {
	Release                ReleaseRef
	Trackers               []string
	IgnoreDupesFor         []string
	IgnoreRuleFailuresFor  []string
	SkipDuplicateCheck     bool
	SkipDuplicateAsActual  bool
	DoubleDuplicateCheck   bool
	QuestionnaireAnswers   map[string]map[string]string
	TrackerIDOverrides     map[string]string
	DescriptionGroups      []DescriptionBuilderGroup
	TrackerConfigOverrides TrackerConfigOverrides
	TrackerSiteOverrides   TrackerSiteOverrides
	ClientOverrides        ClientOverrides
	ImageHostOverrides     ImageHostOverrides
	ScreenshotOverrides    ScreenshotOverrides
	TorrentOverrides       TorrentOverrides
	Options                UploadOptions
}

// UploadReviewOutcome contains tracker-scoped decisions established by one
// completed review. It excludes prepared facts and mutable runtime state.
type UploadReviewOutcome struct {
	ResolvedTrackers    []string
	Eligibility         TrackerEligibility
	MatchedTrackers     []string
	BlockedTrackers     map[string][]TrackerBlockReason
	TrackerRuleFailures map[string][]RuleFailure
	CrossSeedTorrents   []UploadedTorrent
}

// ReviewedUpload is the authoritative review result retained for accepted
// execution. Review is transport-visible; Outcome is execution-only state.
type ReviewedUpload struct {
	Review  UploadReview
	Outcome UploadReviewOutcome
}

// UploadExecutionPlan combines operation choices with the exact outcomes
// authorized by review. Prepared facts remain behind Release and its seed.
type UploadExecutionPlan struct {
	Input   UploadReviewInput
	Outcome UploadReviewOutcome
}

// UploadExecutionInput consumes one opaque, owner-scoped reviewed execution
// snapshot. No live preparation or runtime state is accepted.
type UploadExecutionInput struct {
	ReviewToken string
}

// TrackerDryRunInput contains tracker-preview choices for one exact prepared
// generation.
type TrackerDryRunInput struct {
	Release                ReleaseRef
	Trackers               []string
	IgnoreDupesFor         []string
	IgnoreRuleFailuresFor  []string
	QuestionnaireAnswers   map[string]map[string]string
	DescriptionGroups      []DescriptionBuilderGroup
	TrackerIDOverrides     map[string]string
	TrackerConfigOverrides TrackerConfigOverrides
	TrackerSiteOverrides   TrackerSiteOverrides
	ClientOverrides        ClientOverrides
	ImageHostOverrides     ImageHostOverrides
	ScreenshotOverrides    ScreenshotOverrides
	TorrentOverrides       TorrentOverrides
	Options                UploadOptions
}

// AcceptedDuplicateEvidence contains one completed duplicate-check outcome for
// the exact release and tracker selection consumed by a tracker dry run.
type AcceptedDuplicateEvidence struct {
	Release  ReleaseRef
	Trackers []string
	Results  []DupeCheckResult
}

// TrackerDryRunPlan combines dry-run choices with the completed duplicate
// evidence accepted by the caller. Core validates both parts before building
// tracker payloads or injecting any torrent.
type TrackerDryRunPlan struct {
	Input     TrackerDryRunInput
	Duplicate AcceptedDuplicateEvidence
}

// MediaPlanInput contains media planning choices for one exact prepared
// generation.
type MediaPlanInput struct {
	Release ReleaseRef
	Count   int
	Purpose ScreenshotPurpose
	Options ScreenshotOverrides
}

// DescriptionInput contains description projection choices for one exact
// prepared generation.
type DescriptionInput struct {
	Release           ReleaseRef
	Trackers          []string
	GroupKey          string
	Groups            []DescriptionBuilderGroup
	ImageHost         ImageHostOverrides
	QuestionnaireData map[string]map[string]string
	Options           UploadOptions
}

// ImageHostingInput contains image-host choices for one exact prepared
// generation. Selected images are supplied separately to keep the subject
// contract independent from transport payloads.
type ImageHostingInput struct {
	Release  ReleaseRef
	Trackers []string
	Host     string
	Scope    string
}

// UploadReviewResult returns the review plus the opaque token required for
// execution. The token identifies one exact preparation/runtime snapshot.
type UploadReviewResult struct {
	Review UploadReview
	Token  string
}
