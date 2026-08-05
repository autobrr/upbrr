// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package a4k

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNameFanRes(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName: "Example.Release.2026.FANRES.Open.Matte.2160p.UHD.35mm.FLAC.2.0.x265.V2-GRP",
		Type:        "ENCODE",
		Audio:       "FLAC",
		Channels:    "2.0",
		VideoEncode: "x265",
		Release:     api.ReleaseInfo{Title: "Example Release", Year: 2026},
	}
	if got, want := buildName(meta, config.TrackerConfig{}), "Example Release 2026 FANRES Open Matte 2160p UHD 35mm FLAC 2.0 x265 V2"; got != want {
		t.Fatalf("A4K FanRes name = %q, want %q", got, want)
	}
}

func TestBuildNameAIUpscale(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName: "Example.Release.2026.2160p.AI.Upscale.BluRay.TrueHD.5.1.AV1-GRP",
		Type:        "REMUX",
		Source:      "BluRay",
		Audio:       "TrueHD",
		Channels:    "5.1",
		VideoEncode: "AV1",
		Tag:         "-GRP",
		Release:     api.ReleaseInfo{Title: "Example Release", Year: 2026},
	}
	if got, want := buildName(meta, config.TrackerConfig{}), "Example Release 2026 2160p AI Upscale BluRay TrueHD 5.1 AV1-GRP"; got != want {
		t.Fatalf("A4K AI name = %q, want %q", got, want)
	}
}

func TestBuildNameAIUpscaleWithoutAIToken(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName: "Example.Release.2026.2160p.Upscaled.BluRay.TrueHD.5.1.AV1-GRP",
		Type:        "REMUX",
		Source:      "BluRay",
		Audio:       "TrueHD",
		Channels:    "5.1",
		VideoEncode: "AV1",
		Tag:         "-GRP",
		Release:     api.ReleaseInfo{Title: "Example Release", Year: 2026},
	}
	if got := buildName(meta, config.TrackerConfig{}); !strings.Contains(got, "AI Upscale") {
		t.Fatalf("A4K upscale-only name = %q, want AI Upscale", got)
	}
}

func TestBuildNameVersionOnlyIsNotFanRes(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName: "Example.Release.2026.2160p.V2.BluRay.x265-GRP",
		Type:        "ENCODE",
	}
	if got := buildName(meta, config.TrackerConfig{}); strings.Contains(got, "FANRES") {
		t.Fatalf("ordinary versioned encode was classified as FanRes: %q", got)
	}
}
