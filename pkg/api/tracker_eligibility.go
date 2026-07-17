// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// TrackerEligibilityReasonCode identifies one canonical tracker blocker.
type TrackerEligibilityReasonCode string

const (
	TrackerEligibilityDuplicate                    TrackerEligibilityReasonCode = "duplicate"
	TrackerEligibilityBlockingRule                 TrackerEligibilityReasonCode = "blocking_rule"
	TrackerEligibilityPolicy                       TrackerEligibilityReasonCode = "policy"
	TrackerEligibilityAuthRequired                 TrackerEligibilityReasonCode = "auth_required"
	TrackerEligibilityAssessmentSkipped            TrackerEligibilityReasonCode = "assessment_skipped"
	TrackerEligibilityAssessmentFailed             TrackerEligibilityReasonCode = "assessment_failed"
	TrackerEligibilityBannedGroup                  TrackerEligibilityReasonCode = "banned_group"
	TrackerEligibilityScreenshotPreparationFailed  TrackerEligibilityReasonCode = "screenshot_preparation_failed"
	TrackerEligibilityDescriptionPreparationFailed TrackerEligibilityReasonCode = "description_preparation_failed"
)

// TrackerEligibilityReason is safe presentation data for one blocker.
type TrackerEligibilityReason struct {
	Code    TrackerEligibilityReasonCode
	Message string
}

// TrackerContentFailure is sanitized tracker-scoped evidence that a required
// shared upload-content object failed before its adapter could run.
type TrackerContentFailure struct {
	Tracker string                       `json:"tracker"`
	Code    TrackerEligibilityReasonCode `json:"code"`
	Message string                       `json:"message"`
}

// TrackerReviewChoices records explicit review authorizations for one tracker.
type TrackerReviewChoices struct {
	IgnoreDuplicate    bool
	IgnoreRuleFailures bool
}

// TrackerEligibilityAssessment is Core input gathered from current exact-
// generation policy, duplicate, auth, and dry-run assessment.
type TrackerEligibilityAssessment struct {
	Tracker        string
	Duplicate      DupeCheckResult
	RuleFailures   []RuleFailure
	PolicyBlocks   []TrackerBlockReason
	AuthRequired   bool
	Banned         bool
	BannedReason   string
	ContentFailure *TrackerContentFailure
	Choices        TrackerReviewChoices
}

// TrackerEligibilityInput requests ordered eligibility for an exact release.
type TrackerEligibilityInput struct {
	Release          ReleaseRef
	SelectedTrackers []string
	Assessments      []TrackerEligibilityAssessment
}

// TrackerEligibilityState is one selected tracker's canonical decision.
type TrackerEligibilityState struct {
	Tracker  string
	Eligible bool
	Reasons  []TrackerEligibilityReason
}

// TrackerEligibility is the ordered canonical decision for one exact release.
type TrackerEligibility struct {
	Release          ReleaseRef
	Trackers         []TrackerEligibilityState
	EligibleTrackers []string
}
