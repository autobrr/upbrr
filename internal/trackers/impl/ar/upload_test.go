// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveARNameAddsNoGRP(t *testing.T) {
	t.Parallel()

	got := resolveARName(api.PreparedMetadata{
		SourcePath: "C:/data/My Movie (2024).mkv",
		Release:    api.ReleaseInfo{Title: "My Movie", Year: 2024},
	})
	if got != "My.Movie.2024-NoGRP" {
		t.Fatalf("unexpected AR name %q", got)
	}
}

func TestResolveARNameUsesSceneName(t *testing.T) {
	t.Parallel()

	got := resolveARName(api.PreparedMetadata{
		Scene:     true,
		SceneName: "Scene.Release-GRP",
		Tag:       "-GRP",
	})
	if got != "Scene.Release-GRP" {
		t.Fatalf("expected scene name, got %q", got)
	}
}

type captureLogger struct {
	warnings []string
}

func (l *captureLogger) Tracef(string, ...any) {}
func (l *captureLogger) Debugf(string, ...any) {}
func (l *captureLogger) Infof(string, ...any)  {}
func (l *captureLogger) Errorf(string, ...any) {}
func (l *captureLogger) Warnf(format string, args ...any) {
	l.warnings = append(l.warnings, strings.TrimSpace(format))
}

func TestPersistLoginCookiesAllowsPlaintextFallbackWhenAuthHelperUnavailable(t *testing.T) {
	t.Parallel()

	logger := &captureLogger{}
	err := persistLoginCookies(context.Background(), t.TempDir()+"/upbrr.db", logger, nil)
	if err != nil {
		t.Fatalf("expected plaintext fallback, got %v", err)
	}
	if len(logger.warnings) != 1 {
		t.Fatalf("expected one warning, got %d", len(logger.warnings))
	}
	if !strings.Contains(logger.warnings[0], "plaintext fallback") {
		t.Fatalf("expected plaintext fallback warning, got %q", logger.warnings[0])
	}
}
