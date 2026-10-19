// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestImageCollectionReservationSurvivesReopen(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "collections.db")
	repo, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	binding := testPreparedMediaBinding(filepath.Join(t.TempDir(), "Synthetic.mkv"))
	record := api.ImageCollection{
		Fingerprint:        "set",
		AttemptID:          "first-attempt",
		ContentFingerprint: "content",
		Host:               "slowpics",
		Purpose:            api.ScreenshotPurposeComparison,
		Category:           "Comps",
		Label:              "Set",
		AccountScope:       "anonymous",
		Members:            []api.ImageCollectionMember{{Identity: "hash", Label: "Image"}},
	}
	_, acquired, err := repo.ReserveImageCollection(t.Context(), binding, record)
	if err != nil || !acquired {
		t.Fatalf("reserve = %t, %v", acquired, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	pending, acquired, err := repo.ReserveImageCollection(t.Context(), binding, record)
	if err != nil || acquired || pending == nil || pending.Completed {
		t.Fatalf("reopen recreated pending collection: %#v %t %v", pending, acquired, err)
	}
	next := binding
	next.PreparedGeneration++
	newer := record
	newer.Fingerprint = "new-generation"
	newer.AttemptID = "new-attempt"
	pending, acquired, err = repo.ReserveImageCollection(t.Context(), next, newer)
	if err != nil || acquired || pending == nil || pending.AttemptID != record.AttemptID {
		t.Fatalf("generation bypassed pending fence: %t %v", acquired, err)
	}
	record.URL, record.Completed = "https://slow.pics/c/synthetic", true
	if err := repo.CompleteImageCollection(t.Context(), binding, record); err != nil {
		t.Fatal(err)
	}
	completed, acquired, err := repo.ReserveImageCollection(t.Context(), binding, newer)
	if err != nil || !acquired || completed != nil {
		t.Fatalf("new exact set = %#v %t %v", completed, acquired, err)
	}
	if err := repo.ReleaseImageCollection(t.Context(), binding, newer); err != nil {
		t.Fatal(err)
	}
	cached, err := repo.LoadImageCollection(t.Context(), binding, record.Fingerprint)
	if err != nil || cached == nil || cached.Validate() != nil {
		t.Fatalf("complete set = %#v %v", cached, err)
	}
	if found, err := repo.LoadImageCollection(t.Context(), next, record.Fingerprint); err != nil || found != nil {
		t.Fatalf("cross-generation reuse: %#v %v", found, err)
	}
}

func TestImageCollectionMigrationPreservesLegacyReusableAssets(t *testing.T) {
	t.Parallel()
	repo, legacy := imageCollectionPriorMigrationFixture(t)
	for range 2 {
		if err := repo.MigrateContext(t.Context()); err != nil {
			t.Fatalf("upgrade prior database: %v", err)
		}
	}
	for _, column := range []string{"comparison_group", "comparison_count", "comparison_label", "comparison_member_label"} {
		exists, err := tableColumnExists(t.Context(), repo.RawDB(), "media_reusable_assets", column)
		if err != nil || !exists {
			t.Fatalf("provenance column %s exists=%v error=%v", column, exists, err)
		}
	}
	assertSQLiteObjectExists(t, repo.RawDB(), "table", "image_collections")
	assertSQLiteObjectExists(t, repo.RawDB(), "index", "idx_image_collection_pending")
	for _, id := range []string{"2026_10_image_collections", "2026_10_add_comparison_provenance"} {
		if !slices.Contains(readSchemaMigrationIDs(t, repo.RawDB()), id) {
			t.Fatalf("upgrade did not record migration %s", id)
		}
	}
	loaded, err := repo.LoadReusableMediaAssets(t.Context(), legacy.CompatibilityKey)
	if err != nil || len(loaded) != 1 || !reflect.DeepEqual(loaded[0], legacy) {
		t.Fatalf("legacy row changed or gained comparison authority: got=%+v want=%+v error=%v", loaded, legacy, err)
	}
	if _, err := repo.RawDB().ExecContext(t.Context(),
		`UPDATE media_reusable_assets SET comparison_group = ?, comparison_count = ?, comparison_label = ?, comparison_member_label = ? WHERE image_path = ?`,
		"synthetic-group", 2, "Synthetic comparison", "Source", legacy.Image.Path,
	); err != nil {
		t.Fatal(err)
	}
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatalf("repeat upgrade: %v", err)
	}
	loaded, err = repo.LoadReusableMediaAssets(t.Context(), legacy.CompatibilityKey)
	legacy.Image.ComparisonGroup = "synthetic-group"
	legacy.Image.ComparisonCount = 2
	legacy.Image.ComparisonLabel = "Synthetic comparison"
	legacy.Image.ComparisonMemberLabel = "Source"
	if err != nil || len(loaded) != 1 || !reflect.DeepEqual(loaded[0], legacy) {
		t.Fatalf("repeat upgrade changed explicit provenance: got=%+v want=%+v error=%v", loaded, legacy, err)
	}
}

func TestImageCollectionMigrationPreservesExistingProvenanceColumns(t *testing.T) {
	t.Parallel()
	for _, allColumns := range []bool{false, true} {
		t.Run(fmt.Sprintf("all_columns_%t", allColumns), func(t *testing.T) {
			t.Parallel()
			repo, expected := imageCollectionPriorMigrationFixture(t)
			statements := []string{
				`ALTER TABLE media_reusable_assets ADD COLUMN comparison_group TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE media_reusable_assets ADD COLUMN comparison_member_label TEXT NOT NULL DEFAULT ''`,
				`UPDATE media_reusable_assets SET comparison_group = 'synthetic-group', comparison_member_label = 'Source'`,
			}
			expected.Image.ComparisonGroup = "synthetic-group"
			expected.Image.ComparisonMemberLabel = "Source"
			if allColumns {
				statements = append(statements,
					`ALTER TABLE media_reusable_assets ADD COLUMN comparison_count INTEGER NOT NULL DEFAULT 0`,
					`ALTER TABLE media_reusable_assets ADD COLUMN comparison_label TEXT NOT NULL DEFAULT ''`,
					`UPDATE media_reusable_assets SET comparison_count = 2, comparison_label = 'Synthetic comparison'`,
				)
				expected.Image.ComparisonCount = 2
				expected.Image.ComparisonLabel = "Synthetic comparison"
			}
			for _, statement := range statements {
				if _, err := repo.RawDB().ExecContext(t.Context(), statement); err != nil {
					t.Fatalf("seed existing provenance: %v", err)
				}
			}
			if err := repo.MigrateContext(t.Context()); err != nil {
				t.Fatalf("upgrade existing provenance: %v", err)
			}
			if err := migrateAddComparisonProvenance(t.Context(), repo.RawDB()); err != nil {
				t.Fatalf("reapply provenance migration: %v", err)
			}
			loaded, err := repo.LoadReusableMediaAssets(t.Context(), expected.CompatibilityKey)
			if err != nil || len(loaded) != 1 || !reflect.DeepEqual(loaded[0], expected) {
				t.Fatalf("upgrade changed existing provenance or legacy row: got=%+v want=%+v error=%v", loaded, expected, err)
			}
			if !slices.Contains(readSchemaMigrationIDs(t, repo.RawDB()), "2026_10_add_comparison_provenance") {
				t.Fatal("upgrade did not record provenance migration")
			}
		})
	}
}

func TestImageCollectionMigrationFailureRollsBackSchemaAndAuthority(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"during provenance", "after reservation"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			repo, legacy := imageCollectionPriorMigrationFixture(t)
			beforeIDs := readSchemaMigrationIDs(t, repo.RawDB())
			var beforeVersion int
			if err := repo.RawDB().QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&beforeVersion); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("synthetic collection migration failure")
			registry := slices.Clone(migrationRegistry)
			injected := false
			if stage == "during provenance" {
				for index := range registry {
					if registry[index].id == "2026_10_add_comparison_provenance" {
						registry[index].apply = func(ctx context.Context, exec migrationExecutor) error {
							return migrateAddComparisonProvenance(ctx, comparisonMigrationFailureExecutor{
								migrationExecutor: exec,
								failure:           failure,
								injected:          &injected,
							})
						}
					}
				}
			} else {
				registry = append(registry, migrationStep{
					id:        "test_collection_migration_failure",
					dependsOn: []string{"2026_10_image_collections", "2026_10_add_comparison_provenance"},
					apply: func(ctx context.Context, exec migrationExecutor) error {
						if _, err := exec.ExecContext(ctx, `INSERT INTO image_collections (source_path, prepared_generation, fingerprint, content_fingerprint, completed, record_json) VALUES (?, 1, 'uncommitted', 'uncommitted-content', 0, '{}')`, legacy.Binding.SourcePath); err != nil {
							return fmt.Errorf("insert synthetic reservation: %w", err)
						}
						if _, err := exec.ExecContext(ctx, `UPDATE media_reusable_assets SET comparison_group = 'uncommitted', comparison_count = 2 WHERE image_path = ?`, legacy.Image.Path); err != nil {
							return fmt.Errorf("update synthetic provenance: %w", err)
						}
						injected = true
						return failure
					},
				})
			}
			err := migrateContextWithRegistry(t.Context(), repo.RawDB(), registry)
			if !errors.Is(err, failure) || !injected {
				t.Fatalf("migration failure not reached: injected=%v error=%v", injected, err)
			}
			if ids := readSchemaMigrationIDs(t, repo.RawDB()); !slices.Equal(ids, beforeIDs) {
				t.Fatalf("failed migration left ledger entries: got=%v want=%v", ids, beforeIDs)
			}
			for _, column := range []string{"comparison_group", "comparison_count", "comparison_label", "comparison_member_label"} {
				exists, err := tableColumnExists(t.Context(), repo.RawDB(), "media_reusable_assets", column)
				if err != nil || exists {
					t.Fatalf("failed migration left column %s: exists=%v error=%v", column, exists, err)
				}
			}
			if exists, err := tableExists(t.Context(), repo.RawDB(), "image_collections"); err != nil || exists {
				t.Fatalf("failed migration left reservation table: exists=%v error=%v", exists, err)
			}
			var rowCount, version int
			if err := repo.RawDB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM media_reusable_assets WHERE image_path = ?", legacy.Image.Path).Scan(&rowCount); err != nil {
				t.Fatal(err)
			}
			if err := repo.RawDB().QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
				t.Fatal(err)
			}
			if rowCount != 1 || version != beforeVersion {
				t.Fatalf("rollback lost legacy row or version: rows=%d version=%d want=%d", rowCount, version, beforeVersion)
			}
			if err := repo.MigrateContext(t.Context()); err != nil {
				t.Fatalf("retry after rolled-back upgrade: %v", err)
			}
			loaded, err := repo.LoadReusableMediaAssets(t.Context(), legacy.CompatibilityKey)
			if err != nil || len(loaded) != 1 || !reflect.DeepEqual(loaded[0], legacy) {
				t.Fatalf("retry retained partial comparison authority: got=%+v want=%+v error=%v", loaded, legacy, err)
			}
			var reservations int
			if err := repo.RawDB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM image_collections").Scan(&reservations); err != nil {
				t.Fatal(err)
			}
			if reservations != 0 {
				t.Fatalf("failed upgrade retained %d reservations", reservations)
			}
		})
	}
}

type comparisonMigrationFailureExecutor struct {
	migrationExecutor
	failure  error
	injected *bool
}

func (e comparisonMigrationFailureExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.Contains(query, "ADD COLUMN comparison_count") {
		*e.injected = true
		return nil, e.failure
	}
	result, err := e.migrationExecutor.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("execute comparison migration fixture: %w", err)
	}
	return result, nil
}

func imageCollectionPriorMigrationFixture(t *testing.T) (*SQLiteRepository, api.ReusableMediaAsset) {
	t.Helper()
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	prior := slices.DeleteFunc(slices.Clone(migrationRegistry), func(step migrationStep) bool {
		return step.id == "2026_10_image_collections" || step.id == "2026_10_add_comparison_provenance"
	})
	if err := migrateContextWithRegistry(t.Context(), repo.RawDB(), prior); err != nil {
		t.Fatalf("create pre-collection database: %v", err)
	}
	legacy := api.ReusableMediaAsset{
		Binding:            testPreparedMediaBinding(filepath.Join(t.TempDir(), "Synthetic.Release.2026-GRP.mkv")),
		CompatibilityKey:   api.MediaCompatibilityKey(mediaReuseTestSHA256("synthetic source")),
		CaptureFingerprint: "synthetic-capture",
		ContentSHA256:      mediaReuseTestSHA256("synthetic PNG"),
		Kind:               api.MediaArtifactScreenshot,
		Image: api.ScreenshotImage{
			Path:             filepath.Join(t.TempDir(), "legacy-comparison-named-file.png"),
			Purpose:          api.ScreenshotPurposeFinal,
			Index:            3,
			TimestampSeconds: 12.5,
			Width:            1920,
			Height:           1080,
			SizeBytes:        1234,
		},
		Imported: true,
		Selected: true,
		Order:    4,
	}
	_, err = repo.RawDB().ExecContext(t.Context(), `INSERT INTO media_reusable_assets (
  source_path, prepared_media_fingerprint, prepared_generation, compatibility_key, capture_fingerprint, content_sha256,
  kind, purpose, image_path, image_index, timestamp_seconds, width, height, size_bytes, imported, selected, sort_order, captured_at
 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 1, ?, '2026-10-18T00:00:00Z')`,
		legacy.Binding.SourcePath, legacy.Binding.PreparedMediaFingerprint, legacy.Binding.PreparedGeneration, legacy.CompatibilityKey,
		legacy.CaptureFingerprint, legacy.ContentSHA256, legacy.Kind, legacy.Image.Purpose, legacy.Image.Path, legacy.Image.Index,
		legacy.Image.TimestampSeconds, legacy.Image.Width, legacy.Image.Height, legacy.Image.SizeBytes, legacy.Order,
	)
	if err != nil {
		t.Fatalf("insert legacy reusable image: %v", err)
	}
	for _, column := range []string{"comparison_group", "comparison_count", "comparison_label", "comparison_member_label"} {
		if exists, err := tableColumnExists(t.Context(), repo.RawDB(), "media_reusable_assets", column); err != nil || exists {
			t.Fatalf("prior column %s exists=%v error=%v", column, exists, err)
		}
	}
	if exists, err := tableExists(t.Context(), repo.RawDB(), "image_collections"); err != nil || exists {
		t.Fatalf("prior collection table exists=%v error=%v", exists, err)
	}
	return repo, legacy
}

func TestReusableComparisonGroupsNeverMixCommittedBindings(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	root := t.TempDir()
	binding := testPreparedMediaBinding(filepath.Join(root, "Synthetic.Release.2026-GRP.mkv"))
	compatibilityKey := api.MediaCompatibilityKey(mediaReuseTestSHA256("synthetic comparison source"))
	assetFor := func(bound api.PreparedMediaBinding, group, name string, count, order int) api.ReusableMediaAsset {
		return api.ReusableMediaAsset{
			Binding:            bound,
			CompatibilityKey:   compatibilityKey,
			CaptureFingerprint: api.WorkflowFingerprint(mediaReuseTestSHA256("capture-" + name)),
			ContentSHA256:      mediaReuseTestSHA256(fmt.Sprintf("%s-generation-%d", name, bound.PreparedGeneration)),
			Kind:               api.MediaArtifactScreenshot,
			Image: api.ScreenshotImage{
				Path:                  filepath.Join(root, fmt.Sprintf("generation-%d", bound.PreparedGeneration), name+".png"),
				Purpose:               api.ScreenshotPurposeFinal,
				ComparisonGroup:       group,
				ComparisonCount:       count,
				ComparisonLabel:       "Synthetic " + group,
				ComparisonMemberLabel: name,
			},
			Selected: true,
			Order:    order,
		}
	}
	commit := func(bound api.PreparedMediaBinding, assets []api.ReusableMediaAsset) {
		t.Helper()
		if err := repo.CommitReusableMedia(t.Context(), bound, compatibilityKey, assets, reusableMediaCommitForTest(bound)); err != nil {
			t.Fatalf("commit generation %d: %v", bound.PreparedGeneration, err)
		}
		if _, err := repo.RawDB().ExecContext(t.Context(),
			`UPDATE media_reusable_assets SET captured_at = ? WHERE source_path = ? AND prepared_generation = ?`,
			fmt.Sprintf("2026-10-18T00:00:0%dZ", bound.PreparedGeneration), bound.SourcePath, bound.PreparedGeneration,
		); err != nil {
			t.Fatalf("set deterministic capture order: %v", err)
		}
	}
	unrelated := assetFor(binding, "group-b", "B1", 1, 2)
	commit(binding, []api.ReusableMediaAsset{
		assetFor(binding, "group-a", "A1", 2, 0),
		assetFor(binding, "group-a", "A2", 2, 1),
		unrelated,
	})
	initial, err := repo.LoadReusableMediaAssets(t.Context(), compatibilityKey)
	if err != nil || len(initial) != 3 {
		t.Fatalf("initial two groups = %+v error=%v", initial, err)
	}
	var latest api.ReusableMediaAsset
	for index, declaredCount := range []int{1, 2} {
		currentBinding := binding
		currentBinding.PreparedGeneration = api.PreparedGeneration(index + 2)
		currentBinding.PreparedMediaFingerprint = fmt.Sprintf("prepared-generation-%d", currentBinding.PreparedGeneration)
		replacement := assetFor(currentBinding, "group-a", "A-replacement", declaredCount, 0)
		commit(currentBinding, []api.ReusableMediaAsset{replacement})
		loaded, err := repo.LoadReusableMediaAssets(t.Context(), compatibilityKey)
		if err != nil || len(loaded) != 2 {
			t.Fatalf("generation %d filled group from older members: %+v error=%v", currentBinding.PreparedGeneration, loaded, err)
		}
		if !reflect.DeepEqual(loaded[0], replacement) || !reflect.DeepEqual(loaded[1], unrelated) {
			t.Fatalf("generation %d mixed bindings or lost unrelated group: got=%+v want=%+v", currentBinding.PreparedGeneration, loaded, []api.ReusableMediaAsset{replacement, unrelated})
		}
		latest = replacement
	}
	if err := repo.DeleteReusableMediaAssets(t.Context(), latest.Binding, []string{latest.Image.Path}); err != nil {
		t.Fatalf("delete latest comparison group: %v", err)
	}
	loaded, err := repo.LoadReusableMediaAssets(t.Context(), compatibilityKey)
	if err != nil {
		t.Fatalf("load after comparison deletion: %v", err)
	}
	if !reflect.DeepEqual(loaded, []api.ReusableMediaAsset{unrelated}) {
		t.Errorf("deletion resurrected retired comparison group or changed unrelated group: got=%+v want=%+v", loaded, unrelated)
	}
	deletedSnapshot := reusableMediaCommitForTest(latest.Binding)
	deletedSnapshot.Revision++
	if err := repo.CommitReusableMedia(t.Context(), latest.Binding, compatibilityKey, nil, deletedSnapshot); err != nil {
		t.Fatalf("commit empty comparison snapshot: %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(repo.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if found, err := reopened.HasReusableMediaCommit(t.Context(), deletedSnapshot); err != nil || !found {
		t.Fatalf("reopen lost empty comparison snapshot: found=%v error=%v", found, err)
	}
	loaded, err = reopened.LoadReusableMediaAssets(t.Context(), compatibilityKey)
	if err != nil || !reflect.DeepEqual(loaded, []api.ReusableMediaAsset{unrelated}) {
		t.Fatalf("reopen resurrected retired comparison group or changed unrelated group: got=%+v want=%+v error=%v", loaded, unrelated, err)
	}
}
