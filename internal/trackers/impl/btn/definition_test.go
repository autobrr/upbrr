// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestDefinitionName(t *testing.T) {
	t.Parallel()

	def := New()
	if def.Name() != "BTN" {
		t.Fatalf("expected BTN, got %q", def.Name())
	}
}

func TestApplyBTNNameMapping(t *testing.T) {
	t.Parallel()

	name := "Example.Show.S01E01.1080p.WEB-DL.x265-GRP"
	mapped := applyBTNNameMapping(name, "H.265", "WEB-DL")
	if mapped == "" {
		t.Fatalf("expected mapped name")
	}
	if mapped != "Example.Show.S01E01.1080p.WEB-DL.H.265-GRP" {
		t.Fatalf("unexpected mapped name: %s", mapped)
	}
}

func TestCleanAndNormalizeBTNName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "spaces become dots",
			input:    "Example Show S01E01 1080p Web-DL DD+ 5.1 x265-GRP",
			expected: "Example.Show.S01E01.1080p.Web-DL.DDP5.1.x265-GRP",
		},
		{
			name:     "DDP Atmos compacts before generic DDP channel",
			input:    "Some.Movie.2023.DDP.5.1.Atmos.x264",
			expected: "Some.Movie.2023.DDPA5.1.x264",
		},
		{
			name:     "duplicate dots collapse after DD channel",
			input:    "Another.Show..S02E03.DD.2.0.x264",
			expected: "Another.Show.S02E03.DD2.0.x264",
		},
		{
			name:     "AC3 and DTS channel joins",
			input:    "Test.AC3.5.1.and.DTS.5.1.Show",
			expected: "Test.AC35.1.and.DTS5.1.Show",
		},
		{
			name:     "TrueHD Atmos compacts before generic TrueHD channel",
			input:    "Movie.TrueHD.7.1.Atmos.x264",
			expected: "Movie.TrueHDA7.1.x264",
		},
		{
			name:     "DDP channel joins",
			input:    "Movie.DDP.5.1.x264",
			expected: "Movie.DDP5.1.x264",
		},
		{
			name:     "AAC channel joins",
			input:    "Movie.AAC.2.0.x264",
			expected: "Movie.AAC2.0.x264",
		},
		{
			name:     "FLAC channel joins",
			input:    "Movie.FLAC.2.0.x264",
			expected: "Movie.FLAC2.0.x264",
		},
		{
			name:     "TrueHD channel joins case-insensitively",
			input:    "Movie.truehd.7.1.x264",
			expected: "Movie.TrueHD7.1.x264",
		},
		{
			name:     "PCM channel joins case-insensitively",
			input:    "Movie.pcm.2.0.x264",
			expected: "Movie.PCM2.0.x264",
		},
		{
			name:     "LPCM channel joins case-insensitively",
			input:    "Movie.lpcm.2.0.x264",
			expected: "Movie.LPCM2.0.x264",
		},
		{
			name:     "non-alphanumeric chars become dots",
			input:    "Movie:Title[Cut].DDP.5.1-GRP",
			expected: "Movie.Title.Cut.DDP5.1-GRP",
		},
		{
			name:     "diacritics are removed",
			input:    "Éxample Shōw S01E01",
			expected: "Example.Show.S01E01",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := cleanAndNormalizeBTNName(tc.input)
			if result != tc.expected {
				t.Errorf("cleanAndNormalizeBTNName(%q) = %q; expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestResolveUploadNameGroupTag(t *testing.T) {
	tests := []struct {
		name     string
		meta     api.PreparedMetadata
		expected string
	}{
		{
			name: "Valid group tag in meta.Tag",
			meta: api.PreparedMetadata{
				ReleaseName: "Example.Show.S01E01.1080p.Web-DL.x265-GRP",
				Tag:         "GRP",
			},
			expected: "Example.Show.S01E01.1080p.Web-DL.x265-GRP",
		},
		{
			name: "Missing group tag",
			meta: api.PreparedMetadata{
				ReleaseName: "Example.Show.S01E01.1080p.Web-DL.x265",
				Tag:         "",
			},
			expected: "Example.Show.S01E01.1080p.Web-DL.x265-NOGRP",
		},
		{
			name: "Unknown group tag in meta.Tag",
			meta: api.PreparedMetadata{
				ReleaseName: "Example.Show.S01E01.1080p.Web-DL.x265",
				Tag:         "nogrp",
			},
			expected: "Example.Show.S01E01.1080p.Web-DL.x265-NOGRP",
		},
		{
			name: "Existing unknown group tag in ReleaseName",
			meta: api.PreparedMetadata{
				ReleaseName: "Example.Show.S01E01.1080p.Web-DL.x265-unknown",
				Tag:         "unknown",
			},
			expected: "Example.Show.S01E01.1080p.Web-DL.x265-NOGRP",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := resolveUploadName(tc.meta)
			if result != tc.expected {
				t.Errorf("resolveUploadName() = %q; expected %q", result, tc.expected)
			}
		})
	}
}

func TestValidateBTNAPIDownloadURLAllowsOnlySameOriginPrivateFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if err := validateBTNAPIDownloadURL(ctx, "http://127.0.0.1:1/rpc", "http://127.0.0.1:1/mock-download"); err != nil {
		t.Fatalf("expected same-origin private download URL to be allowed: %v", err)
	}
	if err := validateBTNAPIDownloadURL(ctx, "http://127.0.0.1:1/rpc", "http://127.0.0.1:2/mock-download"); err == nil {
		t.Fatalf("expected cross-origin private download URL to be rejected")
	}
	if err := validateBTNAPIDownloadURL(ctx, "ftp://127.0.0.1/rpc", "ftp://127.0.0.1/mock-download"); err == nil {
		t.Fatalf("expected unsupported same-origin scheme to be rejected")
	}
}
