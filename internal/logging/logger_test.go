// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package logging

import (
	"bytes"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
)

func TestResolveEffectiveLevel(t *testing.T) {
	t.Parallel()

	if got := ResolveEffectiveLevel("info", "", false); got != "info" {
		t.Fatalf("expected configured level info, got %q", got)
	}
	if got := ResolveEffectiveLevel("info", "", true); got != "debug" {
		t.Fatalf("expected debug fallback for debug runs, got %q", got)
	}
	if got := ResolveEffectiveLevel("info", "trace", true); got != "trace" {
		t.Fatalf("expected explicit override trace, got %q", got)
	}
}

func TestLoggerSanitizesLocalPaths(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	logger, err := NewWithLevel(config.LoggingConfig{Level: "debug"}, "", "")
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	logger.SetConsoleOutput(&stdout, &stderr)

	logger.Debugf("source=%s artifact=%s tracker=%s", `D:\media\Example.Release.2026-GRP`, `C:\Users\Tester\.upbrr\tmp\Example.Release.2026-GRP\file.torrent`, "ABC")

	console := stdout.String()
	for _, leaked := range []string{`D:\media`, `C:\Users`, "Example.Release.2026-GRP"} {
		if strings.Contains(console, leaked) {
			t.Fatalf("expected console log to redact local path details")
		}
	}
	for _, expected := range []string{"source=[local path]", "artifact=[db tmp]", "tracker=ABC"} {
		if !strings.Contains(console, expected) {
			t.Fatalf("expected console log to contain %q, got %q", expected, console)
		}
	}

	recent := logger.Recent(1)
	if len(recent) != 1 {
		t.Fatalf("expected one buffered log entry, got %d", len(recent))
	}
	if strings.Contains(recent[0].Message, `D:\media`) || strings.Contains(recent[0].Message, "Example.Release.2026-GRP") {
		t.Fatalf("expected buffered log to redact local path details")
	}
	if !strings.Contains(recent[0].Message, "source=[local path] artifact=[db tmp] tracker=ABC") {
		t.Fatalf("unexpected buffered log message: %q", recent[0].Message)
	}
}

func TestSanitizeLogMessageHandlesUnixLocalPaths(t *testing.T) {
	t.Parallel()

	got := sanitizeLogMessage("cache=/home/tester/.upbrr/cache/banned/file.json source=/media/releases/Example.Release.2026-GRP tracker=ABC")
	for _, leaked := range []string{"/home/tester", "/media/releases", "Example.Release.2026-GRP"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("expected sanitized message to redact %q from %q", leaked, got)
		}
	}
	for _, expected := range []string{"cache=[db cache]", "source=[local path]", "tracker=ABC"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected sanitized message to contain %q, got %q", expected, got)
		}
	}
}
