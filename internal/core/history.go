// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/pkg/api"
)

// historyModule owns history read shaping and deletion policy. Its repository
// and logger are borrowed; Core remains responsible for repository lifetime.
type historyModule struct {
	repo          api.HistoryRepository
	dbPath        string
	fs            historyFilesystem
	logger        api.Logger
	preparedFacts *preparedrelease.Module
}

type historyFilesystem interface {
	Stat(name string) (fs.FileInfo, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	Remove(name string) error
	RemoveAll(path string) error
}

type osHistoryFilesystem struct{}

func (osHistoryFilesystem) Stat(name string) (fs.FileInfo, error) {
	//nolint:wrapcheck // Adapter preserves OS error identity for existing cleanup policy.
	return os.Stat(name)
}

func (osHistoryFilesystem) ReadDir(name string) ([]fs.DirEntry, error) {
	//nolint:wrapcheck // Adapter preserves OS error identity for existing cleanup policy.
	return os.ReadDir(name)
}

func (osHistoryFilesystem) Remove(name string) error {
	//nolint:wrapcheck // Adapter preserves OS error identity for existing cleanup policy.
	return os.Remove(name)
}

func (osHistoryFilesystem) RemoveAll(path string) error {
	//nolint:wrapcheck // Adapter preserves OS error identity for existing cleanup policy.
	return os.RemoveAll(path)
}

func newHistoryModule(repo api.HistoryRepository, dbPath string, logger api.Logger) *historyModule {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &historyModule{
		repo:   repo,
		dbPath: dbPath,
		fs:     osHistoryFilesystem{},
		logger: logger,
	}
}

func (h *historyModule) List(ctx context.Context) ([]api.HistoryEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("core: list history canceled: %w", err)
	}
	if h == nil || h.repo == nil {
		return nil, errors.New("core: repository not initialized")
	}

	entries, err := h.repo.ListHistoryEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("core: %w", err)
	}

	result := make([]api.HistoryEntry, 0, len(entries))
	for _, entry := range entries {
		entryCopy := entry
		entryCopy.LatestUploadStatus = api.HistoryStatusLabel(entry.LatestUploadStatus, entry.RuleFailureCount)
		result = append(result, entryCopy)
	}
	return result, nil
}

func (h *historyModule) Overview(ctx context.Context, sourcePath string) (api.HistoryOverview, error) {
	if err := ctx.Err(); err != nil {
		return api.HistoryOverview{}, fmt.Errorf("core: get history overview canceled: %w", err)
	}
	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return api.HistoryOverview{}, internalerrors.ErrInvalidInput
	}
	if h == nil || h.repo == nil {
		return api.HistoryOverview{}, errors.New("core: repository not initialized")
	}

	record, err := h.repo.LoadHistoryRecord(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("core: %w", err)
	}
	overview := historyOverviewFromRecord(record)
	if record.PreparedReleaseRef != nil {
		if h.preparedFacts == nil {
			return api.HistoryOverview{}, errors.New("core: canonical preparation is not configured")
		}
		prepared, resolveErr := h.preparedFacts.ResolveResult(ctx, *record.PreparedReleaseRef)
		if resolveErr != nil {
			return api.HistoryOverview{}, fmt.Errorf("core: resolve history prepared generation: %w", resolveErr)
		}
		display, displayErr := h.preparedFacts.ResolveDisplay(ctx, *record.PreparedReleaseRef)
		if displayErr != nil {
			return api.HistoryOverview{}, fmt.Errorf("core: project history prepared generation: %w", displayErr)
		}
		overview.Release = *record.PreparedReleaseRef
		overview.Identity = prepared.Release.Identity
		overview.Display = display
	}
	overview.StatusLabel = api.HistoryStatusLabel(overview.LatestUploadStatus, api.CountBlockingRuleFailures(overview.TrackerRuleFailures))
	return overview, nil
}

func historyOverviewFromRecord(record api.HistoryRecord) api.HistoryOverview {
	return api.HistoryOverview{
		SourcePath:           record.SourcePath,
		ReleaseTitle:         record.ReleaseTitle,
		ReleaseSource:        record.ReleaseSource,
		ReleaseResolution:    record.ReleaseResolution,
		MetadataUpdatedAt:    record.MetadataUpdatedAt,
		LatestUploadStatus:   record.LatestUploadStatus,
		LatestUploadAt:       record.LatestUploadAt,
		Metadata:             record.Metadata,
		ReleaseNameOverrides: record.ReleaseNameOverrides,
		DescriptionOverride:  record.DescriptionOverride,
		DescriptionOverrides: append([]api.DescriptionOverride(nil), record.DescriptionOverrides...),
		PlaylistSelection:    record.PlaylistSelection,
		TrackerMetadata:      append([]api.TrackerMetadata(nil), record.TrackerMetadata...),
		TrackerRuleFailures:  append([]api.TrackerRuleFailure(nil), record.TrackerRuleFailures...),
		Screenshots:          append([]api.Screenshot(nil), record.Screenshots...),
		FinalSelections:      append([]api.ScreenshotFinalSelection(nil), record.FinalSelections...),
		UploadedImages:       append([]api.UploadedImageLink(nil), record.UploadedImages...),
		UploadHistory:        append([]api.UploadRecord(nil), record.UploadHistory...),
	}
}
