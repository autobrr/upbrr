// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// InputHistory contains the retained source evidence needed to address a track
// correction without repeating a media probe. Preparation revalidates it.
type InputHistory struct {
	Release           ReleaseRef
	SourceFingerprint string
	Corrections       ReleaseCorrectionsSnapshot
	Tracks            []MediaTrackFacts
}
