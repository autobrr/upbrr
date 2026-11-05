// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTVDBSeriesLinksResolveSiblingsInitiallyAndAfterNameFallback(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial", true: "late"}[late], func(t *testing.T) {
			links := api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusComplete, Links: []api.TVDBRemoteLink{{
				Provider: api.IdentityProviderTMDB,
				ID:       101,
				State:    "valid",
			}, {
				Provider: api.IdentityProviderIMDB,
				ID:       202,
				State:    "valid",
			}}}
			tvdbClient := &stubTVDB{
				searchID: 303,
				searchResults: []tvdb.SeriesSearchResult{{
					TVDBID: 303,
					Name:   "Example Series",
					Year:   "2025",
				}},
				seriesMetadata: tvdb.SeriesMetadata{
					TVDBID:      303,
					Name:        "Example Series",
					RemoteLinks: links,
				},
			}
			tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
				Title:    "Example Series",
				TMDBType: "TV",
				IMDbID:   202,
				TVDBID:   303,
			}}
			imdbClient := &stubIMDB{info: imdb.Info{
				IMDbID: "tt0000202",
				Title:  "Example Series",
				Type:   "tvSeries",
			}}
			service := newTestService(t, &fakeRepo{}, WithTMDBClient(tmdbClient), WithIMDBClient(imdbClient), WithTVDBClient(tvdbClient), WithTVmazeClient(&stubTVmaze{}))
			input := preparationstate.State{
				SourcePath: filepath.Join(t.TempDir(), "Example.Series.2025.S01E01.mkv"),
				Release: api.ReleaseInfo{
					Title:    "Example Series",
					Year:     2025,
					Category: "TV",
				},
				MediaInfoCategory: "TV",
			}
			if !late {
				input.MediaInfoTVDBID = 303
			}
			result, err := service.resolveExternalIdentity(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if result.Identity.TMDBID != 101 || result.Identity.IMDBID != 202 || result.Identity.TVDBID != 303 || result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.IMDB == nil {
				t.Fatalf("incomplete linked generation: %+v %+v", result.Identity, result.ProviderMetadata)
			}
			if result.Identity.Dependencies.TMDB.TVDBID != 303 || result.Identity.Dependencies.IMDB.TVDBID != 303 || tmdbClient.metaCalls != 1 || imdbClient.infoCalls != 1 || tvdbClient.seriesMetadataCalls != 1 {
				t.Fatalf("link dependencies/calls: %+v tmdb=%d imdb=%d tvdb=%d", result.Identity.Dependencies, tmdbClient.metaCalls, imdbClient.infoCalls, tvdbClient.seriesMetadataCalls)
			}
			if !late && (tmdbClient.searchCalls != 0 || imdbClient.searchCalls != 0) {
				t.Fatal("TVDB series links lost precedence to title search")
			}
		})
	}
}

func TestTVDBLinksPreserveManualAuthorityClearsAndConflicts(t *testing.T) {
	for _, test := range []struct {
		name      string
		overrides api.ExternalIDOverrides
		current   int
		state     string
		wantTMDB  int
	}{
		{"clear", api.ExternalIDOverrides{IMDBID: new(0)}, 0, "valid", 101},
		{"manual conflict", api.ExternalIDOverrides{IMDBID: new(999)}, 999, "valid", 0},
		{"automatic conflict", api.ExternalIDOverrides{}, 999, "valid", 0},
		{"malformed", api.ExternalIDOverrides{}, 0, "malformed", 101},
		{"conflicting links", api.ExternalIDOverrides{}, 0, "conflicting", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := preparationstate.State{MediaInfoCategory: "TV"}
			ids := api.ExternalIdentity{
				TVDBID:   303,
				IMDBID:   test.current,
				Category: api.CanonicalCategoryTV,
			}
			links := api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusComplete, Links: []api.TVDBRemoteLink{{
				Provider: api.IdentityProviderTMDB,
				ID:       101,
				State:    "valid",
			}, {
				Provider: api.IdentityProviderIMDB,
				ID:       202,
				State:    test.state,
			}}}
			if test.state == "conflicting" {
				links.Status = api.MetadataEvidenceStatusContradictory
			}
			applyTVDBSeriesLinks(&meta, &ids, api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{TVDBID: 303, RemoteLinks: links}}, test.overrides)
			if ids.IMDBID != test.current || ids.TMDBID != test.wantTMDB {
				t.Fatalf("manual/current identity changed: %+v", ids)
			}
		})
	}
	meta := preparationstate.State{MediaInfoCategory: "TV"}
	ids := api.ExternalIdentity{
		TVDBID:   303,
		TMDBID:   101,
		Category: api.CanonicalCategoryTV,
	}
	applyTVDBSeriesLinks(&meta, &ids, api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{TVDBID: 303, RemoteLinks: api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusComplete, Links: []api.TVDBRemoteLink{{
		Provider: api.IdentityProviderMAL,
		ID:       404,
		State:    "valid",
	}}}}}, api.ExternalIDOverrides{TMDBID: new(101)})
	if ids.MALID != 0 {
		t.Fatal("TVDB preempted manual TMDB's MAL authority")
	}
}

func TestPreparedTVDBLinksReuseAcrossCorrectionsAndRestart(t *testing.T) {
	fixture := newCorrectionEvidenceFixture(t)
	fixture.tvdbRemoteIDs = `[{"id":"555001","sourceName":"TheMovieDB.com","type":12},{"id":"tt0555002","sourceName":"IMDB","type":2}]`
	fixture.tmdbExternalIDs = `{"imdb_id":"tt0555002","tvdb_id":555003}`
	fixture.tvdb = tvdb.NewClient(fixture.http, nil, "synthetic-key", "")
	fixture.imdb = &stubIMDB{info: imdb.Info{
		IMDbID: "tt0555002",
		Title:  "Example Series",
		Type:   "tvSeries",
	}}
	fixture.input.Instructions.Identity = api.ExternalIDOverrides{
		TVDBID:   new(555003),
		TVmazeID: new(0),
		MALID:    new(0),
	}
	module, repo := fixture.open(t)
	initial := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if initial.Release.Identity.TMDBID != 555001 || initial.Release.Identity.IMDBID != 555002 || initial.Release.ProviderMetadata.TMDB == nil || initial.Release.ProviderMetadata.IMDB == nil {
		t.Fatalf("linked preparation incomplete: %+v", initial.Release.Identity)
	}
	before := fixture.requests()
	input := api.PrepareInput{SourcePath: fixture.input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
	changed := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{NoYear: new(true)}}}})
	if changed.Release.Identity.TMDBID != 555001 || !reflect.DeepEqual(before, fixture.requests()) {
		t.Fatal("presentation correction repeated linked provider work")
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	module, _ = fixture.open(t)
	restarted := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if restarted.Release.Identity.TMDBID != 555001 || restarted.Release.Identity.IMDBID != 555002 || !reflect.DeepEqual(before, fixture.requests()) {
		t.Fatal("restart lost linked identity or repeated provider calls")
	}
	cleared := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{TMDBID: new(0)}}}})
	if cleared.Release.Identity.TMDBID != 0 || cleared.Release.ProviderMetadata.TMDB != nil || cleared.Release.Identity.IMDBID != 555002 {
		t.Fatalf("explicit clear lost authority: %+v", cleared.Release.Identity)
	}
}

func TestLateTVDBReverseLookupFinishesLinkedMetadata(t *testing.T) {
	tvdbClient := &stubTVDB{
		searchID: 303,
		searchResults: []tvdb.SeriesSearchResult{{
			TVDBID: 303,
			Name:   "Example Series",
			Year:   "2025",
		}},
		seriesMetadata: tvdb.SeriesMetadata{
			TVDBID:      303,
			Name:        "Example Series",
			RemoteLinks: api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusComplete},
		},
	}
	tmdbClient := &stubTMDB{findResult: tmdb.FindResult{TMDBID: 101, Category: "TV"}, metadata: tmdb.MetadataResult{
		Title:    "Example Series",
		TMDBType: "TV",
		IMDbID:   202,
		TVDBID:   303,
	}}
	imdbClient := &stubIMDB{info: imdb.Info{
		IMDbID: "tt0000202",
		Title:  "Example Series",
		Type:   "tvSeries",
	}}
	service := newTestService(t, &fakeRepo{}, WithTMDBClient(tmdbClient), WithIMDBClient(imdbClient), WithTVDBClient(tvdbClient), WithTVmazeClient(&stubTVmaze{}))
	result, err := service.resolveExternalIdentity(t.Context(), preparationstate.State{
		SourcePath: filepath.Join(t.TempDir(), "Example.Series.2025.S01E01.mkv"),
		Release: api.ReleaseInfo{
			Title:    "Example Series",
			Year:     2025,
			Category: "TV",
		},
		MediaInfoCategory: "TV",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Identity.TMDBID != 101 || result.Identity.IMDBID != 202 || result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.IMDB == nil || tmdbClient.findCalls != 1 || tmdbClient.metaCalls != 1 || imdbClient.infoCalls != 1 {
		t.Fatalf("late reverse lookup left incomplete metadata: %+v tmdb=%d/%d imdb=%d", result.Identity, tmdbClient.findCalls, tmdbClient.metaCalls, imdbClient.infoCalls)
	}
}

func TestTVDBEpisodeRemoteLinkCannotBecomeSeriesIdentity(t *testing.T) {
	tvdbClient := &stubTVDB{seriesMetadata: tvdb.SeriesMetadata{
		TVDBID: 303,
		Name:   "Example Series",
		RemoteLinks: api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusComplete, Links: []api.TVDBRemoteLink{{
			Provider: api.IdentityProviderIMDB,
			ID:       202,
			State:    "valid",
		}}},
	}}
	imdbClient := &stubIMDB{info: imdb.Info{
		IMDbID: "tt0000202",
		Title:  "Example Episode",
		Type:   "tvEpisode",
	}}
	service := newTestService(t, &fakeRepo{}, WithTMDBClient(&stubTMDB{}), WithIMDBClient(imdbClient), WithTVDBClient(tvdbClient), WithTVmazeClient(&stubTVmaze{}))
	result, err := service.resolveExternalIdentity(t.Context(), preparationstate.State{
		SourcePath:        filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv"),
		Release:           api.ReleaseInfo{Title: "Example Series", Category: "TV"},
		MediaInfoCategory: "TV",
		MediaInfoTVDBID:   303,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Identity.IMDBID != 0 || result.ProviderMetadata.IMDB != nil || result.ProviderMetadata.TVDB.RemoteLinks.Links[0].State != "unsupported" || imdbClient.infoCalls != 1 {
		t.Fatalf("episode link survived as a series identity: %+v calls=%d", result.Identity, imdbClient.infoCalls)
	}
}
