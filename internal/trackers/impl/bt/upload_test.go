// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

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

	// For lowercase tv category, it should still prefer the episode overview
	metaTVLower := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{
			Category: "tv",
		},
	}
	if got := resolveOverview(metaTVLower, ptBR); got != "Episode Overview" {
		t.Fatalf("expected episode overview for lowercase tv, got %q", got)
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

func TestResolveTagsPreservesUnknownGenres(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Release: api.ReleaseInfo{
			Genre: "Sci-Fi,MyCustomGenre",
		},
	}
	ptBR := api.TMDBLocalizedData{}

	// Sci-Fi maps to "ficção científica", MyCustomGenre does not map but should be preserved
	got := resolveTags(meta, ptBR)
	expected := "ficcao.cientifica, mycustomgenre"
	if got != expected {
		t.Fatalf("expected tags %q, got %q", expected, got)
	}
}

func TestBuildDescriptionOmitsBlankLocalizedEpisodeTitleRow(t *testing.T) {
	t.Parallel()

	description := buildDescription(trackers.UploadRequest{
		Meta: api.PreparedMetadata{
			ExternalMetadata: api.ExternalMetadata{
				TMDB: &api.TMDBMetadata{
					Localized: map[string]api.TMDBLocalizedData{
						"pt-BR": {EpisodeOverview: "Resumo do episodio"},
					},
				},
			},
		},
	}, trackers.DescriptionAssets{})

	if strings.Contains(description, "[center][/center]") {
		t.Fatalf("expected no blank centered title row, got %q", description)
	}
	if !strings.Contains(description, "[center]Resumo do episodio[/center]") {
		t.Fatalf("expected localized episode overview, got %q", description)
	}
}

func TestBuildDescriptionKeepsLocalizedEpisodeTitleWhenPresent(t *testing.T) {
	t.Parallel()

	description := buildDescription(trackers.UploadRequest{
		Meta: api.PreparedMetadata{
			ExternalMetadata: api.ExternalMetadata{
				TMDB: &api.TMDBMetadata{
					Localized: map[string]api.TMDBLocalizedData{
						"pt-BR": {
							EpisodeTitle:    "Titulo do episodio",
							EpisodeOverview: "Resumo do episodio",
						},
					},
				},
			},
		},
	}, trackers.DescriptionAssets{})

	if !strings.Contains(description, "[center]Titulo do episodio[/center]") {
		t.Fatalf("expected localized episode title, got %q", description)
	}
	if !strings.Contains(description, "[center]Resumo do episodio[/center]") {
		t.Fatalf("expected localized episode overview, got %q", description)
	}
}

func TestBuildDescriptionTreatsWhitespaceLocalizedEpisodeTitleAsEmpty(t *testing.T) {
	t.Parallel()

	description := buildDescription(trackers.UploadRequest{
		Meta: api.PreparedMetadata{
			ExternalMetadata: api.ExternalMetadata{
				TMDB: &api.TMDBMetadata{
					Localized: map[string]api.TMDBLocalizedData{
						"pt-BR": {
							EpisodeTitle:    " \t ",
							EpisodeOverview: "Resumo do episodio",
						},
					},
				},
			},
		},
	}, trackers.DescriptionAssets{})

	if strings.Contains(description, "[center][/center]") {
		t.Fatalf("expected no blank centered title row, got %q", description)
	}
	if !strings.Contains(description, "[center]Resumo do episodio[/center]") {
		t.Fatalf("expected localized episode overview, got %q", description)
	}
}

func TestResolveVideoCodecMapsH264H265(t *testing.T) {
	t.Parallel()

	tests := []struct {
		codec    string
		isHDR    bool
		expected string
	}{
		{codec: "H264", isHDR: false, expected: "x264"},
		{codec: "H265", isHDR: false, expected: "x265"},
		{codec: "H265", isHDR: true, expected: "x265 HDR"},
		{codec: "hevc", isHDR: false, expected: "x265"},
		{codec: "avc", isHDR: false, expected: "x264"},
		{codec: "OtherCodec", isHDR: false, expected: "OtherCodec"},
	}

	for _, tc := range tests {
		t.Run(tc.codec, func(t *testing.T) {
			meta := api.PreparedMetadata{
				VideoCodec: tc.codec,
			}
			if tc.isHDR {
				meta.HDR = "HDR10"
			}
			got := resolveVideoCodec(meta)
			if got != tc.expected {
				t.Fatalf("expected %q for codec %q (HDR=%t), got %q", tc.expected, tc.codec, tc.isHDR, got)
			}
		})
	}
}

func TestParseMediaInfoDurationMinutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		expected int
	}{
		{
			name:     "valid hours and minutes",
			content:  "duration : 2 h 15 m",
			expected: 135,
		},
		{
			name:     "valid milliseconds",
			content:  "duration : 120000",
			expected: 2,
		},
		{
			name:     "empty fields (potential panic)",
			content:  "duration :    ",
			expected: 0,
		},
		{
			name:     "no duration keyword",
			content:  "something else",
			expected: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMediaInfoDurationMinutes(tc.content)
			if got != tc.expected {
				t.Fatalf("expected %d, got %d", tc.expected, got)
			}
		})
	}
}
