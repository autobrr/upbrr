// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAudioAnalysisPrecedesScreenshots(t *testing.T) {
	t.Parallel()
	got := buildDescription(api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{}}}, config.TrackerConfig{}, trackers.DescriptionAssets{
		Description: "Notes\n\n[spoiler=source_audio]\n[img]https://images.example.invalid/audio.png[/img]\n[/spoiler]",
		Screenshots: []api.ScreenshotImage{
			{WebURL: "https://images.example.invalid/shot1", ImgURL: "https://images.example.invalid/shot1.png"},
			{WebURL: "https://images.example.invalid/shot2", ImgURL: "https://images.example.invalid/shot2.png"},
		},
	})
	if strings.Index(got, "Notes") >= strings.Index(got, "audio.png") || strings.Index(got, "audio.png") >= strings.Index(got, "shot1.png") ||
		!strings.Contains(got, "[img=350]https://images.example.invalid/audio.png[/img]") {
		t.Fatalf("audio analysis placement = %q", got)
	}
}
