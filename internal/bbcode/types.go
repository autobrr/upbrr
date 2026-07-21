// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package bbcode provides shared BBCode cleanup and normalization primitives.
package bbcode

// Image describes an image extracted while cleaning a BBCode description.
type Image struct {
	// ImgURL is the BBCode-ready image URL, which may point at a thumbnail.
	ImgURL string
	// RawURL is the direct image URL.
	RawURL string
	// WebURL is the image host's browser-facing page URL when available.
	WebURL string
	// Host is the normalized image-host name, such as "imgbb" or "pixhost".
	Host string
}

// Note reports a non-fatal condition encountered while cleaning a description.
type Note struct {
	// Kind categorizes the note for downstream handling.
	Kind string
	// Message contains caller-visible detail about the condition.
	Message string
}

// Artifact contains auxiliary content extracted from a description.
type Artifact struct {
	// Name is the suggested artifact filename.
	Name string
	// Kind categorizes the extracted artifact.
	Kind string
	// Content is the extracted artifact body.
	Content string
}

// Report contains a cleaned description and content extracted during cleanup.
type Report struct {
	// Description is the normalized BBCode that remains after cleanup.
	Description string
	// Images contains images removed or identified during cleanup.
	Images []Image
	// Notes contains non-fatal cleanup diagnostics.
	Notes []Note
	// Artifacts contains auxiliary files extracted from the description.
	Artifacts []Artifact
}
