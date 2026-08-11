// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rmc

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProfileIdentity(t *testing.T) {
	profile := Profile()
	if profile.Name != "RMC" {
		t.Fatalf("name = %q, want RMC", profile.Name)
	}
	if profile.BaseURL != "https://retro-movies.club" {
		t.Fatalf("base URL = %q", profile.BaseURL)
	}
	if profile.MetadataPolicy == nil || len(profile.MetadataPolicy.Requirements) != 1 ||
		len(profile.MetadataPolicy.Requirements[0].AnyOf) != 1 ||
		profile.MetadataPolicy.Requirements[0].AnyOf[0] != trackers.MetadataFieldTMDBTitle ||
		profile.MetadataPolicy.Requirements[0].Disposition != api.RuleDispositionStrict {
		t.Fatalf("metadata policy = %#v, want current TMDB title", profile.MetadataPolicy)
	}
}

func TestTypeID(t *testing.T) {
	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "BDMV disc",
			meta: api.UploadSubject{DiscType: "BDMV"},
			want: "1",
		},
		{
			name: "Blu-ray remux",
			meta: api.UploadSubject{Type: "REMUX", Source: "BLURAY"},
			want: "2",
		},
		{
			name: "DVD disc",
			meta: api.UploadSubject{DiscType: "DVD"},
			want: "3",
		},
		{
			name: "PAL DVD remux",
			meta: api.UploadSubject{Type: "REMUX", Source: "PAL DVD"},
			want: "4",
		},
		{
			name: "BluRay encode",
			meta: api.UploadSubject{Type: "Bluray Encode"},
			want: "5",
		},
		{
			name: "DVDRip",
			meta: api.UploadSubject{Type: "DVDRIP"},
			want: "6",
		},
		{
			name: "WEB-DL",
			meta: api.UploadSubject{Type: "WEBDL"},
			want: "7",
		},
		{
			name: "WEBRip",
			meta: api.UploadSubject{Type: "WEBRIP"},
			want: "8",
		},
		{
			name: "UHDTV source",
			meta: api.UploadSubject{Source: "UHDTV"},
			want: "9",
		},
		{
			name: "HDTV",
			meta: api.UploadSubject{Type: "HDTV"},
			want: "10",
		},
		{
			name: "SDTV",
			meta: api.UploadSubject{Type: "HDTV", Release: api.ReleaseInfo{Resolution: "540p"}},
			want: "11",
		},
		{
			name: "VHS LD WOC",
			meta: api.UploadSubject{Type: "VHS / LD / WOC"},
			want: "12",
		},
		{
			name: "upscale",
			meta: api.UploadSubject{Type: "Upscale"},
			want: "14",
		},
		{
			name: "restoration",
			meta: api.UploadSubject{Type: "Restoration"},
			want: "15",
		},
		{
			name: "soundtrack",
			meta: api.UploadSubject{Type: "SoundTrack"},
			want: "17",
		},
		{
			name: "unsupported",
			meta: api.UploadSubject{},
			want: "0",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := typeID(test.meta); got != test.want {
				t.Fatalf("typeID(%#v) = %q, want %q", test.meta, got, test.want)
			}
		})
	}
}

func TestResolutionID(t *testing.T) {
	tests := []struct {
		resolution string
		want       string
	}{
		{"4320p", "1"},
		{"2160p", "2"},
		{"1080p", "3"},
		{"1080i", "4"},
		{"720p", "5"},
		{"576p", "6"},
		{"576i", "7"},
		{"480p", "8"},
		{"480i", "9"},
		{"360p", "10"},
		{"540p", "11"},
		{"FLAC", "12"},
		{"1440p", "13"},
		{"unknown", "13"},
		{"", "13"},
	}
	for _, test := range tests {
		t.Run(test.resolution, func(t *testing.T) {
			meta := api.UploadSubject{Release: api.ReleaseInfo{Resolution: test.resolution}}
			if got := resolutionID(meta); got != test.want {
				t.Fatalf("resolutionID(%q) = %q, want %q", test.resolution, got, test.want)
			}
		})
	}
}

func TestResolutionIDPrecedenceAndMarkerRecovery(t *testing.T) {
	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{"soundtrack with video resolution", api.UploadSubject{Type: "SoundTrack", Release: api.ReleaseInfo{Resolution: "1080p"}}, "12"},
		{"360p marker", api.UploadSubject{ReleaseName: "Example.Release.2000.360p-GRP"}, "10"},
		{"540p marker", api.UploadSubject{ReleaseName: "Example.Release.2000.540p-GRP"}, "11"},
		{"embedded 360p marker", api.UploadSubject{ReleaseName: "Example.Release.2000.1360p-GRP"}, "13"},
		{"embedded 540p marker", api.UploadSubject{ReleaseName: "Example.Release.2000.1540p-GRP"}, "13"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolutionID(test.meta); got != test.want {
				t.Fatalf("resolutionID(%#v) = %q, want %q", test.meta, got, test.want)
			}
		})
	}
}

func TestBuildNameSanitizesDisallowedCharacters(t *testing.T) {
	meta := rmcNameSubject("Exämple: Rëlease! (2000) 1080p Bluray x264-GRP", "Exämple: Rëlease!", 2000)
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	want := "Exmple Rlease 2000 1080p Bluray x264-GRP"
	if got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}

func TestBuildNameRemovesAKASegment(t *testing.T) {
	meta := rmcNameSubject("Example Release AKA Example Alternate 1999 1080p Bluray x264-GRP", "Example Release", 1999)
	meta.ProviderMetadata.TMDB.RetrievedAKA = "AKA Example Alternate"
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	want := "Example Release 1999 1080p Bluray x264-GRP"
	if got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}

func TestBuildNameFallsBackToReleaseNameNoTag(t *testing.T) {
	meta := rmcNameSubject("", "Example Release", 2000)
	meta.ReleaseNameNoTag = "Example Release 2000 1080p Bluray x264"
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	if got != "Example Release 2000 1080p Bluray x264" {
		t.Fatalf("name = %q", got)
	}
}

func TestBuildNameUsesTMDBTitleAndYearAndPreservesPlus(t *testing.T) {
	meta := rmcNameSubject("Wrong Title AKA Wrong Alternate 2001 1080p Bluray HDR10+ x264 DD+-GRP", "Example Release", 2000)
	meta.Release.Year = 2001
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	want := "Example Release 2000 1080p Bluray HDR10+ x264 DD+-GRP"
	if got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}

func TestBuildNameRejectsStaleTMDBMetadata(t *testing.T) {
	meta := rmcNameSubject("Example Release 2000 1080p Bluray x264-GRP", "Example Release", 2000)
	meta.SourcePath = "current-source"
	meta.Identity.SourcePath = meta.SourcePath
	meta.ProviderMetadata.SourcePath = "stale-source"
	if got := Profile().Site.BuildName(meta, config.TrackerConfig{}); got != "" {
		t.Fatalf("name = %q, want empty for stale TMDB metadata", got)
	}
}

func TestCheckRequirementsRequiresTMDBYear(t *testing.T) {
	meta := rmcNameSubject("Example Release 2000 1080p Bluray x264-GRP", "Example Release", 0)
	failures, err := checkRequirements(context.Background(), api.NewTrackerValidationSubject(meta, "RMC"), api.NopLogger{})
	if err != nil {
		t.Fatalf("check requirements: %v", err)
	}
	if len(failures) != 1 || failures[0].Rule != "rmc_release_year" {
		t.Fatalf("failures = %#v, want rmc_release_year", failures)
	}
}

func rmcNameSubject(name, title string, year int) api.UploadSubject {
	const tmdbID = 1234567
	return api.UploadSubject{
		ReleaseName: name,
		Release:     api.ReleaseInfo{Year: year},
		Identity:    api.ExternalIdentity{TMDBID: tmdbID},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				TMDBID: tmdbID,
				Title:  title,
				Year:   year,
			},
		},
	}
}
