// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package logging

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if err != nil || len(attempts) != 1 || attempts[0].ID != id {
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

func TestInputLogUsesExistingMemoryBufferWhenFileLoggingOff(t *testing.T) {
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
