// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rmc

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
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
			name: "encode",
			meta: api.UploadSubject{Type: "ENCODE"},
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
		{"1440p", "3"},
		{"1080p", "3"},
		{"1080i", "4"},
		{"720p", "5"},
		{"576p", "6"},
		{"576i", "7"},
		{"480p", "8"},
		{"480i", "9"},
		{"unknown", "11"},
		{"", "11"},
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

func TestBuildNameSanitizesDisallowedCharacters(t *testing.T) {
	meta := api.UploadSubject{ReleaseName: "Exämple: Rëlease! (2000) 1080p Bluray x264-GRP"}
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	want := "Exmple Rlease 2000 1080p Bluray x264-GRP"
	if got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}

func TestBuildNameRemovesAKASegment(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName: "Metropolis AKA Die Stadt 1927 1080p Bluray x264-GRP",
		Release:     api.ReleaseInfo{Alt: "Die Stadt"},
	}
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	want := "Metropolis 1927 1080p Bluray x264-GRP"
	if got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}

func TestBuildNameFallsBackToReleaseNameNoTag(t *testing.T) {
	meta := api.UploadSubject{ReleaseNameNoTag: "Example Release 2000 1080p Bluray x264"}
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	if got != "Example Release 2000 1080p Bluray x264" {
		t.Fatalf("name = %q", got)
	}
}
