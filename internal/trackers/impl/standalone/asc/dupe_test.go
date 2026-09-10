// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"net/http"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestASCSearchDoesNotUseSourcePathAsTitle(t *testing.T) {
	t.Parallel()
	meta := api.DuplicateSubject{Anime: true, SourcePath: `C:\private\Example.Release.2026.mkv`}
	if got := resolveASCTitle(meta); got != "" {
		t.Fatalf("source path became a search title: %q", got)
	}
	result := (dupeSearcher{http: &http.Client{}}).Search(t.Context(), meta)
	if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunMissingMetadata {
		t.Fatalf("missing title outcome = %v/%s", result.Disposition(), result.Code())
	}
}

func TestResolveASCTitleHonorsManualTitle(t *testing.T) {
	t.Parallel()

	projection := &api.TrackerReleaseProjection{DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Projected Release"}}
	manual := api.DuplicateSubject{
		Release:    api.ReleaseInfo{Title: "Automatic Release"},
		Projection: projection,
		EffectiveMetadata: api.EffectiveMetadata{
			Title:           "Manual Release",
			TitleProvenance: api.FactProvenanceManual,
		},
	}
	if got := resolveASCTitle(manual); got != "Manual Release" {
		t.Fatalf("manual ASC search title = %q", got)
	}
	manual.EffectiveMetadata = api.EffectiveMetadata{TitleProvenance: api.FactProvenanceManualEmpty}
	if got := resolveASCTitle(manual); got != "" {
		t.Fatalf("manual-empty ASC search title = %q", got)
	}
	if got := resolveASCTitle(api.DuplicateSubject{Projection: projection}); got != "Projected Release" {
		t.Fatalf("automatic ASC search title = %q", got)
	}
}
