// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package logging

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
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

func TestConsoleLevelDoesNotChangeApplicationLogging(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	logger, err := NewWithConsoleLevel(config.LoggingConfig{
		Level:          "error",
		FileEnabled:    true,
		MaxTotalSizeMB: 1,
		MaxFiles:       1,
	}, dbPath, "info")
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	logger.SetConsoleOutput(&stdout, &stderr)
	subscriptionID, subscription := logger.Subscribe(2)
	defer logger.Unsubscribe(subscriptionID)

	logger.Infof("console-only detail")
	logger.Errorf("application failure")
	if err := logger.Close(); err != nil {
		t.Fatalf("close logger: %v", err)
	}

	if !strings.Contains(stdout.String(), "console-only detail") || !strings.Contains(stderr.String(), "application failure") {
		t.Fatalf("console output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	entries := logger.Recent(10)
	if len(entries) != 1 || entries[0].Level != "error" || entries[0].Message != "application failure" {
		t.Fatalf("application entries = %#v", entries)
	}
	select {
	case entry := <-subscription:
		if entry.Level != "error" || entry.Message != "application failure" {
			t.Fatalf("subscriber entry = %#v", entry)
		}
	default:
		t.Fatal("application entry did not reach subscriber")
	}
	select {
	case entry := <-subscription:
		t.Fatalf("console-only entry reached subscriber: %#v", entry)
	default:
	}
	logPath, err := LogPath(dbPath)
	if err != nil {
		t.Fatalf("resolve log path: %v", err)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(contents), "console-only detail") || !strings.Contains(string(contents), "application failure") {
		t.Fatalf("application log = %q", contents)
	}
}

func TestConsoleLevelCanFilterRetainedApplicationEntries(t *testing.T) {
	t.Parallel()

	logger, err := NewWithConsoleLevel(config.LoggingConfig{Level: "debug"}, "", "warn")
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	logger.SetConsoleOutput(&stdout, &stderr)

	logger.Debugf("application detail")
	logger.Warnf("console warning")

	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "console warning") {
		t.Fatalf("console output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	entries := logger.Recent(10)
	if len(entries) != 2 || entries[0].Level != "debug" || entries[1].Level != "warn" {
		t.Fatalf("application entries = %#v", entries)
	}
}

func TestLoggerPreservesPathsAndRedactsSecretsAcrossSinks(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	logger, err := New(config.LoggingConfig{
		Level:          "debug",
		FileEnabled:    true,
		MaxTotalSizeMB: 1,
		MaxFiles:       1,
	}, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	logger.SetConsoleOutput(&stdout, &stderr)
	id, entries := logger.Subscribe(1)
	defer logger.Unsubscribe(id)
	source := `D:\media\Example's Release.2026-GRP`
	logger.Debugf("source=%s url=https://tracker.example/media/filter?api_token=secret-value state=failed", source)
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	logPath, err := LogPath(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	recent := logger.Recent(1)
	if len(recent) != 1 {
		t.Fatalf("recent entries = %#v", recent)
	}
	messages := []string{stdout.String(), string(contents), recent[0].Message}
	select {
	case entry := <-entries:
		messages = append(messages, entry.Message)
	default:
		t.Fatal("missing subscriber entry")
	}
	for _, message := range messages {
		if strings.Contains(message, "secret-value") {
			t.Fatalf("secret was not redacted: %q", message)
		}
		for _, want := range []string{source, "api_token=[REDACTED]", "state=failed"} {
			if !strings.Contains(message, want) {
				t.Fatalf("missing %q in %q", want, message)
			}
		}
	}
}

func TestSanitizeMessagePreservesDiagnosticPaths(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`source=/media/releases/Example's Release.2026-GRP: state=failed`,
		`source=C:\Users\Tester\Example's Release.mkv tracker=ABC`,
		`source=\\server\share\Example Release.mkv state=ready`,
		`cache=/home/tester/.upbrr/cache/banned/file.json tracker=ABC`,
		`source="/Volumes/Media/Example Release.mkv" state=ready`,
		`source=C:/media/Example.mkv state=ready`,
		"image=https://img.example.com/tmp/poster.jpg tracker=ABC",
		"image=https://img.example.com/home/user/poster.jpg tracker=ABC",
		"image=https://img.example.com/Users/tester/poster.jpg tracker=ABC",
		"image=https://img.example.com/media/poster.jpg source=/media/releases/Example.mkv",
		"web: serving web UI on 127.0.0.1:7480 (browser URL http://127.0.0.1:7480/app)",
		fmt.Sprintf("save_path=%s source=%s", filepath.Join(t.TempDir(), "Example.mkv"), filepath.Join(t.TempDir(), "Source.mkv")),
	} {
		if got := SanitizeMessage(input); got != input {
			t.Fatalf("diagnostic changed: got %q, want %q", got, input)
		}
	}
}

func TestSanitizeMessageRedactsURLUserinfo(t *testing.T) {
	t.Parallel()

	got := SanitizeMessage("client=https://policy-user:policy-password@host.example/path")
	if strings.Contains(got, "policy-user") || strings.Contains(got, "policy-password") {
		t.Fatal("expected URL userinfo credentials redacted")
	}
	if !strings.Contains(got, "https://[REDACTED]@host.example/path") {
		t.Fatal("expected URL host and path preserved")
	}
}

func TestSanitizeMessagePreservesFieldsAfterTerminalRedactedQuery(t *testing.T) {
	t.Parallel()

	input := "tracker=https://tracker.example/announce?passkey=secret state=ready count=4"
	first := SanitizeMessage(input)
	second := SanitizeMessage(first)
	if strings.Contains(first, "secret") || strings.Contains(second, "secret") {
		t.Fatal("expected tracker passkey redacted")
	}
	if first != second {
		t.Fatalf("sanitization is not idempotent:\nfirst:  %q\nsecond: %q", first, second)
	}
	if !strings.Contains(second, "passkey=[REDACTED] state=ready count=4") {
		t.Fatalf("structured suffix was not preserved: %q", second)
	}
}
