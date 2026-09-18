// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package a4k

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNameFanRes(t *testing.T) {
	meta := a4kGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "ENCODE",
		Title:       "Example Release",
		Year:        2026,
		Resolution:  "2160p",
		Source:      "35mm",
		Edition:     "Open Matte",
		Audio:       "FLAC 2.0",
		VideoEncode: "x265",
		Tag:         "-GRP",
	})
	meta.Edition = "Open Matte"
	if got, want := buildName(meta, config.TrackerConfig{}), "Example Release 2026 FANRES Open Matte 2160p UHD 35mm FLAC 2.0 x265"; got != want {
		t.Fatalf("A4K FanRes name = %q, want %q", got, want)
	}
}

func TestBuildNameFanResEditionPhrase(t *testing.T) {
	parsed := metadata.ParseReleaseInfo("Example.Release.2026.Open.Matte.2160p.35mm.x265-GRP.mkv")
	for _, tc := range []struct {
		name, edition string
		wantOpenMatte bool
	}{
		{"parsed dotted edition", strings.Join(parsed.Edition, " "), true},
		{"combined edition", "Director's Cut Open Matte", true},
		{"underscore separator", "Open_Matte", true},
		{"hyphen separator", "Open-Matte", true},
		{"prefix substring", "Reopen Matte", false},
		{"suffix substring", "Open Mattes", false},
		{"title and group only", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := a4kGeneratedSubject(t, api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Open Matte Example",
				Year:        2026,
				Resolution:  "2160p",
				Source:      "35mm",
				Edition:     tc.edition,
				Audio:       "FLAC 2.0",
				VideoEncode: "x265",
				Tag:         "-Open.Matte",
			})
			want := "Open Matte Example 2026 FANRES"
			if tc.wantOpenMatte {
				want += " Open Matte"
			}
			want += " 2160p UHD 35mm FLAC 2.0 x265"
			if got := buildName(meta, config.TrackerConfig{}); got != want {
				t.Fatalf("edition=%q name=%q want=%q", tc.edition, got, want)
			}
		})
	}
}

func TestBuildNameAIUpscale(t *testing.T) {
	meta := a4kGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "REMUX",
		Title:       "Example Release",
		Year:        2026,
		Resolution:  "2160p",
		Source:      "BluRay",
		Audio:       "TrueHD 5.1",
		VideoEncode: "AV1",
		Tag:         "-GRP",
	})
	meta.Release.Other = []string{"Upscaled (AI)"}
	if got, want := buildName(meta, config.TrackerConfig{}), "Example Release 2026 2160p AI Upscale BluRay TrueHD 5.1 AV1-GRP"; got != want {
		t.Fatalf("A4K AI name = %q, want %q", got, want)
	}
}

func TestBuildNameAIUpscaleWithoutAIToken(t *testing.T) {
	meta := a4kGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "REMUX",
		Title:       "Example Release",
		Year:        2026,
		Resolution:  "2160p",
		Source:      "BluRay",
		Audio:       "TrueHD 5.1",
		VideoEncode: "AV1",
		Tag:         "-GRP",
	})
	meta.Release.Other = []string{"Upscaled"}
	if got := buildName(meta, config.TrackerConfig{}); !strings.Contains(got, "AI Upscale") {
		t.Fatalf("A4K upscale-only name = %q, want AI Upscale", got)
	}
}

func TestBuildNameVersionOnlyIsNotFanRes(t *testing.T) {
	meta := a4kGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "ENCODE",
		Title:       "Example Release",
		Year:        2026,
		Resolution:  "2160p",
		Source:      "BluRay",
		VideoEncode: "x265",
		Repack:      "V2",
		Tag:         "-GRP",
	})
	if got := buildName(meta, config.TrackerConfig{}); !strings.Contains(got, "V2") {
		t.Fatalf("versioned encode name omitted V2: %q", got)
	} else if strings.Contains(got, "FANRES") {
		t.Fatalf("ordinary versioned encode was classified as FanRes: %q", got)
	}
}

func TestBuildNameDoesNotInferMarkersFromTitleOrGroup(t *testing.T) {
	meta := a4kGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "REMUX",
		Title:      "AI Upscaled FanRes",
		Year:       2026,
		Resolution: "2160p",
		Source:     "BluRay",
		Tag:        "-INTERNAL",
	})
	if got, want := typeID(meta), "2"; got != want {
		t.Fatalf("A4K typeID inferred title/group marker = %q, want %q", got, want)
	}
	if got := buildName(meta, config.TrackerConfig{}); got != meta.ReleaseName {
		t.Fatalf("A4K name inferred marker from title/group: %q, want %q", got, meta.ReleaseName)
	}
}

func a4kGeneratedSubject(t *testing.T, request api.ReleaseNameRequest) api.UploadSubject {
	t.Helper()
	result := metadata.BuildReleaseName(request, api.NopLogger{})
	if result.GeneratedName == nil {
		t.Fatal("BuildReleaseName did not produce a structured document")
	}
	return api.UploadSubject{
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
		Type:             request.Type,
		Source:           request.Source,
		Edition:          request.Edition,
		Audio:            request.Audio,
		VideoEncode:      request.VideoEncode,
		Tag:              request.Tag,
		Release: api.ReleaseInfo{
			Title:      request.Title,
			Year:       request.Year,
			Resolution: request.Resolution,
		},
	}
}

func TestTitleAndYearHonorsEffectiveMetadata(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release: api.ReleaseInfo{Title: "Canonical Title", Year: 2020},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
			Title:         "TMDB Title",
			OriginalTitle: "TMDB Original",
			Year:          2021,
		}},
	}
	if title, year := titleAndYear(meta); title != "Canonical Title" || year != "2020" {
		t.Fatalf("automatic canonical title/year = (%q, %q)", title, year)
	}
	meta.EffectiveMetadata = api.EffectiveMetadata{
		Title:           "Manual Title",
		TitleProvenance: api.FactProvenanceManual,
		Year:            2030,
		YearProvenance:  api.FactProvenanceManual,
	}
	if title, year := titleAndYear(meta); title != "Manual Title" || year != "2030" {
		t.Fatalf("manual title/year = (%q, %q)", title, year)
	}
	meta.EffectiveMetadata = api.EffectiveMetadata{
		TitleProvenance: api.FactProvenanceManualEmpty,
		YearProvenance:  api.FactProvenanceManualEmpty,
	}
	if title, year := titleAndYear(meta); title != "" || year != "" {
		t.Fatalf("manual empty title/year = (%q, %q)", title, year)
	}
	meta.EffectiveMetadata = api.EffectiveMetadata{}
	meta.Release = api.ReleaseInfo{}
	meta.ProviderMetadata.TMDB.Title = ""
	if title, year := titleAndYear(meta); title != "TMDB Original" || year != "2021" {
		t.Fatalf("automatic provider fallback title/year = (%q, %q)", title, year)
	}
}

func TestBuildNamePreservesParsedFanResMarkers(t *testing.T) {
	meta := a4kGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "ENCODE",
		Title:       "Example Release",
		Year:        2026,
		Resolution:  "2160p",
		Source:      "BluRay",
		Audio:       "FLAC 2.0",
		VideoEncode: "x265",
		Tag:         "-GRP",
	})
	parsed := metadata.ParseReleaseInfo("Example.Release.2026.2160p.BluRay.No-DNR.x265.v2-GRP.mkv")
	meta.Release.Other = parsed.Other
	meta.Release.Version = parsed.Version
	if got, want := buildName(meta, config.TrackerConfig{}), "Example Release 2026 FANRES NoDNR 2160p UHD 35mm FLAC 2.0 x265 v2"; got != want {
		t.Fatalf("name=%q want=%q parsed=%+v", got, want, parsed)
	}
}

func TestBuildNameUsesParsedAIAndFanResMarkers(t *testing.T) {
	for _, tc := range []struct{ marker, wantType, want string }{
		{"AI.Upscale", "8", "Example Release 2026 2160p AI Upscale BluRay FLAC 2.0 x265-GRP"},
		{"Upscaled", "8", "Example Release 2026 2160p AI Upscale BluRay FLAC 2.0 x265-GRP"},
		{"AI.Remaster", "8", "Example Release 2026 2160p AI Remaster BluRay FLAC 2.0 x265-GRP"},
		{"FANRES", "7", "Example Release 2026 FANRES 2160p UHD 35mm FLAC 2.0 x265"},
	} {
		t.Run(tc.marker, func(t *testing.T) {
			meta := a4kGeneratedSubject(t, api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example Release",
				Year:        2026,
				Resolution:  "2160p",
				Source:      "BluRay",
				Audio:       "FLAC 2.0",
				VideoEncode: "x265",
				Tag:         "-GRP",
			})
			meta.Release.Other = metadata.ParseReleaseInfo("Example.Release.2026.2160p.BluRay." + tc.marker + ".x265-GRP.mkv").Other
			if got := typeID(meta); got != tc.wantType {
				t.Fatalf("type=%q want=%q markers=%v", got, tc.wantType, meta.Release.Other)
			}
			if got := buildName(meta, config.TrackerConfig{}); got != tc.want {
				t.Fatalf("name=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestBuildNameFallbackAndEmpty(t *testing.T) {
	meta := api.UploadSubject{ReleaseNameNoTag: "Example   Movie 2026", Type: "ENCODE"}
	if got := buildName(meta, config.TrackerConfig{}); got != "Example Movie 2026" {
		t.Fatalf("no-tag fallback=%q", got)
	}
	meta.ReleaseNameNoTag = ""
	meta.Release.Other = []string{"FANRES"}
	if got := buildName(meta, config.TrackerConfig{}); got != "" {
		t.Fatalf("empty name invented=%q", got)
	}
}

func TestTitleAndYearRejectsStaleProvider(t *testing.T) {
	meta := api.UploadSubject{
		Identity:         api.ExternalIdentity{Generation: 2},
		ProviderMetadata: api.SourceScopedMetadata{Generation: 1, TMDB: &api.TMDBMetadata{Title: "Stale Title", Year: 2020}},
	}
	if title, year := titleAndYear(meta); title != "" || year != "" {
		t.Fatalf("stale provider used: %q %q", title, year)
	}
	meta.ProviderMetadata.Generation = 2
	if title, year := titleAndYear(meta); title != "Stale Title" || year != "2020" {
		t.Fatalf("current provider ignored: %q %q", title, year)
	}
}
