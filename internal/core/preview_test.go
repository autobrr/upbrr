// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"strings"
	"testing"
	"time"

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

func TestBuildMetadataPreviewMapsAniListRichData(t *testing.T) {
	updatedAt := time.Date(2026, 7, 8, 1, 2, 3, 0, time.UTC)
	meta := api.PreparedMetadata{
		SourcePath:  "/media/example-anime.mkv",
		ReleaseName: "Example.Anime.S01.1080p.WEB-DL",
		ExternalIDs: api.ExternalIDs{
			MALID:     5114,
			SourceMAL: "tmdb",
			UpdatedAt: updatedAt,
		},
		ExternalMetadata: api.ExternalMetadata{
			AniList: &api.AniListMetadata{
				AniListID:       1,
				MALID:           5114,
				SiteURL:         "https://anilist.co/anime/1",
				TitleEnglish:    "Example Anime",
				TitleRomaji:     "Example Anime Romaji",
				Description:     "Anime overview",
				Format:          "TV",
				Status:          "FINISHED",
				StartDate:       "2026-04-01",
				SeasonYear:      2026,
				Episodes:        12,
				Duration:        24,
				CoverLarge:      "https://img.example/anilist-cover.jpg",
				BannerImage:     "https://img.example/anilist-banner.jpg",
				Genres:          []string{"Action", "Drama"},
				AverageScore:    82,
				Popularity:      12345,
				CountryOfOrigin: "JP",
				ExternalLinks: []api.AniListExternalLink{
					{Site: "Official", URL: "https://example.invalid/anime", Language: "ja"},
				},
			},
			UpdatedAt: updatedAt,
		},
	}

	preview := buildMetadataPreview(meta, config.Config{})
	if len(preview.ExternalPreview) != 1 {
		t.Fatalf("expected 1 external preview, got %d", len(preview.ExternalPreview))
	}
	item := preview.ExternalPreview[0]
	if item.Provider != "mal" || item.ID != 5114 {
		t.Fatalf("unexpected anilist preview identity: %#v", item)
	}
	if item.Source != "tmdb" {
		t.Fatalf("expected MAL preview source provenance tmdb, got %q", item.Source)
	}
	if len(preview.ExternalIDInfo) != 1 || preview.ExternalIDInfo[0].Provider != "mal" || preview.ExternalIDInfo[0].Source != "tmdb" {
		t.Fatalf("expected MAL external ID provenance in preview info, got %#v", preview.ExternalIDInfo)
	}
	if !preview.ExternalIDs.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("expected fresh external ID timestamp %s, got %s", updatedAt, preview.ExternalIDs.UpdatedAt)
	}
	if item.Title != "Example Anime" || item.Year != 2026 || item.Overview != "Anime overview" {
		t.Fatalf("expected anilist title/year/overview, got %#v", item)
	}
	if item.PosterURL == "" || item.BackdropURL == "" || item.AniList == nil || item.AniList.AniListID != 1 {
		t.Fatalf("expected anilist image urls and nested metadata, got %#v", item)
	}
	if item.AniList.Description != "Anime overview" || item.AniList.Status != "FINISHED" {
		t.Fatalf("expected anilist message/status semantics, got description=%q status=%q", item.AniList.Description, item.AniList.Status)
	}
	if item.Genres != "Action, Drama" || item.Rating != 8.2 || item.RatingCount != 12345 {
		t.Fatalf("expected anilist genres/rating, got %#v", item)
	}
	if item.OriginalLanguage != "ja" {
		t.Fatalf("expected anilist preview original language from link language, got %q", item.OriginalLanguage)
	}
	if item.AniList.CountryOfOrigin != "JP" {
		t.Fatalf("expected country of origin preserved only on nested anilist metadata, got %q", item.AniList.CountryOfOrigin)
	}
}

func TestBuildMetadataPreviewWarnsForMixedSeasonPackGroupTags(t *testing.T) {
	t.Parallel()

	preview := buildMetadataPreview(api.PreparedMetadata{
		SourcePath:     "Example.Show.S01.1080p.WEB-DL.x264-GRP",
		ReleaseName:    "Example.Show.S01.1080p.WEB-DL.x264-GRP",
		LookupWarnings: []string{"existing warning"},
		TVPack:         true,
		FileList: []string{
			"Example.Show.S01E01.1080p.WEB-DL.x264-GRP.mkv",
			"Example.Show.S01E02.1080p.WEB-DL.x264-ALT.mkv",
		},
	}, config.Config{})

	if len(preview.Warnings) != 2 {
		t.Fatalf("expected existing and mixed-group warnings, got %#v", preview.Warnings)
	}
	if preview.Warnings[0] != "existing warning" {
		t.Fatalf("expected existing warning preserved first, got %#v", preview.Warnings)
	}
	if !strings.Contains(preview.Warnings[1], "mixed group tags (ALT, GRP)") {
		t.Fatalf("expected mixed group warning, got %#v", preview.Warnings)
	}
}
