// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

// LoadHistoryRecord assembles one complete persisted history record.
// Optional release records remain zero-valued when absent; collection ordering
// is inherited from the canonical SQLite queries.
func (r *SQLiteRepository) LoadHistoryRecord(ctx context.Context, sourcePath string) (api.HistoryRecord, error) {
	metadata, err := r.GetByPath(ctx, sourcePath)
	if err != nil {
		return api.HistoryRecord{}, fmt.Errorf("db history record metadata: %w", err)
	}
	record := api.HistoryRecord{
		SourcePath:        metadata.Path,
		ReleaseTitle:      metadata.Title,
		ReleaseSource:     metadata.Source,
		ReleaseResolution: metadata.Resolution,
		MetadataUpdatedAt: metadata.UpdatedAt,
		Metadata:          metadata,
	}

	prepared, preparedErr := r.LoadPreparedRelease(ctx, sourcePath)
	if preparedErr == nil {
		record.PreparedRelease = &prepared
	} else if !errors.Is(preparedErr, internalerrors.ErrNotFound) {
		return api.HistoryRecord{}, fmt.Errorf("db history record prepared release: %w", preparedErr)
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

// LoadHistoryCleanupSnapshot returns caller-owned local artifact paths and the
// optional metadata needed to derive the managed release directory.
func (r *SQLiteRepository) LoadHistoryCleanupSnapshot(ctx context.Context, sourcePath string) (api.HistoryCleanupSnapshot, error) {
	snapshot := api.HistoryCleanupSnapshot{}
	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return api.HistoryCleanupSnapshot{}, internalerrors.ErrInvalidInput
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT image_path FROM screenshots WHERE source_path = ?
		UNION
		SELECT image_path FROM uploaded_images WHERE source_path = ?
		UNION
		SELECT image_path FROM screenshot_final_selections WHERE source_path = ?
		UNION
		SELECT image_path FROM screenshot_slots WHERE source_path = ?
		UNION
		SELECT image_path FROM screenshot_slot_variants WHERE source_path = ?
	`, trimmed, trimmed, trimmed, trimmed, trimmed)
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
