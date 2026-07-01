// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"strings"
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

func TestBTNUploadPayloadUsesCanonicalSeasonEpisodeOnly(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV"},
		Release:     api.ReleaseInfo{Season: 4, Episode: 9},
	}

	if got := resolveUploadType(meta); got != "Season" {
		t.Fatalf("expected upload type Season without canonical episode, got %q", got)
	}
	desc := buildAlbumDesc(meta, map[string]string{"album_desc": "fallback overview"})
	for _, value := range []string{"Season: 0", "Episode: 0"} {
		if !strings.Contains(desc, value) {
			t.Fatalf("expected album description to contain %q, got %q", value, desc)
		}
	}
	if strings.Contains(desc, "Season: 4") || strings.Contains(desc, "Episode: 9") {
		t.Fatalf("album description used parsed fallback values: %q", desc)
	}
	if got := btnTVPayloadMetadataMessage(meta); got != "canonical TV season/episode missing; tracker payload uses 0 and ignores parsed season/episode fallback" {
		t.Fatalf("unexpected metadata message %q", got)
	}
}

func TestBTNUploadTypeUsesCanonicalEpisode(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV"},
		SeasonInt:   4,
		EpisodeInt:  9,
		Release:     api.ReleaseInfo{Season: 1, Episode: 2},
	}

	if got := resolveUploadType(meta); got != "Episode" {
		t.Fatalf("expected upload type Episode, got %q", got)
	}
	if got := btnTVPayloadMetadataMessage(meta); got != "" {
		t.Fatalf("unexpected metadata message %q", got)
	}
}
