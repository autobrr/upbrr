// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

// DescriptionResult contains rendered description text and optional diagnostics.
type DescriptionResult struct {
	// Group identifies the tracker-specific description override group.
	Group string
	// Description is rendered tracker markup ready for preview or payload preparation.
	Description string
}
