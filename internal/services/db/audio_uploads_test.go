// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAudioUploadClonesPreserveProvenanceAndHistoryOwnership(t *testing.T) {
	repo := openMigratedTestRepo(t)
	binding := testPreparedMediaBinding(filepath.Join(t.TempDir(), "Example.Release.mkv"))
	prior := api.ReleaseRef{SourcePath: binding.SourcePath, Generation: binding.PreparedGeneration}
	root := t.TempDir()
	from, to := filepath.Join(root, "original.png"), filepath.Join(root, "restored.png")
	for _, scope := range []string{"global", "tracker:ONE"} {
		if err := repo.SaveUploadedImages(t.Context(), binding, "example-host", []api.UploadedImageLink{{
			ImagePath:    from,
			Purpose:      api.ScreenshotPurposeAudioAnalysis,
			UsageScope:   scope,
			AccountScope: "account-fingerprint",
			RawURL:       "https://example.invalid/audio.png",
		}}); err != nil {
			t.Fatal(err)
		}
	}
	normal := filepath.Join(root, "normal.png")
	if err := repo.SaveUploadedImages(t.Context(), binding, "example-host", []api.UploadedImageLink{{
		ImagePath: normal,
		Purpose:   api.ScreenshotPurposeFinal,
		RawURL:    "https://example.invalid/normal.png",
	}}); err != nil {
		t.Fatal(err)
	}
	target := binding
	target.PreparedGeneration++
	target.PreparedMediaFingerprint = "restored-fingerprint"
	paths := map[string]string{from: to, normal: filepath.Join(root, "not-audio.png")}
	for range 2 {
		if err := repo.CloneAudioAnalysisUploads(t.Context(), prior, target, paths); err != nil {
			t.Fatal(err)
		}
	}
	links, err := repo.ListUploadedImagesByPath(t.Context(), target)
	if err != nil || len(links) != 2 {
		t.Fatalf("cloned links = %#v, %v", links, err)
	}
	for _, link := range links {
		if link.ImagePath != to || link.AccountScope != "account-fingerprint" || link.Host != "example-host" || link.Purpose != api.ScreenshotPurposeAudioAnalysis {
			t.Fatalf("changed provenance: %#v", link)
		}
	}
	original, err := repo.ListUploadedImagesByPath(t.Context(), binding)
	if err != nil || len(original) != 3 {
		t.Fatalf("original links = %#v, %v", original, err)
	}
	other := target
	other.SourcePath = filepath.Join(t.TempDir(), "Other.Release.mkv")
	if err := repo.CloneAudioAnalysisUploads(t.Context(), prior, other, paths); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("cross-source copy = %v", err)
	}
	if err := repo.PurgeContentData(t.Context(), binding.SourcePath); err != nil {
		t.Fatal(err)
	}
	if err := repo.CloneAudioAnalysisUploads(t.Context(), prior, target, paths); err != nil {
		t.Fatal(err)
	}
	for _, query := range []api.PreparedMediaBinding{binding, target} {
		links, err := repo.ListUploadedImagesByPath(t.Context(), query)
		if err != nil || len(links) != 0 {
			t.Fatalf("history retained links: %#v, %v", links, err)
		}
	}
}

func TestAudioUploadAccountMigrationPreservesLegacyRecords(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(t.Context(), `CREATE TABLE uploaded_images (image_path TEXT); INSERT INTO uploaded_images VALUES ('original.png')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrateUploadedImageAccountScope(t.Context(), raw); err != nil {
			t.Fatal(err)
		}
	}
	var path, account string
	if err := raw.QueryRowContext(t.Context(), `SELECT image_path, account_scope FROM uploaded_images`).Scan(&path, &account); err != nil {
		t.Fatal(err)
	}
	if path != "original.png" || account != "" {
		t.Fatalf("legacy provenance changed: %q %q", path, account)
	}
}

func TestAudioUploadCloneFailureRollsBackAllNewLinks(t *testing.T) {
	repo := openMigratedTestRepo(t)
	binding := testPreparedMediaBinding(filepath.Join(t.TempDir(), "Example.Release.mkv"))
	prior := api.ReleaseRef{SourcePath: binding.SourcePath, Generation: binding.PreparedGeneration}
	root := t.TempDir()
	paths := make(map[string]string)
	for _, name := range []string{"waveform", "spectrogram"} {
		from := filepath.Join(root, name+".png")
		paths[from] = filepath.Join(root, name+"-copy.png")
		if err := repo.SaveUploadedImages(t.Context(), binding, "example-host", []api.UploadedImageLink{{
			ImagePath:    from,
			Purpose:      api.ScreenshotPurposeAudioAnalysis,
			AccountScope: "account",
			RawURL:       "https://example.invalid/audio.png",
		}}); err != nil {
			t.Fatal(err)
		}
	}
	target := binding
	target.PreparedGeneration++
	target.PreparedMediaFingerprint = "next"
	if _, err := repo.RawDB().ExecContext(t.Context(), `CREATE TRIGGER fail_second_audio_clone BEFORE INSERT ON uploaded_images
  WHEN NEW.prepared_generation = 2 AND (SELECT COUNT(*) FROM uploaded_images WHERE prepared_generation = 2) > 0
  BEGIN SELECT RAISE(ABORT, 'second clone failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := repo.CloneAudioAnalysisUploads(t.Context(), prior, target, paths); err == nil {
		t.Fatal("second insert succeeded unexpectedly")
	}
	links, err := repo.ListUploadedImagesByPath(t.Context(), target)
	if err != nil || len(links) != 0 {
		t.Fatalf("partial transaction survived: %#v %v", links, err)
	}
	original, err := repo.ListUploadedImagesByPath(t.Context(), binding)
	if err != nil || len(original) != 2 {
		t.Fatalf("original links lost: %#v %v", original, err)
	}
}

func TestAudioUploadCloneRejectsStalePreparedGeneration(t *testing.T) {
	repo := openMigratedTestRepo(t)
	binding := testPreparedMediaBinding(filepath.Join(t.TempDir(), "Example.Release.mkv"))
	prior := api.ReleaseRef{SourcePath: binding.SourcePath, Generation: binding.PreparedGeneration}
	if _, err := repo.RawDB().ExecContext(t.Context(), `INSERT INTO prepared_release_current (
  source_path, generation, source_fingerprint, fact_instruction_fingerprint,
  policy_fingerprint, contract_version, source_json, naming_json, episode_json,
  media_json, disc_json, assessments_json, prepared_at
 ) VALUES (?, 3, 'source', 'facts', 'policy', 'contract', '{}', '{}', '{}', '{}', '{}', '{}', '2026-10-01T00:00:00Z')`, binding.SourcePath); err != nil {
		t.Fatal(err)
	}
	binding.PreparedGeneration = 2
	root := t.TempDir()
	if err := repo.CloneAudioAnalysisUploads(t.Context(), prior, binding, map[string]string{filepath.Join(root, "original.png"): filepath.Join(root, "restored.png")}); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("stale prepared clone = %v", err)
	}
}
