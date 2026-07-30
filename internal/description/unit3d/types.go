// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package unit3d builds and cleans descriptions shared by Unit3D trackers.
package unit3d

// Image describes an image extracted from a Unit3D description.
type Image struct {
	// ImgURL is the BBCode-ready image URL, which may point at a thumbnail.
	ImgURL string
	// RawURL is the direct image URL.
	RawURL string
	// WebURL is the image host's browser-facing page URL when available.
	WebURL string
	// Host is the normalized image-host name.
	Host string
}

// Note reports a non-fatal condition encountered while cleaning a Unit3D description.
type Note struct {
	// Kind categorizes the note for downstream handling.
	Kind string
	// Message contains caller-visible detail about the condition.
	Message string
}

// Report contains a cleaned Unit3D description and extracted content.
type Report struct {
	// Description is the normalized BBCode that remains after cleanup.
	Description string
	// Images contains images removed or identified during cleanup.
	Images []Image
	// Notes contains non-fatal cleanup diagnostics.
	Notes []Note
}
