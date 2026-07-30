// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"testing"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

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
