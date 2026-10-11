// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTDescriptionAlwaysOmitsTMDBLogos(t *testing.T) {
	t.Parallel()
	const logo = "https://image.tmdb.org/t/p/original/example-logo.png"
	const fallbackLogo = "https://image.tmdb.org/t/p/original/example-fallback-logo.png"
	const resizedLogo = "[img]https://image.tmdb.org/t/p/w300/example-logo.png[/img]"
	const artwork = "[img]https://image.tmdb.org/t/p/original/example-poster.png[/img]\n" +
		"[img]https://image.tmdb.org/t/p/w300/example-poster.png[/img]\n" +
		"[img]https://images.example.invalid/example-logo.png[/img]"
	const content = "[h1]Reviewed title[/h1]\n" + artwork + "\n[url=https://example.invalid]Reviewed signature[/url]"
	const logoImages = "[center][IMG=300]" + logo + "[/IMG][/center]\n[img]" + fallbackLogo + "[/img]"
	const literal = "[code][img]" + logo + "[/img][/code]\n[pre]" + resizedLogo + "[/pre]"
	for _, tc := range []struct {
		name         string
		metadata     *api.TMDBMetadata
		addLogo      bool
		final        bool
		body         string
		wantURLCount int
	}{
		{"generated primary logo", &api.TMDBMetadata{Logo: logo}, true, false, content, 0},
		{"generated fallback logo", &api.TMDBMetadata{TMDBLogo: fallbackLogo}, true, false, content, 0},
		{"imported logo images", &api.TMDBMetadata{Logo: logo, TMDBLogo: fallbackLogo}, true, false, content + "\n" + logoImages, 0},
		{"imported resized logo", &api.TMDBMetadata{Logo: logo, TMDBLogo: "example-logo.png"}, true, false, content + "\n" + resizedLogo, 0},
		{"reviewed resized logo", &api.TMDBMetadata{Logo: logo, TMDBLogo: "example-logo.png"}, true, true, content + "\n" + resizedLogo, 0},
		{"reviewed resized logo with URL only", &api.TMDBMetadata{Logo: logo}, true, true, content + "\n" + resizedLogo, 0},
		{"reviewed resized logo with filename only", &api.TMDBMetadata{TMDBLogo: "example-logo.png"}, true, true, content + "\n" + resizedLogo, 0},
		{"reviewed logo images", &api.TMDBMetadata{Logo: logo, TMDBLogo: fallbackLogo}, true, true, content + "\n" + logoImages, 0},
		{"reviewed logos with global insertion disabled", &api.TMDBMetadata{Logo: logo, TMDBLogo: fallbackLogo}, false, true, content + "\n" + logoImages, 0},
		{"reviewed literal example", &api.TMDBMetadata{Logo: logo, TMDBLogo: fallbackLogo}, true, true, content + "\n" + logoImages + "\n" + literal, 1},
		{"missing metadata preserves artwork", nil, true, true, content, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := prepareDescription(t.Context(), trackers.PreparationInput{
				Meta: api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{TMDB: tc.metadata}},
				Runtime: trackers.PreparationRuntimeFromConfig(config.Config{Description: config.DescriptionSettingsConfig{
					AddLogo: tc.addLogo,
				}}),
				Assets: &trackers.DescriptionAssets{Description: tc.body, Final: tc.final},
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(result.Description, logo) != tc.wantURLCount || strings.Contains(result.Description, fallbackLogo) || strings.Count(result.Description, resizedLogo) != tc.wantURLCount {
				t.Fatalf("TMDB logo remains in ANT description: %q", result.Description)
			}
			for _, kept := range []string{"Reviewed title", artwork, "Reviewed signature"} {
				if !strings.Contains(result.Description, kept) {
					t.Fatalf("description lost %q: %q", kept, result.Description)
				}
			}
			if tc.final && !strings.Contains(result.Description, content) {
				t.Fatalf("reviewed non-logo content changed: %q", result.Description)
			}
			if tc.wantURLCount != 0 && !strings.Contains(result.Description, literal) {
				t.Fatalf("literal logo example changed: %q", result.Description)
			}
		})
	}
}
