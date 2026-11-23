// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package logging

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
)

func TestInputLogExportAcrossRotation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	root := t.TempDir()
	database := filepath.Join(root, "upbrr.db")
	repo, err := db.OpenContext(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	logger, err := New(config.LoggingConfig{
		Level:          "warn",
		FileEnabled:    true,
		MaxTotalSizeMB: 1,
		MaxFiles:       8,
	}, database)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	logger.SetConsoleOutput(io.Discard, io.Discard)
	writer, ok := logger.closer.(*rotatingWriter)
	if !ok {
		t.Fatal("expected the rotating file sink")
	}
	writer.maxBytes = 235
	source := filepath.Join(root, "original", "Example.mkv")
	other := filepath.Join(root, "other", "Example.mkv")
	first, id, finish := logger.BeginInputLog(ctx, repo, source, "open input")
	canonical := filepath.Join(root, "verified", "Example.mkv")
	_, nestedID, bindVerified := logger.BeginInputLog(first, repo, source, "verify")
	if nestedID != id {
		t.Fatal("nested verification opened an independent attempt")
	}
	bindVerified(canonical, "completed")
	second, otherID, otherFinish := logger.BeginInputLog(ctx, repo, other, "open input")
	FromContext(first, logger).Tracef("first detail\ncontinued api_key=example-secret")
	FromContext(second, logger).Tracef("not this input")
	// Application level overrides must not discard ownership or disk TRACE.
	view, err := NewOperationLogger(logger, "error")
	if err != nil {
		t.Fatal(err)
	}
	FromContext(WithOperationLogger(first, view), logger).Tracef("last detail")
	otherFinish("", "completed")
	finish("", "completed")
	if id == "" || id == otherID {
		t.Fatal("processing attempts did not get distinct IDs")
	}
	if len(logger.Recent(0)) != 0 {
		t.Fatal("file TRACE raised the application threshold")
	}

	attempts, err := db.ReadOnlyInputLogs(ctx, database, source)
	if err != nil || len(attempts) != 1 || attempts[0].ID != id || attempts[0].SourcePath != canonical {
		t.Fatalf("attempt lookup: %v, %v", attempts, err)
	}
	restarted, err := ReadInputLog(ctx, nil, database, attempts[0])
	if err != nil || restarted.Coverage != "complete" {
		t.Fatalf("restart export coverage: %s, %v", restarted.Coverage, err)
	}
	start, end := strings.Index(restarted.Text, "first detail\ncontinued"), strings.Index(restarted.Text, "last detail")
	if start < 0 || end <= start || strings.Contains(restarted.Text, "not this input") ||
		strings.Contains(restarted.Text, "example-secret") || !strings.Contains(restarted.Text, "[REDACTED]") {
		t.Fatal("export did not preserve order, isolation and redaction")
	}
	rotated, err := filepath.Glob(writer.path + ".*")
	if err != nil || len(rotated) < 2 {
		t.Fatalf("fixtures did not rotate: %v, %v", rotated, err)
	}
	if err := os.Remove(rotated[len(rotated)-1]); err != nil {
		t.Fatal(err)
	}
	partial, err := ReadInputLog(ctx, nil, database, attempts[0])
	if err != nil || partial.Coverage != "partial" {
		t.Fatalf("rotation gap was not reported: %s, %v", partial.Coverage, err)
	}
}

func TestInputLogExportLimitKeepsNewestRecords(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"disk", "memory"} {
		t.Run("older attempt on "+source, func(t *testing.T) {
			fromMemory := source == "memory"
			ctx := t.Context()
			database := filepath.Join(t.TempDir(), "upbrr.db")
			logger, err := New(config.LoggingConfig{
				Level:          "warn",
				FileEnabled:    true,
				MaxTotalSizeMB: 1,
				MaxFiles:       1,
			}, database)
			if err != nil {
				t.Fatal(err)
			}
			defer logger.Close()
			source := filepath.Join(filepath.Dir(database), "Example.mkv")
			store := &recordingAttempts{}
			old, _, finishOld := logger.BeginInputLog(ctx, store, source, "preparation")
			FromContext(old, logger).Tracef("old detail %s", strings.Repeat("o", 80))
			finishOld("", "completed")
			if fromMemory {
				writer, ok := logger.closer.(*rotatingWriter)
				if !ok {
					t.Fatal("expected rotating log writer")
				}
				if err := writer.rotate(); err != nil {
					t.Fatal(err)
				}
				// Put the earlier captured entry in a different timestamp second
				// without depending on a sleep or the time the test starts.
				logger.inputLogs[0].Time = time.Now().Add(-time.Hour)
			}
			current, _, finishCurrent := logger.BeginInputLog(ctx, store, source, "upload")
			FromContext(current, logger).Tracef("latest failure detail")
			finishCurrent("", "failed")
			store.attempts[1].Outcome = "failed"
			attempts := []db.InputLogAttempt{store.attempts[1], store.attempts[0]}
			if fromMemory {
				full, err := ReadInputLogs(ctx, logger, database, attempts)
				if err != nil || !strings.Contains(full.Text, "Coverage: memory") || !strings.Contains(full.Text, "old detail") {
					t.Fatalf("earlier attempt did not use the memory fallback: %#v, %v", full, err)
				}
			}
			data, err := readInputLogs(ctx, logger, database, attempts, 120)
			if err != nil || data.Coverage != "partial" || !strings.Contains(data.Text, "latest failure detail") ||
				strings.Contains(data.Text, "old detail") {
				t.Fatalf("bounded export = %#v, %v", data, err)
			}
		})
	}
}

func TestInputLogExportOrdersMemoryAfterOlderDiskInSameSecond(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                  string
		diskOffset            time.Duration
		legacy, prefersMemory bool
		trimDisk              bool
	}{
		{
			name:          "newer memory",
			diskOffset:    100 * time.Millisecond,
			prefersMemory: true,
		},
		{name: "newer disk", diskOffset: 950 * time.Millisecond},
		{
			name:          "legacy disk",
			legacy:        true,
			prefersMemory: true,
		},
		{
			name:          "legacy disk trimmed before merge",
			legacy:        true,
			prefersMemory: true,
			trimDisk:      true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			database := filepath.Join(t.TempDir(), "upbrr.db")
			source := filepath.Join(filepath.Dir(database), "Example.mkv")
			logger, err := New(config.LoggingConfig{Level: "warn"}, database)
			if err != nil {
				t.Fatal(err)
			}
			logPath, err := LogPath(database)
			if err != nil {
				t.Fatal(err)
			}
			// Use the persisted format from before and after precise markers.
			second := time.Date(2026, 10, 5, 12, 1, 2, 0, time.Local)
			at := ""
			if !test.legacy {
				at = fmt.Sprintf(" %d", second.Add(test.diskOffset).UnixNano())
			}
			line := fmt.Sprintf("%s TRACE: [input-log disk 1 record%s] disk detail %s\n",
				second.Format(inputLogTimeFormat), at, strings.Repeat("d", 60))
			limit, payload := 120, strings.Repeat("m", 50)
			if test.trimDisk {
				// Only one disk line fits in its own queue, but that line
				// and the memory line together fit the shared limit.
				line += fmt.Sprintf("%s TRACE: [input-log disk 2 record] recent disk record %s\n",
					second.Format(inputLogTimeFormat), strings.Repeat("d", 60))
				limit, payload = 160, ""
			}
			if err := os.WriteFile(logPath, []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}
			store := &recordingAttempts{}
			current, _, finish := logger.BeginInputLog(ctx, store, source, "upload")
			FromContext(current, logger).Tracef("memory-only failure %s", payload)
			finish("", "failed")
			logger.inputLogs[0].Time = second.Add(900 * time.Millisecond)
			attempts := []db.InputLogAttempt{store.attempts[0], {
				ID:         "disk",
				SourcePath: source,
				Kind:       "preparation",
				StartedAt:  second,
			}}
			result, err := readInputLogs(ctx, logger, database, attempts, limit)
			if err != nil || result.Coverage != "partial" ||
				strings.Contains(result.Text, "memory-only failure") != test.prefersMemory ||
				strings.Contains(result.Text, "disk detail") == test.prefersMemory {
				t.Fatalf("bounded same-second export = %#v, %v", result, err)
			}
			if strings.Contains(result.Text, "recent disk record") != test.trimDisk {
				t.Fatalf("unexpected retained disk record: %s", result.Text)
			}
			if strings.Contains(result.Text, "order within a second may be uncertain") != test.legacy {
				t.Fatalf("expected ordering warning only for legacy disk records: %s", result.Text)
			}
		})
	}
}

func TestInputLogUsesMemoryWhenFileLoggingOff(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	database := filepath.Join(t.TempDir(), "upbrr.db")
	logger, err := New(config.LoggingConfig{Level: "info"}, database)
	if err != nil {
		t.Fatal(err)
	}
	logger.SetConsoleOutput(io.Discard, io.Discard)
	source := filepath.Join(filepath.Dir(database), "Example.mkv")
	store := &recordingAttempts{}
	first, _, finishFirst := logger.BeginInputLog(ctx, store, source, "CLI input")
	second, _, finishSecond := logger.BeginInputLog(ctx, store, source, "CLI input")
	FromContext(first, logger).Infof("older attempt")
	FromContext(second, logger).Infof("requested input")
	logger.Infof("global line")
	finishFirst("", "failed")
	finishSecond("", "completed")
	data, err := ReadInputLog(ctx, logger, database, store.attempts[1])
	if err != nil || data.Coverage != "memory" || !strings.Contains(data.Text, "requested input") ||
		strings.Contains(data.Text, "older attempt") || strings.Contains(data.Text, "global line") {
		t.Fatalf("memory export = %#v, %v", data, err)
	}
	if data, err := ReadInputLog(ctx, nil, database, store.attempts[1]); err != nil || data.Coverage != "expired" {
		t.Fatalf("memory incorrectly persisted: %#v, %v", data, err)
	}
}

type recordingAttempts struct {
	attempts []db.InputLogAttempt
}

func (s *recordingAttempts) BeginInputLog(_ context.Context, attempt db.InputLogAttempt) error {
	s.attempts = append(s.attempts, attempt)
	return nil
}

func (s *recordingAttempts) FinishInputLog(context.Context, string, string, string) error {
	return nil
}
