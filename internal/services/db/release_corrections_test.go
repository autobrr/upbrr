// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestMigrateReleaseCorrectionsBackfillsLegacyPresence(t *testing.T) {
	t.Parallel()

	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	ctx := context.Background()
	if err := createBaselineSchema(ctx, rawDB); err != nil {
		t.Fatalf("create baseline schema: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `
		INSERT INTO release_overrides (source_path, manual_year, no_tag, no_dual, tag, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"legacy-source", 0, 0, 1, "", "2026-09-09T00:00:00Z"); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if err := migrateAddReleaseCorrections(ctx, rawDB); err != nil {
		t.Fatalf("migrate corrections: %v", err)
	}

	var payload string
	var revision uint64
	if err := rawDB.QueryRowContext(ctx, `
		SELECT corrections_json, corrections_revision FROM release_overrides WHERE source_path = ?`, "legacy-source").Scan(&payload, &revision); err != nil {
		t.Fatalf("load migrated correction row: %v", err)
	}
	var stored api.StoredReleaseCorrectionsV1
	if err := json.Unmarshal([]byte(payload), &stored); err != nil {
		t.Fatalf("decode migrated correction payload: %v", err)
	}
	if revision != 1 || stored.Version != 1 {
		t.Fatalf("migration version/revision = %d/%d, want 1/1", stored.Version, revision)
	}
	if stored.ReleaseName.ManualYear == nil || *stored.ReleaseName.ManualYear != 0 ||
		stored.ReleaseName.NoTag == nil || *stored.ReleaseName.NoTag ||
		stored.ReleaseName.NoDual == nil || !*stored.ReleaseName.NoDual ||
		stored.ReleaseName.Tag == nil || *stored.ReleaseName.Tag != "" {
		t.Fatalf("legacy presence lost during backfill: %#v", stored.ReleaseName)
	}
}

func TestHistoryLoadsCorrectionOnlyRecordAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrections.db")
	repo, err := OpenWithLogger(path, nopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.UpdateReleaseCorrections(t.Context(), "release-source", api.ReleaseCorrectionUpdate{
		Mode:  api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{Title: new("Manual title")}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = OpenWithLogger(path, nopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	record, err := repo.LoadHistoryRecord(t.Context(), "release-source")
	if err != nil {
		t.Fatal(err)
	}
	if record.SourcePath != "release-source" || !reflect.DeepEqual(record.Corrections, stored) || record.PreparedRelease != nil {
		t.Fatalf("correction-only history = %#v", record)
	}
	if _, err := repo.LoadHistoryRecord(t.Context(), "missing-source"); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("missing history error = %v", err)
	}
}

func TestReleaseCorrectionsUpdateResetAndRestart(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "corrections.db")
	repo, err := OpenWithLogger(path, nopLogger{})
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}
	ctx := context.Background()
	sourcePath := "release-source"

	missing, err := repo.LoadReleaseCorrections(ctx, sourcePath)
	if err != nil {
		t.Fatalf("load missing corrections: %v", err)
	}
	if missing.Revision != 0 || missing.Corrections.Version != 1 {
		t.Fatalf("missing snapshot = %#v, want empty v1 revision zero", missing)
	}
	if noOp, err := repo.UpdateReleaseCorrections(ctx, sourcePath, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}); err != nil || !reflect.DeepEqual(noOp, missing) {
		t.Fatalf("inherit missing = %#v, %v; want %#v, nil", noOp, err, missing)
	}

	noTag := false
	patch := api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{NoTag: &noTag}}}
	stored, err := repo.UpdateReleaseCorrections(ctx, sourcePath, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &patch})
	if err != nil {
		t.Fatalf("store correction: %v", err)
	}
	if stored.Revision != 1 || stored.Corrections.ReleaseName.NoTag == nil || *stored.Corrections.ReleaseName.NoTag {
		t.Fatalf("stored correction = %#v", stored)
	}
	if noOp, err := repo.UpdateReleaseCorrections(ctx, sourcePath, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
		Values:           patch.Values,
		ExpectedRevision: &stored.Revision,
	}}); err != nil || noOp.Revision != stored.Revision {
		t.Fatalf("same correction changed revision: %#v, %v", noOp, err)
	}

	expectedZero := uint64(0)
	_, err = repo.UpdateReleaseCorrections(ctx, sourcePath, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
		ExpectedRevision: &expectedZero,
	}})
	var revisionConflict *api.CorrectionRevisionConflictError
	if !errors.As(err, &revisionConflict) || revisionConflict.Actual != stored.Revision {
		t.Fatalf("stale update error = %v, want revision conflict at %d", err, stored.Revision)
	}

	if _, err := repo.RawDB().ExecContext(ctx, `UPDATE release_overrides SET no_tag = 1 WHERE source_path = ?`, sourcePath); err != nil {
		t.Fatalf("seed retained legacy value: %v", err)
	}
	reset, err := repo.UpdateReleaseCorrections(ctx, sourcePath, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateResetAll})
	if err != nil {
		t.Fatalf("reset corrections: %v", err)
	}
	if reset.Revision != 2 || reset.Corrections.ReleaseName.NoTag != nil {
		t.Fatalf("reset snapshot = %#v", reset)
	}
	if err := repo.Close(); err != nil {
		t.Fatalf("close repository: %v", err)
	}
	repo, err = OpenWithLogger(path, nopLogger{})
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("remigrate repository: %v", err)
	}
	restarted, err := repo.LoadReleaseCorrections(ctx, sourcePath)
	if err != nil {
		t.Fatalf("load restarted corrections: %v", err)
	}
	if !reflect.DeepEqual(restarted, reset) {
		t.Fatalf("restart corrections = %#v, want %#v", restarted, reset)
	}
}

func TestReleaseCorrectionsPreserveUnknownVersionBytes(t *testing.T) {
	t.Parallel()

	repo := openPreparedReleaseTestRepo(t)
	ctx := context.Background()
	const sourcePath = "unknown-version-source"
	const payload = `{"version":99,"releaseName":{"tag":"keep"}}`
	if _, err := repo.RawDB().ExecContext(ctx, `
		INSERT INTO release_overrides (source_path, corrections_json, corrections_revision, updated_at)
		VALUES (?, ?, ?, ?)`, sourcePath, payload, 7, "2026-09-09T00:00:00Z"); err != nil {
		t.Fatalf("seed unknown correction version: %v", err)
	}
	if _, err := repo.LoadReleaseCorrections(ctx, sourcePath); !errors.Is(err, api.ErrUnsupportedCorrectionVersion) {
		t.Fatalf("load unknown version error = %v", err)
	}
	if _, err := repo.UpdateReleaseCorrections(ctx, sourcePath, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateResetAll}); !errors.Is(err, api.ErrUnsupportedCorrectionVersion) {
		t.Fatalf("update unknown version error = %v", err)
	}
	var gotPayload string
	var revision uint64
	if err := repo.RawDB().QueryRowContext(ctx, `
		SELECT corrections_json, corrections_revision FROM release_overrides WHERE source_path = ?`, sourcePath).Scan(&gotPayload, &revision); err != nil {
		t.Fatalf("read unknown correction version: %v", err)
	}
	if gotPayload != payload || revision != 7 {
		t.Fatalf("unknown payload changed to %q at revision %d", gotPayload, revision)
	}
}

func TestIdentityResetMarkersSurviveRestartAndRejectInvalidRows(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "identity-resets.db")
	repo, err := OpenWithLogger(path, nopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.UpdateReleaseCorrections(t.Context(), "source", api.ReleaseCorrectionUpdate{
		Mode: api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{
			Values:      api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{IMDBID: new(1234567)}},
			ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldIdentityTMDB}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = OpenWithLogger(path, nopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	restarted, err := repo.LoadReleaseCorrections(t.Context(), "source")
	if err != nil || !reflect.DeepEqual(stored, restarted) || restarted.Corrections.Identity.TMDBID != nil || restarted.Corrections.Identity.IMDBID == nil || !slices.Equal(restarted.Corrections.IdentityResetFields, []api.CorrectionField{api.CorrectionFieldIdentityTMDB}) {
		t.Fatalf("identity reset did not survive restart: %v", err)
	}
	const payload = `{"version":1,"identityResetFields":["metadata.title"]}`
	if _, err := repo.RawDB().ExecContext(t.Context(), `UPDATE release_overrides SET corrections_json = ? WHERE source_path = ?`, payload, "source"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.LoadReleaseCorrections(t.Context(), "source"); !errors.Is(err, api.ErrCorrectionConflict) {
		t.Fatalf("invalid marker loaded: %v", err)
	}
	if _, err := repo.UpdateReleaseCorrections(t.Context(), "source", api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateResetAll}); !errors.Is(err, api.ErrCorrectionConflict) {
		t.Fatalf("invalid marker rewritten: %v", err)
	}
	var unchanged string
	if err := repo.RawDB().QueryRowContext(t.Context(), `SELECT corrections_json FROM release_overrides WHERE source_path = ?`, "source").Scan(&unchanged); err != nil || unchanged != payload {
		t.Fatal("invalid saved marker changed")
	}
}

func TestReleaseNameOverrideAdapterPreservesOtherCorrections(t *testing.T) {
	t.Parallel()

	repo := openPreparedReleaseTestRepo(t)
	ctx := context.Background()
	const sourcePath = "adapter-source"
	commentary := false
	manualYear := 2025
	initial := api.StoredReleaseCorrectionsV1{
		Version:     1,
		Metadata:    api.MetadataOverrides{Commentary: &commentary},
		ReleaseName: api.ReleaseNameOverrides{ManualYear: &manualYear},
		ContentBindings: map[api.CorrectionField]api.ContentBinding{
			api.CorrectionFieldReleaseNameManualYear: {},
		},
		StaleContentFields: []api.CorrectionField{api.CorrectionFieldReleaseNameManualYear},
	}
	if _, err := repo.CompareAndSwapReleaseCorrections(ctx, sourcePath, 0, initial); err != nil {
		t.Fatalf("save initial corrections: %v", err)
	}
	tag := "GRP"
	if err := repo.SaveReleaseNameOverrides(ctx, sourcePath, api.ReleaseNameOverrides{Tag: &tag}); err != nil {
		t.Fatalf("save release name overrides: %v", err)
	}
	stored, err := repo.LoadReleaseCorrections(ctx, sourcePath)
	if err != nil {
		t.Fatalf("load saved corrections: %v", err)
	}
	if stored.Corrections.Metadata.Commentary == nil || *stored.Corrections.Metadata.Commentary ||
		stored.Corrections.ReleaseName.Tag == nil || *stored.Corrections.ReleaseName.Tag != tag {
		t.Fatalf("adapter lost correction fields: %#v", stored.Corrections)
	}
	if _, exists := stored.Corrections.ContentBindings[api.CorrectionFieldReleaseNameManualYear]; exists ||
		slices.Contains(stored.Corrections.StaleContentFields, api.CorrectionFieldReleaseNameManualYear) {
		t.Fatalf("adapter retained state for cleared release-name field: %#v", stored.Corrections)
	}
}

func TestCommitPreparedReleaseWithCorrectionsRejectsStaleRevisionWithoutGeneration(t *testing.T) {
	t.Parallel()

	repo := openPreparedReleaseTestRepo(t)
	ctx := context.Background()
	release := preparedReleaseDBFixture("correction-commit-source", 1)
	tag := "GRP"
	snapshot, err := repo.CompareAndSwapReleaseCorrections(ctx, release.Source.SourcePath, 0, api.StoredReleaseCorrectionsV1{
		Version:     1,
		ReleaseName: api.ReleaseNameOverrides{Tag: &tag},
	})
	if err != nil {
		t.Fatalf("save corrections: %v", err)
	}
	_, err = repo.CommitPreparedReleaseWithCorrections(ctx, release, 0, snapshot.Corrections,
		func(uint64) (api.PreparationCompatibility, error) {
			t.Fatal("stale corrections reached compatibility calculation")
			return release.Compatibility, nil
		})
	var conflict *api.CorrectionRevisionConflictError
	if !errors.As(err, &conflict) || conflict.Actual != snapshot.Revision {
		t.Fatalf("commit conflict = %v, want current correction revision %d", err, snapshot.Revision)
	}
	if _, err := repo.LoadPreparedRelease(ctx, release.Source.SourcePath); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("prepared generation after conflict = %v, want not found", err)
	}
}

func TestReleaseNameAdapterMaintainsCategoryResetAuthority(t *testing.T) {
	t.Parallel()
	repo := openPreparedReleaseTestRepo(t)
	const source = "category-reset-source"
	if _, err := repo.UpdateReleaseCorrections(t.Context(), source, api.ReleaseCorrectionUpdate{
		Mode:  api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldReleaseNameCategory}}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, category := range []*string{new("tv"), nil} {
		if err := repo.SaveReleaseNameOverrides(t.Context(), source, api.ReleaseNameOverrides{Category: category}); err != nil {
			t.Fatal(err)
		}
		stored, err := repo.LoadReleaseCorrections(t.Context(), source)
		if err != nil || slices.Contains(stored.Corrections.IdentityResetFields, api.CorrectionFieldReleaseNameCategory) != (category == nil) {
			t.Fatalf("legacy name adapter lost category authority: %v", err)
		}
	}
}

func TestCommitPreparedReleaseWithCorrectionsUsesCommittedRevision(t *testing.T) {
	t.Parallel()

	repo := openPreparedReleaseTestRepo(t)
	ctx := context.Background()
	release := preparedReleaseDBFixture("correction-commit-revision-source", 1)
	tag := "GRP"
	record := api.StoredReleaseCorrectionsV1{Version: 1, ReleaseName: api.ReleaseNameOverrides{Tag: &tag}}
	compatibility := func(revision uint64) (api.PreparationCompatibility, error) {
		result := release.Compatibility
		result.FactInstructionFingerprint = fmt.Sprintf("instructions-at-revision-%d", revision)
		return result, nil
	}
	finalRevision, err := repo.CommitPreparedReleaseWithCorrections(ctx, release, 0, record, compatibility)
	if err != nil {
		t.Fatalf("commit prepared release with corrections: %v", err)
	}
	if finalRevision != 1 {
		t.Fatalf("final correction revision = %d, want 1", finalRevision)
	}
	stored, err := repo.LoadReleaseCorrections(ctx, release.Source.SourcePath)
	if err != nil {
		t.Fatalf("load committed corrections: %v", err)
	}
	if stored.Revision != finalRevision || !reflect.DeepEqual(stored.Corrections, record) {
		t.Fatalf("committed corrections = %#v, want %#v at revision %d", stored.Corrections, record, finalRevision)
	}
	committed, err := repo.LoadPreparedRelease(ctx, release.Source.SourcePath)
	if err != nil {
		t.Fatalf("load committed generation: %v", err)
	}
	wantCompatibility, _ := compatibility(finalRevision)
	if committed.Compatibility != wantCompatibility {
		t.Fatalf("first commit compatibility = %#v, want %#v", committed.Compatibility, wantCompatibility)
	}
	// Empty optional collections differ under reflect.DeepEqual but serialize to
	// the same stored correction payload. SQLite keeps the existing revision.
	record.ContentBindings = map[api.CorrectionField]api.ContentBinding{}
	record.StaleContentFields = []api.CorrectionField{}
	if noOpRevision, err := repo.CommitPreparedReleaseWithCorrections(ctx, release, finalRevision, record, compatibility); err != nil || noOpRevision != finalRevision {
		t.Fatalf("no-op commit revision = %d, %v; want %d, nil", noOpRevision, err, finalRevision)
	}
	committed, err = repo.LoadPreparedRelease(ctx, release.Source.SourcePath)
	if err != nil || committed.Compatibility != wantCompatibility {
		t.Fatalf("no-op commit changed compatibility: got %#v, want %#v; error: %v", committed.Compatibility, wantCompatibility, err)
	}
}

func TestCommitPreparedReleaseWithCorrectionsRollsBackCompatibilityFailure(t *testing.T) {
	t.Parallel()
	repo := openPreparedReleaseTestRepo(t)
	release := preparedReleaseDBFixture("correction-compatibility-failure", 1)
	record := api.StoredReleaseCorrectionsV1{Version: 1, ReleaseName: api.ReleaseNameOverrides{Tag: new("GRP")}}
	wantErr := errors.New("compatibility failure")
	_, err := repo.CommitPreparedReleaseWithCorrections(t.Context(), release, 0, record,
		func(uint64) (api.PreparationCompatibility, error) { return api.PreparationCompatibility{}, wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("compatibility failure lost: %v", err)
	}
	stored, err := repo.LoadReleaseCorrections(t.Context(), release.Source.SourcePath)
	if err != nil || stored.Revision != 0 || stored.Corrections.ReleaseName.Tag != nil {
		t.Fatalf("failed commit persisted corrections: %v", err)
	}
	if _, err := repo.LoadPreparedRelease(t.Context(), release.Source.SourcePath); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("failed commit persisted a generation: %v", err)
	}
}
