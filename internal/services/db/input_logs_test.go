// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestInputLogLookupsAndRequestedAlias(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := openMigratedTestRepo(t)
	root := t.TempDir()
	now := time.Date(2026, time.November, 24, 10, 0, 0, 0, time.UTC)

	plain := filepath.Join(root, "Plain", "Example.mkv")
	requested := filepath.Join(root, "AliasDir", "Requested.Release.mkv")
	canonical := filepath.Join(root, "VerifiedDir", "Canonical.Release.mkv")

	plainAttempt := InputLogAttempt{
		ID:         "000000000000000000000001",
		SourcePath: plain,
		Kind:       "CLI input",
		StartedAt:  now,
	}
	if err := repo.BeginInputLog(ctx, plainAttempt); err != nil {
		t.Fatal(err)
	}
	plainAttempt.SourcePath = ""
	plainAttempt.RequestedPath = plain
	plainAttempt.Outcome = "completed"
	if err := repo.FinishInputLog(ctx, plainAttempt); err != nil {
		t.Fatal(err)
	}
	aliasAttempt := InputLogAttempt{
		ID:            "000000000000000000000002",
		SourcePath:    requested,
		RequestedPath: requested,
		Kind:          "CLI input",
		StartedAt:     now.Add(time.Second),
	}
	if err := repo.BeginInputLog(ctx, aliasAttempt); err != nil {
		t.Fatal(err)
	}
	aliasAttempt.SourcePath = canonical
	aliasAttempt.Outcome = "completed"
	if err := repo.FinishInputLog(ctx, aliasAttempt); err != nil {
		t.Fatal(err)
	}

	// Plain-path lookup works across both WebUI (InputLogs) and CLI (ReadOnlyInputLogs).
	if got, err := repo.InputLogs(ctx, plain, ""); err != nil || len(got) != 1 || got[0].ID != "000000000000000000000001" {
		t.Fatalf("plain InputLogs = %#v, %v", got, err)
	}
	if got, err := ReadOnlyInputLogs(ctx, repo.DBPath(), plain); err != nil || len(got) != 1 || got[0].ID != "000000000000000000000001" {
		t.Fatalf("plain ReadOnlyInputLogs = %#v, %v", got, err)
	}

	// Exact-ID lookup remains exact.
	if got, err := repo.InputLogs(ctx, "", "000000000000000000000002"); err != nil || len(got) != 1 || got[0].SourcePath != canonical {
		t.Fatalf("exact-ID InputLogs = %#v, %v", got, err)
	}

	// WebUI InputLogs groups by verified source_path only, not distinct requested_path.
	if got, err := repo.InputLogs(ctx, canonical, ""); err != nil || len(got) != 1 || got[0].ID != "000000000000000000000002" {
		t.Fatalf("canonical InputLogs = %#v, %v", got, err)
	}
	if got, err := repo.InputLogs(ctx, requested, ""); err != nil || len(got) != 0 {
		t.Fatalf("WebUI InputLogs should not match distinct requested alias: %#v, %v", got, err)
	}

	// ReadOnlyInputLogs matches canonical source and exact requested alias across platforms.
	for _, query := range []string{canonical, requested} {
		if got, err := ReadOnlyInputLogs(ctx, repo.DBPath(), query); err != nil || len(got) != 1 || got[0].ID != "000000000000000000000002" {
			t.Fatalf("ReadOnlyInputLogs(%q) = %#v, %v", query, got, err)
		}
	}

	// Case-variant original requested alias: matches on Windows, stays distinct on Unix.
	caseVariantRequested := filepath.Join(root, "aliasdir", "requested.release.mkv")
	gotVariant, err := ReadOnlyInputLogs(ctx, repo.DBPath(), caseVariantRequested)
	if err != nil {
		t.Fatalf("ReadOnlyInputLogs(%q): %v", caseVariantRequested, err)
	}
	if runtime.GOOS == "windows" {
		if len(gotVariant) != 1 || gotVariant[0].ID != "000000000000000000000002" || gotVariant[0].SourcePath != canonical {
			t.Fatalf("Windows case-variant requested alias = %#v", gotVariant)
		}
		slashVariant := strings.ToUpper(filepath.ToSlash(requested))
		if gotSlash, err := ReadOnlyInputLogs(ctx, repo.DBPath(), slashVariant); err != nil || len(gotSlash) != 1 || gotSlash[0].ID != "000000000000000000000002" {
			t.Fatalf("Windows slash/case-variant requested alias = %#v, %v", gotSlash, err)
		}
	} else if len(gotVariant) != 0 {
		t.Fatalf("Unix case-variant requested alias unexpectedly matched: %#v", gotVariant)
	}
}

func TestInputLogOrderingBreaksTiedStartTimesByInsertionOrder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := openMigratedTestRepo(t)
	source := filepath.Join(t.TempDir(), "Tied.Timestamps.mkv")
	base := time.Date(2026, time.November, 24, 10, 0, 0, 0, time.UTC)

	attempts := []InputLogAttempt{
		{
			ID:         "ffffffffffffffffffffffff",
			SourcePath: source,
			Kind:       "CLI input",
			StartedAt:  base,
		},
		{
			ID:         "000000000000000000000001",
			SourcePath: source,
			Kind:       "CLI input",
			StartedAt:  base,
		},
		{
			ID:         "aaaaaaaaaaaaaaaaaaaaaaaa",
			SourcePath: source,
			Kind:       "CLI input",
			StartedAt:  base.Add(-time.Second),
		},
		{
			ID:         "555555555555555555555555",
			SourcePath: source,
			Kind:       "CLI input",
			StartedAt:  base.Add(time.Second),
		},
	}
	for _, attempt := range attempts {
		if err := repo.BeginInputLog(ctx, attempt); err != nil {
			t.Fatal(err)
		}
	}

	want := []string{
		"555555555555555555555555",
		"000000000000000000000001",
		"ffffffffffffffffffffffff",
		"aaaaaaaaaaaaaaaaaaaaaaaa",
	}
	extractIDs := func(got []InputLogAttempt) []string {
		ids := make([]string, len(got))
		for i, item := range got {
			ids[i] = item.ID
		}
		return ids
	}

	gotNormal, err := repo.InputLogs(ctx, source, "")
	if err != nil {
		t.Fatalf("InputLogs: %v", err)
	}
	if ids := extractIDs(gotNormal); !slices.Equal(ids, want) {
		t.Fatalf("InputLogs order = %v, want %v", ids, want)
	}

	gotReadOnly, err := ReadOnlyInputLogs(ctx, repo.DBPath(), source)
	if err != nil {
		t.Fatalf("ReadOnlyInputLogs: %v", err)
	}
	if ids := extractIDs(gotReadOnly); !slices.Equal(ids, want) {
		t.Fatalf("ReadOnlyInputLogs order = %v, want %v", ids, want)
	}
}

func TestInputLogBusyHandlingAndReadOnlyContract(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("read-only lookup does not create missing database", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "missing.db")
		if _, err := ReadOnlyInputLogs(ctx, missing, filepath.Join(t.TempDir(), "Example.mkv")); err == nil {
			t.Fatal("expected error for missing database")
		}
		if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read-only open created database file: %v", err)
		}
	})

	t.Run("read-only lookup retries under exclusive lock and honors cancellation", func(t *testing.T) {
		t.Parallel()
		dbPath := filepath.Join(t.TempDir(), "locked.db")
		raw, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = raw.Close() })
		if err := MigrateContext(ctx, raw); err != nil {
			t.Fatal(err)
		}
		// Rollback journal + BEGIN EXCLUSIVE forces SQLITE_BUSY on readers so
		// retryBusyContext enters Go-layer backoff and surfaces the caller's
		// context cancellation instead of returning immediate SQLITE_BUSY.
		lockConn, err := raw.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = lockConn.Close() })
		if _, err := lockConn.ExecContext(ctx, `BEGIN EXCLUSIVE`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = lockConn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`) })
		guard, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		lockedCtx, cancel := context.WithCancel(guard)
		defer cancel()
		retried := false
		_, readErr := readOnlyInputLogs(lockedCtx, dbPath, filepath.Join(t.TempDir(), "Locked.mkv"), sqliteMigrationTraceLogger{
			record: func(event string) {
				if strings.Contains(event, "read input logs busy, retrying") {
					retried = true
					cancel()
				}
			},
		})
		if !retried || !errors.Is(readErr, context.Canceled) {
			t.Fatalf("ReadOnlyInputLogs retry observed = %t, err = %v; want cancellation during retry", retried, readErr)
		}
	})

	t.Run("association writes recover after busy retry", func(t *testing.T) {
		t.Parallel()
		dbPath := filepath.Join(t.TempDir(), "busy-writes.db")
		holder, err := OpenContext(ctx, dbPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = holder.Close() })
		if err := holder.MigrateContext(ctx); err != nil {
			t.Fatal(err)
		}
		retrying := make(chan string, 4)
		writer, err := OpenWithLoggerContext(ctx, dbPath, sqliteMigrationTraceLogger{
			record: func(event string) {
				select {
				case retrying <- event:
				default:
				}
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = writer.Close() })
		if _, err := writer.db.ExecContext(ctx, `PRAGMA busy_timeout = 1`); err != nil {
			t.Fatal(err)
		}

		runAfterBusyRetry := func(wantOp string, writeFn func() error) {
			t.Helper()
			for len(retrying) > 0 {
				<-retrying
			}
			lockConn, err := holder.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lockConn.Close() }()
			if _, err := lockConn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
				t.Fatal(err)
			}
			locked := true
			defer func() {
				if locked {
					_, _ = lockConn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`)
				}
			}()
			errCh := make(chan error, 1)
			go func() {
				errCh <- writeFn()
			}()
			select {
			case msg := <-retrying:
				if !strings.Contains(msg, wantOp) {
					t.Fatalf("retry log = %q, want operation %q", msg, wantOp)
				}
			case err := <-errCh:
				t.Fatalf("%s completed before busy retry: %v", wantOp, err)
			case <-time.After(10 * time.Second):
				t.Fatalf("%s did not signal busy retry", wantOp)
			}
			if _, err := lockConn.ExecContext(ctx, `ROLLBACK`); err != nil {
				t.Fatal(err)
			}
			locked = false
			select {
			case err := <-errCh:
				if err != nil {
					t.Fatalf("%s after lock release: %v", wantOp, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("%s did not finish after lock release", wantOp)
			}
		}

		root := t.TempDir()
		requested := filepath.Join(root, "Requested.mkv")
		verified := filepath.Join(root, "Verified.mkv")
		attemptID := "000000000000000000000099"
		startedAt := time.Now().UTC()
		runAfterBusyRetry("save input log association", func() error {
			return writer.BeginInputLog(ctx, InputLogAttempt{
				ID:            attemptID,
				SourcePath:    requested,
				RequestedPath: requested,
				Kind:          "CLI input",
				StartedAt:     startedAt,
			})
		})
		runAfterBusyRetry("save input log outcome", func() error {
			return writer.FinishInputLog(ctx, InputLogAttempt{
				ID:            attemptID,
				SourcePath:    verified,
				RequestedPath: requested,
				Kind:          "CLI input",
				Outcome:       "completed",
				StartedAt:     startedAt,
			})
		})

		got, err := holder.InputLogs(ctx, verified, "")
		if err != nil || len(got) != 1 || got[0].ID != attemptID || got[0].SourcePath != verified || got[0].Outcome != "completed" {
			t.Fatalf("persisted attempt after retries = %#v, %v", got, err)
		}
	})
}

func TestDeleteInputLogsIsExactLexicalAndPreservesStoredReleases(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := openMigratedTestRepo(t)
	now := time.Date(2026, time.November, 24, 12, 0, 0, 0, time.UTC)

	parentDir := filepath.Join(t.TempDir(), "SeasonPack")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	childFile := filepath.Join(parentDir, "Example.Child.2026.1080p-GRP.mkv")
	if err := os.WriteFile(childFile, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	concurrentFile := filepath.Join(parentDir, "Concurrent.Release.2026.1080p-GRP.mkv")
	if err := os.WriteFile(concurrentFile, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Stored child release and upload record under parentDir.
	if err := repo.Save(ctx, FileMetadata{
		Path:  childFile,
		Title: "Example Child",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateUploadRecord(ctx, UploadRecord{
		Tracker:    "BLU",
		Status:     "uploaded",
		SourcePath: childFile,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.BeginInputLog(ctx, InputLogAttempt{
		ID:         "000000000000000000000010",
		SourcePath: childFile,
		Kind:       "preparation",
		StartedAt:  now,
	}); err != nil {
		t.Fatal(err)
	}

	// Failed parent log-only attempt and a platform-specific path variant.
	if err := repo.BeginInputLog(ctx, InputLogAttempt{
		ID:         "000000000000000000000011",
		SourcePath: parentDir,
		Kind:       "open input",
		StartedAt:  now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	parentVariant := parentDir + " "
	if runtime.GOOS == "windows" {
		parentVariant = strings.ToLower(filepath.ToSlash(parentDir))
	}
	if err := repo.BeginInputLog(ctx, InputLogAttempt{
		ID:         "000000000000000000000012",
		SourcePath: parentVariant,
		Kind:       "open input",
		StartedAt:  now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := repo.DeleteInputLogs(ctx, parentVariant); err != nil {
			t.Fatal(err)
		}
		if got, err := repo.InputLogs(ctx, parentVariant, ""); err != nil || len(got) != 0 {
			t.Fatalf("literal-space path logs after delete = %#v, %v", got, err)
		}
		if got, err := repo.InputLogs(ctx, parentDir, ""); err != nil || len(got) != 1 {
			t.Fatalf("parent path logs after deleting distinct literal-space path = %#v, %v", got, err)
		}
	}

	// Concurrent stored release at the exact lexical path of a log entry.
	if err := repo.Save(ctx, FileMetadata{
		Path:  concurrentFile,
		Title: "Concurrent Release",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateUploadRecord(ctx, UploadRecord{
		Tracker:    "BLU",
		Status:     "uploaded",
		SourcePath: concurrentFile,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.BeginInputLog(ctx, InputLogAttempt{
		ID:         "000000000000000000000013",
		SourcePath: concurrentFile,
		Kind:       "open input",
		StartedAt:  now.Add(3 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	// Make childFile the active input: deleting parentDir log-only rows must
	// succeed without touching childFile, while deleting childFile's own logs
	// must be rejected as busy.
	slot, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opening := api.ActiveInputRecord{
		Revision:       slot.Revision + 1,
		State:          api.ActiveInputOpening,
		OwnerID:        "session-1",
		CoordinatorID:  "coord-1",
		Fence:          slot.Fence + 1,
		ReservationID:  "res-1",
		IdempotencyKey: "open-child-1",
		RequestedPath:  childFile,
		LeaseExpiresAt: now.Add(time.Minute),
	}
	if err := repo.CompareAndSwapActiveInput(ctx, slot, opening, now); err != nil {
		t.Fatal(err)
	}

	if err := repo.DeleteInputLogs(ctx, parentDir); err != nil {
		t.Fatalf("DeleteInputLogs(parentDir): %v", err)
	}
	if got, err := repo.InputLogs(ctx, parentDir, ""); err != nil || len(got) != 0 {
		t.Fatalf("parentDir input logs after delete = %#v, %v", got, err)
	}
	if meta, err := repo.GetByPath(ctx, childFile); err != nil || meta.Title != "Example Child" {
		t.Fatalf("child metadata erased or changed: %#v, %v", meta, err)
	}
	if uploads, err := repo.ListUploadHistoryByPath(ctx, childFile); err != nil || len(uploads) != 1 || uploads[0].Tracker != "BLU" {
		t.Fatalf("child upload history erased or changed: %#v, %v", uploads, err)
	}
	if childLogs, err := repo.InputLogs(ctx, childFile, ""); err != nil || len(childLogs) != 1 || childLogs[0].ID != "000000000000000000000010" {
		t.Fatalf("child input logs erased: %#v, %v", childLogs, err)
	}

	if err := repo.DeleteInputLogs(ctx, childFile); !errors.Is(err, api.ErrActiveInputBusy) {
		t.Fatalf("DeleteInputLogs(active childFile) err = %v, want api.ErrActiveInputBusy", err)
	}
	if childLogs, err := repo.InputLogs(ctx, childFile, ""); err != nil || len(childLogs) != 1 {
		t.Fatalf("busy active child input logs were deleted: %#v, %v", childLogs, err)
	}

	// Exact lexical log deletion on a path that concurrently became a stored
	// release removes only input_log_attempts and preserves metadata/uploads.
	if err := repo.DeleteInputLogs(ctx, concurrentFile); err != nil {
		t.Fatalf("DeleteInputLogs(concurrentFile): %v", err)
	}
	if got, err := repo.InputLogs(ctx, concurrentFile, ""); err != nil || len(got) != 0 {
		t.Fatalf("concurrentFile input logs after delete = %#v, %v", got, err)
	}
	if meta, err := repo.GetByPath(ctx, concurrentFile); err != nil || meta.Title != "Concurrent Release" {
		t.Fatalf("concurrent release metadata erased: %#v, %v", meta, err)
	}
	if uploads, err := repo.ListUploadHistoryByPath(ctx, concurrentFile); err != nil || len(uploads) != 1 {
		t.Fatalf("concurrent release upload history erased: %#v, %v", uploads, err)
	}
}

func TestFinishInputLogRecreatesDeletedInFlightAttemptAndRejectsIncomplete(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := openMigratedTestRepo(t)
	root := t.TempDir()
	requested := filepath.Join(root, "Alias", "Retry.Release.2026-GRP.mkv")
	verified := filepath.Join(root, "Canonical", "Retry.Release.2026-GRP.mkv")
	now := time.Date(2026, time.November, 24, 14, 0, 0, 0, time.UTC)

	for _, invalid := range []InputLogAttempt{
		{
			SourcePath: requested,
			Kind:       "CLI input",
			StartedAt:  now,
		},
		{
			ID:        "0000000000000000000000aa",
			Kind:      "CLI input",
			StartedAt: now,
		},
		{
			ID:         "0000000000000000000000aa",
			SourcePath: requested,
			StartedAt:  now,
		},
		{
			ID:         "0000000000000000000000aa",
			SourcePath: requested,
			Kind:       "CLI input",
		},
	} {
		if err := repo.FinishInputLog(ctx, invalid); !errors.Is(err, internalerrors.ErrInvalidInput) {
			t.Fatalf("FinishInputLog(%#v) err = %v, want ErrInvalidInput", invalid, err)
		}
	}

	oldAttempt := InputLogAttempt{
		ID:            "0000000000000000000000a1",
		SourcePath:    requested,
		RequestedPath: requested,
		Kind:          "open input",
		StartedAt:     now,
	}
	if err := repo.BeginInputLog(ctx, oldAttempt); err != nil {
		t.Fatal(err)
	}
	oldAttempt.Outcome = "failed"
	if err := repo.FinishInputLog(ctx, oldAttempt); err != nil {
		t.Fatal(err)
	}

	inFlight := InputLogAttempt{
		ID:            "0000000000000000000000a2",
		SourcePath:    requested,
		RequestedPath: requested,
		Kind:          "CLI input",
		StartedAt:     now.Add(time.Second),
	}
	if err := repo.BeginInputLog(ctx, inFlight); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteInputLogs(ctx, requested); err != nil {
		t.Fatalf("DeleteInputLogs: %v", err)
	}

	inFlight.SourcePath = verified
	inFlight.Outcome = "completed"
	if err := repo.FinishInputLog(ctx, inFlight); err != nil {
		t.Fatalf("FinishInputLog after delete: %v", err)
	}

	gotCanonical, err := repo.InputLogs(ctx, verified, "")
	if err != nil || len(gotCanonical) != 1 || gotCanonical[0].ID != inFlight.ID ||
		gotCanonical[0].SourcePath != verified || gotCanonical[0].RequestedPath != requested ||
		gotCanonical[0].Kind != inFlight.Kind || gotCanonical[0].Outcome != "completed" ||
		!gotCanonical[0].StartedAt.Equal(inFlight.StartedAt) {
		t.Fatalf("InputLogs(verified) = %#v, %v", gotCanonical, err)
	}
	if gotOld, err := repo.InputLogs(ctx, "", oldAttempt.ID); err != nil || len(gotOld) != 0 {
		t.Fatalf("old deleted attempt survived: %#v, %v", gotOld, err)
	}
	for _, query := range []string{requested, verified} {
		gotRO, err := ReadOnlyInputLogs(ctx, repo.DBPath(), query)
		if err != nil || len(gotRO) != 1 || gotRO[0].ID != inFlight.ID || gotRO[0].SourcePath != verified || gotRO[0].RequestedPath != requested {
			t.Fatalf("ReadOnlyInputLogs(%q) = %#v, %v", query, gotRO, err)
		}
	}
}
