// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
)

// DataLookupResult contains tracker-derived metadata for one release.
type DataLookupResult struct {
	// TrackerID is the tracker-side torrent or release identifier.
	TrackerID string
	// InfoHash is the normalized BitTorrent info hash when supplied by the tracker.
	InfoHash string
	// TMDBID is the resolved TMDB identifier.
	TMDBID int
	// IMDBID is the numeric portion of the resolved IMDb identifier.
	IMDBID int
	// TVDBID is the resolved TVDB identifier.
	TVDBID int
	// MALID is the resolved MyAnimeList identifier.
	MALID int
	// Category is the normalized tracker category.
	Category string
	// Description is tracker-provided BBCode after applicable cleanup.
	Description string
	// Images contains images extracted from Description.
	Images []bbcode.Image
	// Validated contains extracted images that passed remote URL validation.
	Validated []bbcode.Image
	// FileName is the tracker-provided torrent or release filename.
	FileName string
}

// HasIDs reports whether the result contains any supported external metadata ID.
func (r DataLookupResult) HasIDs() bool {
	return r.TMDBID != 0 || r.IMDBID != 0 || r.TVDBID != 0 || r.MALID != 0
}

// HasData reports whether the result contains any usable metadata or release identity.
func (r DataLookupResult) HasData() bool {
	return r.HasIDs() || strings.TrimSpace(r.Description) != "" || len(r.Images) > 0 || len(r.Validated) > 0 || strings.TrimSpace(r.InfoHash) != "" ||
		strings.TrimSpace(r.FileName) != "" ||
		strings.TrimSpace(r.Category) != ""
}
