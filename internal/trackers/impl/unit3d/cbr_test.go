// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildCBRName(t *testing.T) {
	tests := []struct {
		name      string
		meta      api.PreparedMetadata
		customTag string
		want      string
	}{
		{
			name: "Basic movie",
			meta: api.PreparedMetadata{
				ReleaseName: "Movie 2023 1080p WEB-DL DDP5.1 H.264-GRP",
				Release:     api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				Tag:         "GRP",
			},
			want: "Movie 2023 1080p WEB-DL DDP5.1 H.264-GRP",
		},
		{
			name: "Portuguese DUAL",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie 2023 1080p WEB-DL H.264-GRP",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"English", "Portuguese"},
				Tag:            "GRP",
			},
			want: "Movie 2023 1080p WEB-DL H.264 DUAL-GRP",
		},
		{
			name: "Custom tag with original group (same as internal)",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie 2023 1080p WEB-DL H.264-CBR",
				Filename:       "Movie.2023.1080p.WEB-DL.H.264-GRP.DUAL.mkv",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"English", "Portuguese"},
				Tag:            "CBR",
			},
			customTag: "-CBR",
			want:      "Movie 2023 1080p WEB-DL H.264 DUAL-CBR",
		},
		{
			name: "Brazilian PT override",
			meta: api.PreparedMetadata{
				ReleaseName: "A Foreign Movie 2023 1080p WEB-DL H.264-GRP",
				Release: api.ReleaseInfo{
					Title: "A Foreign Movie",
					Year:  2023,
					Group: "GRP",
				},
				ExternalIDs: api.ExternalIDs{
					Category: "MOVIE",
				},
				ExternalMetadata: api.ExternalMetadata{
					TMDB: &api.TMDBMetadata{
						OriginalLanguage: "pt",
						RetrievedAKA:     "Filme Brasileiro AKA",
					},
				},
				Tag: "-GRP",
			},
			want: "Filme Brasileiro 2023 1080p WEB-DL H.264-GRP",
		},
		{
			name: "TV year stripping",
			meta: api.PreparedMetadata{
				ReleaseName: "Show Series 2023 S01E01 1080p WEB-DL-GRP",
				Release:     api.ReleaseInfo{Title: "Show Series", Year: 2023, Group: "GRP"},
				ExternalIDs: api.ExternalIDs{Category: "TV"},
				Tag:         "GRP",
			},
			want: "Show Series S01E01 1080p WEB-DL-GRP",
		},
		{
			name: "Anime year stripping",
			meta: api.PreparedMetadata{
				ReleaseName: "Anime Movie (2023) 1080p-GRP",
				Release:     api.ReleaseInfo{Title: "Anime Movie", Year: 2023, Group: "GRP"},
				Anime:       true,
				Tag:         "GRP",
			},
			want: "Anime Movie 1080p-GRP",
		},
		{
			name: "Non-pt AKA removal (with spaces)",
			meta: api.PreparedMetadata{
				ReleaseName: "Title AKA Some AKA 2023 1080p -GRP",
				Release:     api.ReleaseInfo{Title: "Title", Year: 2023, Group: "GRP"},
				ExternalMetadata: api.ExternalMetadata{
					TMDB: &api.TMDBMetadata{
						OriginalLanguage: "en",
						RetrievedAKA:     "Some AKA",
					},
				},
				Tag: "-GRP",
			},
			want: "Title AKA 2023 1080p -GRP",
		},
		{
			name: "Portuguese MULTI",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie 2023 1080p WEB-DL H.264-GRP",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"English", "Portuguese", "French"},
				Tag:            "GRP",
			},
			want: "Movie 2023 1080p WEB-DL H.264 MULTI-GRP",
		},
		{
			name: "Disc type bypass",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie 2023 1080p BluRay REMUX AVC-GRP",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				DiscType:       "BDMV",
				AudioLanguages: []string{"English", "Portuguese"},
				Tag:            "GRP",
			},
			want: "Movie 2023 1080p BluRay REMUX AVC-GRP",
		},
		{
			name: "Custom tag with original group from filename",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie 2023 1080p WEB-DL-CBR",
				Filename:       "Movie.2023.1080p.WEB-DL-ORIGGRP.DUAL.mkv",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "NEWGRP"},
				AudioLanguages: []string{"English", "Portuguese"},
				Tag:            "CBR",
			},
			customTag: "-CBR",
			want:      "Movie 2023 1080p WEB-DL-ORIGGRP DUAL-CBR",
		},
		{
			name: "Custom tag and same group from filename",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie 2023 1080p WEB-DL-CBR",
				Filename:       "Movie.2023.1080p.WEB-DL-GRP.DUAL.mkv",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"English", "Portuguese"},
				Tag:            "CBR",
			},
			customTag: "-CBR",
			want:      "Movie 2023 1080p WEB-DL DUAL-CBR",
		},
		{
			name: "No group suffix",
			meta: api.PreparedMetadata{
				ReleaseName: "Movie 2023 1080p WEB-DL-NoGrp",
				Release:     api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
			},
			want: "Movie 2023 1080p WEB-DL-NoGroup",
		},
		{
			name: "Blank audio entries with one usable language",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie 2023 1080p WEB-DL H.264-GRP",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"", "Portuguese", "   "},
				Tag:            "GRP",
			},
			want: "Movie 2023 1080p WEB-DL H.264-GRP",
		},
		{
			name: "Blank audio entries with two usable languages",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie 2023 1080p WEB-DL H.264-GRP",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"English", "", "Portuguese", "   "},
				Tag:            "GRP",
			},
			want: "Movie 2023 1080p WEB-DL H.264 DUAL-GRP",
		},
		{
			name: "Blank audio entries with three usable languages",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie 2023 1080p WEB-DL H.264-GRP",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"English", "", "Portuguese", "   ", "French"},
				Tag:            "GRP",
			},
			want: "Movie 2023 1080p WEB-DL H.264 MULTI-GRP",
		},
		{
			name: "Custom tag substring in title / source must not inject filename group",
			meta: api.PreparedMetadata{
				ReleaseName:    "CBR Movie 2023 1080p WEB-DL H.264-GRP",
				Filename:       "CBR.Movie.2023.1080p.WEB-DL-ORIGGRP.DUAL.mkv",
				Release:        api.ReleaseInfo{Title: "CBR Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"English", "Portuguese"},
				Tag:            "GRP",
			},
			customTag: "-CBR",
			want:      "CBR Movie 2023 1080p WEB-DL H.264 DUAL-GRP",
		},
		{
			name: "Real configured custom suffix should still inject filename group",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie 2023 1080p WEB-DL H.264-CBR",
				Filename:       "Movie.2023.1080p.WEB-DL-ORIGGRP.DUAL.mkv",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"English", "Portuguese"},
				Tag:            "CBR",
			},
			customTag: "-CBR",
			want:      "Movie 2023 1080p WEB-DL H.264-ORIGGRP DUAL-CBR",
		},
		{
			name: "Dubbed inputs with Portuguese replacement eligibility",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie Dubbed 2023 1080p WEB-DL-GRP",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"English", "Portuguese"},
				Tag:            "GRP",
			},
			want: "Movie 2023 1080p WEB-DL DUAL-GRP",
		},
		{
			name: "Dubbed inputs without Portuguese replacement eligibility",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie Dubbed 2023 1080p WEB-DL-GRP",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"English"},
				Tag:            "GRP",
			},
			want: "Movie Dubbed 2023 1080p WEB-DL-GRP",
		},
		{
			name: "Dual-Audio inputs with Portuguese replacement eligibility",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie Dual-Audio 2023 1080p WEB-DL-GRP",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"English", "Portuguese"},
				Tag:            "GRP",
			},
			want: "Movie 2023 1080p WEB-DL DUAL-GRP",
		},
		{
			name: "Dual-Audio inputs without Portuguese replacement eligibility",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie Dual-Audio 2023 1080p WEB-DL-GRP",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"English"},
				Tag:            "GRP",
			},
			want: "Movie Dual-Audio 2023 1080p WEB-DL-GRP",
		},
		{
			name: "Existing DD / DDP / AAC / FLAC normalization",
			meta: api.PreparedMetadata{
				ReleaseName:    "Movie DD+ 5.1 DD 5.1 AAC 2.0 FLAC 2.0-GRP",
				Release:        api.ReleaseInfo{Title: "Movie", Year: 2023, Group: "GRP"},
				AudioLanguages: []string{"English"},
				Tag:            "GRP",
			},
			want: "Movie DDP5.1 DD5.1 AAC2.0 FLAC2.0-GRP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildCBRName(tt.meta, tt.customTag); got != tt.want {
				t.Errorf("BuildCBRName() = %q, want %q", got, tt.want)
			}
		})
	}
}
