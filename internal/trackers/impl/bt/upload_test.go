// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveOverviewUsesScopedTVOverviewOnlyForEpisodeOrSeasonPack(t *testing.T) {
	t.Parallel()

	ptBR := api.TMDBLocalizedData{
		Overview:        "Series Overview",
		EpisodeOverview: "Episode Overview",
	}

	tests := []struct {
		name string
		meta api.PreparedMetadata
		want string
	}{
		{
			name: "episode upload uses episode overview",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: "TV"},
				SeasonInt:   1,
				EpisodeInt:  2,
			},
			want: "Episode Overview",
		},
		{
			name: "lowercase episode upload uses episode overview",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: "tv"},
				SeasonInt:   1,
				EpisodeInt:  2,
			},
			want: "Episode Overview",
		},
		{
			name: "season pack uses season overview from episode field",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: "TV"},
				SeasonInt:   1,
				TVPack:      true,
			},
			want: "Episode Overview",
		},
		{
			name: "series upload uses title overview",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: "TV"},
			},
			want: "Series Overview",
		},
		{
			name: "movie ignores episode overview",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
				SeasonInt:   1,
				EpisodeInt:  2,
			},
			want: "Series Overview",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := resolveOverview(tc.meta, ptBR); got != tc.want {
				t.Fatalf("expected overview %q, got %q", tc.want, got)
			}
		})
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
			name:     "duration string label",
			content:  "Duration/String : 2 h 5 min 2 s",
			expected: 125,
		},
		{
			name:     "duration string1 label",
			content:  "\tDuRaTiOn/String1   : 2 h 5 min 2 s",
			expected: 125,
		},
		{
			name:     "duration spaced slash label",
			content:  "Duration / String2 : 2 hrs 5 mins 2 secs",
			expected: 125,
		},
		{
			name:     "duration string3 colon label",
			content:  "Duration/String3 : 02:05:02.000",
			expected: 125,
		},
		{
			name:     "iso duration value",
			content:  "Duration/String : PT2H5M2S",
			expected: 125,
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
			name:     "invalid string1 skips to later duration",
			content:  "Duration/String1 : not a duration\nDuration/String2 : 01:30:00.000",
			expected: 90,
		},
		{
			name:     "duration string4 remains ignored",
			content:  "Duration/String4 : 01:30:00.000",
			expected: 0,
		},
		{
			name:     "adjacent duration fields remain ignored",
			content:  "Source_Duration : 01:30:00.000\nDuration_Start : 01:30:00.000",
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

func TestResolveRuntimeFallsBackWhenMediaInfoDurationInvalid(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		DVDVOBMediaInfoText: "Duration/String : not a duration",
		DiscType:            "BDMV",
		BDInfo: map[string]any{
			"length": "01:30:00.000",
		},
	}

	if got := resolveRuntime(meta); got != 90 {
		t.Fatalf("expected BDInfo runtime fallback, got %d", got)
	}
}
