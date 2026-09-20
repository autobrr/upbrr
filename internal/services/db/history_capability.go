// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

type historyTransactionKey struct{}

type historyTransaction struct {
	repository *SQLiteRepository
	tx         *sql.Tx
}

type historyQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r *SQLiteRepository) historyTransaction(ctx context.Context) *sql.Tx {
	transaction, ok := ctx.Value(historyTransactionKey{}).(historyTransaction)
	if ok && transaction.repository == r {
		return transaction.tx
	}
	return nil
}

func (r *SQLiteRepository) historyQuery(ctx context.Context) historyQueryer {
	if tx := r.historyTransaction(ctx); tx != nil {
		return tx
	}
	return r.db
}

// WithHistoryDeletion excludes other SQLite writers for the entire cleanup.
// The callback runs once: filesystem removal must never be transaction-retried.
// A failed commit can leave files removed with database references retained;
// cleanup is idempotent so the explicit deletion can be retried.
func (r *SQLiteRepository) WithHistoryDeletion(ctx context.Context, cleanup func(context.Context) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db history deletion begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// Acquire SQLite's writer reservation before taking any cleanup snapshots.
	if _, err := tx.ExecContext(ctx, `UPDATE active_input SET revision = revision WHERE singleton = 1`); err != nil {
		return fmt.Errorf("db history deletion reserve writer: %w", err)
	}
	workCtx := context.WithValue(ctx, historyTransactionKey{}, historyTransaction{repository: r, tx: tx})
	if err := cleanup(workCtx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db history deletion commit: %w", err)
	}
	return nil
}

// LoadHistoryRecord assembles one complete persisted history record.
// Optional release records remain zero-valued when absent; collection ordering
// is inherited from the canonical SQLite queries.
func (r *SQLiteRepository) LoadHistoryRecord(ctx context.Context, sourcePath string) (api.HistoryRecord, error) {
	metadata, err := r.GetByPath(ctx, sourcePath)
	metadataMissing := errors.Is(err, internalerrors.ErrNotFound)
	if err != nil && !metadataMissing {
		return api.HistoryRecord{}, fmt.Errorf("db history record metadata: %w", err)
	}
	if metadataMissing {
		metadata.Path = sourcePath
	}
	record := api.HistoryRecord{
		SourcePath:        metadata.Path,
		ReleaseTitle:      metadata.Title,
		ReleaseSource:     metadata.Source,
		ReleaseResolution: metadata.Resolution,
		MetadataUpdatedAt: metadata.UpdatedAt,
		Metadata:          metadata,
	}
	if record.Corrections, err = r.LoadReleaseCorrections(ctx, sourcePath); err != nil {
		return api.HistoryRecord{}, fmt.Errorf("db history record corrections: %w", err)
	}

	prepared, preparedErr := r.LoadPreparedRelease(ctx, sourcePath)
	if preparedErr == nil {
		record.PreparedRelease = &prepared
	} else if !errors.Is(preparedErr, internalerrors.ErrNotFound) {
		return api.HistoryRecord{}, fmt.Errorf("db history record prepared release: %w", preparedErr)
	}
	if metadataMissing && record.PreparedRelease == nil && record.Corrections.Revision == 0 {
		return api.HistoryRecord{}, fmt.Errorf("db history record: %w", internalerrors.ErrNotFound)
	}
	if record.ReleaseNameOverrides, err = r.GetReleaseNameOverrides(ctx, sourcePath); err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryRecord{}, fmt.Errorf("db history record release overrides: %w", err)
	}
	descriptionOverrides, err := r.ListDescriptionOverridesByPath(ctx, sourcePath)
	if err == nil {
		record.DescriptionOverrides = append([]api.DescriptionOverride(nil), descriptionOverrides...)
		record.DescriptionOverride = preferredHistoryDescriptionOverride(descriptionOverrides)
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryRecord{}, fmt.Errorf("db history record description overrides: %w", err)
	}
	if record.PlaylistSelection, err = r.GetPlaylistSelection(ctx, sourcePath); err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryRecord{}, fmt.Errorf("db history record playlist selection: %w", err)
	}
	if record.TrackerMetadata, err = r.ListTrackerMetadataByPath(ctx, sourcePath); err != nil {
		return api.HistoryRecord{}, fmt.Errorf("db history record tracker metadata: %w", err)
	}
	if record.TrackerRuleFailures, err = r.ListTrackerRuleFailuresByPath(ctx, sourcePath); err != nil {
		return api.HistoryRecord{}, fmt.Errorf("db history record tracker failures: %w", err)
	}
	if record.PreparedRelease != nil {
		binding, bindingErr := record.PreparedRelease.MediaBinding()
		if bindingErr != nil {
			return api.HistoryRecord{}, fmt.Errorf("db history record media binding: %w", bindingErr)
		}
		if record.Screenshots, err = r.ListScreenshotsByPath(ctx, binding); err != nil {
			return api.HistoryRecord{}, fmt.Errorf("db history record screenshots: %w", err)
		}
		if record.FinalSelections, err = r.ListFinalSelections(ctx, binding); err != nil {
			return api.HistoryRecord{}, fmt.Errorf("db history record final selections: %w", err)
		}
		if record.UploadedImages, err = r.ListUploadedImagesByPath(ctx, binding); err != nil {
			return api.HistoryRecord{}, fmt.Errorf("db history record uploaded images: %w", err)
		}
	}
	if record.UploadHistory, err = r.ListUploadHistoryByPath(ctx, sourcePath); err != nil {
		return api.HistoryRecord{}, fmt.Errorf("db history record upload history: %w", err)
	}
	if len(record.UploadHistory) > 0 {
		record.LatestUploadStatus = record.UploadHistory[0].Status
		record.LatestUploadAt = record.UploadHistory[0].CreatedAt
	}
	return record, nil
}

const historyArtifactPathsQuery = `
	SELECT source_path, image_path FROM screenshots
	UNION SELECT source_path, image_path FROM uploaded_images
	UNION SELECT source_path, image_path FROM screenshot_final_selections
	UNION SELECT source_path, image_path FROM screenshot_slots
	UNION SELECT source_path, image_path FROM screenshot_slot_variants
	UNION SELECT source_path, image_path FROM media_reusable_assets
`

const historyProtectedWorkflowStatesQuery = `
	SELECT busy.owner_id, busy.workflow_id, workflow.state_json
	FROM (
		SELECT owner_id, workflow_id
		FROM release_workflow_operations
		WHERE status IN (?, ?)
		UNION
		SELECT owner_id, workflow_id
		FROM release_workflow_work
		WHERE completed_at IS NULL AND lease_expires_at > ?
	) AS busy
	LEFT JOIN release_workflow_states AS workflow
		ON workflow.owner_id = busy.owner_id AND workflow.workflow_id = busy.workflow_id
`

type historyProtectedSources struct {
	paths         []string
	scopes        map[api.WorkflowScope]struct{}
	missingSource bool
}

// ListHistoryProtectedSourcePaths returns source paths held by active input
// ownership or executable workflow work. A busy record without a durable source
// fails closed so cleanup cannot remove pending local artifacts.
func (r *SQLiteRepository) ListHistoryProtectedSourcePaths(ctx context.Context) ([]string, error) {
	protected, err := r.listHistoryProtectedSources(ctx)
	if err != nil {
		return nil, err
	}
	if protected.missingSource {
		return nil, api.ErrActiveInputBusy
	}
	return protected.paths, nil
}

func (r *SQLiteRepository) listHistoryProtectedSources(ctx context.Context) (historyProtectedSources, error) {
	query := r.historyQuery(ctx)
	protected := historyProtectedSources{scopes: make(map[api.WorkflowScope]struct{})}
	seen := make(map[string]struct{})
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if _, exists := seen[path]; exists {
			return
		}
		seen[path] = struct{}{}
		protected.paths = append(protected.paths, path)
	}

	slot, err := loadActiveInput(ctx, query)
	if err != nil {
		return historyProtectedSources{}, err
	}
	if slot.State != api.ActiveInputEmpty {
		pathCount := len(protected.paths)
		if strings.TrimSpace(slot.OwnerID) != "" && slot.WorkflowID != "" {
			protected.scopes[api.WorkflowScope{OwnerID: slot.OwnerID, WorkflowID: slot.WorkflowID}] = struct{}{}
		}
		add(slot.RequestedPath)
		if slot.InputID != "" {
			var canonicalPath string
			err := query.QueryRowContext(ctx, `SELECT canonical_path FROM input_records WHERE id = ?`, slot.InputID).Scan(&canonicalPath)
			switch {
			case err == nil:
				add(canonicalPath)
			case !errors.Is(err, sql.ErrNoRows):
				return historyProtectedSources{}, fmt.Errorf("db list history protected input: %w", err)
			}
		}
		if slot.WorkflowID != "" {
			var payload []byte
			err := query.QueryRowContext(ctx, `SELECT state_json FROM release_workflow_states
				WHERE owner_id = ? AND workflow_id = ?`, slot.OwnerID, slot.WorkflowID).Scan(&payload)
			switch {
			case err == nil:
				for _, path := range storedReleaseWorkflowSourcePaths(payload) {
					add(path)
				}
			case !errors.Is(err, sql.ErrNoRows):
				return historyProtectedSources{}, fmt.Errorf("db list history protected active workflow: %w", err)
			}
		}
		if len(protected.paths) == pathCount {
			protected.missingSource = true
		}
	}

	rows, err := query.QueryContext(
		ctx,
		historyProtectedWorkflowStatesQuery,
		api.StageStatusQueued,
		api.StageStatusRunning,
		formatWorkflowStateTime(time.Now().UTC()),
	)
	if err != nil {
		return historyProtectedSources{}, fmt.Errorf("db list history protected workflows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ownerID string
		var workflowID api.WorkflowID
		var payload []byte
		if err := rows.Scan(&ownerID, &workflowID, &payload); err != nil {
			return historyProtectedSources{}, fmt.Errorf("db scan history protected workflow: %w", err)
		}
		protected.scopes[api.WorkflowScope{OwnerID: ownerID, WorkflowID: workflowID}] = struct{}{}
		workflowPaths := storedReleaseWorkflowSourcePaths(payload)
		if len(workflowPaths) == 0 {
			protected.missingSource = true
			continue
		}
		for _, path := range workflowPaths {
			add(path)
		}
	}
	if err := rows.Err(); err != nil {
		return historyProtectedSources{}, fmt.Errorf("db iterate history protected workflows: %w", err)
	}
	slices.Sort(protected.paths)
	return protected, nil
}

// ListStoredHistoryArtifactPaths returns file references still held by stored
// sources, including images shared through reusable-media associations.
func (r *SQLiteRepository) ListStoredHistoryArtifactPaths(ctx context.Context) ([]string, error) {
	rows, err := r.historyQuery(ctx).QueryContext(ctx, `SELECT DISTINCT image_path FROM (`+historyArtifactPathsQuery+`) WHERE TRIM(image_path) <> ''`)
	if err != nil {
		return nil, fmt.Errorf("db list stored history artifacts: %w", err)
	}
	defer rows.Close()
	paths := make([]string, 0)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("db scan stored history artifact: %w", err)
		}
		paths = append(paths, strings.TrimSpace(path))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate stored history artifacts: %w", err)
	}
	return paths, nil
}

// LoadHistoryCleanupSnapshot returns caller-owned local artifact paths and the
// optional metadata needed to derive the managed release directory.
func (r *SQLiteRepository) LoadHistoryCleanupSnapshot(ctx context.Context, sourcePath string) (api.HistoryCleanupSnapshot, error) {
	snapshot := api.HistoryCleanupSnapshot{}
	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return api.HistoryCleanupSnapshot{}, internalerrors.ErrInvalidInput
	}
	query := r.historyQuery(ctx)
	workflowStates, err := matchingStoredReleaseWorkflowStates(ctx, query, trimmed)
	if err != nil {
		return api.HistoryCleanupSnapshot{}, fmt.Errorf("db history cleanup workflow scopes: %w", err)
	}
	if err := rejectActiveHistoryPurge(ctx, query, trimmed); err != nil {
		return api.HistoryCleanupSnapshot{}, err
	}
	if err := rejectHistoryPurgeWorkflowActivity(ctx, query, workflowStates); err != nil {
		return api.HistoryCleanupSnapshot{}, err
	}
	for _, workflowState := range workflowStates {
		snapshot.WorkflowScopes = append(snapshot.WorkflowScopes, api.WorkflowScope{
			OwnerID:    workflowState.ownerID,
			WorkflowID: workflowState.workflowID,
		})
	}
	rows, err := query.QueryContext(ctx, `SELECT image_path FROM (`+historyArtifactPathsQuery+`) WHERE source_path = ?`, trimmed)
	if err != nil {
		return api.HistoryCleanupSnapshot{}, fmt.Errorf("db history cleanup artifact paths: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var artifactPath string
		if err := rows.Scan(&artifactPath); err != nil {
			return api.HistoryCleanupSnapshot{}, fmt.Errorf("db history cleanup scan artifact path: %w", err)
		}
		if artifactPath = strings.TrimSpace(artifactPath); artifactPath != "" {
			snapshot.ArtifactPaths = append(snapshot.ArtifactPaths, artifactPath)
		}
	}
	if err := rows.Err(); err != nil {
		return api.HistoryCleanupSnapshot{}, fmt.Errorf("db history cleanup iterate artifact paths: %w", err)
	}
	// Metadata only refines the derived temp directory. Preserve cleanup of
	// known artifact paths when the optional release row cannot be read.
	if metadata, metadataErr := r.GetByPath(ctx, trimmed); metadataErr == nil {
		snapshot.Metadata = &metadata
	}
	return snapshot, nil
}

func preferredHistoryDescriptionOverride(overrides []api.DescriptionOverride) api.DescriptionOverride {
	if len(overrides) == 0 {
		return api.DescriptionOverride{}
	}
	for _, override := range overrides {
		if strings.TrimSpace(override.GroupKey) == "" {
			return override
		}
	}
	for _, override := range overrides {
		if strings.TrimSpace(override.Description) != "" {
			return override
		}
	}
	return overrides[0]
}

var (
	_ api.ReleaseStateRepository     = (*SQLiteRepository)(nil)
	_ api.ReleaseSelectionRepository = (*SQLiteRepository)(nil)
	_ api.HistoryRepository          = (*SQLiteRepository)(nil)
	_ api.UploadLedgerRepository     = (*SQLiteRepository)(nil)
	_ api.TrackerStateRepository     = (*SQLiteRepository)(nil)
	_ api.MediaAssetRepository       = (*SQLiteRepository)(nil)
)
