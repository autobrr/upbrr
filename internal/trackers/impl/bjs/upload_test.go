// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveRuntimePrefersMediaInfoDuration(t *testing.T) {
	root := t.TempDir()
	miPath := filepath.Join(root, "MEDIAINFO.txt")
	if err := os.WriteFile(miPath, []byte("General\nDuration                                 : 1 h 31 min\n"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	meta := api.PreparedMetadata{
		MediaInfoTextPath: miPath,
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{RuntimeMinutes: 120},
		},
	}

	if got := resolveRuntime(meta); got != 91 {
		t.Fatalf("expected MediaInfo runtime 91 minutes, got %d", got)
	}
}

func TestResolveRuntimeFallsBackToExternalMetadata(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{RuntimeMinutes: 95},
		},
	}

	if got := resolveRuntime(meta); got != 95 {
		t.Fatalf("expected IMDb runtime fallback, got %d", got)
	}
}

func TestResolveRuntimePrefersBDInfoLengthForBDMV(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType: "BDMV",
		BDInfo: map[string]any{
			"length": "01:40:00.000",
		},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{RuntimeMinutes: 120},
		},
	}

	if got := resolveRuntime(meta); got != 100 {
		t.Fatalf("expected BDInfo runtime 100 minutes, got %d", got)
	}
}

func TestBuildFieldsLimitsDirectorCreatorAndSetsRepack(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Repack: "REPACK",
		ExternalIDs: api.ExternalIDs{
			Category: "TV",
		},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{
				Creators:      []string{"Creator One", "Creator Two"},
				Directors:     []string{"Director One", "Director Two"},
				OriginCountry: []string{"BR", "US"},
				Title:         "Title",
			},
			IMDB: &api.IMDBMetadata{Year: 2026},
		},
	}

	fields := buildFields(meta, "description", "auth", nil)
	if got := fields["diretor"]; got != "Creator One" {
		t.Fatalf("expected first creator only, got %q", got)
	}
	if got := fields["diretorserie"]; got != "Director One" {
		t.Fatalf("expected first director only, got %q", got)
	}
	if got := fields["repack"]; got != "on" {
		t.Fatalf("expected repack flag, got %q", got)
	}
	if got := fields["pais"]; got != "Brasil, Estados Unidos da América" {
		t.Fatalf("expected translated countries, got %q", got)
	}
}

func TestBuildFieldsWithNilMetadata(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Release: api.ReleaseInfo{
			Title: "Test TV Title",
		},
		ExternalIDs: api.ExternalIDs{
			Category: "TV",
		},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: nil,
			IMDB: nil,
		},
	}

	// Make sure buildFields does not panic and populates fallback title from release
	fields := buildFields(meta, "description", "auth", nil)
	if got := fields["title"]; got != "Test TV Title" {
		t.Fatalf("expected title to fall back to release title, got %q", got)
	}
	if got := fields["pais"]; got != "" {
		t.Fatalf("expected empty country for nil TMDB, got %q", got)
	}
	if got := fields["numtemporadas"]; got != "0" {
		t.Fatalf("expected 0 seasons for nil IMDB, got %q", got)
	}
}

func TestResolveOverviewPrefersEpisodeOverviewForTV(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{
			Category: "TV",
		},
	}
	ptBR := api.TMDBLocalizedData{
		Overview:        "Series Overview",
		EpisodeOverview: "Episode Overview",
	}

	if got := resolveOverview(meta, ptBR); got != "Episode Overview" {
		t.Fatalf("expected episode overview, got %q", got)
	}

	// For Movie, it should prefer the series overview
	metaMovie := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{
			Category: "MOVIE",
		},
	}
	if got := resolveOverview(metaMovie, ptBR); got != "Series Overview" {
		t.Fatalf("expected series overview for movie, got %q", got)
	}
}
