// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/internal/metadata/tvmaze"
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
			applyTVDBSeriesLinks(&meta, &ids, api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{TVDBID: 303, RemoteLinks: links}}, test.overrides, 0)
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
	}}}}}, api.ExternalIDOverrides{TMDBID: new(101)}, 0)
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
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{
		SelectedID: 404,
		TVDBID:     303,
		IMDBID:     202,
		Candidates: []tvmaze.Candidate{{ID: 404, Externals: tvmaze.Externals{TVDB: 303, IMDB: "tt0000202"}}},
	}}
	service := newTestService(t, &fakeRepo{}, WithTMDBClient(&stubTMDB{}), WithIMDBClient(imdbClient), WithTVDBClient(tvdbClient), WithTVmazeClient(tvmazeClient))
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
	if result.Identity.TVmazeID != 404 || result.ProviderMetadata.TVmaze == nil || tvmazeClient.calls != 2 || tvmazeClient.inputs[1].ImdbID != "" || tvmazeClient.inputs[1].TVDBID != "303" {
		t.Fatalf("series metadata was not resolved again after episode rejection: %+v inputs=%+v", result.Identity, tvmazeClient.inputs)
	}
}

func TestTVDBRefreshRecoversFromEarlyLinkFailure(t *testing.T) {
	tvdbClient := &stubTVDB{remoteLinksErr: errors.New("temporary remote-link failure"), seriesMetadata: tvdb.SeriesMetadata{
		TVDBID:      303,
		Name:        "Example Series",
		RemoteLinks: api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusComplete},
	}}
	service := newTestService(t, &fakeRepo{}, WithTMDBClient(&stubTMDB{}), WithIMDBClient(&stubIMDB{}), WithTVDBClient(tvdbClient), WithTVmazeClient(&stubTVmaze{}))
	result, err := service.resolveExternalIdentity(t.Context(), preparationstate.State{
		SourcePath:          filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv"),
		MediaInfoCategory:   "TV",
		ExternalFreshness:   api.ExternalFreshnessRefresh,
		ExternalIDOverrides: api.ExternalIDOverrides{TVDBID: new(303)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderMetadata.TVDB == nil || result.ProviderMetadata.TVDB.Name != "Example Series" {
		t.Fatal("successful TVDB refresh lost metadata")
	}
	for _, warning := range result.LookupWarnings {
		if strings.Contains(warning, "TVDB refresh failed") {
			t.Fatalf("successful refresh still warns: %s", warning)
		}
	}
}

func TestTVDBManualResourcesCannotChooseConflictingSibling(t *testing.T) {
	for _, provider := range []string{"TMDB", "TVmaze"} {
		for _, phase := range []string{"initial", "late", "inferred TVDB"} {
			t.Run(provider+"/"+phase, func(t *testing.T) {
				overrides := api.ExternalIDOverrides{
					TVDBID:   new(303),
					TMDBID:   new(0),
					TVmazeID: new(0),
				}
				wantIMDB := 0
				if phase == "inferred TVDB" {
					overrides.TVDBID = nil
					wantIMDB = 909
				}
				if provider == "TMDB" {
					overrides.TMDBID = new(101)
				} else {
					overrides.TVmazeID = new(404)
				}
				tvdbClient := &stubTVDB{seriesMetadata: tvdb.SeriesMetadata{
					TVDBID: 303,
					Name:   "Example Series",
					RemoteLinks: api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusComplete, Links: []api.TVDBRemoteLink{{
						Provider: api.IdentityProviderIMDB,
						ID:       202,
						State:    "valid",
					}}},
				}}
				if phase == "late" {
					tvdbClient.remoteLinksErr = errors.New("temporary remote-link failure")
				}
				tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
					Title:          "Example Selected Series",
					TMDBType:       "TV",
					IMDbID:         909,
					TVDBID:         303,
					ExternalIMDbID: 909,
					ExternalTVDBID: 303,
					MALID:          505,
				}, anilistMetadata: tmdb.AniListMetadataResult{MALID: 505, TitleRomaji: "Example Selected Series"}}
				tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{
					SelectedID: 404,
					IMDBID:     909,
					TVDBID:     303,
					Candidates: []tvmaze.Candidate{{
						ID:        404,
						Name:      "Example Selected Series",
						Externals: tvmaze.Externals{IMDB: "tt0000909", TVDB: 303},
					}},
				}}
				service := newTestService(t, &fakeRepo{}, WithTMDBClient(tmdbClient), WithIMDBClient(&stubIMDB{}), WithTVDBClient(tvdbClient), WithTVmazeClient(tvmazeClient))
				result, err := service.resolveExternalIdentity(t.Context(), preparationstate.State{
					SourcePath:          filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv"),
					MediaInfoCategory:   "TV",
					ExternalIDOverrides: overrides,
				})
				if err != nil {
					t.Fatal(err)
				}
				if result.Identity.IMDBID != wantIMDB || result.Identity.TVDBID != 303 || result.ProviderMetadata.TVDB == nil {
					t.Fatalf("conflicting links chose a sibling or discarded manual TVDB: %+v", result.Identity)
				}
				if provider == "TMDB" && (result.Identity.TMDBID != 101 || result.ProviderMetadata.TMDB == nil || result.Identity.MALID != 505 || result.ProviderMetadata.AniList == nil) ||
					provider == "TVmaze" && (result.Identity.TVmazeID != 404 || result.ProviderMetadata.TVmaze == nil) {
					t.Fatal("manual resource metadata or TMDB's MAL authority was lost")
				}
			})
		}
	}
}

func TestTVDBEpisodeRejectionReusesAcceptedTMDBSeriesLink(t *testing.T) {
	for _, rawIMDB := range []int{909, 202} {
		t.Run(map[int]string{909: "series replacement", 202: "same rejected episode"}[rawIMDB], func(t *testing.T) {
			tvdbClient := &stubTVDB{seriesMetadata: tvdb.SeriesMetadata{
				TVDBID: 303,
				Name:   "Example Series",
				RemoteLinks: api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusComplete, Links: []api.TVDBRemoteLink{
					{
						Provider: api.IdentityProviderTMDB,
						ID:       101,
						State:    "valid",
					},
					{
						Provider: api.IdentityProviderIMDB,
						ID:       202,
						State:    "valid",
					},
				}},
			}}
			tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
				Title:               "Example Series",
				TMDBType:            "TV",
				IMDbID:              202,
				ExternalIMDbID:      rawIMDB,
				TVDBID:              303,
				ExternalTVDBID:      303,
				LogoLookupAttempted: true,
			}}
			imdbIDs := []string{}
			imdbClient := &stubIMDB{infoFn: func(id string) imdb.Info {
				imdbIDs = append(imdbIDs, id)
				if id == "tt0000202" {
					return imdb.Info{
						IMDbID: id,
						Title:  "Example Episode",
						Type:   "tvEpisode",
					}
				}
				return imdb.Info{
					IMDbID: id,
					Title:  "Example Series",
					Type:   "tvSeries",
				}
			}}
			service := newTestService(t, &fakeRepo{}, WithTMDBClient(tmdbClient), WithIMDBClient(imdbClient), WithTVDBClient(tvdbClient), WithTVmazeClient(&stubTVmaze{}))
			result, err := service.resolveExternalIdentity(t.Context(), preparationstate.State{
				SourcePath:        filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv"),
				MediaInfoCategory: "TV",
				MediaInfoTVDBID:   303,
			})
			if err != nil {
				t.Fatal(err)
			}
			if rawIMDB == 202 {
				if result.Identity.IMDBID != 0 || result.ProviderMetadata.IMDB != nil || tmdbClient.metaCalls != 1 || !reflect.DeepEqual(imdbIDs, []string{"tt0000202"}) {
					t.Fatalf("reintroduced a rejected episode: %+v imdbIDs=%v", result.Identity, imdbIDs)
				}
				return
			}
			if result.Identity.IMDBID != 909 || result.ProviderMetadata.IMDB == nil || result.ProviderMetadata.IMDB.IMDBID != 909 ||
				result.Identity.Dependencies.IMDB.TMDBID != 101 || tmdbClient.metaCalls != 1 || !reflect.DeepEqual(imdbIDs, []string{"tt0000202", "tt0000909"}) {
				t.Fatalf("accepted series link was not reused: %+v tmdbCalls=%d imdbIDs=%v", result.Identity, tmdbClient.metaCalls, imdbIDs)
			}
		})
	}
}
