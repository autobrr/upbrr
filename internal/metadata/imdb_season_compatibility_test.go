// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedIMDbStringSeasonRecollectsV35AfterRestart(t *testing.T) {
	t.Parallel()
	for _, source := range []struct {
		name, filename string
		season         *string
	}{
		{name: "source special", filename: "Example.Series.S00E01.1080p.WEB-DL.H264-GRP.mkv"},
		{
			name:     "corrected positive source",
			filename: "Example.Series.S01E01.1080p.WEB-DL.H264-GRP.mkv",
			season:   new("S00"),
		},
	} {
		t.Run(source.name, func(t *testing.T) {
			fixture := newCorrectionEvidenceFixtureForSource(t, source.filename)
			fixture.input.Instructions.ReleaseName.Season = source.season
			fixture.input.Instructions.Identity.TMDBID = new(0)
			fixture.input.Instructions.Identity.IMDBID = new(555001)
			httpClient, calls := evidenceHTTPClient(func(request *http.Request) (int, string) {
				if request.URL.Host != "caching.graphql.imdb.com" {
					t.Errorf("unexpected HTTP destination: %s", request.URL.Host)
				}
				return http.StatusOK, `{"data":{"title":{"id":"tt555001","titleText":{"text":"Example Series"},"titleType":{"id":"tvSeries"},"releaseYear":{"year":2026},"episodes":{"episodes":{"edges":[{"node":{"id":"tt555002","titleText":{"text":"The Special Signal"},"releaseYear":{"year":2026},"series":{"displayableEpisodeNumber":{"displayableSeason":{"season":"0"},"episodeNumber":{"text":"1"}}}}}]}}}}}`
			})
			openModule := func() (*preparedrelease.Module, *db.SQLiteRepository) {
				t.Helper()
				repo, err := db.OpenContext(t.Context(), fixture.dbPath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = repo.Close() })
				if err := repo.MigrateContext(t.Context()); err != nil {
					t.Fatal(err)
				}
				service := NewService(repo,
					WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: fixture.dbPath}}),
					WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}),
					WithTMDBClient(&stubTMDB{}), WithIMDBClient(imdb.NewClient(httpClient, api.NopLogger{})),
					WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{}))
				collector, err := preparedrelease.NewEvidenceCollector(service)
				if err != nil {
					t.Fatal(err)
				}
				identity, err := externalidentity.NewWithCandidateSource(repo, collector)
				if err != nil {
					t.Fatal(err)
				}
				module, err := preparedrelease.New(repo, identity, collector)
				if err != nil {
					t.Fatal(err)
				}
				return module, repo
			}
			assertSpecial := func(release api.PreparedRelease) {
				t.Helper()
				metadata := release.ProviderMetadata.IMDB
				if metadata == nil || len(metadata.Episodes) != 1 || !metadata.Episodes[0].SeasonKnown || metadata.Episodes[0].Season != 0 {
					t.Fatalf("IMDb string season zero did not survive snapshot: %+v", metadata)
				}
				if release.Episode.Season != 0 || release.Episode.SeasonLabel != "S00" || release.Episode.Episode != 1 ||
					release.Episode.Title != "The Special Signal" || release.Episode.Year != 2026 || !strings.Contains(release.Naming.ReleaseName, "S00E01") {
					t.Fatalf("IMDb special did not become canonical facts: episode=%+v name=%q", release.Episode, release.Naming.ReleaseName)
				}
			}
			module, repo := openModule()
			initial := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
			assertSpecial(initial.Release)
			old, err := repo.LoadPreparedRelease(t.Context(), fixture.input.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			assertSpecial(old)
			if calls("") != 1 {
				t.Fatalf("initial IMDb HTTP requests = %d, want 1", calls(""))
			}

			// Published v35 generations lost string-valued season presence and therefore
			// could not attach IMDb special-episode facts, even with valid raw evidence.
			old.Compatibility.ContractVersion = "prepared-release-v35"
			old.ProviderMetadata.IMDB.Episodes[0].SeasonKnown = false
			old.Episode.Title = ""
			old.Episode.Year = 0
			if err := repo.CommitPreparedRelease(t.Context(), old); err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			module, repo = openModule()
			stale, err := repo.LoadPreparedRelease(t.Context(), fixture.input.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if stale.Compatibility.ContractVersion != "prepared-release-v35" || stale.ProviderMetadata.IMDB == nil ||
				len(stale.ProviderMetadata.IMDB.Episodes) != 1 || stale.ProviderMetadata.IMDB.Episodes[0].SeasonKnown || stale.Episode.Title != "" {
				t.Fatalf("stale v35 facts were not persisted: %+v", stale)
			}
			input := api.PrepareInput{SourcePath: fixture.input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
			recollected := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
			if recollected.Release.Generation != old.Generation+1 {
				t.Fatalf("v35 generation was reused: old=%d current=%d", old.Generation, recollected.Release.Generation)
			}
			assertSpecial(recollected.Release)
			persisted, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			assertSpecial(persisted)
			if persisted.Compatibility.ContractVersion != preparedrelease.ContractVersion || persisted.Generation != recollected.Release.Generation {
				t.Fatalf("recollected generation was not persisted: %+v", persisted.Compatibility)
			}
			reused := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
			if !reflect.DeepEqual(reused.Release, recollected.Release) {
				t.Fatal("current prepared generation was not reused unchanged")
			}
			if calls("") != 1 {
				t.Fatalf("recollection did not reuse persisted IMDb evidence: requests=%d", calls(""))
			}
		})
	}
}

func TestResolveIMDbSpecialSeasonReplayPreservesOtherEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		change        func(*preparationstate.State)
		emptyResponse bool
		wantCalls     int
	}{
		{name: "unproven special", wantCalls: 1},
		{
			name:          "empty replay response",
			emptyResponse: true,
			wantCalls:     1,
		},
		{name: "proven special", change: func(meta *preparationstate.State) { meta.ProviderMetadata.IMDB.Episodes[0].SeasonKnown = true }},
		{name: "positive season", change: func(meta *preparationstate.State) { meta.SeasonInt, meta.SeasonStr = 1, "S01" }},
		{name: "unknown season", change: func(meta *preparationstate.State) { meta.SeasonStr = "" }},
		{name: "wrong cached season", change: func(meta *preparationstate.State) { meta.ProviderMetadata.IMDB.Episodes[0].Season = 1 }},
		{name: "wrong cached episode", change: func(meta *preparationstate.State) { meta.ProviderMetadata.IMDB.Episodes[0].EpisodeText = "2" }},
		{name: "no cached episode", change: func(meta *preparationstate.State) { meta.ProviderMetadata.IMDB.Episodes = nil }},
		{name: "special pack", change: func(meta *preparationstate.State) { meta.TVPack = true }},
		{name: "unknown episode", change: func(meta *preparationstate.State) { meta.EpisodeInt = 0 }},
		{
			name:      "positive source corrected to special",
			wantCalls: 1,
			change: func(meta *preparationstate.State) {
				meta.SeasonInt, meta.SeasonStr, meta.EpisodeInt = 1, "S01", 2
				meta.ReleaseNameOverrides = api.ReleaseNameOverrides{Season: new("00"), Episode: new("01")}
			},
		},
		{
			name:      "unknown source corrected to special",
			wantCalls: 1,
			change: func(meta *preparationstate.State) {
				meta.SeasonStr = ""
				meta.ReleaseNameOverrides.Season = new("S00")
			},
		},
		{name: "special corrected to positive season", change: func(meta *preparationstate.State) { meta.ReleaseNameOverrides.Season = new("S01") }},
		{name: "cleared season", change: func(meta *preparationstate.State) { meta.ReleaseNameOverrides.Season = new("") }},
		{
			name:      "corrected episode matches cache",
			wantCalls: 1,
			change: func(meta *preparationstate.State) {
				meta.ReleaseNameOverrides.Episode = new("E02")
				meta.ProviderMetadata.IMDB.Episodes[0].EpisodeText = "2"
			},
		},
		{name: "corrected episode excludes cache", change: func(meta *preparationstate.State) { meta.ReleaseNameOverrides.Episode = new("E02") }},
		{name: "cleared episode", change: func(meta *preparationstate.State) { meta.ReleaseNameOverrides.Episode = new("") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := retainedIMDbSpecialState(filepath.Join(t.TempDir(), "Example.Series.S00E01.mkv"))
			if test.change != nil {
				test.change(&meta)
			}
			imdbClient := &stubIMDB{}
			if !test.emptyResponse {
				imdbClient.info = imdb.Info{
					IMDbID:   "tt0555001",
					Title:    "Example Series",
					Type:     "tvSeries",
					Episodes: []imdb.Episode{{Season: 0, EpisodeText: "1"}},
				}
			}
			tmdbClient := &stubTMDB{}
			result, err := NewService(&fakeRepo{}, WithIMDBClient(imdbClient), WithTMDBClient(tmdbClient),
				WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{})).resolveExternalIdentity(t.Context(), meta)
			if err != nil {
				t.Fatal(err)
			}
			if imdbClient.infoCalls != test.wantCalls {
				t.Fatalf("IMDb replay calls = %d, want %d", imdbClient.infoCalls, test.wantCalls)
			}
			if tmdbClient.metaCalls+tmdbClient.searchCalls+tmdbClient.findCalls != 0 ||
				!reflect.DeepEqual(result.ProviderMetadata.TMDB, meta.ProviderMetadata.TMDB) {
				t.Fatal("IMDb season replay changed retained TMDB metadata")
			}
		})
	}
}

func TestIMDbSpecialSeasonReplayRetainsEmptyAndFailedEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status int
	}{
		{name: "empty title", status: http.StatusOK},
		{name: "failed query", status: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, source := openProviderEvidenceStore(t)
			httpClient, calls := evidenceHTTPClient(func(*http.Request) (int, string) {
				return test.status, `{"data":{"title":null}}`
			})
			client := imdb.NewClient(httpClient, api.NopLogger{})
			ctx, scope := evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessLoad, nil)
			_, err := client.GetInfo(ctx, "tt0555001", "", false)
			if (err != nil) != (test.status != http.StatusOK) || scope.Err() != nil || calls("") != 1 {
				t.Fatalf("seed IMDb evidence: error=%v persistence=%v calls=%d", err, scope.Err(), calls(""))
			}
			ctx, scope = evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessReuse, nil)
			result, err := NewService(repo, WithIMDBClient(client), WithTMDBClient(&stubTMDB{}),
				WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{})).resolveExternalIdentity(ctx, retainedIMDbSpecialState(source))
			if err != nil || scope.Err() != nil {
				t.Fatalf("replay IMDb evidence: error=%v persistence=%v", err, scope.Err())
			}
			if calls("") != 1 || result.EpisodeTitle != "" {
				t.Fatalf("empty/failed IMDb evidence was retried or enriched: calls=%d title=%q", calls(""), result.EpisodeTitle)
			}
		})
	}
}

func retainedIMDbSpecialState(source string) preparationstate.State {
	return preparationstate.State{
		SourcePath:      source,
		StoredDataFresh: true,
		SeasonStr:       "S00",
		EpisodeInt:      1,
		Release:         api.ReleaseInfo{Category: "TV", Title: "Example Series"},
		MediaInfoTMDBID: 555003,
		MediaInfoIMDBID: 555001,
		ExternalIDOverrides: api.ExternalIDOverrides{
			TVDBID:   new(0),
			TVmazeID: new(0),
			MALID:    new(0),
		},
		Identity: api.ExternalIdentity{
			SourcePath: source,
			IMDBID:     555001,
			TMDBID:     555003,
			Category:   api.CanonicalCategoryTV,
			Provenance: api.IdentityProvenanceSet{IMDB: api.IdentityProvenanceMediaInfo, TMDB: api.IdentityProvenanceMediaInfo},
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: source,
			IMDB: &api.IMDBMetadata{
				IMDBID:   555001,
				Title:    "Example Series",
				Type:     "tvSeries",
				Episodes: []api.IMDBEpisode{{Season: 0, EpisodeText: "1"}},
			},
			TMDB: &api.TMDBMetadata{
				TMDBID:              555003,
				Category:            "TV",
				Title:               "Example Series",
				LogoLookupAttempted: true,
			},
		},
	}
}
