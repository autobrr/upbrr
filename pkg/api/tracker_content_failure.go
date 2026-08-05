// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// TrackerContentFailureCode identifies one sanitized shared-content failure.
type TrackerContentFailureCode string

const (
	TrackerContentFailureScreenshotPreparation  TrackerContentFailureCode = "screenshot_preparation_failed"
	TrackerContentFailureDescriptionPreparation TrackerContentFailureCode = "description_preparation_failed"
	TrackerContentFailureImageHostUnavailable   TrackerContentFailureCode = "image_host_unavailable"
)

// TrackerContentFailure is sanitized tracker-scoped evidence that a required
// shared upload-content object failed before its adapter could run.
type TrackerContentFailure struct {
	Tracker string                    `json:"tracker"`
	Code    TrackerContentFailureCode `json:"code"`
	Message string                    `json:"message"`
}
