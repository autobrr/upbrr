// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestScreenshotProvenanceMigrationPreservesLegacyUnknown(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repo, err := Open(filepath.Join(root, "legacy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	prior := slices.DeleteFunc(slices.Clone(migrationRegistry), func(step migrationStep) bool {
		return step.id == "2026_10_add_screenshot_provenance"
	})
	if err := migrateContextWithRegistry(t.Context(), repo.db, prior); err != nil {
		t.Fatal(err)
	}
	binding := testPreparedMediaBinding(filepath.Join(root, "Example.Release.mkv"))
	imagePath := filepath.Join(root, "legacy.png")
	key := api.MediaCompatibilityKey(mediaReuseTestSHA256("source"))
	digest := mediaReuseTestSHA256("legacy image")
	if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO screenshots (
		source_path, prepared_media_fingerprint, prepared_generation, image_path, timestamp,
		frame_number, width, height, purpose, captured_at
	) VALUES (?, ?, ?, ?, 12, 24, 1920, 1080, 'final', '')`,
		binding.SourcePath, binding.PreparedMediaFingerprint, binding.PreparedGeneration, imagePath); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO uploaded_images (
		source_path, prepared_media_fingerprint, prepared_generation, image_path, host, raw_url, uploaded_at
	) VALUES (?, ?, ?, ?, 'example-host', 'https://images.example.invalid/legacy.png', '')`,
		binding.SourcePath, binding.PreparedMediaFingerprint, binding.PreparedGeneration, imagePath); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO media_reusable_assets (
		source_path, prepared_media_fingerprint, prepared_generation, compatibility_key,
		capture_fingerprint, content_sha256, kind, purpose, image_path, captured_at
	) VALUES (?, ?, ?, ?, ?, ?, 'screenshot', 'final', ?, '')`,
		binding.SourcePath, binding.PreparedMediaFingerprint, binding.PreparedGeneration, key,
		mediaReuseTestSHA256("capture"), digest, imagePath); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO media_reusable_hosted_links (
		source_path, prepared_media_fingerprint, prepared_generation, compatibility_key,
		capture_fingerprint, content_sha256, host, usage_scope, account_scope, raw_url, uploaded_at
	) VALUES (?, ?, ?, ?, ?, ?, 'example-host', 'global', 'account', 'https://images.example.invalid/legacy.png', '')`,
		binding.SourcePath, binding.PreparedMediaFingerprint, binding.PreparedGeneration, key,
		mediaReuseTestSHA256("capture"), digest); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		reopened, err := Open(repo.path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		if err := reopened.Migrate(); err != nil {
			t.Fatal(err)
		}
		if err := migrateAddScreenshotProvenance(t.Context(), reopened.db); err != nil {
			t.Fatalf("repeat additive migration: %v", err)
		}
		snapshot, err := reopened.LoadMediaAssetSnapshot(t.Context(), binding)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Screenshots) != 1 || snapshot.Screenshots[0].Tonemapping != "" ||
			snapshot.Screenshots[0].ContentSHA256 != "" || snapshot.Screenshots[0].Width != 1920 {
			t.Fatalf("migration changed legacy screenshot: %#v", snapshot.Screenshots)
		}
		if len(snapshot.UploadedImages) != 1 || snapshot.UploadedImages[0].Tonemapping != "" || snapshot.UploadedImages[0].ContentSHA256 != "" ||
			snapshot.UploadedImages[0].RawURL != "https://images.example.invalid/legacy.png" {
			t.Fatalf("migration changed legacy upload: %#v", snapshot.UploadedImages)
		}
		assets, err := reopened.LoadReusableMediaAssets(t.Context(), key)
		if err != nil || len(assets) != 1 || assets[0].Image.Tonemapping != "" || assets[0].ContentSHA256 != digest ||
			len(assets[0].HostedLinks) != 1 || assets[0].HostedLinks[0].Tonemapping != "" || assets[0].HostedLinks[0].ContentSHA256 != digest {
			t.Fatalf("migration changed reusable provenance: %#v, %v", assets, err)
		}
		if !slices.Contains(readSchemaMigrationIDs(t, reopened.db), "2026_10_add_screenshot_provenance") {
			t.Fatal("screenshot provenance migration was not recorded")
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScreenshotProvenanceSurvivesReopenAndReplacement(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	root := t.TempDir()
	binding := testPreparedMediaBinding(filepath.Join(root, "Example.Release.mkv"))
	imagePath := filepath.Join(root, "screenshot.png")
	digest := mediaReuseTestSHA256("tonemapped image")
	shot := api.Screenshot{
		ImagePath:     imagePath,
		Purpose:       api.ScreenshotPurposeFinal,
		Tonemapping:   api.ScreenshotTonemapped,
		ContentSHA256: digest,
	}
	link := api.UploadedImageLink{
		ImagePath:     imagePath,
		RawURL:        "https://images.example.invalid/tonemapped.png",
		Tonemapping:   api.ScreenshotTonemapped,
		ContentSHA256: digest,
	}
	if err := repo.SaveScreenshot(t.Context(), binding, shot); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveUploadedImages(t.Context(), binding, "example-host", []api.UploadedImageLink{link}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(repo.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	for range 2 {
		if err := reopened.Migrate(); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := reopened.LoadMediaAssetSnapshot(t.Context(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Screenshots) != 1 || snapshot.Screenshots[0].Tonemapping != api.ScreenshotTonemapped ||
		snapshot.Screenshots[0].ContentSHA256 != digest {
		t.Fatalf("reopened screenshot provenance = %#v", snapshot.Screenshots)
	}
	if len(snapshot.UploadedImages) != 1 || snapshot.UploadedImages[0].Tonemapping != api.ScreenshotTonemapped ||
		snapshot.UploadedImages[0].ContentSHA256 != digest || snapshot.UploadedImages[0].RawURL != link.RawURL {
		t.Fatalf("reopened hosted provenance = %#v", snapshot.UploadedImages)
	}
	shot.Tonemapping, shot.ContentSHA256 = "", ""
	if err := reopened.SaveScreenshot(t.Context(), binding, shot); err != nil {
		t.Fatal(err)
	}
	// Replacing local bytes must not rewrite the evidence bound to an older hosted URL.
	links, err := reopened.ListUploadedImagesByPath(t.Context(), binding)
	if err != nil || len(links) != 1 || links[0].Tonemapping != api.ScreenshotTonemapped || links[0].ContentSHA256 != digest {
		t.Fatalf("existing hosted evidence changed: %#v, %v", links, err)
	}
	link.Tonemapping, link.ContentSHA256 = "", ""
	link.RawURL = "https://images.example.invalid/replacement.png"
	if err := reopened.SaveUploadedImages(t.Context(), binding, "example-host", []api.UploadedImageLink{link}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = reopened.LoadMediaAssetSnapshot(t.Context(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Screenshots) != 1 || snapshot.Screenshots[0].Tonemapping != "" || snapshot.Screenshots[0].ContentSHA256 != "" {
		t.Fatalf("replacement retained screenshot evidence: %#v", snapshot.Screenshots)
	}
	if len(snapshot.UploadedImages) != 1 || snapshot.UploadedImages[0].Tonemapping != "" ||
		snapshot.UploadedImages[0].ContentSHA256 != "" || snapshot.UploadedImages[0].RawURL != link.RawURL {
		t.Fatalf("replacement retained hosted evidence: %#v", snapshot.UploadedImages)
	}
}

func TestMenuScreenshotProvenanceSurvivesCompensation(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	root := t.TempDir()
	binding := testPreparedMediaBinding(filepath.Join(root, "Example.Release.DVD"))
	imagePath := filepath.Join(root, "menu.png")
	digest := mediaReuseTestSHA256("menu image")
	shot := api.Screenshot{
		ImagePath:     imagePath,
		Purpose:       api.ScreenshotPurposeMenu,
		Tonemapping:   api.ScreenshotNotTonemapped,
		ContentSHA256: digest,
	}
	selection := api.ScreenshotFinalSelection{ImagePath: imagePath, Source: api.ScreenshotSelectionSourceMenu}
	if err := repo.AppendManualMenuScreenshots(t.Context(), binding, []api.Screenshot{shot}, []api.ScreenshotFinalSelection{selection}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveUploadedImages(t.Context(), binding, "example-host", []api.UploadedImageLink{{
		ImagePath:     imagePath,
		Purpose:       api.ScreenshotPurposeMenu,
		RawURL:        "https://images.example.invalid/menu.png",
		Tonemapping:   api.ScreenshotNotTonemapped,
		ContentSHA256: digest,
	}}); err != nil {
		t.Fatal(err)
	}
	deleted, err := repo.DeleteDiscMenuScreenshot(t.Context(), binding, imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Screenshot == nil || deleted.Screenshot.Tonemapping != api.ScreenshotNotTonemapped || deleted.Screenshot.ContentSHA256 != digest ||
		len(deleted.UploadedImages) != 1 || deleted.UploadedImages[0].Tonemapping != api.ScreenshotNotTonemapped ||
		deleted.UploadedImages[0].ContentSHA256 != digest {
		t.Fatalf("compensation lost provenance: %#v", deleted)
	}
	if err := repo.RestoreDiscMenuScreenshot(t.Context(), binding, deleted); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.LoadMediaAssetSnapshot(t.Context(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Screenshots) != 1 || snapshot.Screenshots[0].Tonemapping != api.ScreenshotNotTonemapped ||
		snapshot.Screenshots[0].ContentSHA256 != digest || len(snapshot.UploadedImages) != 1 ||
		snapshot.UploadedImages[0].Tonemapping != api.ScreenshotNotTonemapped || snapshot.UploadedImages[0].ContentSHA256 != digest {
		t.Fatalf("restored provenance = %#v", snapshot)
	}
	shot.Tonemapping, shot.ContentSHA256 = "", ""
	if err := repo.AppendManualMenuScreenshots(t.Context(), binding, []api.Screenshot{shot}, []api.ScreenshotFinalSelection{selection}); err != nil {
		t.Fatal(err)
	}
	shots, err := repo.ListScreenshotsByPath(t.Context(), binding)
	if err != nil || len(shots) != 1 || shots[0].Tonemapping != "" || shots[0].ContentSHA256 != "" {
		t.Fatalf("menu replacement retained provenance: %#v, %v", shots, err)
	}
}

func TestReusableScreenshotProvenanceSurvivesReopen(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	root := t.TempDir()
	binding := testPreparedMediaBinding(filepath.Join(root, "Example.Release.mkv"))
	key := api.MediaCompatibilityKey(mediaReuseTestSHA256("source"))
	digest := mediaReuseTestSHA256("screenshot image")
	asset := api.ReusableMediaAsset{
		Binding:            binding,
		CompatibilityKey:   key,
		CaptureFingerprint: api.WorkflowFingerprint(mediaReuseTestSHA256("capture")),
		ContentSHA256:      digest,
		Kind:               api.MediaArtifactScreenshot,
		Image: api.ScreenshotImage{
			Path:          filepath.Join(root, "image.png"),
			Purpose:       api.ScreenshotPurposeFinal,
			Tonemapping:   api.ScreenshotTonemapped,
			ContentSHA256: digest,
		},
		HostedLinks: []api.UploadedImageLink{{
			Host:          "example-host",
			UsageScope:    "global",
			AccountScope:  mediaReuseTestSHA256("account"),
			RawURL:        "https://images.example.invalid/screenshot.png",
			UploadedAt:    time.Now().UTC(),
			Tonemapping:   api.ScreenshotTonemapped,
			ContentSHA256: digest,
		}, {
			Host:         "unknown-host",
			UsageScope:   "global",
			AccountScope: mediaReuseTestSHA256("account"),
			RawURL:       "https://images.example.invalid/unknown.png",
			UploadedAt:   time.Now().UTC(),
		}},
	}
	if err := repo.CommitReusableMedia(t.Context(), binding, key, []api.ReusableMediaAsset{asset}, reusableMediaCommitForTest(binding)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(repo.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Migrate(); err != nil {
		t.Fatal(err)
	}
	assets, err := reopened.LoadReusableMediaAssets(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || assets[0].Image.Tonemapping != api.ScreenshotTonemapped || assets[0].Image.ContentSHA256 != digest ||
		len(assets[0].HostedLinks) != 2 || assets[0].HostedLinks[0].Tonemapping != api.ScreenshotTonemapped ||
		assets[0].HostedLinks[0].ContentSHA256 != digest || assets[0].HostedLinks[0].RawURL != asset.HostedLinks[0].RawURL ||
		assets[0].HostedLinks[1].Tonemapping != "" {
		t.Fatalf("reusable provenance = %#v", assets)
	}
	asset.Image.Tonemapping = ""
	if err := reopened.CommitReusableMedia(t.Context(), binding, key, []api.ReusableMediaAsset{asset}, reusableMediaCommitForTest(binding)); err != nil {
		t.Fatal(err)
	}
	assets, err = reopened.LoadReusableMediaAssets(t.Context(), key)
	if err != nil || len(assets) != 1 || assets[0].Image.Tonemapping != "" ||
		len(assets[0].HostedLinks) != 2 || assets[0].HostedLinks[0].Tonemapping != api.ScreenshotTonemapped || assets[0].HostedLinks[1].Tonemapping != "" {
		t.Fatalf("reusable replacement retained provenance: %#v, %v", assets, err)
	}
	asset.HostedLinks[0].Tonemapping = ""
	if err := reopened.CommitReusableMedia(t.Context(), binding, key, []api.ReusableMediaAsset{asset}, reusableMediaCommitForTest(binding)); err != nil {
		t.Fatal(err)
	}
	assets, err = reopened.LoadReusableMediaAssets(t.Context(), key)
	if err != nil || len(assets) != 1 || len(assets[0].HostedLinks) != 2 || assets[0].HostedLinks[0].Tonemapping != "" {
		t.Fatalf("hosted replacement retained provenance: %#v, %v", assets, err)
	}
}

func TestReusableScreenshotRejectsContradictoryProvenance(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*api.ReusableMediaAsset)
	}{
		{name: "image digest", change: func(asset *api.ReusableMediaAsset) {
			asset.Image.ContentSHA256 = mediaReuseTestSHA256("different bytes")
		}},
		{name: "hosted digest", change: func(asset *api.ReusableMediaAsset) {
			asset.HostedLinks[0].ContentSHA256 = mediaReuseTestSHA256("different bytes")
		}},
		{name: "hosted processing", change: func(asset *api.ReusableMediaAsset) { asset.HostedLinks[0].Tonemapping = api.ScreenshotNotTonemapped }},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := openMigratedTestRepo(t)
			root := t.TempDir()
			binding := testPreparedMediaBinding(filepath.Join(root, "Example.Release.mkv"))
			key := api.MediaCompatibilityKey(mediaReuseTestSHA256("source"))
			digest := mediaReuseTestSHA256("screenshot image")
			asset := api.ReusableMediaAsset{
				Binding:            binding,
				CompatibilityKey:   key,
				CaptureFingerprint: api.WorkflowFingerprint(mediaReuseTestSHA256("capture")),
				ContentSHA256:      digest,
				Kind:               api.MediaArtifactScreenshot,
				Image: api.ScreenshotImage{
					Path:          filepath.Join(root, "image.png"),
					Purpose:       api.ScreenshotPurposeFinal,
					Tonemapping:   api.ScreenshotTonemapped,
					ContentSHA256: digest,
				},
				HostedLinks: []api.UploadedImageLink{{
					Host:          "example-host",
					UsageScope:    "global",
					AccountScope:  mediaReuseTestSHA256("account"),
					RawURL:        "https://images.example.invalid/screenshot.png",
					UploadedAt:    time.Now().UTC(),
					Tonemapping:   api.ScreenshotTonemapped,
					ContentSHA256: digest,
				}},
			}
			test.change(&asset)
			if err := repo.CommitReusableMedia(t.Context(), binding, key, []api.ReusableMediaAsset{asset}, reusableMediaCommitForTest(binding)); err == nil {
				t.Fatal("contradictory reusable provenance accepted")
			}
		})
	}
}
