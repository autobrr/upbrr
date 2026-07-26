// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "time"

// BlurayCandidateSelection records a candidate selected from retained preparation authority.
type BlurayCandidateSelection struct {
	ReleaseID string             `json:"releaseId"`
	Release   ReleaseSnapshotRef `json:"release"`
}

// WorkflowResourceRef is an opaque reference to one owner-scoped staged resource.
type WorkflowResourceRef struct {
	ID          WorkflowResourceID `json:"id"`
	ContentType string             `json:"contentType,omitempty"`
	SizeBytes   int64              `json:"sizeBytes,omitempty"`
}

// MediaCaptureRequirement is one tracker-derived final-media requirement.
type MediaCaptureRequirement struct {
	TrackerID       TrackerID         `json:"trackerId"`
	ScreenshotCount int               `json:"screenshotCount"`
	DVDMenuCount    int               `json:"dvdMenuCount"`
	Purpose         ScreenshotPurpose `json:"purpose"`
}

// MediaPlan is the safe page-facing plan for one exact workflow revision.
type MediaPlan struct {
	ID                  MediaPlanID                    `json:"id"`
	WorkflowID          WorkflowID                     `json:"workflowId"`
	Revision            WorkflowRevision               `json:"revision"`
	Release             ReleaseSnapshotRef             `json:"release"`
	ProjectionSet       TrackerReleaseProjectionSetRef `json:"projectionSet"`
	DurationSeconds     float64                        `json:"durationSeconds"`
	FrameRate           float64                        `json:"frameRate"`
	DiscType            string                         `json:"discType,omitempty"`
	SuggestedSelections []ScreenshotSelection          `json:"suggestedSelections,omitempty"`
	Requirements        []MediaCaptureRequirement      `json:"requirements,omitempty"`
	ExistingArtifacts   []MediaArtifact                `json:"existingArtifacts,omitempty"`
	CreatedAt           time.Time                      `json:"createdAt" ts_type:"string"`
}

// FramePreview is one non-authoritative opaque frame preview.
type FramePreview struct {
	ID               PublicResourceID   `json:"id"`
	WorkflowID       WorkflowID         `json:"workflowId"`
	WorkflowRevision WorkflowRevision   `json:"workflowRevision"`
	Release          ReleaseSnapshotRef `json:"release"`
	TimestampSeconds float64            `json:"timestampSeconds"`
	ContentURL       string             `json:"contentUrl"`
	ExpiresAt        time.Time          `json:"expiresAt" ts_type:"string"`
}

// MediaAttachment describes staged private bytes to attach to retained workflow media.
type MediaAttachment struct {
	Resource WorkflowResourceRef `json:"resource"`
	Kind     MediaArtifactKind   `json:"kind"`
	Purpose  ScreenshotPurpose   `json:"purpose"`
	Order    int                 `json:"order,omitempty"`
}

// HostedImageAttempt is the absolute outcome of one host attempt for exact media lineage.
type HostedImageAttempt struct {
	ID          PublicResourceID    `json:"id"`
	Media       MediaArtifactSetRef `json:"media"`
	Host        string              `json:"host"`
	UsageScope  string              `json:"usageScope,omitempty"`
	TrackerIDs  []TrackerID         `json:"trackerIds,omitempty"`
	Fallback    bool                `json:"fallback"`
	Status      StageStatus         `json:"status"`
	ArtifactIDs []PublicResourceID  `json:"artifactIds"`
	Results     []MediaArtifact     `json:"results,omitempty"`
	Failures    []WorkflowFailure   `json:"failures,omitempty"`
	AttemptedAt time.Time           `json:"attemptedAt" ts_type:"string"`
}

// DescriptionOverrideMutation changes one exact description group only.
type DescriptionOverrideMutation struct {
	Descriptions DescriptionSetRef `json:"descriptions"`
	GroupKey     string            `json:"groupKey"`
	Source       string            `json:"source"`
}

// ClientInjectionOutcome records client injection independently from tracker submission.
type ClientInjectionOutcome struct {
	Status   StageStatus       `json:"status"`
	Message  string            `json:"message,omitempty"`
	Failures []WorkflowFailure `json:"failures,omitempty"`
}

// TrackerDryRunReport is one safe tracker-scoped dry-run outcome.
type TrackerDryRunReport struct {
	TrackerID           TrackerID              `json:"trackerId"`
	DisplayName         string                 `json:"displayName"`
	UploadReleaseName   string                 `json:"uploadReleaseName"`
	Status              StageStatus            `json:"status"`
	Endpoint            string                 `json:"endpoint,omitempty"`
	Fields              []UploadPlanField      `json:"fields,omitempty"`
	Files               []UploadPlanFile       `json:"files,omitempty"`
	PreparedOperationID PublicResourceID       `json:"preparedOperationId,omitempty"`
	TorrentArtifactID   PublicResourceID       `json:"torrentArtifactId,omitempty"`
	TorrentFingerprint  WorkflowFingerprint    `json:"torrentFingerprint,omitempty"`
	SemanticFingerprint WorkflowFingerprint    `json:"semanticFingerprint"`
	ClientInjection     ClientInjectionOutcome `json:"clientInjection"`
	Warnings            []string               `json:"warnings,omitempty"`
	Failures            []WorkflowFailure      `json:"failures,omitempty"`
}

// UploadDryRunResult retains one report per applicable tracker.
type UploadDryRunResult struct {
	ID               UploadDryRunResultID           `json:"id"`
	WorkflowID       WorkflowID                     `json:"workflowId"`
	Revision         WorkflowRevision               `json:"revision"`
	ProjectionSet    TrackerReleaseProjectionSetRef `json:"projectionSet"`
	Dupes            DupeAssessmentRef              `json:"dupes"`
	TrackerApproval  *TrackerApprovalSnapshotRef    `json:"trackerApproval,omitempty"`
	Media            MediaArtifactSetRef            `json:"media"`
	Descriptions     DescriptionSetRef              `json:"descriptions"`
	InputFingerprint WorkflowFingerprint            `json:"inputFingerprint"`
	NoSeed           bool                           `json:"noSeed"`
	TrackerIDs       []TrackerID                    `json:"trackerIds"`
	Reports          []TrackerDryRunReport          `json:"reports"`
	SucceededCount   int                            `json:"succeededCount"`
	FailedCount      int                            `json:"failedCount"`
	SkippedCount     int                            `json:"skippedCount"`
	Status           StageStatus                    `json:"status"`
	CreatedAt        time.Time                      `json:"createdAt" ts_type:"string"`
}

// FailedTrackerRetryRef targets only failed trackers from one exact prior result.
type FailedTrackerRetryRef struct {
	Result     UploadResultRef `json:"result"`
	TrackerIDs []TrackerID     `json:"trackerIds"`
}

// ClientInjectionRetryRef targets failed client effects from one exact prior
// result without granting tracker-submission authority.
type ClientInjectionRetryRef struct {
	Result     UploadResultRef `json:"result"`
	TrackerIDs []TrackerID     `json:"trackerIds"`
}
