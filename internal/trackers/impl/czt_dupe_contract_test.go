// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl_test

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCZTServiceIgnoresAPIKeyOnlyConfig(t *testing.T) {
	t.Parallel()
	cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"CZT": {APIKey: "bearer-token"}}}}
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	svc := dupe.NewServiceWithRegistry(cfg, api.NopLogger{}, registry)
	summary, err := svc.Check(context.Background(), api.DuplicateSubject{SourcePath: "source.mkv", Release: api.ReleaseInfo{Title: "Movie"}}, []string{"CZT"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	result := summary.Results[0]
	if result.Status != "skipped" || !result.Skipped || result.Error != "" || result.SkipCode != dupe.NotRunMissingCredentials {
		t.Fatalf("unexpected skipped result: %#v", result)
	}
	if !strings.Contains(result.SkipReason, "missing passkey") {
		t.Fatalf("expected passkey skip, got %q", result.SkipReason)
	}
	if strings.Contains(result.SkipReason, "bearer-token") || strings.Contains(strings.Join(result.Notes, " "), "bearer-token") {
		t.Fatalf("skip result leaked API key: %#v", result)
	}
}
