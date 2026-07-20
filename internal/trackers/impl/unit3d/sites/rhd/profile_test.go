// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rhd

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProfileNameParity(t *testing.T) {
	build := Profile().Site.BuildName
	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "localized web",
			meta: api.UploadSubject{
				Type:             "WEBDL",
				Tag:              "-GRP",
				Audio:            "DD+ 5.1",
				VideoEncode:      "H.264",
				AudioLanguages:   []string{"German"},
				Release:          api.ReleaseInfo{Resolution: "1080p"},
				ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Year: 2025, LocalizedTitles: map[string]string{"de": "Beispiel Film"}}},
			},
			want: "Beispiel Film 2025 GERMAN 1080p WEB-DL DD+ 5.1 H.264-GRP",
		},
		{
			name: "full disc",
			meta: api.UploadSubject{
				Type:           "DISC",
				Region:         "GER",
				Tag:            "-GRP",
				Audio:          "DTS-HD MA 5.1",
				VideoCodec:     "AVC",
				AudioLanguages: []string{"German", "English"},
				Release: api.ReleaseInfo{
					Title:      "Example Movie",
					Year:       2024,
					Resolution: "1080p",
					Source:     "Blu-ray",
					Size:       "BD50",
				},
			},
			want: "Example Movie 2024 1080p COMPLETE GER Blu-ray BD50 DTS-HD MA 5.1 AVC-GRP",
		},
		{
			name: "markers",
			meta: api.UploadSubject{
				ReleaseName:    "Example.Movie.2024.[INTERNAL].(UPSCALED).1080p.WEB-DL.DDP5.1.H.264-GRP",
				Type:           "WEBDL",
				Tag:            "-GRP",
				Audio:          "DDP5.1",
				VideoEncode:    "H.264",
				AudioLanguages: []string{"German"},
				Release: api.ReleaseInfo{
					Title:      "Example Movie",
					Year:       2024,
					Resolution: "1080p",
				},
			},
			want: "Example Movie 2024 GERMAN 1080p UPSCALE WEB-DL DDP5.1 H.264 iNTERNAL-GRP",
		},
		{
			name: "hdr",
			meta: api.UploadSubject{
				Type:           "WEBDL",
				Tag:            "-GRP",
				Audio:          "DDP5.1",
				HDR:            "DV HDR",
				VideoEncode:    "H.265",
				AudioLanguages: []string{"German"},
				Release: api.ReleaseInfo{
					Title:      "Example Movie",
					Year:       2026,
					Resolution: "2160p",
				},
			},
			want: "Example Movie 2026 GERMAN 2160p WEB-DL DDP5.1 DV HDR H.265-GRP",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := build(test.meta, config.TrackerConfig{}); got != test.want {
				t.Fatalf("name = %q, want %q", got, test.want)
			}
		})
	}
	ignored := build(api.UploadSubject{
		ReleaseName:    "Example.Movie.2024.Regradedness.Internalized.Lineage.1080p.WEB-DL.DDP5.1.H.264-LD",
		Type:           "WEBDL",
		Tag:            "-LD",
		Audio:          "DDP5.1",
		VideoEncode:    "H.264",
		AudioLanguages: []string{"English"},
		Release: api.ReleaseInfo{
			Title:      "Example Movie",
			Year:       2024,
			Resolution: "1080p",
		},
	}, config.TrackerConfig{})
	for _, marker := range []string{"REGRADED", "UPSCALE", "iNTERNAL", "DUBBED"} {
		if strings.Contains(ignored, marker) {
			t.Fatalf("unexpected marker %s in %q", marker, ignored)
		}
	}
}

func TestProfileResolutionAndLanguages(t *testing.T) {
	if got := Profile().Site.ResolveResolutionID(api.UploadSubject{Release: api.ReleaseInfo{Resolution: "576p"}}); got != "12" {
		t.Fatalf("resolution = %q", got)
	}
	for _, test := range []struct {
		values []string
		want   string
	}{{[]string{"", "French", "   "}, "FRENCH"}, {[]string{"English", "eng", "English"}, "ENGLISH"}, {[]string{"German", "deu", "de-DE"}, "GERMAN"}, {[]string{"English", "French"}, "ENGLISH DL"}} {
		if got := resolveLanguage(api.UploadSubject{AudioLanguages: test.values}); got != test.want {
			t.Fatalf("language = %q, want %q", got, test.want)
		}
	}
}
