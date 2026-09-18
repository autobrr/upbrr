// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rhd

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProfileNameParity(t *testing.T) {
	profile := Profile().Site
	if profile.BuildNameVersion != "v4" {
		t.Fatalf("RHD build-name version = %q", profile.BuildNameVersion)
	}
	build := profile.BuildName
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
			name: "full DVD does not repeat capacity",
			meta: api.UploadSubject{
				Type:     "DISC",
				DiscType: "DVD",
				Tag:      "-GRP",
				Release: api.ReleaseInfo{
					Title:  "Example Release",
					Year:   2026,
					Source: "PAL DVD",
					Size:   "DVD9",
				},
			},
			want: "Example Release 2026 COMPLETE PAL DVD9-GRP",
		},
		{
			name: "markers",
			meta: rhdMarkerSubject(t, "Example Movie", "-GRP", []string{"UPSCALED"}),
			want: "Example Movie 2024 GERMAN 1080p UPSCALE WEB-DL DDP5.1 H.264-GRP",
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
		{
			name: "daily without year",
			meta: api.UploadSubject{
				Type:             "WEBDL",
				Tag:              "-GRP",
				DailyEpisodeDate: "2026-02-03",
				VideoEncode:      "H.264",
				AudioLanguages:   []string{"German"},
				Release:          api.ReleaseInfo{Title: "Example Show", Resolution: "1080p"},
			},
			want: "Example Show 2026-02-03 GERMAN 1080p WEB-DL H.264-GRP",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := build(test.meta, config.TrackerConfig{}); got != test.want {
				t.Fatalf("name = %q, want %q", got, test.want)
			}
		})
	}
	ignored := build(rhdMarkerSubject(t, "Example Regraded Upscaled Incomplete Dubbed", "-INTERNAL", nil), config.TrackerConfig{})
	for _, marker := range []string{"REGRADED", "UPSCALE", "iNTERNAL", "DUBBED"} {
		if strings.Contains(ignored, marker) {
			t.Fatalf("unexpected marker %s in %q", marker, ignored)
		}
	}
}

func rhdMarkerSubject(t *testing.T, title, tag string, other []string) api.UploadSubject {
	t.Helper()
	request := api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Title:       title,
		Year:        2024,
		Resolution:  "1080p",
		Audio:       "DDP5.1",
		VideoEncode: "H.264",
		Tag:         tag,
	}
	result := metadata.BuildReleaseName(request, api.NopLogger{})
	if result.GeneratedName == nil {
		t.Fatal("BuildReleaseName did not produce a structured document")
	}
	return api.UploadSubject{
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
		Type:             request.Type,
		Tag:              request.Tag,
		Audio:            request.Audio,
		VideoEncode:      request.VideoEncode,
		AudioLanguages:   []string{"German"},
		Release: api.ReleaseInfo{
			Title:      request.Title,
			Year:       request.Year,
			Resolution: request.Resolution,
			Other:      other,
		},
	}
}

func TestBuildNameUsesExactOtherMarkers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*api.UploadSubject)
		want string
	}{
		{"upscale", func(meta *api.UploadSubject) { meta.Release.Other = []string{"UPSCALED"} }, "UPSCALE"},
		{"ac3d dubbed", func(meta *api.UploadSubject) { meta.Release.Audio = []string{"AC3D"} }, "GERMAN DUBBED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			meta := rhdMarkerSubject(t, "Example Movie", "-GRP", nil)
			test.edit(&meta)
			if got := buildName(meta, config.TrackerConfig{}); !strings.Contains(got, test.want) {
				t.Fatalf("name = %q, missing %q", got, test.want)
			}
		})
	}
}

func TestTypeAndSourceDVDDoesNotRepeatCapacity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		size   string
		want   string
	}{
		{
			name:   "PAL DVD9",
			source: "PAL DVD",
			size:   "DVD9",
			want:   "COMPLETE PAL DVD9",
		},
		{
			name:   "bare DVD9",
			source: "DVD",
			size:   "DVD9",
			want:   "COMPLETE DVD9",
		},
		{
			name:   "NTSC DVD5",
			source: "NTSC DVD",
			size:   "DVD5",
			want:   "COMPLETE NTSC DVD5",
		},
		{
			name:   "surrounding whitespace",
			source: " PAL DVD ",
			size:   " DVD9 ",
			want:   "COMPLETE PAL DVD9",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := strings.Join(typeAndSource(api.UploadSubject{
				Type:     "DISC",
				DiscType: "DVD",
				Release:  api.ReleaseInfo{Source: test.source, Size: test.size},
			}), " ")
			if got != test.want {
				t.Fatalf("typeAndSource() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBuildNamePrefersManualTitleAndYear(t *testing.T) {
	meta := api.UploadSubject{
		Type:           "WEBDL",
		Tag:            "-GRP",
		AudioLanguages: []string{"German"},
		Release: api.ReleaseInfo{
			Title:      "Parsed Title",
			Year:       2021,
			Resolution: "1080p",
		},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
			Title:           "Provider Title",
			Year:            2020,
			LocalizedTitles: map[string]string{"de": "Lokaler Titel"},
		}},
		EffectiveMetadata: api.EffectiveMetadata{
			Title:           "Manual Title",
			TitleProvenance: api.FactProvenanceManual,
			Year:            2030,
			YearProvenance:  api.FactProvenanceManual,
		},
	}
	if got := buildName(meta, config.TrackerConfig{}); !strings.HasPrefix(got, "Manual Title 2030 ") {
		t.Fatalf("manual name = %q", got)
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

func TestBuildNameUsesParsedTechnicalMarkers(t *testing.T) {
	parsed := metadata.ParseReleaseInfo("Example.Show.S01.1080p.WEB-DL.Incomplete.Regraded.UPSCL.Internal.x264-GRP.mkv")
	meta := rhdMarkerSubject(t, "Example Show", "-GRP", parsed.Other)
	meta.SeasonStr = "S01"
	if got, want := buildName(meta, config.TrackerConfig{}), "Example Show 2024 S01 iNCOMPLETE GERMAN 1080p REGRADED UPSCALE WEB-DL DDP5.1 H.264 iNTERNAL-GRP"; got != want {
		t.Fatalf("name=%q want=%q parsed=%+v", got, want, parsed)
	}
}

func TestLanguageUsesParsedDubbedMarkers(t *testing.T) {
	for _, marker := range []string{"LD", "MD", "DUBBED", "SYNCED", "AC3D", "LINE", "MIC"} {
		t.Run(marker, func(t *testing.T) {
			parsed := metadata.ParseReleaseInfo("Example.Movie.2024.1080p.BluRay." + marker + ".x264-GRP.mkv")
			meta := api.UploadSubject{Release: parsed, AudioLanguages: []string{"German"}}
			if got := resolveLanguage(meta); got != "GERMAN DUBBED" {
				t.Fatalf("language=%q parsed=%+v", got, parsed)
			}
		})
	}
}

func TestBuildNameRejectsStaleProvider(t *testing.T) {
	meta := rhdMarkerSubject(t, "Example Movie", "-GRP", nil)
	meta.Identity.Generation = 2
	meta.ProviderMetadata = api.SourceScopedMetadata{
		Generation: 1, TMDB: &api.TMDBMetadata{LocalizedTitles: map[string]string{"de": "Provider Title"}},
	}
	if got := buildName(meta, config.TrackerConfig{}); !strings.HasPrefix(got, "Example Movie 2024 ") {
		t.Fatalf("stale provider used: %q", got)
	}
	meta.ProviderMetadata.Generation = 2
	if got := buildName(meta, config.TrackerConfig{}); !strings.HasPrefix(got, "Provider Title 2024 ") {
		t.Fatalf("current provider ignored: %q", got)
	}
}
