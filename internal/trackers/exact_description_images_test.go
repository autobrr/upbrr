// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedExactMediaPreservesDescriptionArtwork(t *testing.T) {
	t.Parallel()
	const body = "[align=center]https://www.blu-ray.com/movies/Example-Movie-Blu-ray/12345/[/align]\n" +
		"[align=center][img]https://images.blu-ray.com/movies/covers/example-front.jpg[/img]\n" +
		"[img]https://images.blu-ray.com/movies/covers/example-back.jpg[/img][/align]"
	for _, tracker := range []string{"ANT", "AITHER"} {
		t.Run(tracker, func(t *testing.T) {
			registry := NewRegistry()
			group := strings.ToLower(tracker)
			if err := registry.Register(stubPreparationDefinition{
				name:                tracker,
				group:               group,
				useAssetDescription: true,
			}); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			sourcePath := filepath.Join(root, "Example.Movie.2026.1080p.Blu-ray-GRP")
			exact := &api.ExactMediaAssets{}
			for index := range 4 {
				imagePath := filepath.Join(root, fmt.Sprintf("screen-%d.png", index))
				exact.Screenshots = append(exact.Screenshots, api.ScreenshotImage{Path: imagePath, Purpose: api.ScreenshotPurposeFinal})
				exact.ScreenshotUploads = append(exact.ScreenshotUploads, api.UploadedImageLink{
					ImagePath:  imagePath,
					Host:       "ptscreens",
					UsageScope: globalImageUsageScope,
					RawURL:     fmt.Sprintf("https://images.example.invalid/screen-%d.png", index),
				})
			}
			skipUpload := true
			meta := api.UploadSubject{
				MediaBinding:           trackerTestMediaBinding(sourcePath),
				SourcePath:             sourcePath,
				ExactMedia:             exact,
				DescriptionGroupsFinal: true,
				DescriptionGroups: []api.DescriptionBuilderGroup{{
					GroupKey:       group,
					Trackers:       []string{tracker},
					RawDescription: body,
				}},
				ImageHostOverrides: api.ImageHostOverrides{SkipUpload: &skipUpload},
			}
			repo := &stubRepo{}
			service := NewServiceWithRegistry(config.Config{}, api.NopLogger{}, repo, registry)
			content := service.prepareUploadContent(t.Context(), tracker, meta, config.TrackerConfig{}, nil, nil)
			if content.Failure != nil || content.Assets == nil {
				t.Fatalf("prepare final content: %+v", content)
			}
			assets := content.Assets
			if !assets.Final || assets.Description != body {
				t.Fatalf("artwork replaced by screenshots: %q", assets.Description)
			}
			if len(assets.Screenshots) != 4 || len(assets.Slots) != 4 {
				t.Fatalf("prepared screenshots changed: %+v", assets)
			}
			for index, screenshot := range assets.Screenshots {
				if screenshot.Path != exact.Screenshots[index].Path || screenshot.RawURL != exact.ScreenshotUploads[index].RawURL ||
					assets.Slots[index].OriginalURL != "" {
					t.Fatalf("screenshot %d acquired description artwork identity: %+v", index, assets)
				}
			}
			preview, err := service.BuildPreparation(t.Context(), api.NewDescriptionSubject(meta), []string{tracker})
			if err != nil || len(preview.ContentFailures) != 0 || len(preview.Descriptions) != 1 || preview.Descriptions[0].RawDescription != body {
				t.Fatalf("prepared description preview changed: %+v err=%v", preview, err)
			}
			if repo.screenshotSlotCalls != 0 || repo.selectionsCalls != 0 || repo.uploadsCalls != 0 || len(repo.screenshotSlots) != 0 {
				t.Fatalf("exact media consulted or replaced ambient screenshot state: %+v", repo)
			}
		})
	}
}

func TestSkippedUploadSlotResolutionUsesExactMedia(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		withRepo bool
		empty    bool
	}{
		{"prepared screenshots", true, false},
		{"prepared screenshots without repository", false, false},
		{"authoritative empty screenshots", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sourcePath := filepath.Join(t.TempDir(), "Example.Movie.2026-GRP")
			exact := &api.ExactMediaAssets{}
			if !tc.empty {
				exact.Screenshots = []api.ScreenshotImage{{Path: filepath.Join(t.TempDir(), "screen.png"), Purpose: api.ScreenshotPurposeFinal}}
			}
			meta := api.UploadSubject{
				SourcePath: sourcePath,
				ExactMedia: exact,
				DescriptionGroups: []api.DescriptionBuilderGroup{{
					Trackers:       []string{"ANT"},
					RawDescription: "[center][img]https://images.blu-ray.com/movies/covers/example-front.jpg[/img][/center]",
					Final:          true,
				}},
			}
			registry := descriptionAssetsTestRegistry(t)
			preloaded, err := preloadDescriptionAssetData(t.Context(), meta, nil, registry)
			if err != nil {
				t.Fatal(err)
			}
			want := cloneScreenshotSlots(preloaded.screenshotSlots)
			var repo UploadPersistence
			if tc.withRepo {
				repo = &stubRepo{}
			}
			slots, err := screenshotSlotsForImageHostResolution(t.Context(), "ANT", meta, repo, api.NopLogger{}, preloaded, registry, true)
			if err != nil || !reflect.DeepEqual(slots, want) || !reflect.DeepEqual(cloneScreenshotSlots(preloaded.screenshotSlots), want) {
				t.Fatalf("exact screenshot identities rebuilt from artwork: slots=%+v want=%+v err=%v", slots, want, err)
			}
			if len(slots) > 0 {
				slots[0].ImagePath = "changed"
				if !reflect.DeepEqual(preloaded.screenshotSlots, want) {
					t.Fatal("returned slots changed the prepared snapshot")
				}
			}
		})
	}
}
