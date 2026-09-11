// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedIdentityDependenciesSurviveRemovalAndRestart(t *testing.T) {
	ctx := t.Context()
	base := t.TempDir()
	sourcePath := filepath.Join(base, "Example.Series.S01E01.1080p-GRP.mkv")
	if err := os.WriteFile(sourcePath, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(base, "metadata.sqlite")
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 401001, Category: "TV"},
		metadata:      tmdb.MetadataResult{
TMDBType: "TV",
 Title: "Different Series",
 TVDBID: 401004,
 ExternalTVDBID: 401004,
},
	}
	imdbClient := &stubIMDB{
		searchResult: imdb.SearchResult{IMDbID: 401002},
		info:         imdb.Info{
IMDbID: "tt401002",
 Title: "Example Series",
 Type: "tvSeries",
},
	}
	tvdbClient := &stubTVDB{
		id:             401003,
		name:           "Example Series",
		seriesMetadata: tvdb.SeriesMetadata{TVDBID: 401004, Name: "Different Series"},
	}
	openModule := func() (*preparedrelease.Module, *db.SQLiteRepository) {
		t.Helper()
		repo, err := db.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Migrate(); err != nil {
			t.Fatal(err)
		}
		service := NewService(repo,
			WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
			WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}),
			WithTMDBClient(tmdbClient), WithIMDBClient(imdbClient), WithTVDBClient(tvdbClient), WithTVmazeClient(&stubTVmaze{}))
		collector, err := preparedrelease.NewEvidenceCollector(service)
		if err != nil {
			t.Fatal(err)
		}
		resolver, err := externalidentity.NewWithCandidateSource(repo, collector)
		if err != nil {
			t.Fatal(err)
		}
		module, err := preparedrelease.New(repo, resolver, collector)
		if err != nil {
			t.Fatal(err)
		}
		return module, repo
	}
	module, repo := openModule()
	t.Cleanup(func() { _ = repo.Close() })
	initial, err := module.Prepare(ctx, api.PrepareInput{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("initial preparation: %v", err)
	}
	if initial.Release.Identity.TVDBID != 401004 || initial.Release.Identity.Dependencies.TVDB.TMDBID != 401001 {
		t.Fatalf("initial TVDB dependency = %#v", initial.Release.Identity)
	}
	tvdbClient.seriesMetadata = tvdb.SeriesMetadata{TVDBID: 401003, Name: "Example Series"}
	tmdbCalls := tmdbClient.findCalls + tmdbClient.searchCalls + tmdbClient.metaCalls
	removed, err := module.Prepare(ctx, api.PrepareInput{
		SourcePath:   sourcePath,
		Instructions: api.ReleaseFactInstructions{Identity: api.ExternalIDOverrides{TMDBID: new(0)}},
	})
	if err != nil {
		t.Fatalf("preparation after removal: %v", err)
	}
	if removed.Release.Identity.TMDBID != 0 || removed.Release.Identity.IMDBID != 401002 || removed.Release.Identity.TVDBID != 401003 {
		t.Fatalf("identity after removal = %#v", removed.Release.Identity)
	}
	if removed.Release.Identity.Dependencies.TVDB != (api.IdentityDependency{ID: 401003, IMDBID: 401002}) {
		t.Fatalf("refreshed TVDB dependency = %#v", removed.Release.Identity.Dependencies.TVDB)
	}
	if removed.Release.ProviderMetadata.TVDB == nil || removed.Release.ProviderMetadata.TVDB.Name != "Example Series" {
		t.Fatalf("refreshed TVDB metadata = %#v", removed.Release.ProviderMetadata.TVDB)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	module, repo = openModule()
	restarted, err := module.Prepare(ctx, api.PrepareInput{SourcePath: sourcePath, Force: true})
	if err != nil {
		t.Fatalf("preparation after restart: %v", err)
	}
	if restarted.Release.Identity.TMDBID != 0 || restarted.Release.Identity.TVDBID != 401003 ||
		restarted.Release.Identity.Dependencies.TVDB != removed.Release.Identity.Dependencies.TVDB {
		t.Fatalf("identity after restart = %#v", restarted.Release.Identity)
	}
	if imdbClient.searchCalls != 1 || tmdbCalls != tmdbClient.findCalls+tmdbClient.searchCalls+tmdbClient.metaCalls {
		t.Fatalf("unexpected repeat lookup: IMDb=%d TMDB=%d", imdbClient.searchCalls, tmdbClient.findCalls+tmdbClient.searchCalls+tmdbClient.metaCalls)
	}
}
