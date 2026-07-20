// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedReleaseCommitExactReadback(t *testing.T) {
	repo := openPreparedReleaseTestRepo(t)
	release := preparedReleaseDBFixture(filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv"), 7)

	if err := repo.CommitPreparedRelease(context.Background(), release); err != nil {
		t.Fatalf("commit prepared release: %v", err)
	}
	loaded, err := repo.LoadPreparedRelease(context.Background(), release.Source.SourcePath)
	if err != nil {
		t.Fatalf("load prepared release: %v", err)
	}
	if !reflect.DeepEqual(loaded, release) {
		t.Fatalf("loaded prepared release differs\ngot:  %#v\nwant: %#v", loaded, release)
	}
}

func TestPreparedReleaseCommitFailureRollsBackWholeGeneration(t *testing.T) {
	repo := openPreparedReleaseTestRepo(t)
	release := preparedReleaseDBFixture(filepath.Join(t.TempDir(), "Example.Release.2026.2160p-GRP.mkv"), 9)
	ctx := context.Background()
	if _, err := repo.RawDB().ExecContext(ctx, `
		CREATE TRIGGER fail_prepared_metadata
		BEFORE INSERT ON external_metadata
		BEGIN
			SELECT RAISE(ABORT, 'forced provider metadata failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	err := repo.CommitPreparedRelease(ctx, release)
	if err == nil || !strings.Contains(err.Error(), "forced provider metadata failure") {
		t.Fatalf("commit error = %v", err)
	}
	if _, err := repo.LoadPreparedRelease(ctx, release.Source.SourcePath); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("load after rollback error = %v, want not found", err)
	}
	var identityCount int
	if err := repo.RawDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM external_ids WHERE source_path = ?`, release.Source.SourcePath).Scan(&identityCount); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if identityCount != 0 {
		t.Fatalf("identity rows after rollback = %d", identityCount)
	}
}

func TestPreparedReleaseLoadRejectsMixedGeneration(t *testing.T) {
	repo := openPreparedReleaseTestRepo(t)
	release := preparedReleaseDBFixture(filepath.Join(t.TempDir(), "Example.Release.2026.720p-GRP.mkv"), 4)
	ctx := context.Background()
	if err := repo.CommitPreparedRelease(ctx, release); err != nil {
		t.Fatalf("commit prepared release: %v", err)
	}
	if _, err := repo.RawDB().ExecContext(
		ctx,
		`UPDATE external_metadata SET generation = ? WHERE source_path = ?`,
		int64(release.Generation+1),
		release.Source.SourcePath,
	); err != nil {
		t.Fatalf("corrupt provider generation: %v", err)
	}

	_, err := repo.LoadPreparedRelease(ctx, release.Source.SourcePath)
	if err == nil || !strings.Contains(err.Error(), "generation mismatch") {
		t.Fatalf("load error = %v, want generation mismatch", err)
	}
}

func TestPreparedReleasePurgeDeletesGenerationState(t *testing.T) {
	repo := openPreparedReleaseTestRepo(t)
	release := preparedReleaseDBFixture(filepath.Join(t.TempDir(), "Example.Release.2026.WEB-GRP.mkv"), 3)
	ctx := context.Background()
	if err := repo.CommitPreparedRelease(ctx, release); err != nil {
		t.Fatalf("commit prepared release: %v", err)
	}
	if err := repo.PurgePreparedRelease(ctx, release.Source.SourcePath); err != nil {
		t.Fatalf("purge prepared release: %v", err)
	}
	if _, err := repo.LoadPreparedRelease(ctx, release.Source.SourcePath); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("load after purge error = %v, want not found", err)
	}
	for _, table := range []string{"external_ids", "external_metadata"} {
		var count int
		query := `SELECT COUNT(*) FROM ` + table + ` WHERE source_path = ?`
		if err := repo.RawDB().QueryRowContext(ctx, query, release.Source.SourcePath).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows after purge = %d", table, count)
		}
	}
}

func TestCanonicalReleaseGenerationMigrationSeedsLegacyIdentityLineage(t *testing.T) {
	repoPath := filepath.Join(t.TempDir(), "legacy-prepared-release.db")
	repo, err := OpenWithLogger(repoPath, nopLogger{})
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	ctx := context.Background()
	statements := []string{
		`CREATE TABLE external_ids (
			source_path TEXT PRIMARY KEY, tmdb_id INTEGER NOT NULL DEFAULT 0,
			imdb_id INTEGER NOT NULL DEFAULT 0, tvdb_id INTEGER NOT NULL DEFAULT 0,
			tvmaze_id INTEGER NOT NULL DEFAULT 0, mal_id INTEGER NOT NULL DEFAULT 0,
			category TEXT NOT NULL DEFAULT "", source_tmdb TEXT NOT NULL DEFAULT "",
			source_imdb TEXT NOT NULL DEFAULT "", source_tvdb TEXT NOT NULL DEFAULT "",
			source_tvmaze TEXT NOT NULL DEFAULT "", source_mal TEXT NOT NULL DEFAULT "",
			updated_at TEXT NOT NULL
		)`,
		externalMetadataTableDDL,
		`INSERT INTO external_ids (source_path, tmdb_id, category, updated_at)
			VALUES ("", 123456, "MOVIE", "2026-07-14T00:00:00Z")`,
	}
	for _, statement := range statements {
		if _, err := repo.RawDB().ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed legacy schema: %v", err)
		}
	}
	if err := migrateAddCanonicalReleaseGenerations(ctx, repo.RawDB()); err != nil {
		t.Fatalf("migrate prepared release generations: %v", err)
	}
	if err := migrateAddCanonicalReleaseGenerations(ctx, repo.RawDB()); err != nil {
		t.Fatalf("repeat prepared release migration: %v", err)
	}

	var generation int64
	var sourceTMDB string
	var categoryProvenance string
	var contractVersion string
	if err := repo.RawDB().QueryRowContext(ctx, `
		SELECT generation, source_tmdb, category_provenance, contract_version
		FROM external_ids WHERE source_path = ""
	`).Scan(&generation, &sourceTMDB, &categoryProvenance, &contractVersion); err != nil {
		t.Fatalf("read migrated identity: %v", err)
	}
	if generation != 0 || sourceTMDB != "legacy" || categoryProvenance != "legacy" || contractVersion != "legacy" {
		t.Fatalf(
			"migrated identity lineage = generation=%d tmdb=%q category=%q contract=%q",
			generation,
			sourceTMDB,
			categoryProvenance,
			contractVersion,
		)
	}
}

func openPreparedReleaseTestRepo(t *testing.T) *SQLiteRepository {
	t.Helper()
	repo, err := OpenWithLogger(filepath.Join(t.TempDir(), "prepared-release.db"), nopLogger{})
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}
	return repo
}

func preparedReleaseDBFixture(sourcePath string, generation api.PreparedGeneration) api.PreparedRelease {
	preparedAt := time.Date(2026, time.July, 14, 1, 2, 3, 4, time.UTC)
	return api.PreparedRelease{
		Generation: generation,
		Compatibility: api.PreparationCompatibility{
			SourceFingerprint:          "source-fingerprint",
			FactInstructionFingerprint: "instruction-fingerprint",
			PolicyFingerprint:          "policy-fingerprint",
			ContractVersion:            "prepared-v1",
		},
		Source: api.SourceManifest{
			SourcePath: sourcePath,
			Size:       42,
			Entries: []api.SourceManifestEntry{
				{
					Path:       sourcePath,
					Type:       api.SourceEntryTypeFile,
					Size:       42,
					ModifiedAt: preparedAt.Add(-time.Hour),
				},
			},
			Classification: api.SourceClassification{Container: "Matroska", MediaType: "encode"},
		},
		Naming: api.NamingFacts{
			Filename:    filepath.Base(sourcePath),
			ReleaseName: "Example.Release.2026.1080p-GRP",
			Title:       "Example Release 2026",
			Year:        2026,
			Codecs:      []string{"H.264"},
			Languages:   []string{"English"},
		},
		Episode: api.EpisodeFacts{
Season: 1,
 Episode: 2,
 SeasonLabel: "S01",
 EpisodeLabel: "E02",
},
		Media: api.MediaFacts{
			AudioLanguages:    []string{"English"},
			SubtitleLanguages: []string{"English"},
			Container:         "Matroska",
			VideoCodec:        "H.264",
			MediaInfoUniqueID: "example-unique-id",
		},
		Disc: api.DiscFacts{Type: "", Summary: "not applicable"},
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Generation: generation,
			TMDBID:     123456,
			IMDBID:     7654321,
			Category:   api.CanonicalCategoryMovie,
			Provenance: api.IdentityProvenanceSet{
				TMDB:     api.IdentityProvenanceExplicit,
				IMDB:     api.IdentityProvenanceProvider,
				TVDB:     api.IdentityProvenanceUnknown,
				TVmaze:   api.IdentityProvenanceUnknown,
				MAL:      api.IdentityProvenanceUnknown,
				Category: api.IdentityProvenanceExplicit,
			},
			Overrides: api.IdentityOverrideState{
				TMDB:     api.OverrideStateValue,
				IMDB:     api.OverrideStateUnset,
				TVDB:     api.OverrideStateClear,
				TVmaze:   api.OverrideStateUnset,
				MAL:      api.OverrideStateUnset,
				Category: api.OverrideStateValue,
			},
			Conflict: api.IdentityConflictNone,
			Resolution: api.IdentityResolutionKey{
				SourceFingerprint: "source-fingerprint",
				IntentFingerprint: "identity-intent-fingerprint",
				ContractVersion:   "identity-v1",
			},
			ResolvedAt: preparedAt.Add(-time.Minute),
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			Generation: generation,
			TMDB: &api.TMDBMetadata{
				TMDBID:          123456,
				Title:           "Example Release 2026",
				LocalizedTitles: map[string]string{"en": "Example Release 2026"},
			},
			UpdatedAt: preparedAt,
		},
		Assessments: api.ReleaseAssessments{
			MediaInfoUniqueID:       api.UniqueIDStatusPresent,
			MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent,
			Naming: api.NamingAssessment{
				Status: api.NamingStatusComplete,
			},
		},
		PreparedAt: preparedAt,
	}
}
