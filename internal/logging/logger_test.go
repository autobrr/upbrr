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

	logger.Debugf("source=%s artifact=%s tracker=%s", `D:\media\Example.Release.2026-GRP`, `C:\Users\Tester\.upbrr\tmp\file.torrent`, "ABC")

	console := stdout.String()
	for _, leaked := range []string{`D:\media`, `C:\Users`, "Example.Release.2026-GRP"} {
		if strings.Contains(console, leaked) {
			t.Fatalf("expected console log to redact local path details")
		}
	}
	for _, expected := range []string{"source=[local path]", "artifact=.upbrr/tmp/file.torrent", "tracker=ABC"} {
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
	if !strings.Contains(recent[0].Message, "source=[local path] artifact=.upbrr/tmp/file.torrent tracker=ABC") {
		t.Fatalf("unexpected buffered log message: %q", recent[0].Message)
	}
}

func TestSanitizeLogMessageHandlesUnixLocalPaths(t *testing.T) {
	t.Parallel()

	got := SanitizeMessage("cache=/home/tester/.upbrr/cache/banned/file.json source=/media/releases/Example.Release.2026-GRP tracker=ABC")
	for _, leaked := range []string{"/home/tester", "/media/releases", "Example.Release.2026-GRP"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("expected sanitized message to redact %q from %q", leaked, got)
		}
	}
	for _, expected := range []string{"cache=.upbrr/cache/banned/file.json", "source=[local path]", "tracker=ABC"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected sanitized message to contain %q, got %q", expected, got)
		}
	}
}

func TestSanitizeLogMessagePreservesURLs(t *testing.T) {
	t.Parallel()

	cases := []string{
		"web: serving web UI on 127.0.0.1:7480 (browser URL http://127.0.0.1:7480/app)",
		"image=https://img.example.com/media/poster.jpg tracker=ABC",
		"image=https://img.example.com/tmp/poster.jpg tracker=ABC",
		"image=https://img.example.com/home/user/poster.jpg tracker=ABC",
		"image=https://img.example.com/Users/tester/poster.jpg tracker=ABC",
	}
	for _, tc := range cases {
		got := SanitizeMessage(tc)
		if strings.Contains(got, "[local path]") {
			t.Fatalf("expected URL to remain intact, got %q", got)
		}
		if got != tc {
			t.Fatalf("expected URL to remain intact, got %q", got)
		}
	}

	got := SanitizeMessage("image=https://img.example.com/media/poster.jpg source=/media/releases/Example.Release.2026-GRP")
	if strings.Contains(got, "/media/releases") || strings.Contains(got, "Example.Release.2026-GRP") {
		t.Fatalf("expected local path after URL to be redacted, got %q", got)
	}
	if !strings.Contains(got, "image=https://img.example.com/media/poster.jpg source=[local path]") {
		t.Fatalf("expected URL preserved and local path redacted, got %q", got)
	}
}
