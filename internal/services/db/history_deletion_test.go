// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestHistoryDeletionReservesWriterThroughCleanup(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	repo, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if _, err := other.db.ExecContext(t.Context(), "PRAGMA busy_timeout = 0"); err != nil {
		t.Fatal(err)
	}
	assertWriterExcluded := func() {
		t.Helper()
		_, err := other.db.ExecContext(t.Context(), `UPDATE active_input SET revision = revision WHERE singleton = 1`)
		if !IsBusyError(err) {
			t.Fatalf("another coordinator could write during history cleanup: %v", err)
		}
	}
	source := filepath.Join(t.TempDir(), "Example.mkv")
	if err := repo.WithHistoryDeletion(t.Context(), func(ctx context.Context) error {
		assertWriterExcluded()
		if _, err := repo.ListStoredReleasePaths(ctx); err != nil {
			return err
		}
		if _, err := repo.LoadHistoryCleanupSnapshot(ctx, source); err != nil {
			return err
		}
		if err := repo.PurgeContentData(ctx, source); err != nil {
			return err
		}
		if _, err := repo.ListStoredHistoryArtifactPaths(ctx); err != nil {
			return err
		}
		assertWriterExcluded()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := other.db.ExecContext(t.Context(), `UPDATE active_input SET revision = revision WHERE singleton = 1`); err != nil {
		t.Fatalf("writer reservation survived completed deletion: %v", err)
	}
}

func TestHistoryDeletionDoesNotRetryCleanupAndRollsBack(t *testing.T) {
	t.Parallel()
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	want := errors.New("synthetic cleanup failure")
	calls := 0
	err = repo.WithHistoryDeletion(t.Context(), func(ctx context.Context) error {
		calls++
		if _, err := repo.historyTransaction(ctx).ExecContext(ctx, `UPDATE active_input SET revision = 42 WHERE singleton = 1`); err != nil {
			return fmt.Errorf("update test revision: %w", err)
		}
		return want
	})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("cleanup error = %v, calls = %d", err, calls)
	}
	var revision int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT revision FROM active_input WHERE singleton = 1`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != 0 {
		t.Fatalf("failed cleanup committed revision %d", revision)
	}
}
