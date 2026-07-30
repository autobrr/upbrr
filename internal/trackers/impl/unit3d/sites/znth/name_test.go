// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package znth

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildZNTHNameTV(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName:  "Show.S01E01.Episode.Title.1080p.WEB-DL-GRP",
		EpisodeTitle: "Episode Title",
		Identity:     api.ExternalIdentity{Category: "TV"},
		Release:      api.ReleaseInfo{Resolution: "1080p"},
	}
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	expected := "Show.S01E01.1080p.WEB-DL-GRP"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameMovieYearMismatch(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName: "Movie.2024.1080p.WEB-DL-GRP",
		Release:     api.ReleaseInfo{Year: 2024},
		Identity:    api.ExternalIdentity{Category: "MOVIE"},
		ProviderMetadata: api.SourceScopedMetadata{
			IMDB: &api.IMDBMetadata{Year: 2025},
		},
	}
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	expected := "Movie.2025.1080p.WEB-DL-GRP"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameBlankCategoryDoesNotInferTVCategory(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName:  "Show.1x01.Episode.Title.1080p.WEB-DL-GRP",
		EpisodeTitle: "Episode Title",
		SeasonInt:    1,
		EpisodeInt:   1,
		Release: api.ReleaseInfo{
			Category:   "TV",
			Resolution: "1080p",
		},
	}

	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	expected := "Show.1x01.Episode.Title.1080p.WEB-DL-GRP"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameUnknownCategoryDoesNotInferTVCategory(t *testing.T) {
	tests := []struct {
		name string
		meta api.UploadSubject
	}{
		{
			name: "external unknown",
			meta: api.UploadSubject{
				Identity: api.ExternalIdentity{Category: "animation"},
			},
		},
		{
			name: "identity absent",
			meta: api.UploadSubject{},
		},
		{
			name: "external unknown ignores release tv",
			meta: api.UploadSubject{
				Identity: api.ExternalIdentity{Category: "animation"},
			},
		},
	}

	for _, tc := range tests {
		tc.meta.ReleaseName = "Show.S01E01.2024.Episode.Title.1080p.WEB-DL-GRP"
		tc.meta.EpisodeTitle = "Episode Title"
		tc.meta.SeasonInt = 1
		tc.meta.EpisodeInt = 1
		tc.meta.Release = api.ReleaseInfo{
			Category:   "TV",
			Resolution: "1080p",
			Year:       2024,
		}
		tc.meta.ProviderMetadata = api.SourceScopedMetadata{
			IMDB: &api.IMDBMetadata{Year: 2025},
		}

		got := Profile().Site.BuildName(tc.meta, config.TrackerConfig{})
		expected := "Show.S01E01.2024.Episode.Title.1080p.WEB-DL-GRP"
		if got != expected {
			t.Fatalf("%s: expected %q, got %q", tc.name, expected, got)
		}
	}
}

func TestBuildZNTHNameExplicitMoviePreservesMovieBranchOverParsedTV(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName:  "Show.S01E01.2024.Episode.Title.1080p.WEB-DL-GRP",
		EpisodeTitle: "Episode Title",
		Identity:     api.ExternalIdentity{Category: "MOVIE"},
		SeasonInt:    1,
		EpisodeInt:   1,
		Release: api.ReleaseInfo{
			Category:   "TV",
			Resolution: "1080p",
			Year:       2024,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			IMDB: &api.IMDBMetadata{Year: 2025},
		},
	}

	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	expected := "Show.S01E01.2025.Episode.Title.1080p.WEB-DL-GRP"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameBlankCategoryDoesNotInferMovieCategory(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName: "Example.Movie.2026.1080p.WEB-DL-GRP",
		Release: api.ReleaseInfo{
			Category: "MOVIE",
			Year:     2026,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			IMDB: &api.IMDBMetadata{Year: 2027},
		},
	}

	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	expected := "Example.Movie.2026.1080p.WEB-DL-GRP"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameMovieYearMismatchNoResolutionHyphenatedTitle(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName: "Movie - Part One 2024",
		Release:     api.ReleaseInfo{Year: 2024},
		Identity:    api.ExternalIdentity{Category: "MOVIE"},
		ProviderMetadata: api.SourceScopedMetadata{
			IMDB: &api.IMDBMetadata{Year: 2025},
		},
	}
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	expected := "Movie - Part One 2025"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameMovieYearMismatchNoResolutionGroupSuffix(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName: "Movie.Title.2024-GRP2024",
		Release:     api.ReleaseInfo{Year: 2024, Group: "GRP2024"},
		Identity:    api.ExternalIdentity{Category: "MOVIE"},
		ProviderMetadata: api.SourceScopedMetadata{
			IMDB: &api.IMDBMetadata{Year: 2025},
		},
	}
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	expected := "Movie.Title.2025-GRP2024"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameTVUnicodePrefix(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName:  "\u212aShow.S01E01.Episode.Title.1080p.WEB-DL-GRP",
		EpisodeTitle: "Episode Title",
		Identity:     api.ExternalIdentity{Category: "TV"},
		Release:      api.ReleaseInfo{Resolution: "1080p"},
	}
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	expected := "\u212aShow.S01E01.1080p.WEB-DL-GRP"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameMovieYearMismatchUnicodeTitle(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName: "\u212aMovie.2024",
		Release:     api.ReleaseInfo{Year: 2024},
		Identity:    api.ExternalIdentity{Category: "MOVIE"},
		ProviderMetadata: api.SourceScopedMetadata{
			IMDB: &api.IMDBMetadata{Year: 2025},
		},
	}
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	expected := "\u212aMovie.2025"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFindZNTHTokenIndexesUnicodeBoundaries(t *testing.T) {
	got := findZNTHTokenIndexes("Title.\u212a.1080p.Source", "1080p")
	expected := len("Title.\u212a.")
	if len(got) != 1 || got[0] != expected {
		t.Fatalf("expected index %d, got %#v", expected, got)
	}

	if got := findZNTHTokenIndexes("Title.\u06611080p.Source", "1080p"); len(got) != 0 {
		t.Fatalf("expected adjacent Unicode digit prefix to reject token, got %#v", got)
	}
	if got := findZNTHTokenIndexes("Title.1080p\u0661.Source", "1080p"); len(got) != 0 {
		t.Fatalf("expected adjacent Unicode digit suffix to reject token, got %#v", got)
	}
}

func TestZNTHEmptyTokenInputs(t *testing.T) {
	name := "Show.S01E01.1080p.WEB-DL-GRP"
	if got := replaceZNTHEpisodeTitle(name, "", "1080p"); got != name {
		t.Fatalf("expected empty episode title to leave name unchanged, got %q", got)
	}
	if got := findZNTHTokenIndexes(name, " "); got != nil {
		t.Fatalf("expected empty token indexes to be nil, got %#v", got)
	}
}
