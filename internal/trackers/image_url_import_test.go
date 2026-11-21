// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestNativeSlotsRequireCurrentAccountRecordAndExactBinding(t *testing.T) {
	registry := NewRegistry()
	policy := &ImageHostPolicy{AllowedHosts: []string{"kshare", "pixhost", "imgbb"}, URLImport: &ImageURLImportCapability{
		ID:         "gpw-native-v1",
		Host:       "kshare",
		UsageScope: "tracker:GPW",
	}}
	if err := registry.RegisterDescriptor(Descriptor{
		Name:       "GPW",
		Definition: stubDefinition{name: "GPW"},
		ImageHost:  policy,
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"GPW": {APIKey: "synthetic-key"}}}}
	binding := api.PreparedMediaBinding{
		SourcePath:               filepath.Join(t.TempDir(), "Example.Release.mkv"),
		PreparedMediaFingerprint: "fingerprint",
		PreparedGeneration:       1,
	}
	imagePath := filepath.Join(t.TempDir(), "image.png")
	scope, err := ImageHostAccountScope(cfg, registry, "kshare", "tracker:GPW")
	if err != nil {
		t.Fatal(err)
	}
	link := api.UploadedImageLink{
		SourcePath:               binding.SourcePath,
		PreparedMediaFingerprint: binding.PreparedMediaFingerprint,
		PreparedGeneration:       binding.PreparedGeneration,
		ImagePath:                imagePath,
		Purpose:                  api.ScreenshotPurposeFinal,
		Host:                     "kshare",
		UsageScope:               "tracker:GPW",
		AccountScope:             scope,
		RawURL:                   "https://kshare.club/image.png",
	}
	for _, scenario := range []string{"current", "orphan", "credential", "generation", "version", "URL"} {
		t.Run(scenario, func(t *testing.T) {
			localCfg := cfg
			record := link
			if scenario == "credential" {
				localCfg.Trackers.Trackers = map[string]config.TrackerConfig{"GPW": {APIKey: "replacement-key"}}
			}
			if scenario == "generation" {
				record.PreparedGeneration++
			}
			if scenario == "URL" {
				record.RawURL = "https://kshare.club/other.png"
			}
			if scenario == "version" {
				registry.descriptors["GPW"].ImageHost.URLImport.ID = "gpw-native-v2"
				defer func() { registry.descriptors["GPW"].ImageHost.URLImport.ID = "gpw-native-v1" }()
			}
			records := []api.UploadedImageLink{record}
			if scenario == "orphan" {
				records = nil
			}
			slots := []api.ScreenshotSlot{{ImagePath: imagePath, Variants: []api.ScreenshotSlotVariant{
				{
					ImagePath:  imagePath,
					Host:       "kshare",
					UsageScope: "tracker:GPW",
					RawURL:     link.RawURL,
				},
				{
					ImagePath:  imagePath,
					Host:       "kshare",
					UsageScope: "global",
					RawURL:     "https://kshare.club/public.png",
				},
			}}}
			filterNativeSlotVariants(slots, records, localCfg, registry, binding)
			want := 1
			if scenario == "current" {
				want = 2
			}
			if len(slots[0].Variants) != want || slots[0].Variants[len(slots[0].Variants)-1].UsageScope != "global" {
				t.Fatal("native slot bypassed exact authority or public link was invalidated")
			}
		})
	}
	normalized := link
	normalized.UsageScope = "tracker:gpw"
	if !NativeImageLinkCurrent(cfg, registry, normalized) {
		t.Fatal("normalized native scope lost its current account")
	}
	normalized.AccountScope = "stale-account"
	if NativeImageLinkCurrent(cfg, registry, normalized) {
		t.Fatal("normalized native scope bypassed account validation")
	}
	t.Run("bare fallback", func(t *testing.T) {
		publicURL := "https://pixhost.to/public.png"
		fallback := reusableFallbackImageURLs([]string{link.RawURL, "https://kshare.club:443/image.png", publicURL}, registry)
		if len(fallback) != 1 || fallback[0] != publicURL {
			t.Fatalf("expected only the ordinary fallback, got %v", fallback)
		}
	})
	// The description consumes both hosts for a mixed native/local selection.
	other := filepath.Join(t.TempDir(), "local.png")
	exact := &api.ExactMediaAssets{
		ImageHostUploadSkipped: true,
		Screenshots:            []api.ScreenshotImage{{Path: imagePath, Purpose: api.ScreenshotPurposeFinal}, {Path: other, Purpose: api.ScreenshotPurposeFinal}},
		ScreenshotUploads: []api.UploadedImageLink{link, {
			ImagePath:  other,
			Purpose:    api.ScreenshotPurposeFinal,
			Host:       "pixhost",
			UsageScope: "global",
			RawURL:     "https://pixhost.to/local.png",
		}},
	}
	meta := api.UploadSubject{
		SourcePath:   binding.SourcePath,
		MediaBinding: binding,
		ExactMedia:   exact,
	}
	filtered, err := exactMediaForTrackerHost("GPW", meta, cfg, config.TrackerConfig{}, registry)
	if err != nil || len(filtered.ScreenshotUploads) != 2 {
		t.Fatalf("mixed native result dropped a selected image: %v", err)
	}
	meta.ExactMedia = filtered
	preloaded, err := preloadDescriptionAssetData(t.Context(), meta, nil, registry)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ensureDescriptionImageHostWithDataAndRegistry(t.Context(), "GPW", meta, cfg, config.TrackerConfig{}, &stubRepo{}, nil, api.NopLogger{}, registry, preloaded)
	if err != nil || len(resolved.screenshots) != 2 {
		t.Fatalf("description could not consume mixed retained results without writes: %v", err)
	}
	t.Run("description reader checks the current account", func(t *testing.T) {
		assets := clonePreloadedDescriptionAssetData(preloaded)
		assets.screenshotSlots[0].OriginalURL = link.RawURL
		rotated := cfg
		rotated.Trackers.Trackers = map[string]config.TrackerConfig{"GPW": {APIKey: "synthetic-replacement"}}
		_, images, err := resolveDescriptionScreenshots(t.Context(), "GPW", meta, rotated, nil, api.NopLogger{}, assets, registry)
		if err == nil || len(images) != 0 {
			t.Fatalf("description reused an unverified native original: %v, %v", images, err)
		}

		publicURL := "https://kshare.club/public.png"
		assets.screenshotSlots[0].Variants = append(assets.screenshotSlots[0].Variants, api.ScreenshotSlotVariant{
			ImagePath:  imagePath,
			Host:       "kshare",
			UsageScope: "global",
			RawURL:     publicURL,
		})

		_, images, err = resolveDescriptionScreenshots(t.Context(), "GPW", meta, rotated, nil, api.NopLogger{}, assets, registry)
		if err != nil || len(images) != 2 || images[0].RawURL != publicURL {
			t.Fatalf("description did not retain the ordinary replacement: %v, %v", images, err)
		}
		_, images, err = resolveDescriptionScreenshots(t.Context(), "GPW", meta, cfg, nil, api.NopLogger{}, assets, registry)
		if err != nil || len(images) != 2 || images[0].RawURL != link.RawURL {
			t.Fatalf("reader removed the current native link or mutated reusable slots: %v, %v", images, err)
		}
	})
	t.Run("non-renderable native slot cannot authorize mixed selection", func(t *testing.T) {
		assets := clonePreloadedDescriptionAssetData(preloaded)
		assets.screenshotSlots[0].RenderInScreenshots = false
		assets.screenshotSlots = append(assets.screenshotSlots, api.ScreenshotSlot{
			SlotOrder:           2,
			RenderInScreenshots: true,
			Variants: []api.ScreenshotSlotVariant{{
				Host:       "imgbb",
				UsageScope: "global",
				RawURL:     "https://i.ibb.co/sample/other.png",
			}},
		})
		resolved, err := ensureDescriptionImageHostWithDataAndRegistry(t.Context(), "GPW", meta, cfg, config.TrackerConfig{}, &stubRepo{}, nil, api.NopLogger{}, registry, assets)
		if err != nil || len(resolved.screenshots) != 0 || resolved.feedback.Status != "warning" {
			t.Fatalf("non-renderable slot changed the ordinary hosting policy: %v, %v", resolved, err)
		}
	})
}
