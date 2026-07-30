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
		record.PreparedReleaseRef = &api.ReleaseRef{SourcePath: prepared.Source.SourcePath, Generation: prepared.Generation}
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
	if record.Screenshots, err = r.ListScreenshotsByPath(ctx, sourcePath); err != nil {
		return api.HistoryRecord{}, fmt.Errorf("db history record screenshots: %w", err)
	}
	if record.FinalSelections, err = r.ListFinalSelections(ctx, sourcePath); err != nil {
		return api.HistoryRecord{}, fmt.Errorf("db history record final selections: %w", err)
	}
	if record.UploadedImages, err = r.ListUploadedImagesByPath(ctx, sourcePath); err != nil {
		return api.HistoryRecord{}, fmt.Errorf("db history record uploaded images: %w", err)
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
	shots, err := r.ListScreenshotsByPath(ctx, sourcePath)
	if err != nil {
		return api.HistoryCleanupSnapshot{}, fmt.Errorf("db history cleanup screenshots: %w", err)
	}
	for _, shot := range shots {
		snapshot.ArtifactPaths = append(snapshot.ArtifactPaths, shot.ImagePath)
	}
	uploaded, err := r.ListUploadedImagesByPath(ctx, sourcePath)
	if err != nil {
		return api.HistoryCleanupSnapshot{}, fmt.Errorf("db history cleanup uploaded images: %w", err)
	}
	for _, image := range uploaded {
		snapshot.ArtifactPaths = append(snapshot.ArtifactPaths, image.ImagePath)
	}
	finals, err := r.ListFinalSelections(ctx, sourcePath)
	if err != nil {
		return api.HistoryCleanupSnapshot{}, fmt.Errorf("db history cleanup final selections: %w", err)
	}
	for _, image := range finals {
		snapshot.ArtifactPaths = append(snapshot.ArtifactPaths, image.ImagePath)
	}
	slots, err := r.ListScreenshotSlotsByPath(ctx, sourcePath)
	if err != nil {
		return api.HistoryCleanupSnapshot{}, fmt.Errorf("db history cleanup screenshot slots: %w", err)
	}
	for _, slot := range slots {
		snapshot.ArtifactPaths = append(snapshot.ArtifactPaths, slot.ImagePath)
		for _, variant := range slot.Variants {
			snapshot.ArtifactPaths = append(snapshot.ArtifactPaths, variant.ImagePath)
		}
	}
	// Metadata only refines the derived temp directory. Preserve cleanup of
	// known artifact paths when the optional release row cannot be read.
	if metadata, metadataErr := r.GetByPath(ctx, sourcePath); metadataErr == nil {
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
