// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

type historyRecordRepo struct {
	stubRepo
	record api.HistoryRecord
}

func (r historyRecordRepo) LoadHistoryRecord(context.Context, string) (api.HistoryRecord, error) {
	return r.record, nil
}

func TestHistoryOperationsHonorPreCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	history := newHistoryModule(&stubRepo{}, "", api.NopLogger{})

	if _, err := history.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("list history: expected cancellation, got %v", err)
	}
	if _, err := history.Overview(ctx, "source"); !errors.Is(err, context.Canceled) {
		t.Fatalf("history overview: expected cancellation, got %v", err)
	}
	if err := history.Delete(ctx, "source"); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete history: expected cancellation, got %v", err)
	}
	if _, err := history.DeleteAll(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete all history: expected cancellation, got %v", err)
	}
}

func TestCoreHistoryOperationsWithoutRepositoryReturnInitializationError(t *testing.T) {
	t.Parallel()

	core := newTestCore(testCoreOptions{})
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "list",
			call: func() error {
				_, err := core.ListHistory(context.Background())
				return err
			},
		},
		{
			name: "overview",
			call: func() error {
				_, err := core.GetHistoryOverview(context.Background(), "source")
				return err
			},
		},
		{
			name: "delete",
			call: func() error {
				return core.DeleteHistoryRelease(context.Background(), "source")
			},
		},
		{
			name: "delete all",
			call: func() error {
				_, err := core.DeleteAllHistoryReleases(context.Background())
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil || err.Error() != "core: repository not initialized" {
				t.Fatalf("expected repository initialization error, got %v", err)
			}
		})
	}
}

func TestHistoryOverviewRejectsBlankPathBeforeRepositoryRead(t *testing.T) {
	t.Parallel()

	history := newHistoryModule(&stubRepo{}, "", api.NopLogger{})
	if _, err := history.Overview(context.Background(), "  "); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

func TestHistoryOverviewProjectsStoredPreparedReleaseWithoutPublishedSession(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	prepared := api.PreparedRelease{
		Generation: 1,
		Source:     api.SourceManifest{SourcePath: sourcePath},
		Naming:     api.NamingFacts{ReleaseName: "Example.Release.2026.1080p-GRP"},
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Generation: 1,
			TMDBID:     1234567,
			Category:   api.CanonicalCategoryMovie,
		},
	}
	history := newHistoryModule(historyRecordRepo{record: api.HistoryRecord{
		SourcePath:      sourcePath,
		PreparedRelease: &prepared,
	}}, "", api.NopLogger{})

	overview, err := history.Overview(context.Background(), sourcePath)
	if err != nil {
		t.Fatalf("history overview: %v", err)
	}
	if overview.Release.SourcePath != sourcePath || overview.Release.Generation != prepared.Generation {
		t.Fatalf("release ref = %#v", overview.Release)
	}
	if overview.Identity.TMDBID != prepared.Identity.TMDBID {
		t.Fatalf("TMDB ID = %d, want %d", overview.Identity.TMDBID, prepared.Identity.TMDBID)
	}
	if overview.Display.ReleaseName != prepared.Naming.ReleaseName {
		t.Fatalf("release name = %q, want %q", overview.Display.ReleaseName, prepared.Naming.ReleaseName)
	}
}
