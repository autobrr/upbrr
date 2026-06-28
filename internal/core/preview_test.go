// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildMetadataPreviewMapsExternalData(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath:  "/media/example.mkv",
		ReleaseName: "Example.Release.2026",
		ExternalIDs: api.ExternalIDs{
			TMDBID:     123,
			SourceTMDB: "tracker",
		},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{
				TMDBID:   123,
				Title:    "Example",
				Year:     2026,
				Overview: "Overview text",
				Poster:   "https://img.example/poster.jpg",
				Backdrop: "https://img.example/backdrop.jpg",
			},
		},
		TrackerData: []api.TrackerMetadata{
			{Tracker: "AITHER", TMDBID: 123},
		},
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"NBL": {{Rule: "require_tv_only", Reason: "category movie is not tv"}},
		},
	}

	preview := buildMetadataPreview(meta, config.Config{})

	if preview.SourcePath != meta.SourcePath {
		t.Fatalf("expected source path %q, got %q", meta.SourcePath, preview.SourcePath)
	}
	if preview.ReleaseName != meta.ReleaseName {
		t.Fatalf("expected release name %q, got %q", meta.ReleaseName, preview.ReleaseName)
	}
	if preview.TrackerName != "AITHER" {
		t.Fatalf("expected tracker name %q, got %q", "AITHER", preview.TrackerName)
	}
	if len(preview.ExternalPreview) != 1 {
		t.Fatalf("expected 1 external preview, got %d", len(preview.ExternalPreview))
	}

	previewItem := preview.ExternalPreview[0]
	if previewItem.Provider != "tmdb" {
		t.Fatalf("expected provider tmdb, got %q", previewItem.Provider)
	}
	if previewItem.ID != 123 {
		t.Fatalf("expected TMDB ID 123, got %d", previewItem.ID)
	}
	if previewItem.Title != "Example" {
		t.Fatalf("expected title %q, got %q", "Example", previewItem.Title)
	}
	if previewItem.Year != 2026 {
		t.Fatalf("expected year 2026, got %d", previewItem.Year)
	}
	if previewItem.PosterURL == "" || previewItem.BackdropURL == "" {
		t.Fatalf("expected poster and backdrop URLs to be populated")
	}
	if got := preview.TrackerRuleFailures["NBL"]; len(got) != 1 || got[0].Rule != "require_tv_only" {
		t.Fatalf("expected tracker rule failures in preview, got %#v", preview.TrackerRuleFailures)
	}
	preview.TrackerRuleFailures["NBL"][0].Rule = "mutated"
	if meta.TrackerRuleFailures["NBL"][0].Rule != "require_tv_only" {
		t.Fatalf("preview rule failures must not alias prepared metadata, got %#v", meta.TrackerRuleFailures)
	}
}

func TestBuildMetadataPreviewMapsTVmazeRichData(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath:  "/media/example-tv.mkv",
		ReleaseName: "Example.Show.S01E01.2160p.WEB-DL",
		ExternalIDs: api.ExternalIDs{
			TVmazeID:     55,
			SourceTVmaze: "tvmaze",
		},
		ExternalMetadata: api.ExternalMetadata{
			TVmaze: &api.TVmazeMetadata{
				TVmazeID:  55,
				Name:      "Example Show",
				Premiered: "2021-04-03",
				Summary:   "Series overview",
				Poster:    "https://img.example/tvmaze-poster.jpg",
				Backdrop:  "https://img.example/tvmaze-backdrop.jpg",
				Type:      "Scripted",
				Language:  "English",
				Genres:    "Drama, Mystery",
				Runtime:   52,
				Rating:    8.6,
				Weight:    88,
				Country:   "United States",
				IMDBID:    1234567,
				TVDBID:    9988,
			},
		},
	}

	preview := buildMetadataPreview(meta, config.Config{})
	if len(preview.ExternalPreview) != 1 {
		t.Fatalf("expected 1 external preview, got %d", len(preview.ExternalPreview))
	}
	item := preview.ExternalPreview[0]
	if item.Provider != "tvmaze" || item.ID != 55 {
		t.Fatalf("unexpected tvmaze preview identity: %#v", item)
	}
	if item.Overview != "Series overview" || item.PosterURL == "" || item.BackdropURL == "" {
		t.Fatalf("expected overview and image urls, got %#v", item)
	}
	if item.Rating != 8.6 || item.RatingCount != 88 || item.Country != "United States" {
		t.Fatalf("expected rating/country populated, got %#v", item)
	}
}
