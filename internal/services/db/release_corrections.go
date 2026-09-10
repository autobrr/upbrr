// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

const legacyReleaseOverrideColumns = `
	category, release_type, release_source, release_resolution,
	tag, service, edition, season, episode, episode_title,
	manual_year, manual_date, use_season_episode, no_season, no_year, no_aka, no_tag,
	no_episode_title, no_distributor, no_edition, no_dub, no_dual, dual_audio, region`

// LoadReleaseCorrections returns the saved correction record for path. A
// missing row behaves as an empty v1 record at revision zero.
func (r *SQLiteRepository) LoadReleaseCorrections(ctx context.Context, path string) (api.ReleaseCorrectionsSnapshot, error) {
	if r == nil || r.db == nil {
		return api.ReleaseCorrectionsSnapshot{}, errors.New("db: repository not initialized")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return api.ReleaseCorrectionsSnapshot{}, internalerrors.ErrInvalidInput
	}
	snapshot, _, err := loadReleaseCorrectionsTx(ctx, r.db, path)
	return snapshot, err
}

// UpdateReleaseCorrections applies one explicit correction operation within a
// short write transaction. It does not perform any remote work.
func (r *SQLiteRepository) UpdateReleaseCorrections(
	ctx context.Context,
	path string,
	update api.ReleaseCorrectionUpdate,
) (api.ReleaseCorrectionsSnapshot, error) {
	if r == nil || r.db == nil {
		return api.ReleaseCorrectionsSnapshot{}, errors.New("db: repository not initialized")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return api.ReleaseCorrectionsSnapshot{}, internalerrors.ErrInvalidInput
	}

	var updated api.ReleaseCorrectionsSnapshot
	err := r.withWriteTx(ctx, "update release corrections", func(tx *sql.Tx) error {
		snapshot, found, err := loadReleaseCorrectionsTx(ctx, tx, path)
		if err != nil {
			return err
		}
		record, err := api.ApplyReleaseCorrectionUpdate(snapshot, update)
		if err != nil {
			return fmt.Errorf("db update release corrections: %w", err)
		}
		if releaseCorrectionsEqual(snapshot.Corrections, record) {
			updated = snapshot
			return nil
		}
		if snapshot.Revision == math.MaxUint64 {
			return errors.New("db update release corrections: revision overflow")
		}
		updated = api.ReleaseCorrectionsSnapshot{Corrections: record, Revision: snapshot.Revision + 1}
		if err := writeReleaseCorrectionsTx(ctx, tx, path, snapshot.Revision, updated, found); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return api.ReleaseCorrectionsSnapshot{}, err
	}
	return updated, nil
}

// CompareAndSwapReleaseCorrections atomically replaces one known correction
// record. It is used to bind accepted content without re-running preparation.
func (r *SQLiteRepository) CompareAndSwapReleaseCorrections(
	ctx context.Context,
	path string,
	expected uint64,
	record api.StoredReleaseCorrectionsV1,
) (api.ReleaseCorrectionsSnapshot, error) {
	if r == nil || r.db == nil {
		return api.ReleaseCorrectionsSnapshot{}, errors.New("db: repository not initialized")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return api.ReleaseCorrectionsSnapshot{}, internalerrors.ErrInvalidInput
	}
	if record.Version != 1 {
		return api.ReleaseCorrectionsSnapshot{}, &api.UnsupportedCorrectionVersionError{Version: record.Version}
	}

	var updated api.ReleaseCorrectionsSnapshot
	err := r.withWriteTx(ctx, "compare and swap release corrections", func(tx *sql.Tx) error {
		snapshot, found, err := loadReleaseCorrectionsTx(ctx, tx, path)
		if err != nil {
			return err
		}
		if snapshot.Revision != expected {
			return &api.CorrectionRevisionConflictError{Expected: expected, Actual: snapshot.Revision}
		}
		if releaseCorrectionsEqual(snapshot.Corrections, record) {
			updated = snapshot
			return nil
		}
		if snapshot.Revision == math.MaxUint64 {
			return errors.New("db compare and swap release corrections: revision overflow")
		}
		updated = api.ReleaseCorrectionsSnapshot{Corrections: record, Revision: snapshot.Revision + 1}
		return writeReleaseCorrectionsTx(ctx, tx, path, expected, updated, found)
	})
	if err != nil {
		return api.ReleaseCorrectionsSnapshot{}, err
	}
	return updated, nil
}

func (r *SQLiteRepository) saveReleaseNameOverrides(ctx context.Context, path string, overrides api.ReleaseNameOverrides) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return internalerrors.ErrInvalidInput
	}
	return r.withWriteTx(ctx, "save release overrides", func(tx *sql.Tx) error {
		snapshot, found, err := loadReleaseCorrectionsTx(ctx, tx, path)
		if err != nil {
			return err
		}
		record := snapshot.Corrections
		record.Version = 1
		clearReplacedReleaseNameCorrectionState(&record, record.ReleaseName, overrides)
		record.ReleaseName = overrides
		if releaseCorrectionsEqual(snapshot.Corrections, record) {
			return nil
		}
		if snapshot.Revision == math.MaxUint64 {
			return errors.New("db save release overrides: correction revision overflow")
		}
		return writeReleaseCorrectionsTx(ctx, tx, path, snapshot.Revision, api.ReleaseCorrectionsSnapshot{
			Corrections: record,
			Revision:    snapshot.Revision + 1,
		}, found)
	})
}

func releaseCorrectionsEqual(left, right api.StoredReleaseCorrectionsV1) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func clearReplacedReleaseNameCorrectionState(
	record *api.StoredReleaseCorrectionsV1,
	previous api.ReleaseNameOverrides,
	current api.ReleaseNameOverrides,
) {
	if record == nil {
		return
	}
	for _, field := range releaseNameCorrectionFields {
		oldValue := reflect.ValueOf(previous).FieldByName(field.member).Interface()
		newValue := reflect.ValueOf(current).FieldByName(field.member).Interface()
		if reflect.DeepEqual(oldValue, newValue) {
			continue
		}
		if field.field == api.CorrectionFieldReleaseNameCategory {
			record.IdentityResetFields = removeCorrectionField(record.IdentityResetFields, field.field)
			if current.Category == nil {
				record.IdentityResetFields = append(record.IdentityResetFields, field.field)
				slices.Sort(record.IdentityResetFields)
			}
		}
		delete(record.ContentBindings, field.field)
		record.StaleContentFields = removeCorrectionField(record.StaleContentFields, field.field)
	}
}

func removeCorrectionField(fields []api.CorrectionField, target api.CorrectionField) []api.CorrectionField {
	for index, field := range slices.Backward(fields) {
		if field == target {
			fields = append(fields[:index], fields[index+1:]...)
		}
	}
	return fields
}

var releaseNameCorrectionFields = []struct {
	field  api.CorrectionField
	member string
}{
	{api.CorrectionFieldReleaseNameCategory, "Category"},
	{api.CorrectionFieldReleaseNameType, "Type"},
	{api.CorrectionFieldReleaseNameSource, "Source"},
	{api.CorrectionFieldReleaseNameResolution, "Resolution"},
	{api.CorrectionFieldReleaseNameTag, "Tag"},
	{api.CorrectionFieldReleaseNameService, "Service"},
	{api.CorrectionFieldReleaseNameEdition, "Edition"},
	{api.CorrectionFieldReleaseNameSeason, "Season"},
	{api.CorrectionFieldReleaseNameEpisode, "Episode"},
	{api.CorrectionFieldReleaseNameEpisodeTitle, "EpisodeTitle"},
	{api.CorrectionFieldReleaseNameManualYear, "ManualYear"},
	{api.CorrectionFieldReleaseNameManualDate, "ManualDate"},
	{api.CorrectionFieldReleaseNameUseSeasonEpisode, "UseSeasonEpisode"},
	{api.CorrectionFieldReleaseNameNoSeason, "NoSeason"},
	{api.CorrectionFieldReleaseNameNoYear, "NoYear"},
	{api.CorrectionFieldReleaseNameNoAKA, "NoAKA"},
	{api.CorrectionFieldReleaseNameNoTag, "NoTag"},
	{api.CorrectionFieldReleaseNameNoEpisodeTitle, "NoEpisodeTitle"},
	{api.CorrectionFieldReleaseNameNoDistributor, "NoDistributor"},
	{api.CorrectionFieldReleaseNameNoEdition, "NoEdition"},
	{api.CorrectionFieldReleaseNameNoDub, "NoDub"},
	{api.CorrectionFieldReleaseNameNoDual, "NoDual"},
	{api.CorrectionFieldReleaseNameDualAudio, "DualAudio"},
	{api.CorrectionFieldReleaseNameRegion, "Region"},
}

func loadReleaseCorrectionsTx(
	ctx context.Context,
	exec interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	path string,
) (api.ReleaseCorrectionsSnapshot, bool, error) {
	row := exec.QueryRowContext(ctx, `SELECT corrections_json, corrections_revision, `+legacyReleaseOverrideColumns+`
		FROM release_overrides WHERE source_path = ?`, path)
	var payload sql.NullString
	var revision int64
	legacy := releaseNameOverrideColumns{}
	arguments := append([]any{&payload, &revision}, legacy.scanTargets()...)
	if err := row.Scan(arguments...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return api.ReleaseCorrectionsSnapshot{Corrections: api.StoredReleaseCorrectionsV1{Version: 1}}, false, nil
		}
		return api.ReleaseCorrectionsSnapshot{}, false, fmt.Errorf("db load release corrections: %w", err)
	}
	if revision < 0 {
		return api.ReleaseCorrectionsSnapshot{}, true, errors.New("db load release corrections: negative revision")
	}
	snapshot := api.ReleaseCorrectionsSnapshot{Revision: uint64(revision)}
	if !payload.Valid {
		snapshot.Corrections = api.StoredReleaseCorrectionsV1{Version: 1, ReleaseName: legacy.overrides()}
		return snapshot, true, nil
	}
	if err := json.Unmarshal([]byte(payload.String), &snapshot.Corrections); err != nil {
		return api.ReleaseCorrectionsSnapshot{}, true, fmt.Errorf("db load release corrections: decode: %w", err)
	}
	if snapshot.Corrections.Version != 1 {
		return api.ReleaseCorrectionsSnapshot{}, true, &api.UnsupportedCorrectionVersionError{Version: snapshot.Corrections.Version}
	}
	normalized, err := api.ApplyReleaseCorrectionUpdate(snapshot, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if err != nil {
		return api.ReleaseCorrectionsSnapshot{}, true, fmt.Errorf("db load release corrections: validate: %w", err)
	}
	snapshot.Corrections = normalized
	return snapshot, true, nil
}

func writeReleaseCorrectionsTx(
	ctx context.Context,
	tx *sql.Tx,
	path string,
	expected uint64,
	snapshot api.ReleaseCorrectionsSnapshot,
	found bool,
) error {
	if snapshot.Corrections.Version != 1 {
		return &api.UnsupportedCorrectionVersionError{Version: snapshot.Corrections.Version}
	}
	if expected > math.MaxInt64 || snapshot.Revision > math.MaxInt64 {
		return errors.New("db write release corrections: revision exceeds sqlite integer range")
	}
	normalized, err := api.ApplyReleaseCorrectionUpdate(snapshot, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if err != nil {
		return fmt.Errorf("db write release corrections: validate: %w", err)
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("db write release corrections: encode: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO release_overrides (source_path, corrections_json, corrections_revision, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			corrections_json = excluded.corrections_json,
			corrections_revision = excluded.corrections_revision,
			updated_at = excluded.updated_at
		WHERE release_overrides.corrections_revision = ?`,
		path,
		string(payload),
		int64(snapshot.Revision),
		time.Now().UTC().Format(time.RFC3339Nano),
		int64(expected),
	)
	if err != nil {
		return fmt.Errorf("db write release corrections: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("db write release corrections: rows affected: %w", err)
	}
	if affected != 0 {
		return nil
	}
	if !found {
		return &api.CorrectionRevisionConflictError{Expected: expected, Actual: expected + 1}
	}
	var actual int64
	if err := tx.QueryRowContext(ctx, `SELECT corrections_revision FROM release_overrides WHERE source_path = ?`, path).Scan(&actual); err != nil {
		return fmt.Errorf("db write release corrections: read current revision: %w", err)
	}
	if actual < 0 {
		return errors.New("db write release corrections: negative current revision")
	}
	return &api.CorrectionRevisionConflictError{Expected: expected, Actual: uint64(actual)}
}

func backfillLegacyReleaseCorrections(ctx context.Context, exec migrationExecutor) error {
	rows, err := exec.QueryContext(ctx, `SELECT source_path, `+legacyReleaseOverrideColumns+`
		FROM release_overrides WHERE corrections_json IS NULL`)
	if err != nil {
		return fmt.Errorf("db: read legacy release corrections: %w", err)
	}
	defer rows.Close()
	type backfillRow struct {
		path    string
		payload string
	}
	var backfill []backfillRow
	for rows.Next() {
		var path string
		legacy := releaseNameOverrideColumns{}
		arguments := append([]any{&path}, legacy.scanTargets()...)
		if err := rows.Scan(arguments...); err != nil {
			return fmt.Errorf("db: scan legacy release corrections: %w", err)
		}
		payload, err := json.Marshal(api.StoredReleaseCorrectionsV1{Version: 1, ReleaseName: legacy.overrides()})
		if err != nil {
			return fmt.Errorf("db: encode legacy release corrections: %w", err)
		}
		backfill = append(backfill, backfillRow{path: path, payload: string(payload)})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("db: iterate legacy release corrections: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("db: close legacy release corrections: %w", err)
	}
	for _, row := range backfill {
		if _, err := exec.ExecContext(ctx, `
			UPDATE release_overrides
			SET corrections_json = ?, corrections_revision = 1
			WHERE source_path = ? AND corrections_json IS NULL`, row.payload, row.path); err != nil {
			return fmt.Errorf("db: backfill legacy release corrections: %w", err)
		}
	}
	return nil
}

type releaseNameOverrideColumns struct {
	category, releaseType, releaseSource, releaseResolution, tag, service, edition, season, episode, episodeTitle sql.NullString
	manualYear                                                                                                    sql.NullInt64
	manualDate                                                                                                    sql.NullString
	useSeasonEpisode, noSeason, noYear, noAKA, noTag, noEpisodeTitle, noDistributor, noEdition, noDub, noDual,
	dualAudio sql.NullBool
	region sql.NullString
}

func (c *releaseNameOverrideColumns) scanTargets() []any {
	return []any{
		&c.category, &c.releaseType, &c.releaseSource, &c.releaseResolution,
		&c.tag, &c.service, &c.edition, &c.season, &c.episode, &c.episodeTitle,
		&c.manualYear, &c.manualDate, &c.useSeasonEpisode, &c.noSeason, &c.noYear, &c.noAKA, &c.noTag,
		&c.noEpisodeTitle, &c.noDistributor, &c.noEdition, &c.noDub, &c.noDual, &c.dualAudio, &c.region,
	}
}

func (c *releaseNameOverrideColumns) overrides() api.ReleaseNameOverrides {
	return api.ReleaseNameOverrides{
		Category:         nullStringPtr(c.category),
		Type:             nullStringPtr(c.releaseType),
		Source:           nullStringPtr(c.releaseSource),
		Resolution:       nullStringPtr(c.releaseResolution),
		Tag:              nullStringPtr(c.tag),
		Service:          nullStringPtr(c.service),
		Edition:          nullStringPtr(c.edition),
		Season:           nullStringPtr(c.season),
		Episode:          nullStringPtr(c.episode),
		EpisodeTitle:     nullStringPtr(c.episodeTitle),
		ManualYear:       nullIntPtr(c.manualYear),
		ManualDate:       nullStringPtr(c.manualDate),
		UseSeasonEpisode: nullBoolPtr(c.useSeasonEpisode),
		NoSeason:         nullBoolPtr(c.noSeason),
		NoYear:           nullBoolPtr(c.noYear),
		NoAKA:            nullBoolPtr(c.noAKA),
		NoTag:            nullBoolPtr(c.noTag),
		NoEpisodeTitle:   nullBoolPtr(c.noEpisodeTitle),
		NoDistributor:    nullBoolPtr(c.noDistributor),
		NoEdition:        nullBoolPtr(c.noEdition),
		NoDub:            nullBoolPtr(c.noDub),
		NoDual:           nullBoolPtr(c.noDual),
		DualAudio:        nullBoolPtr(c.dualAudio),
		Region:           nullStringPtr(c.region),
	}
}
