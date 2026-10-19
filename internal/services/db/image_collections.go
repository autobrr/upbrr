// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// ReserveImageCollection durably fences one exact set before any remote creation.
// Existing incomplete reservations block retries, including across preparation generations.
func (r *SQLiteRepository) ReserveImageCollection(
	ctx context.Context,
	binding api.PreparedMediaBinding,
	record api.ImageCollection,
) (*api.ImageCollection, bool, error) {
	bound, err := normalizePreparedMediaBinding(binding)
	if err != nil || record.Fingerprint == "" || record.ContentFingerprint == "" || record.AttemptID == "" || record.Completed {
		return nil, false, errors.New("db: invalid image collection reservation")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, false, fmt.Errorf("db: encode image collection: %w", err)
	}
	var existing *api.ImageCollection
	acquired := false
	err = r.withWriteTx(ctx, "reserve image collection", func(tx *sql.Tx) error {
		if _, err := collectionStartedEffect(ctx, tx, record); err != nil {
			return err
		}
		result, err := tx.ExecContext(
			ctx,
			`INSERT INTO image_collections (source_path, prepared_generation, fingerprint, content_fingerprint, completed, record_json) VALUES (?, ?, ?, ?, 0, ?) ON CONFLICT DO NOTHING`,
			bound.SourcePath,
			bound.PreparedGeneration,
			record.Fingerprint,
			record.ContentFingerprint,
			string(data),
		)
		if err != nil {
			return fmt.Errorf("db: reserve image collection: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("db: count reserved collection rows: %w", err)
		}
		acquired = count == 1
		if acquired {
			return nil
		}
		var stored string
		err = tx.QueryRowContext(
			ctx,
			`SELECT record_json FROM image_collections WHERE source_path = ? AND ((prepared_generation = ? AND fingerprint = ?) OR (content_fingerprint = ? AND completed = 0)) ORDER BY completed ASC LIMIT 1`,
			bound.SourcePath,
			bound.PreparedGeneration,
			record.Fingerprint,
			record.ContentFingerprint,
		).Scan(
			&stored,
		)
		if err != nil {
			return fmt.Errorf("db: load conflicting collection: %w", err)
		}
		existing = new(api.ImageCollection)
		if err := json.Unmarshal([]byte(stored), existing); err != nil {
			return fmt.Errorf("db: decode conflicting collection: %w", err)
		}
		return nil
	})
	return existing, acquired, err
}

// LoadImageCollection returns the exact durable attempt, including unknown outcomes.
func (r *SQLiteRepository) LoadImageCollection(ctx context.Context, binding api.PreparedMediaBinding, fingerprint string) (*api.ImageCollection, error) {
	bound, err := normalizePreparedMediaBinding(binding)
	if err != nil {
		return nil, err
	}
	var data string
	err = r.db.QueryRowContext(
		ctx,
		`SELECT record_json FROM image_collections WHERE source_path = ? AND prepared_generation = ? AND fingerprint = ?`,
		bound.SourcePath,
		bound.PreparedGeneration,
		fingerprint,
	).Scan(
		&data,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("db: load image collection: %w", err)
	}
	var record api.ImageCollection
	if err := json.Unmarshal([]byte(data), &record); err != nil {
		return nil, fmt.Errorf("db: decode image collection: %w", err)
	}
	return &record, nil
}

// CompleteImageCollection atomically publishes a complete set and its linked effect.
// A superseded attempt or an effect already marked unknown cannot publish a result.
func (r *SQLiteRepository) CompleteImageCollection(ctx context.Context, binding api.PreparedMediaBinding, record api.ImageCollection) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("db: validate image collection: %w", err)
	}
	return r.finishImageCollection(ctx, binding, record, true)
}

// ReleaseImageCollection removes only the exact attempt known not to have created a remote object.
// Unknown outcomes require explicit workflow reconciliation instead.
func (r *SQLiteRepository) ReleaseImageCollection(ctx context.Context, binding api.PreparedMediaBinding, record api.ImageCollection) error {
	return r.finishImageCollection(ctx, binding, record, false)
}

func (r *SQLiteRepository) finishImageCollection(ctx context.Context, binding api.PreparedMediaBinding, record api.ImageCollection, completed bool) error {
	bound, err := normalizePreparedMediaBinding(binding)
	if err != nil {
		return err
	}
	if record.AttemptID == "" {
		return api.ErrReleaseWorkflowEffectConflict
	}
	return r.withWriteTx(ctx, "finish image collection", func(tx *sql.Tx) error {
		effect, err := collectionStartedEffect(ctx, tx, record)
		if err != nil {
			return err
		}
		var data string
		err = tx.QueryRowContext(
			ctx,
			`SELECT record_json FROM image_collections WHERE source_path = ? AND prepared_generation = ? AND fingerprint = ? AND completed = 0`,
			bound.SourcePath,
			bound.PreparedGeneration,
			record.Fingerprint,
		).Scan(
			&data,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return api.ErrReleaseWorkflowEffectConflict
		}
		if err != nil {
			return fmt.Errorf("db: load collection reservation for completion: %w", err)
		}
		var existing api.ImageCollection
		if err := json.Unmarshal([]byte(data), &existing); err != nil {
			return fmt.Errorf("db: decode collection reservation for completion: %w", err)
		}
		if existing.AttemptID != record.AttemptID || existing.EffectID != record.EffectID {
			return api.ErrReleaseWorkflowEffectConflict
		}
		var result sql.Result
		if completed {
			encoded, err := json.Marshal(record)
			if err != nil {
				return fmt.Errorf("db: encode completed collection: %w", err)
			}
			result, err = tx.ExecContext(
				ctx,
				`UPDATE image_collections SET completed = 1, record_json = ? WHERE source_path = ? AND prepared_generation = ? AND fingerprint = ? AND record_json = ? AND completed = 0`,
				string(encoded),
				bound.SourcePath,
				bound.PreparedGeneration,
				record.Fingerprint,
				data,
			)
			if err != nil {
				return fmt.Errorf("db: complete collection reservation: %w", err)
			}
		} else {
			result, err = tx.ExecContext(
				ctx,
				`DELETE FROM image_collections WHERE source_path = ? AND prepared_generation = ? AND fingerprint = ? AND record_json = ? AND completed = 0`,
				bound.SourcePath,
				bound.PreparedGeneration,
				record.Fingerprint,
				data,
			)
			if err != nil {
				return fmt.Errorf("db: release known-failed collection reservation: %w", err)
			}
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("db: count finished collection rows: %w", err)
		}
		if count != 1 {
			return api.ErrReleaseWorkflowEffectConflict
		}
		if effect != nil {
			now := time.Now().UTC()
			effect.UpdatedAt, effect.CompletedAt = now, &now
			status := api.WorkflowEffectStatusFailed
			if completed {
				status = api.WorkflowEffectStatusSucceeded
			}
			return completeReleaseWorkflowEffect(ctx, tx, status, *effect, "collection effect")
		}
		return nil
	})
}

// collectionStartedEffect admits linked writes only under the current input fence.
// Unlinked service-only attempts remain usable but cannot be reconciled by the UI.
func collectionStartedEffect(ctx context.Context, tx *sql.Tx, record api.ImageCollection) (*api.ReleaseWorkflowEffectRecord, error) {
	if record.EffectID == "" {
		return nil, nil
	}
	if record.AttemptID == "" {
		return nil, api.ErrReleaseWorkflowEffectConflict
	}
	slot, err := loadActiveInput(ctx, tx)
	if err != nil {
		return nil, err
	}
	authority, ok := api.ActiveInputAuthorityFromContext(ctx)
	if !ok || authority.Fence == 0 {
		return nil, api.ErrActiveInputLeaseLost
	}
	if err := requireActiveInputFence(ctx, tx, slot.OwnerID, slot.WorkflowID, authority.CoordinatorID, authority.Fence, time.Now().UTC()); err != nil {
		return nil, err
	}
	effect, err := loadLatestReleaseWorkflowEffect(
		ctx,
		tx,
		slot.OwnerID,
		slot.WorkflowID,
		string(api.WorkflowExternalEffectImageHosting),
		api.ImageCollectionEffectScope(record.AttemptID),
	)
	if err != nil {
		return nil, err
	}
	if effect.EffectID != record.EffectID || effect.Status != api.WorkflowEffectStatusStarted {
		return nil, api.ErrReleaseWorkflowEffectConflict
	}
	return &effect, nil
}

// reconcileImageCollection releases only the reservation attached to this exact unknown effect.
// No row is valid when a process stopped after beginning the effect but before reserving.
func reconcileImageCollection(ctx context.Context, tx *sql.Tx, effect api.ReleaseWorkflowEffectRecord) error {
	var data string
	var completed bool
	err := tx.QueryRowContext(
		ctx,
		`SELECT record_json, completed FROM image_collections WHERE json_extract(record_json, '$.EffectID') = ?`,
		effect.EffectID,
	).Scan(
		&data,
		&completed,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("db: load collection reservation for reconciliation: %w", err)
	}
	var record api.ImageCollection
	if err := json.Unmarshal([]byte(data), &record); err != nil {
		return fmt.Errorf("db: decode collection reservation for reconciliation: %w", err)
	}
	if completed || record.Completed || record.AttemptID == "" || api.ImageCollectionEffectScope(record.AttemptID) != effect.ScopeID {
		return api.ErrReleaseWorkflowEffectConflict
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM image_collections WHERE record_json = ? AND completed = 0`, data)
	if err != nil {
		return fmt.Errorf("db: release reconciled collection reservation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("db: count reconciled collection rows: %w", err)
	}
	if count != 1 {
		return api.ErrReleaseWorkflowEffectConflict
	}
	return nil
}

// ListUnknownCollectionEffects returns only this workflow's unresolved collection attempts.
// Recovery can publish confirmations from these durable records without retained media.
func (r *SQLiteRepository) ListUnknownCollectionEffects(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
) ([]api.ReleaseWorkflowEffectRecord, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || workflowID == "" {
		return nil, errors.New("db: collection recovery owner and workflow are required")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT owner_id, workflow_id, operation_id, effect_id, kind, scope_id,
  semantic_fingerprint, status, started_at, updated_at, completed_at
  FROM release_workflow_effects WHERE owner_id = ? AND workflow_id = ? AND kind = ? AND scope_id GLOB ? AND status = ?
  ORDER BY started_at, effect_id`, ownerID, workflowID, api.WorkflowExternalEffectImageHosting, api.ImageCollectionEffectScope("?*"), api.WorkflowEffectStatusUnknown)
	if err != nil {
		return nil, fmt.Errorf("db list unknown collection effects: %w", err)
	}
	defer rows.Close()
	var effects []api.ReleaseWorkflowEffectRecord
	for rows.Next() {
		effect, err := scanReleaseWorkflowEffect(rows)
		if err != nil {
			return nil, err
		}
		effects = append(effects, effect)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate unknown collection effects: %w", err)
	}
	return effects, nil
}

func migrateImageCollections(ctx context.Context, exec migrationExecutor) error {
	_, err := exec.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS image_collections (
 source_path TEXT NOT NULL,
 prepared_generation INTEGER NOT NULL,
 fingerprint TEXT NOT NULL,
 content_fingerprint TEXT NOT NULL,
 completed INTEGER NOT NULL DEFAULT 0,
 record_json TEXT NOT NULL,
 PRIMARY KEY (source_path, prepared_generation, fingerprint)
 );
 CREATE UNIQUE INDEX IF NOT EXISTS idx_image_collection_pending ON image_collections(source_path, content_fingerprint) WHERE completed = 0`)
	if err != nil {
		return fmt.Errorf("db: migrate image collections: %w", err)
	}
	return nil
}
