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
}

// UploadSubjectInput contains workflow upload choices for one exact prepared generation.
type UploadSubjectInput struct {
	Release               ReleaseRef
	Trackers              []string
	IgnoreDupesFor        []string
	SkipDuplicateCheck    bool
	SkipDuplicateAsActual bool
	DoubleDuplicateCheck  bool
	QuestionnaireAnswers  map[string]map[string]string
	TrackerIDOverrides    map[string]string
	DescriptionGroups     []DescriptionBuilderGroup
	// DescriptionOverride is explicit description text supplied directly by
	// the caller, before tracker-scoped group selection.
	DescriptionOverride string
	// DescriptionGroupsFinal reports that DescriptionGroups contains retained
	// workflow output and no later description stage can add required content.
	DescriptionGroupsFinal bool
	TrackerConfigOverrides TrackerConfigOverrides
	TrackerSiteOverrides   TrackerSiteOverrides
	ClientOverrides        ClientOverrides
	ImageHostOverrides     ImageHostOverrides
	ScreenshotOverrides    ScreenshotOverrides
	TorrentOverrides       TorrentOverrides
	Options                UploadOptions
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
	Release       ReleaseRef
	Trackers      []string
	Host          string
	Scope         string
	ExcludedHosts []string
}
