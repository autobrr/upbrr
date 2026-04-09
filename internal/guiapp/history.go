// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"errors"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func (a *App) listHistoryFromRepo(ctx context.Context) ([]api.HistoryEntry, error) {
	if err := a.requireHistoryRepo(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries, err := a.repo.ListHistoryEntries(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]api.HistoryEntry, 0, len(entries))
	for _, entry := range entries {
		entryCopy := entry
		entryCopy.LatestUploadStatus = historyStatusLabel(entry.LatestUploadStatus, entry.RuleFailureCount)
		result = append(result, entryCopy)
	}

	return result, nil
}

func (a *App) getHistoryOverviewFromRepo(ctx context.Context, sourcePath string) (api.HistoryOverview, error) {
	if err := a.requireHistoryRepo(); err != nil {
		return api.HistoryOverview{}, err
	}
	if err := ctx.Err(); err != nil {
		return api.HistoryOverview{}, err
	}

	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return api.HistoryOverview{}, internalerrors.ErrInvalidInput
	}

	metadata, err := a.repo.GetByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
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
		return api.HistoryOverview{}, err
	}

	externalMetadata, err := a.repo.GetExternalMetadata(ctx, trimmed)
	if err == nil {
		overview.ExternalMetadata = externalMetadata
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, err
	}

	releaseOverrides, err := a.repo.GetReleaseNameOverrides(ctx, trimmed)
	if err == nil {
		overview.ReleaseNameOverrides = releaseOverrides
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, err
	}

	descriptionOverride, err := a.repo.GetDescriptionOverride(ctx, trimmed, "")
	if err == nil {
		overview.DescriptionOverride = descriptionOverride
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, err
	}

	playlistSelection, err := a.repo.GetPlaylistSelection(ctx, trimmed)
	if err == nil {
		overview.PlaylistSelection = playlistSelection
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, err
	}

	trackerMetadata, err := a.repo.ListTrackerMetadataByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
	}
	overview.TrackerMetadata = trackerMetadata

	ruleFailures, err := a.repo.ListTrackerRuleFailuresByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
	}
	overview.TrackerRuleFailures = ruleFailures

	screenshots, err := a.repo.ListScreenshotsByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
	}
	overview.Screenshots = screenshots

	finalSelections, err := a.repo.ListFinalSelections(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
	}
	overview.FinalSelections = finalSelections

	uploadedImages, err := a.repo.ListUploadedImagesByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
	}
	overview.UploadedImages = uploadedImages

	uploadHistory, err := a.repo.ListUploadHistoryByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
	}
	overview.UploadHistory = uploadHistory

	if len(uploadHistory) > 0 {
		overview.LatestUploadStatus = uploadHistory[0].Status
		overview.LatestUploadAt = uploadHistory[0].CreatedAt
	}
	overview.StatusLabel = historyStatusLabel(overview.LatestUploadStatus, len(ruleFailures))

	return overview, nil
}

func historyStatusLabel(rawStatus string, ruleFailureCount int) string {
	status := strings.TrimSpace(strings.ToLower(rawStatus))
	switch status {
	case "pending":
		return "Pending"
	case "pending-internal":
		return "Pending Internal"
	case "uploaded", "success", "completed":
		return "Uploaded"
	case "failed", "error":
		return "Failed"
	}
	if status != "" {
		normalized := strings.ReplaceAll(status, "-", " ")
		words := strings.Fields(normalized)
		for idx, word := range words {
			if word == "" {
				continue
			}
			words[idx] = strings.ToUpper(word[:1]) + word[1:]
		}
		return strings.Join(words, " ")
	}
	if ruleFailureCount > 0 {
		return "Rule Issues"
	}
	return "Stored"
}
