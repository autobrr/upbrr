// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedImportedScreenshotEvidenceSurvivesRestart(t *testing.T) {
	repo := openPreparedReleaseTestRepo(t)
	release := preparedReleaseDBFixture(filepath.Join(t.TempDir(), "Synthetic.2026.mkv"), 3)
	release.ProviderMetadata.ImportedScreenshotEvidence = &api.ImportedScreenshotEvidence{
		Tracker:                "AITHER",
		TrackerID:              "42",
		SourceFingerprint:      release.Compatibility.SourceFingerprint,
		PolicyVersion:          api.ImportedScreenshotEvidencePolicyVersion,
		DescriptionFingerprint: "description-images",
		References:             []api.ImportedScreenshotReference{{RawURL: "https://images.example/full"}},
	}
	if err := repo.CommitPreparedRelease(t.Context(), release); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.LoadPreparedRelease(t.Context(), release.Source.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.ProviderMetadata.ImportedScreenshotEvidence, release.ProviderMetadata.ImportedScreenshotEvidence) {
		t.Fatal("prepared receipt did not round-trip")
	}
	// Source-level legacy provider writes cannot replace prepared receipt authority.
	if err := repo.SaveExternalMetadata(t.Context(), api.SourceScopedMetadata{SourcePath: release.Source.SourcePath}); err != nil {
		t.Fatal(err)
	}
	loaded, err = repo.LoadPreparedRelease(t.Context(), release.Source.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.ProviderMetadata.ImportedScreenshotEvidence, release.ProviderMetadata.ImportedScreenshotEvidence) {
		t.Fatal("source cache write altered receipt")
	}
	legacy, err := repo.GetExternalMetadata(t.Context(), release.Source.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ImportedScreenshotEvidence != nil {
		t.Fatal("source cache read regained prepared receipt")
	}
}

func TestImportedScreenshotEvidenceMigrationDefaultsUnknown(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(t.Context(), `CREATE TABLE external_metadata (source_path TEXT, tmdb_json TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(t.Context(), `INSERT INTO external_metadata VALUES ('synthetic', '{}')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrateImportedScreenshotEvidence(t.Context(), raw); err != nil {
			t.Fatal(err)
		}
	}
	var receipt, tmdb string
	if err := raw.QueryRowContext(t.Context(), `SELECT imported_screenshot_evidence_json, tmdb_json FROM external_metadata`).Scan(&receipt, &tmdb); err != nil {
		t.Fatal(err)
	}
	if receipt != "" || tmdb != "{}" {
		t.Fatalf("legacy authority changed: %q %q", receipt, tmdb)
	}
}
