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
	got := buildDescription(api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{}}}, config.TrackerConfig{}, false, trackers.DescriptionAssets{
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

func TestPrepareDescriptionRespectsLogoSetting(t *testing.T) {
	meta := api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{Logo: "https://image.tmdb.org/t/p/original/title.png"},
	}}
	for _, test := range []struct {
		name    string
		addLogo bool
	}{
		{name: "disabled", addLogo: false},
		{name: "enabled", addLogo: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := prepareDescription(t.Context(), trackers.PreparationInput{
				Meta: meta,
				Runtime: trackers.PreparationRuntime{
					Description: config.DescriptionSettingsConfig{AddLogo: test.addLogo},
				},
			})
			if err != nil {
				t.Fatalf("prepare description: %v", err)
			}
			if containsLogo := strings.Contains(result.Description, "title.png"); containsLogo != test.addLogo {
				t.Fatalf("logo included = %t, want %t: %q", containsLogo, test.addLogo, result.Description)
			}
		})
	}
}
