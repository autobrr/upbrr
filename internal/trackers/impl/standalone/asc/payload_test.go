// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestASCMigratedUploadPayload(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Identity:          api.ExternalIdentity{Category: api.CanonicalCategoryTV, IMDBID: 1234567},
		Anime:             true,
		Type:              "WEBDL",
		Container:         "mkv",
		Audio:             "DD+ 5.1",
		AudioLanguages:    []string{"Portuguese", "English"},
		SubtitleLanguages: []string{"pt-BR"},
		VideoCodec:        "AVC",
		Release:           api.ReleaseInfo{Year: 2026, Resolution: "1080p"},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
			OriginalLanguage: "ja",
			Genres:           "Drama, Drama, Comedy",
		}},
	}
	payload := buildPayload(meta, config.TrackerConfig{Anon: true}, "Synthetic overview", "Synthetic release", "General report")
	wantAttributes := []string{"5", "59", "199", "92", "195", "161", "231", "227"}
	fields := payload.multipartFields([]string{"screenshots/synthetic-a.png", "screenshots/synthetic-b.png"})
	if !slices.Equal(fields["attribute_ids[]"], wantAttributes) {
		t.Fatalf("attribute IDs = %v, want %v", fields["attribute_ids[]"], wantAttributes)
	}
	for k, want := range map[string]string{
		"category_id": "61",
		"anonymous":   "1",
		"subtitle":    "embedded",
		"name":        "Synthetic release",
		"width":       "1920",
		"height":      "1080",
		"mediainfo":   "General report",
	} {
		if !slices.Equal(fields[k], []string{want}) {
			t.Errorf("%s = %v, want [%q]", k, fields[k], want)
		}
	}
	if len(fields["screenshot_paths[]"]) != 2 {
		t.Fatalf("missing repeated screenshot fields")
	}
}
