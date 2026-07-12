// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func (a *App) listHistoryFromRepo(ctx context.Context) ([]api.HistoryEntry, error) {
	if err := a.requireHistoryRepo(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("gui: list history canceled: %w", err)
	}

	entries, err := a.repo.ListHistoryEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("gui: %w", err)
	}

	result := make([]api.HistoryEntry, 0, len(entries))
	for _, entry := range entries {
		entryCopy := entry
		entryCopy.LatestUploadStatus = api.HistoryStatusLabel(entry.LatestUploadStatus, entry.RuleFailureCount)
		result = append(result, entryCopy)
	}

	return result, nil
}

func (a *App) getHistoryOverviewFromRepo(ctx context.Context, sourcePath string) (api.HistoryOverview, error) {
	if err := a.requireHistoryRepo(); err != nil {
		return api.HistoryOverview{}, err
	}
	if err := ctx.Err(); err != nil {
		return api.HistoryOverview{}, fmt.Errorf("gui: get history overview canceled: %w", err)
	}

	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return api.HistoryOverview{}, internalerrors.ErrInvalidInput
	}

	metadata, err := a.repo.GetByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("gui: %w", err)
	}

	overview := api.HistoryOverview{
		SourcePath:        metadata.Path,
		ReleaseTitle:      metadata.Title,
		ReleaseSource:     metadata.Source,
		ReleaseResolution: metadata.Resolution,
		MetadataUpdatedAt: metadata.UpdatedAt,
		Metadata:          metadata,
	}

	externalIDs, err := a.repo.GetExternalIDs(ctx, trimmed)
	if err == nil {
		overview.ExternalIDs = externalIDs
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, fmt.Errorf("gui: %w", err)
	}

	externalMetadata, err := a.repo.GetExternalMetadata(ctx, trimmed)
	if err == nil {
		overview.ExternalMetadata = externalMetadata
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, fmt.Errorf("gui: %w", err)
	}

	releaseOverrides, err := a.repo.GetReleaseNameOverrides(ctx, trimmed)
	if err == nil {
		overview.ReleaseNameOverrides = releaseOverrides
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, fmt.Errorf("gui: %w", err)
	}

	descriptionOverrides, err := a.repo.ListDescriptionOverridesByPath(ctx, trimmed)
	if err == nil {
		overview.DescriptionOverrides = append([]api.DescriptionOverride(nil), descriptionOverrides...)
		overview.DescriptionOverride = preferredHistoryDescriptionOverride(descriptionOverrides)
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, fmt.Errorf("gui: %w", err)
	}

	playlistSelection, err := a.repo.GetPlaylistSelection(ctx, trimmed)
	if err == nil {
		overview.PlaylistSelection = playlistSelection
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, fmt.Errorf("gui: %w", err)
	}

	trackerMetadata, err := a.repo.ListTrackerMetadataByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("gui: %w", err)
	}
	overview.TrackerMetadata = trackerMetadata

	ruleFailures, err := a.repo.ListTrackerRuleFailuresByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("gui: %w", err)
	}
	overview.TrackerRuleFailures = ruleFailures

	screenshots, err := a.repo.ListScreenshotsByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("gui: %w", err)
	}
	overview.Screenshots = screenshots

	finalSelections, err := a.repo.ListFinalSelections(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("gui: %w", err)
	}
	overview.FinalSelections = finalSelections

	uploadedImages, err := a.repo.ListUploadedImagesByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("gui: %w", err)
	}
	overview.UploadedImages = uploadedImages

	uploadHistory, err := a.repo.ListUploadHistoryByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("gui: %w", err)
	}
	overview.UploadHistory = uploadHistory

	if len(uploadHistory) > 0 {
		overview.LatestUploadStatus = uploadHistory[0].Status
		overview.LatestUploadAt = uploadHistory[0].CreatedAt
	}
	blockingRuleFailures := api.CountBlockingRuleFailures(ruleFailures)
	overview.StatusLabel = api.HistoryStatusLabel(overview.LatestUploadStatus, blockingRuleFailures)

	return overview, nil
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
