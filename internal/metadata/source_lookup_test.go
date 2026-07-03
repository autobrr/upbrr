// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveSourceLookupURLTracker(t *testing.T) {
	result, err := resolveSourceLookupURL("https://aither.cc/torrents/12345")
	if err != nil {
		t.Fatalf("resolve tracker url: %v", err)
	}
	if result.Mode != "tracker" {
		t.Fatalf("expected tracker mode, got %q", result.Mode)
	}
	if result.Tracker != "AITHER" {
		t.Fatalf("expected AITHER tracker, got %q", result.Tracker)
	}
	if result.TrackerID != "12345" {
		t.Fatalf("expected tracker id 12345, got %q", result.TrackerID)
	}
}

func TestResolveSourceLookupURLMedia(t *testing.T) {
	cases := []struct {
		name     string
		url      string
		provider string
		id       int
	}{
		{name: "imdb", url: "https://www.imdb.com/title/tt1234567/", provider: "imdb", id: 1234567},
		{name: "tmdb", url: "https://www.themoviedb.org/movie/765432-example-movie", provider: "tmdb", id: 765432},
		{name: "tvmaze", url: "https://www.tvmaze.com/shows/12345/example-show", provider: "tvmaze", id: 12345},
		{name: "tvdb", url: "https://thetvdb.com/series/456789", provider: "tvdb", id: 456789},
		{name: "tvdb query", url: "https://www.thetvdb.com/?tab=series&id=456790", provider: "tvdb", id: 456790},
		{name: "mal anime", url: "https://myanimelist.net/anime/54321/example-anime", provider: "mal", id: 54321},
	}

	for _, tc := range cases {
		result, err := resolveSourceLookupURL(tc.url)
		if err != nil {
			t.Fatalf("%s: resolve media url: %v", tc.name, err)
		}
		if result.Mode != "media" {
			t.Fatalf("%s: expected media mode, got %q", tc.name, result.Mode)
		}
		switch tc.provider {
		case "imdb":
			if result.IMDBID != tc.id {
				t.Fatalf("%s: expected imdb id %d, got %d", tc.name, tc.id, result.IMDBID)
			}
		case "tmdb":
			if result.TMDBID != tc.id {
				t.Fatalf("%s: expected tmdb id %d, got %d", tc.name, tc.id, result.TMDBID)
			}
		case "tvmaze":
			if result.TVmazeID != tc.id {
				t.Fatalf("%s: expected tvmaze id %d, got %d", tc.name, tc.id, result.TVmazeID)
			}
		case "tvdb":
			if result.TVDBID != tc.id {
				t.Fatalf("%s: expected tvdb id %d, got %d", tc.name, tc.id, result.TVDBID)
			}
		case "mal":
			if result.MALID != tc.id {
				t.Fatalf("%s: expected mal id %d, got %d", tc.name, tc.id, result.MALID)
			}
		}
	}
}

func TestApplySourceLookupOverrideTracker(t *testing.T) {
	meta := api.PreparedMetadata{
		SourceLookupURL: "https://aither.cc/torrents/778899",
		Trackers:        []string{"ANT", "AITHER"},
	}

	applySourceLookupOverride(&meta)

	if !meta.SourceLookupActive {
		t.Fatalf("expected source lookup to be active")
	}
	if meta.SourceLookupMode != "tracker" {
		t.Fatalf("expected tracker mode, got %q", meta.SourceLookupMode)
	}
	if got := meta.TrackerIDs["aither"]; got != "778899" {
		t.Fatalf("expected aither tracker id 778899, got %q", got)
	}
	if len(meta.Trackers) != 1 || meta.Trackers[0] != "AITHER" {
		t.Fatalf("expected tracker list to be narrowed to AITHER, got %v", meta.Trackers)
	}
}

func TestApplySourceLookupOverrideMedia(t *testing.T) {
	meta := api.PreparedMetadata{
		SourceLookupURL: "https://www.imdb.com/title/tt7654321/",
		Trackers:        []string{"AITHER"},
		TrackerIDs:      map[string]string{"aither": "101"},
	}

	applySourceLookupOverride(&meta)

	if !meta.SourceLookupActive {
		t.Fatalf("expected source lookup to be active")
	}
	if meta.SourceLookupMode != "media" {
		t.Fatalf("expected media mode, got %q", meta.SourceLookupMode)
	}
	if meta.ExternalIDOverrides.IMDBID == nil || *meta.ExternalIDOverrides.IMDBID != 7654321 {
		t.Fatalf("expected imdb override 7654321, got %#v", meta.ExternalIDOverrides.IMDBID)
	}
	if len(meta.Trackers) != 0 {
		t.Fatalf("expected trackers cleared for media url, got %v", meta.Trackers)
	}
	if len(meta.TrackerIDs) != 0 {
		t.Fatalf("expected tracker ids cleared for media url, got %v", meta.TrackerIDs)
	}
}

func TestApplySourceLookupOverrideFallbackWarning(t *testing.T) {
	meta := api.PreparedMetadata{SourceLookupURL: "notaurl"}
	applySourceLookupOverride(&meta)

	if meta.SourceLookupActive {
		t.Fatalf("expected inactive source lookup on invalid url")
	}
	if len(meta.LookupWarnings) == 0 {
		t.Fatalf("expected fallback warning for invalid source lookup url")
	}
}
