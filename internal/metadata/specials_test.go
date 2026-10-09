// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/seasonep"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/internal/metadata/tvmaze"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSpecialProviderSeasonPresence(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"IMDb", "TMDB", "TVDB"} {
		for _, test := range []struct {
			name, season string
			matched      bool
		}{
			{name: "absent season"},
			{name: "null season", season: "null"},
			{
				name:    "explicit zero",
				season:  "0",
				matched: true,
			},
			{name: "positive season", season: "1"},
		} {
			t.Run(provider+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				seasonProperty := ""
				if test.season != "" {
					switch provider {
					case "IMDb":
						seasonProperty = `"season":` + test.season
					case "TMDB":
						seasonProperty = `"season_number":` + test.season + `,`
					case "TVDB":
						seasonProperty = `"seasonNumber":` + test.season + `,`
					}
				}
				httpClient, _ := evidenceHTTPClient(func(request *http.Request) (int, string) {
					payload := `{}`
					switch provider {
					case "IMDb":
						payload = `{"data":{"title":{"id":"tt555001","titleText":{"text":"Example Series"},"titleType":{"id":"tvSeries"},"episodes":{"episodes":{"edges":[{"node":{"id":"tt555002","titleText":{"text":"The Special Signal"},"releaseYear":{"year":2026},"series":{"displayableEpisodeNumber":{"displayableSeason":{` + seasonProperty + `},"episodeNumber":{"text":"1"}}}}}]}}}}}`
					case "TMDB":
						payload = `{` + seasonProperty + `"name":"The Special Signal","overview":"Special overview","episode_number":1,"air_date":"2026-01-01"}`
					case "TVDB":
						switch {
						case strings.HasSuffix(request.URL.Path, "/login"):
							payload = `{"data":{"token":"synthetic-token"}}`
						case strings.Contains(request.URL.Path, "/episodes/default"):
							payload = `{"data":{"episodes":[{` + seasonProperty + `"id":555002,"name":"The Special Signal","overview":"Special overview","number":1,"year":2026,"aired":"2026-01-01"}]}}`
						case strings.Contains(request.URL.Path, "/extended"):
							payload = `{"data":{"id":555001,"name":"Example Series","originalLanguage":"eng"}}`
						default:
							payload = `{"data":{}}`
						}
					}
					return http.StatusOK, payload
				})
				meta := preparationstate.State{
					SourcePath: filepath.Join(t.TempDir(), "Example.Series.S00E01.1080p.WEB-DL-GRP.mkv"),
					Release:    api.ReleaseInfo{Category: "TV", Title: "Example Series"},
				}
				applySeasonEpisodeMetadata(&meta, seasonep.Extract(meta.SourcePath, meta), nil)
				ids := api.ExternalIdentity{Category: api.CanonicalCategoryTV}
				external := api.SourceScopedMetadata{}
				var tmdbClient TMDBClient
				var tvdbClient TVDBClient = &stubTVDB{}
				switch provider {
				case "IMDb":
					ids.IMDBID = 555001
					info, err := imdb.NewClient(httpClient, api.NopLogger{}).GetInfo(t.Context(), "tt555001", "", false)
					if err != nil {
						t.Fatal(err)
					}
					external.IMDB = mapIMDBMetadata(info)
					encoded, err := json.Marshal(external)
					if err != nil {
						t.Fatal(err)
					}
					external = api.SourceScopedMetadata{}
					if err := json.Unmarshal(encoded, &external); err != nil {
						t.Fatal(err)
					}
				case "TMDB":
					ids.TMDBID = 555001
					tmdbClient = tmdb.NewClient(httpClient, api.NopLogger{}, "synthetic-key")
				case "TVDB":
					ids.TVDBID = 555001
					tvdbClient = tvdb.NewClient(httpClient, api.NopLogger{}, "synthetic-key", "")
					external.TVDB = &api.TVDBMetadata{TVDBID: ids.TVDBID, OriginalLanguage: "eng"}
				}
				updated := NewService(&fakeRepo{}).applyTVEpisodeMetadata(t.Context(), meta, &ids, &external, tmdbClient, tvdbClient, nil)
				if test.matched {
					if updated.EpisodeTitle != "The Special Signal" || updated.EpisodeYear != 2026 {
						t.Fatalf("explicit season zero did not match: %+v", updated)
					}
				} else if updated.EpisodeTitle != "" || updated.EpisodeOverview != "" || updated.EpisodeYear != 0 || updated.TVDBAiredDate != "" {
					t.Fatalf("unproven special episode metadata leaked: title=%q overview=%q year=%d aired=%q",
						updated.EpisodeTitle, updated.EpisodeOverview, updated.EpisodeYear, updated.TVDBAiredDate)
				}
			})
		}
	}
}

func TestIMDbMissingSeasonRetainsDailyDateMatch(t *testing.T) {
	t.Parallel()
	payload := `{"data":{"title":{"id":"tt555001","titleText":{"text":"Example Series"},"titleType":{"id":"tvSeries"},"episodes":{"episodes":{"edges":[{"node":{"id":"tt555002","titleText":{"text":"The Daily Signal"},"releaseYear":{"year":2026},"releaseDate":{"year":2026,"month":1,"day":1},"series":{"displayableEpisodeNumber":{"displayableSeason":{"season":null},"episodeNumber":{"text":"1"}}}}}]}}}}}`
	httpClient, _ := evidenceHTTPClient(func(*http.Request) (int, string) { return http.StatusOK, payload })
	client := imdb.NewClient(httpClient, api.NopLogger{})
	info, err := client.GetInfo(t.Context(), "tt555001", "", false)
	if err != nil {
		t.Fatal(err)
	}
	external := api.SourceScopedMetadata{IMDB: mapIMDBMetadata(info)}
	meta := preparationstate.State{
		SourcePath: filepath.Join(t.TempDir(), "Example.Series.2026-01-01.1080p.WEB-DL-GRP.mkv"),
		Release:    api.ReleaseInfo{Category: "TV", Title: "Example Series"},
	}
	applySeasonEpisodeMetadata(&meta, seasonep.Extract(meta.SourcePath, meta), nil)
	ids := api.ExternalIdentity{IMDBID: 555001, Category: api.CanonicalCategoryTV}
	updated := NewService(&fakeRepo{}).applyTVEpisodeMetadata(t.Context(), meta, &ids, &external, nil, &stubTVDB{}, nil)
	if updated.EpisodeTitle != "The Daily Signal" || updated.DailyEpisodeDate != "2026-01-01" || updated.EpisodeInt != 1 {
		t.Fatalf("unknown provider season changed daily matching: title=%q date=%q episode=%d",
			updated.EpisodeTitle, updated.DailyEpisodeDate, updated.EpisodeInt)
	}
}

func TestPreparedSpecialSeasonCorrectionsSurviveRestart(t *testing.T) {
	t.Parallel()
	fixture := newCorrectionEvidenceFixtureForSource(t, "Example.Series.S00E01.1080p.WEB-DL.H264-GRP.mkv")
	transport := fixture.http.Transport
	fixture.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/3/tv/555001/season/0/episode/1" {
			return transport.RoundTrip(request)
		}
		fixture.mu.Lock()
		fixture.calls[request.URL.Path]++
		fixture.mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":55500101,"name":"The Special Signal","season_number":0,"episode_number":1,"air_date":"2026-01-01"}`,
			)),
			Request: request,
		}, nil
	})
	module, repo := fixture.open(t)
	initial := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if initial.Release.Episode.Season != 0 || initial.Release.Episode.SeasonLabel != "S00" || initial.Release.Episode.Episode != 1 ||
		initial.Release.Episode.Title != "The Special Signal" || !strings.Contains(initial.Release.Naming.ReleaseName, "S00E01") {
		t.Fatalf("source special lost before correction: episode=%+v name=%q", initial.Release.Episode, initial.Release.Naming.ReleaseName)
	}
	original, err := json.Marshal(initial.Release)
	if err != nil {
		t.Fatal(err)
	}
	input := api.PrepareInput{SourcePath: fixture.input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
	previous := initial.Release.Generation
	for _, test := range []struct {
		name   string
		value  *string
		season int
		label  string
		title  string
	}{
		{
			name:   "positive correction",
			value:  new("S02"),
			season: 2,
			label:  "S02",
			title:  "The Returning Signal",
		},
		{
			name:  "manual zero",
			value: new("00"),
			label: "S00",
			title: "The Special Signal",
		},
		{name: "explicit clear", value: new("")},
		{
			name:  "reset after restart",
			label: "S00",
			title: "The Special Signal",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			patch := &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Season: test.value}}}
			if test.value == nil {
				if err := repo.Close(); err != nil {
					t.Fatal(err)
				}
				module, repo = fixture.open(t)
				patch.ResetFields = []api.CorrectionFieldRef{{Field: api.CorrectionFieldReleaseNameSeason}}
			}
			result := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: patch})
			facts := result.Release.Episode
			if result.Release.Generation <= previous || facts.Season != test.season || facts.SeasonLabel != test.label || facts.Episode != 1 ||
				test.title != "" && facts.Title != test.title {
				t.Fatalf("corrected generation=%d episode=%+v", result.Release.Generation, facts)
			}
			if test.label != "" && !strings.Contains(result.Release.Naming.ReleaseName, test.label+"E01") ||
				test.label == "" && strings.Contains(result.Release.Naming.ReleaseName, "S00") {
				t.Fatalf("corrected name=%q", result.Release.Naming.ReleaseName)
			}
			if !reflect.DeepEqual(result.Corrections.Corrections.ReleaseName.Season, test.value) {
				t.Fatalf("stored correction=%v want=%v", result.Corrections.Corrections.ReleaseName.Season, test.value)
			}
			persisted, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
			if err != nil || !reflect.DeepEqual(persisted.Episode, facts) {
				t.Fatalf("persisted episode=%+v error=%v", persisted.Episode, err)
			}
			previous = result.Release.Generation
		})
	}
	unchanged, err := json.Marshal(initial.Release)
	if err != nil || string(unchanged) != string(original) {
		t.Fatalf("later corrections mutated the original special generation: %v", err)
	}
}

func TestSpecialEpisodeEnrichmentRequiresExactCoordinates(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"TMDB", "TVDB", "IMDb"} {
		for _, coordinates := range []struct {
			name    string
			season  int
			known   bool
			episode int
			matched bool
		}{
			{
				name:    "matching special",
				known:   true,
				episode: 1,
				matched: true,
			},
			{
				name:    "wrong season",
				season:  1,
				known:   true,
				episode: 1,
			},
			{
				name:    "wrong episode",
				known:   true,
				episode: 2,
			},
			{name: "unproven cached season", episode: 1},
		} {
			t.Run(provider+"/"+coordinates.name, func(t *testing.T) {
				t.Parallel()
				meta := preparationstate.State{
					SourcePath: filepath.Join(t.TempDir(), "Example.Series.S00E01.1080p.WEB-DL-GRP.mkv"),
					Release:    api.ReleaseInfo{Category: "TV", Title: "Example Series"},
				}
				applySeasonEpisodeMetadata(&meta, seasonep.Extract(meta.SourcePath, meta), nil)
				if meta.SeasonStr != "S00" || meta.EpisodeInt != 1 {
					t.Fatalf("source extraction lost special coordinates: %+v", meta)
				}
				ids := api.ExternalIdentity{Category: api.CanonicalCategoryTV}
				external := api.SourceScopedMetadata{}
				tmdbClient := &stubTMDB{}
				tvdbClient := &stubTVDB{}
				switch provider {
				case "TMDB":
					ids.TMDBID = 555001
					tmdbClient.episodeDetails = tmdb.EpisodeDetails{
						SeasonNumber:  coordinates.season,
						SeasonKnown:   coordinates.known,
						EpisodeNumber: coordinates.episode,
						Name:          "The Special Signal",
						Overview:      "Special overview",
						AirDate:       "2026-01-01",
					}
				case "TVDB":
					ids.TVDBID = 555002
					external.TVDB = &api.TVDBMetadata{TVDBID: ids.TVDBID, OriginalLanguage: "eng"}
					tvdbClient.episodes = tvdb.EpisodesData{Episodes: []tvdb.Episode{{
						SeasonNumber: coordinates.season,
						SeasonKnown:  coordinates.known,
						Number:       coordinates.episode,
						Name:         "The Special Signal",
						Overview:     "Special overview",
						Aired:        "2026-01-01",
						Year:         2026,
					}}}
				case "IMDb":
					ids.IMDBID = 555003
					external.IMDB = &api.IMDBMetadata{Episodes: []api.IMDBEpisode{{
						Season:      coordinates.season,
						SeasonKnown: coordinates.known,
						EpisodeText: seasonep.FormatEpisode(coordinates.episode)[1:],
						Title:       "The Special Signal",
						ReleaseYear: 2026,
					}}}
				}
				beforeIDs := ids
				service := NewService(&fakeRepo{})
				updated := service.applyTVEpisodeMetadata(t.Context(), meta, &ids, &external, tmdbClient, tvdbClient, &stubTVmaze{})
				if updated.SeasonInt != 0 || updated.SeasonStr != "S00" || updated.EpisodeInt != 1 || updated.EpisodeStr != "E01" ||
					!reflect.DeepEqual(ids, beforeIDs) {
					t.Fatalf("provider changed canonical special or series identity: episode=%d/%d labels=%q/%q ids=%+v",
						updated.SeasonInt, updated.EpisodeInt, updated.SeasonStr, updated.EpisodeStr, ids)
				}
				if coordinates.matched {
					if updated.EpisodeTitle != "The Special Signal" || updated.EpisodeYear != 2026 {
						t.Fatalf("matching special text was not applied: title=%q year=%d", updated.EpisodeTitle, updated.EpisodeYear)
					}
				} else if updated.EpisodeTitle != "" || updated.EpisodeOverview != "" || updated.EpisodeYear != 0 || updated.TVDBAiredDate != "" {
					t.Fatalf("wrong-coordinate episode text leaked: title=%q overview=%q year=%d aired=%q",
						updated.EpisodeTitle, updated.EpisodeOverview, updated.EpisodeYear, updated.TVDBAiredDate)
				}
			})
		}
	}
}

func TestSpecialEpisodeSkipsDailyAndTVmazeRemapping(t *testing.T) {
	t.Parallel()
	meta := preparationstate.State{
		SourcePath: filepath.Join(t.TempDir(), "Example.Series.S00E01.2026-01-01.1080p.WEB-DL-GRP.mkv"),
		Release:    api.ReleaseInfo{Category: "TV", Title: "Example Series"},
	}
	applySeasonEpisodeMetadata(&meta, seasonep.Extract(meta.SourcePath, meta), nil)
	tmdbClient := &stubTMDB{
		dailySeason:  2,
		dailyEpisode: 7,
		episodeDetails: tmdb.EpisodeDetails{
			SeasonNumber:  0,
			SeasonKnown:   true,
			EpisodeNumber: 1,
			Name:          "The Special Signal",
		},
	}
	tvmazeClient := &stubTVmaze{episodeData: &tvmaze.EpisodeData{
		SeasonNumber:  3,
		EpisodeNumber: 5,
		EpisodeName:   "Wrong Signal",
	}}
	ids := api.ExternalIdentity{
		TMDBID:   555001,
		TVmazeID: 555002,
		Category: api.CanonicalCategoryTV,
	}
	updated := NewService(&fakeRepo{}).applyTVEpisodeMetadata(t.Context(), meta, &ids, nil, tmdbClient, &stubTVDB{}, tvmazeClient)
	if tmdbClient.dailyCalls != 0 || tvmazeClient.episodeDateCalls != 0 || tvmazeClient.episodeNumberCalls != 0 || tmdbClient.episodeCalls != 1 {
		t.Fatalf("special used a remapping path: daily=%d TVmaze date=%d number=%d TMDB details=%d",
			tmdbClient.dailyCalls, tvmazeClient.episodeDateCalls, tvmazeClient.episodeNumberCalls, tmdbClient.episodeCalls)
	}
	if updated.SeasonInt != 0 || updated.SeasonStr != "S00" || updated.EpisodeInt != 1 || updated.EpisodeTitle != "The Special Signal" || updated.TMDBDateMatch {
		t.Fatalf("special remapped: season=%d label=%q episode=%d title=%q date-match=%t",
			updated.SeasonInt, updated.SeasonStr, updated.EpisodeInt, updated.EpisodeTitle, updated.TMDBDateMatch)
	}
}

func TestSpecialTVDBSelectedSnapshotRequiresSeasonPresence(t *testing.T) {
	t.Parallel()
	for _, season := range []string{"missing", "null"} {
		for _, refresh := range []string{"unproven", "error"} {
			t.Run(season+"/"+refresh, func(t *testing.T) {
				t.Parallel()
				httpClient, _ := evidenceHTTPClient(func(request *http.Request) (int, string) {
					switch {
					case strings.HasSuffix(request.URL.Path, "/login"):
						return http.StatusOK, "{\"data\":{\"token\":\"synthetic-token\"}}"
					case strings.Contains(request.URL.Path, "/episodes/default"):
						seasonField := ""
						if season == "null" {
							seasonField = ",\"seasonNumber\":null"
						}
						return http.StatusOK, "{\"data\":{\"episodes\":[{\"id\":555002,\"number\":1,\"name\":\"Unproven daily episode\",\"aired\":\"2026-01-01\"" + seasonField + "}]}}"
					case strings.Contains(request.URL.Path, "/extended"):
						return http.StatusOK, "{\"data\":{\"id\":555001,\"name\":\"Example Series\",\"originalLanguage\":\"eng\"}}"
					default:
						return http.StatusOK, "{\"data\":{}}"
					}
				})
				client := tvdb.NewClient(httpClient, api.NopLogger{}, "synthetic-key", "")
				source := filepath.Join(t.TempDir(), "Example.Series.2026-01-01.1080p.WEB-DL-GRP.mkv")
				ids := api.ExternalIdentity{
					SourcePath: source,
					TVDBID:     555001,
					Category:   api.CanonicalCategoryTV,
				}
				snapshot := api.SourceScopedMetadata{SourcePath: source, TVDB: &api.TVDBMetadata{TVDBID: ids.TVDBID, OriginalLanguage: "eng"}}
				service := NewService(&fakeRepo{})
				daily := preparationstate.State{
					SourcePath:       source,
					Release:          api.ReleaseInfo{Category: "TV", Title: "Example Series"},
					DailyEpisodeDate: "2026-01-01",
				}
				daily = service.applyTVEpisodeMetadata(t.Context(), daily, &ids, &snapshot, nil, client, nil)
				if daily.EpisodeTitle != "Unproven daily episode" || snapshot.TVDB.EpisodeNumber != 1 {
					t.Fatalf("ordinary daily producer lost its match: state=%+v snapshot=%+v", daily, snapshot.TVDB)
				}
				encoded, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				snapshot = api.SourceScopedMetadata{}
				if err := json.Unmarshal(encoded, &snapshot); err != nil {
					t.Fatal(err)
				}
				corrected := preparationstate.State{
					SourcePath:           source,
					Release:              api.ReleaseInfo{Category: "TV", Title: "Example Series"},
					ReleaseNameOverrides: api.ReleaseNameOverrides{Season: new("S00"), Episode: new("E01")},
				}
				var refreshed TVDBClient = client
				if refresh == "error" {
					refreshed = &stubTVDB{episodeErr: errors.New("synthetic episode refresh unavailable")}
				}
				corrected = service.applyTVEpisodeMetadata(t.Context(), corrected, &ids, &snapshot, nil, refreshed, nil)
				corrected.Identity = ids
				corrected.ProviderMetadata = snapshot
				if corrected.EpisodeTitle != "" || preferredGeneratedEpisodeTitle(corrected) != "" {
					t.Fatalf("unproven selected snapshot supplied special text: title=%q snapshot=%+v",
						preferredGeneratedEpisodeTitle(corrected), snapshot.TVDB)
				}
				corrected.ReleaseNameOverrides.EpisodeTitle = new("Manual special title")
				if resolvedEpisodeTitle(corrected) != "Manual special title" {
					t.Fatal("manual episode title lost precedence")
				}
			})
		}
	}
}

func TestSpecialTVDBEnglishTextRetainsOnlyMatchingEpisode(t *testing.T) {
	t.Parallel()
	for _, cached := range []struct {
		name    string
		season  int
		known   bool
		episode int
		retain  bool
	}{
		{
			name:    "wrong cached season",
			season:  1,
			episode: 1,
		},
		{name: "wrong cached episode", episode: 2},
		{name: "unproven cached special", episode: 1},
		{
			name:    "matching cached special",
			known:   true,
			episode: 1,
			retain:  true,
		},
	} {
		for _, translation := range []string{"empty", "error", "missing episode ID"} {
			t.Run(cached.name+"/"+translation, func(t *testing.T) {
				t.Parallel()
				meta := preparationstate.State{
					SourcePath: filepath.Join(t.TempDir(), "Example.Series.S00E01.1080p.WEB-DL-GRP.mkv"),
					Release:    api.ReleaseInfo{Category: "TV", Title: "Example Series"},
				}
				applySeasonEpisodeMetadata(&meta, seasonep.Extract(meta.SourcePath, meta), nil)
				ids := api.ExternalIdentity{TVDBID: 555002, Category: api.CanonicalCategoryTV}
				external := api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{
					TVDBID:                 ids.TVDBID,
					OriginalLanguage:       "jpn",
					EpisodeSeason:          cached.season,
					EpisodeSeasonKnown:     cached.known,
					EpisodeNumber:          cached.episode,
					EpisodeNameEnglish:     "Cached episode title",
					EpisodeOverviewEnglish: "Cached episode overview",
				}}
				episodeID := 555201
				if translation == "missing episode ID" {
					episodeID = 0
				}
				client := &stubTVDB{episodes: tvdb.EpisodesData{Episodes: []tvdb.Episode{{
					ID:           episodeID,
					SeasonNumber: 0,
					SeasonKnown:  true,
					Number:       1,
					Name:         "Original special title",
					Overview:     "Original special overview",
				}}}}
				if translation == "error" {
					client.episodeTransErr = errors.New("synthetic translation unavailable")
				}
				updated := NewService(&fakeRepo{}).applyTVEpisodeMetadata(t.Context(), meta, &ids, &external, nil, client, nil)
				wantTitle, wantOverview := "", ""
				if cached.retain {
					wantTitle, wantOverview = "Cached episode title", "Cached episode overview"
				}
				if updated.SeasonStr != "S00" || updated.EpisodeInt != 1 || updated.EpisodeTitle != wantTitle || updated.EpisodeOverview != wantOverview {
					t.Fatalf("canonical special reused wrong text: season=%q episode=%d title=%q overview=%q",
						updated.SeasonStr, updated.EpisodeInt, updated.EpisodeTitle, updated.EpisodeOverview)
				}
				provider := external.TVDB
				if provider.EpisodeSeason != 0 || provider.EpisodeNumber != 1 || provider.EpisodeNameEnglish != wantTitle || provider.EpisodeOverviewEnglish != wantOverview {
					t.Fatalf("selected provider text was relabelled across episodes: %+v", provider)
				}
				if len(provider.Episodes) != 1 {
					t.Fatalf("selected episode snapshot=%+v", provider.Episodes)
				}
				selected := provider.Episodes[0]
				wantSelectedTitle, wantSelectedOverview := wantTitle, wantOverview
				if episodeID == 0 {
					wantSelectedTitle, wantSelectedOverview = "", ""
				}
				if selected.ID != episodeID || selected.SeasonNumber != 0 || selected.EpisodeNumber != 1 ||
					selected.EpisodeName != "Original special title" || selected.EpisodeOverview != "Original special overview" ||
					selected.EpisodeNameEnglish != wantSelectedTitle || selected.EpisodeOverviewEnglish != wantSelectedOverview {
					t.Fatalf("selected episode snapshot retained stale translation: %+v", selected)
				}
				if (len(client.episodeTransCalls) == 0) != (episodeID == 0) {
					t.Fatalf("translation calls=%v for episode ID=%d", client.episodeTransCalls, episodeID)
				}
				updated.ReleaseNameOverrides.EpisodeTitle = new("Manual special title")
				applyReleaseNameValueOverrides(&updated)
				if updated.EpisodeTitle != "Manual special title" || updated.EpisodeOverview != wantOverview || provider.EpisodeNameEnglish != wantTitle {
					t.Fatal("manual episode title lost precedence or changed provider evidence")
				}
			})
		}
	}
}
