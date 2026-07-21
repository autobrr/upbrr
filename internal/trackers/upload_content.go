// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import "github.com/autobrr/upbrr/pkg/api"

// UploadContentMode identifies the shared content object a tracker consumes
// before its tracker-local upload adapter is invoked.
type UploadContentMode string

const (
	// UploadContentModeNone bypasses shared description and image preparation.
	UploadContentModeNone UploadContentMode = "none"
	// UploadContentModeScreenshots consumes shared screenshot and menu-image assets only.
	UploadContentModeScreenshots UploadContentMode = "screenshots"
	// UploadContentModeDescription consumes the aggregate description and image object.
	UploadContentModeDescription UploadContentMode = "description"
)

// Valid reports whether mode is one of the supported semantic workflow shapes.
func (m UploadContentMode) Valid() bool {
	switch m {
	case UploadContentModeNone, UploadContentModeScreenshots, UploadContentModeDescription:
		return true
	default:
		return false
	}
}

// UsesDescription reports whether shared description construction is required.
func (m UploadContentMode) UsesDescription() bool { return m == UploadContentModeDescription }

// UsesImages reports whether shared screenshot or menu-image preparation is required.
func (m UploadContentMode) UsesImages() bool {
	return m == UploadContentModeScreenshots || m == UploadContentModeDescription
}

// FailureReasonCode returns the canonical eligibility reason for a failed
// associated content object.
func (m UploadContentMode) FailureReasonCode() api.TrackerEligibilityReasonCode {
	switch m {
	case UploadContentModeNone:
		return ""
	case UploadContentModeScreenshots:
		return api.TrackerEligibilityScreenshotPreparationFailed
	case UploadContentModeDescription:
		return api.TrackerEligibilityDescriptionPreparationFailed
	default:
		return ""
	}
}

// UploadContentModeProvider declares a tracker's shared upload-content workflow.
type UploadContentModeProvider interface {
	// UploadContentMode returns the tracker-owned semantic content mode.
	UploadContentMode() UploadContentMode
}
