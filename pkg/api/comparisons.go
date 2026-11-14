// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// ComparisonSource is an ordered comparison column. Video paths remain private
// workflow resources; imported columns have no inferred video or processing facts.
type ComparisonSource struct {
	ID                string   `json:"id"`
	Label             string   `json:"label"`
	Fingerprint       string   `json:"fingerprint,omitempty"`
	Imported          bool     `json:"imported"`
	DurationSeconds   float64  `json:"durationSeconds,omitempty"`
	FrameRate         float64  `json:"frameRate,omitempty"`
	Width             int      `json:"width,omitempty"`
	Height            int      `json:"height,omitempty"`
	AllowedFrameTypes []string `json:"allowedFrameTypes,omitempty"`
	// PreferBFrames enables the default B/P/I policy. False preserves explicit
	// choices and plans saved before this policy was introduced.
	PreferBFrames bool `json:"preferBFrames,omitempty"`
	Overlay       bool `json:"overlay"`
}

// ComparisonCell binds one image to a row and source. Nil timing, frame and
// confidence fields mean unknown; an imported image never inherits video facts.
type ComparisonCell struct {
	SourceID            string           `json:"sourceId"`
	ArtifactID          PublicResourceID `json:"artifactId,omitempty"`
	ContentSHA256       string           `json:"contentSha256,omitempty"`
	TimestampSeconds    *float64         `json:"timestampSeconds,omitempty"`
	FrameNumber         *int64           `json:"frameNumber,omitempty"`
	ObservedPTS         *int64           `json:"observedPts,omitempty"`
	TimeBaseNumerator   int64            `json:"timeBaseNumerator,omitempty"`
	TimeBaseDenominator int64            `json:"timeBaseDenominator,omitempty"`
	PictureType         string           `json:"pictureType,omitempty"`
	Confidence          *float64         `json:"confidence,omitempty"`
	Warning             string           `json:"warning,omitempty"`
}

// ComparisonRow is one common visual moment. Exclusion is explicit and retained;
// complete rows are never inferred by zipping independent source-image lists.
type ComparisonRow struct {
	ID       string           `json:"id"`
	Excluded bool             `json:"excluded"`
	Scene    string           `json:"scene,omitempty"`
	Cells    []ComparisonCell `json:"cells"`
}

// ComparisonAnchor records an inspected manual correspondence. Anchors are hints
// for matching, not permission to extrapolate through an unverified edit.
type ComparisonAnchor struct {
	SourceID      string  `json:"sourceId"`
	MainSeconds   float64 `json:"mainSeconds"`
	SourceSeconds float64 `json:"sourceSeconds"`
}

// ComparisonPreferences requests common scenes; unmet preferences are reported.
type ComparisonPreferences struct {
	Count         int `json:"count"`
	MotionPercent int `json:"motionPercent"`
	DarkPercent   int `json:"darkPercent"`
}

// ComparisonSceneMix reports measured thumbnail selection outcomes. It does not
// establish whole-video scene coverage or calibrated luminance.
type ComparisonSceneMix struct {
	RequestedDark   int `json:"requestedDark"`
	RequestedMotion int `json:"requestedMotion"`
	AchievedDark    int `json:"achievedDark"`
	AchievedMotion  int `json:"achievedMotion"`
}

// ComparisonProblem identifies a correction required before review or hosting.
type ComparisonProblem struct {
	RowID    string `json:"rowId,omitempty"`
	SourceID string `json:"sourceId,omitempty"`
	Message  string `json:"message"`
}

// ComparisonPlan is workflow-owned comparison intent and captured row identity.
// Fingerprint binds the reviewed mapping, settings and image content. Approval
// must match that fingerprint and verified current bytes before any host call.
type ComparisonPlan struct {
	SceneMix             *ComparisonSceneMix   `json:"sceneMix,omitempty"`
	Sources              []ComparisonSource    `json:"sources"`
	Rows                 []ComparisonRow       `json:"rows"`
	MainSourceID         string                `json:"mainSourceId"`
	ReuseMainScreenshots bool                  `json:"reuseMainScreenshots"`
	Imported             bool                  `json:"imported"`
	Anchors              []ComparisonAnchor    `json:"anchors,omitempty"`
	Preferences          ComparisonPreferences `json:"preferences"`
	Problems             []ComparisonProblem   `json:"problems,omitempty"`
	Warnings             []string              `json:"warnings,omitempty"`
	Fingerprint          WorkflowFingerprint   `json:"fingerprint"`
	ApprovedFingerprint  WorkflowFingerprint   `json:"approvedFingerprint,omitempty"`
}

// Clone detaches editable rows, source settings and observation pointers from a
// retained workflow revision. Non-serializable numeric evidence returns an error.
func (p ComparisonPlan) Clone() (ComparisonPlan, error) { return cloneWorkflowValue(p) }
