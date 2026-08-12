// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNameAppliesAitherNamingGuide(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "foreign language precedes cut and release modifiers",
			meta: api.UploadSubject{
				Type:           "WEBDL",
				Source:         "Web",
				Audio:          "DD+ 5.1",
				AudioLanguages: []string{"Japanese"},
				Edition:        "Extended",
				WebDV:          true,
				Repack:         "REPACK",
				Tag:            "-GRP",
				ReleaseName:    "Example Release 2026 Extended Hybrid REPACK 1080p EXM WEB-DL DD+ 5.1 H.264-GRP",
				Release: api.ReleaseInfo{
					Resolution: "1080p",
				},
			},
			want: "Example Release 2026 JAPANESE Extended Hybrid REPACK 1080p EXM WEB-DL DD+ 5.1 H.264-GRP",
		},
		{
			name: "actual English audio removes parsed foreign marker",
			meta: api.UploadSubject{
				Type:           "WEBDL",
				Source:         "Web",
				Audio:          "DD+ 5.1",
				AudioLanguages: []string{"English"},
				Tag:            "-GRP",
				ReleaseName:    "Example Release 2026 FRENCH 1080p EXM WEB-DL DD+ 5.1 H.264-GRP",
				Release: api.ReleaseInfo{
					Resolution: "1080p",
					Language:   []string{"French"},
				},
			},
			want: "Example Release 2026 1080p EXM WEB-DL DD+ 5.1 H.264-GRP",
		},
		{
			name: "no linguistic content uses ZXX",
			meta: api.UploadSubject{
				Type:           "REMUX",
				Source:         "BluRay",
				Audio:          "FLAC 2.0",
				AudioLanguages: []string{"ZXX"},
				VideoCodec:     "AVC",
				Tag:            "-GRP",
				ReleaseName:    "Example Release 2026 1080p BluRay REMUX AVC FLAC 2.0-GRP",
				Release: api.ReleaseInfo{
					Resolution: "1080p",
				},
			},
			want: "Example Release 2026 ZXX 1080p BluRay REMUX AVC FLAC 2.0-GRP",
		},
		{
			name: "single multilingual track uses full marker",
			meta: api.UploadSubject{
				Type:           "WEBDL",
				Source:         "Web",
				Audio:          "AAC 2.0",
				AudioLanguages: []string{"Multiple Languages"},
				Tag:            "-GRP",
				ReleaseName:    "Example Release 2026 1080p EXM WEB-DL AAC 2.0 H.264-GRP",
				Release: api.ReleaseInfo{
					Resolution: "1080p",
				},
			},
			want: "Example Release 2026 MULTIPLE LANGUAGES 1080p EXM WEB-DL AAC 2.0 H.264-GRP",
		},
		{
			name: "original foreign and English tracks retain Dual-Audio",
			meta: api.UploadSubject{
				Type:           "WEBDL",
				Source:         "Web",
				Audio:          "Dual-Audio AAC 2.0",
				AudioLanguages: []string{"Japanese", "English"},
				Tag:            "-GRP",
				ReleaseName:    "Example Release 2026 1080p EXM WEB-DL Dual-Audio AAC 2.0 H.264-GRP",
				Release: api.ReleaseInfo{
					Resolution: "1080p",
				},
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
				},
			},
			want: "Example Release 2026 1080p EXM WEB-DL Dual-Audio AAC 2.0 H.264-GRP",
		},
		{
			name: "DVD remux follows component order",
			meta: api.UploadSubject{
				Type:           "REMUX",
				Source:         "PAL DVD",
				Audio:          "DD 2.0",
				AudioLanguages: []string{"French"},
				VideoCodec:     "MPEG-2",
				Edition:        "Extended",
				Repack:         "REPACK",
				Tag:            "-GRP",
				ReleaseName:    "Example Release 2026 Extended REPACK PAL DVD REMUX DD 2.0-GRP",
				Release: api.ReleaseInfo{
					Resolution: "576i",
				},
			},
			want: "Example Release 2026 FRENCH Extended REPACK 576i PAL DVD REMUX MPEG-2 DD 2.0-GRP",
		},
		{
			name: "DVDRip uses encode order and spacing",
			meta: api.UploadSubject{
				Type:        "DVDRIP",
				Source:      "DVD",
				Audio:       "DD 1.0",
				VideoEncode: "x264",
				Tag:         "-GRP",
				ReleaseName: "Example Release 2026 DVD x264 DVDRip DD 1.0-GRP",
				Release: api.ReleaseInfo{
					Resolution: "480p",
				},
			},
			want: "Example Release 2026 480p DVDRip DD 1.0 x264-GRP",
		},
		{
			name: "DVD disc omits language and orders cut before resolution and region",
			meta: api.UploadSubject{
				DiscType:       "DVD",
				Type:           "DISC",
				Source:         "NTSC",
				Audio:          "DD 2.0",
				AudioLanguages: []string{"French"},
				VideoCodec:     "MPEG-2",
				Edition:        "Extended",
				Repack:         "REPACK",
				Region:         "USA",
				Tag:            "-GRP",
				ReleaseName:    "Example Release 2026 REPACK Extended USA NTSC DVD5 DD 2.0-GRP",
				Release: api.ReleaseInfo{
					Resolution: "480p",
					Size:       "DVD5",
				},
			},
			want: "Example Release 2026 Extended REPACK 480p USA NTSC DVD5 MPEG-2 DD 2.0-GRP",
		},
		{
			name: "edition and no-group placeholder are omitted",
			meta: api.UploadSubject{
				Type:        "ENCODE",
				Source:      "BluRay",
				Audio:       "DD 5.1",
				VideoEncode: "x264",
				Edition:     "Collector's",
				Tag:         "-NOGRP",
				ReleaseName: "Example Release 2026 Collector's 1080p BluRay DD 5.1 x264-NOGRP",
				Release: api.ReleaseInfo{
					Resolution: "1080p",
				},
			},
			want: "Example Release 2026 1080p BluRay DD 5.1 x264",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := buildName(test.meta, config.TrackerConfig{}); got != test.want {
				t.Fatalf("buildName() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBuildNameAppliesAitherTVDBDisambiguation(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Type:           "WEBDL",
		Source:         "Web",
		Audio:          "DD+ 5.1",
		SeasonStr:      "S01",
		EpisodeStr:     "E01",
		VideoEncode:    "H.264",
		Tag:            "-GRP",
		ReleaseName:    "Example Series 2026 AKA Example Original S01E01 1080p EXM WEB-DL DD+ 5.1 H.264-GRP",
		Identity:       api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		Release:        api.ReleaseInfo{Resolution: "1080p"},
		AudioLanguages: []string{"English"},
		ProviderMetadata: api.SourceScopedMetadata{
			TVDB: &api.TVDBMetadata{
				NameDisambiguation: api.TVDBNameDisambiguation{
					CanonicalName: "Example Series",
					SeriesYear:    2026,
					Locale:        "US",
					IncludeYear:   true,
					IncludeLocale: true,
				},
			},
		},
	}
	want := "Example Series AKA Example Original US 2026 S01E01 1080p EXM WEB-DL DD+ 5.1 H.264-GRP"
	if got := buildName(meta, config.TrackerConfig{}); got != want {
		t.Fatalf("buildName() = %q, want %q", got, want)
	}
}

func TestProfileBuildNameVersion(t *testing.T) {
	t.Parallel()

	if got := Profile().Site.BuildNameVersion; got != "v2" {
		t.Fatalf("BuildNameVersion = %q, want v2", got)
	}
}
