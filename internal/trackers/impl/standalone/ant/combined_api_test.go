// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCombinedAPIUploadCategoriesAndTVPayload(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, kind, season, episode string
		category                    api.CanonicalCategory
		pack, batch                 bool
		seasonNumber, episodeNumber int
	}{
		{
			name:     "feature",
			category: api.CanonicalCategoryMovie,
			kind:     "1",
		},
		{
			name:     "short",
			category: api.CanonicalCategoryMovie,
			kind:     "2",
		},
		{
			name:         "season",
			category:     api.CanonicalCategoryTV,
			kind:         "3",
			pack:         true,
			seasonNumber: 2,
			season:       "2",
		},
		{
			name:          "episode",
			category:      api.CanonicalCategoryTV,
			kind:          "4",
			seasonNumber:  2,
			episodeNumber: 3,
			season:        "2",
			episode:       "3",
		},
		{
			name:          "special",
			category:      api.CanonicalCategoryTV,
			kind:          "4",
			episodeNumber: 1,
			season:        "0",
			episode:       "1",
		},
		{
			name:     "unnumbered anime batch",
			category: api.CanonicalCategoryTV,
			kind:     "3",
			batch:    true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			mediaPath, torrentPath := filepath.Join(dir, "MEDIAINFO.txt"), filepath.Join(dir, "Example.torrent")
			for path, data := range map[string]string{mediaPath: "General\nVideo\nFormat : AVC", torrentPath: "synthetic torrent"} {
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			meta := api.UploadSubject{
				SourcePath:        filepath.Join(dir, "Example.mkv"),
				TorrentPath:       torrentPath,
				MediaInfoTextPath: mediaPath,
				ReleaseName:       "Example.Release.2026.S02E03.1080p.WEB-DL.x264-GRP",
				Source:            "WEB-DL",
				Tag:               "GRP",
				Identity:          api.ExternalIdentity{Category: test.category},
				TVPack:            test.pack,
				MultipleEpisodes:  test.batch,
				SeasonInt:         test.seasonNumber,
				EpisodeInt:        test.episodeNumber,
				LanguageFacts:     api.LanguageFacts{ProgrammeLanguages: []string{"English"}, ProgrammeStatus: api.MetadataEvidenceStatusComplete},
			}
			if test.name == "special" {
				meta.SeasonStr = "S00"
			}
			if test.category == api.CanonicalCategoryTV {
				meta.Identity.TVDBID = 100001
				meta.ProviderMetadata.TVDB = &api.TVDBMetadata{
					TVDBID: 100001,
					Name:   "Example Series",
					Genres: "Drama",
				}
				// Old film/screenshot answers must not alter TV payloads or add image controls.
				meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"ANT": {"type": "Short Film", "adult_screens": "yes"}}
			} else {
				meta.Identity.TMDBID = 200001
				meta.ProviderMetadata.TMDB = &api.TMDBMetadata{
					TMDBID: 200001,
					Title:  "Example Film",
					Genres: "Drama",
				}
				if test.name == "short" {
					meta.ProviderMetadata.TMDB.Runtime = 20
				}
			}
			entry, err := New().prepareDryRun(t.Context(), trackers.PreparationInput{
				Tracker:       "ANT",
				Meta:          meta,
				Logger:        api.NopLogger{},
				TrackerConfig: config.TrackerConfig{APIKey: "test-key"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if entry.Payload["type"] != test.kind || entry.Payload["season"] != test.season || entry.Payload["episode"] != test.episode {
				t.Fatalf("wrong category/coordinates: %#v", entry.Payload)
			}
			if entry.Payload["mediainfo"] != "General\nVideo\nFormat : AVC" || entry.Payload["tags"] != "drama" {
				t.Fatalf("raw media or genres lost: %#v", entry.Payload)
			}
			if test.category == api.CanonicalCategoryTV {
				for _, key := range []string{"tmdbid", "screenshots", "release_desc"} {
					if _, present := entry.Payload[key]; present {
						t.Fatalf("TVDB-only TV upload has unwanted %s field", key)
					}
				}
				if entry.Payload["tvdbid"] != "100001" || entry.Description != "" || slices.ContainsFunc(entry.Questionnaire.Fields, func(f api.TrackerQuestionnaireField) bool {
					return f.Key == "type" || f.Key == "adult_screens"
				}) {
					t.Fatalf("TV identity/content controls = %#v", entry)
				}
			} else if entry.Payload["tmdbid"] != "200001" {
				t.Fatal("movie TMDB identity lost")
			}
		})
	}
}

func TestTVCoordinatesRejectInconsistentFinalizedScope(t *testing.T) {
	t.Parallel()
	for _, meta := range []api.UploadSubject{
		{
			TVPack:     true,
			SeasonInt:  1,
			EpisodeInt: 2,
		},
		{SeasonInt: 1},
		{EpisodeInt: 2},
		{SeasonInt: -1, EpisodeInt: 2},
	} {
		if _, err := tvCoordinates(meta); err == nil {
			t.Fatalf("invalid TV scope accepted: %+v", meta)
		}
	}
	if fields, err := tvCoordinates(api.UploadSubject{DailyEpisodeDate: "2026-10-28"}); err != nil || len(fields) != 0 {
		t.Fatalf("daily episode must retain site inference: %v %v", fields, err)
	}
}

func TestTVSearchUsesTVCategoryAndFiltersWrongWork(t *testing.T) {
	t.Parallel()
	for _, useTVDB := range []bool{true, false} {
		identity := api.ExternalIdentity{Category: api.CanonicalCategoryTV, TMDBID: 200001}
		if useTVDB {
			identity.TVDBID = 100001
		}
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			if query.Get("t") != "tvsearch" || query.Get("cat") != "5000" || query.Get("season") != "" || query.Get("ep") != "" {
				t.Errorf("TV search must enumerate the whole series: %s", req.URL.RawQuery)
			}
			if useTVDB && (query.Get("tvdbid") != "100001" || query.Get("tmdbid") != "") || !useTVDB && query.Get("tmdbid") != "200001" {
				t.Errorf("wrong TV provider: %s", req.URL.RawQuery)
			}
			body := `{"response":{"offset":0,"total":4},"item":[
			{"category":[5000,100003],"tvdbid":100001,"tmdb":200001,"season":2,"fileName":"Example.Series.2026.S02.1080p.WEB.H264.torrent","media":"WEB","codec":"H264","resolution":"1080p"},
			{"category":[5000,100004],"tvdbid":100001,"tmdb":200002,"season":2,"episode":3,"fileName":"Example.Series.2026.S02E03.torrent"},
			{"category":[2000,100001],"tmdb":200001,"fileName":"Unrelated.Film.2026.torrent"},
			{"category":[5000,100004],"tvdbid":100002,"tmdb":200002,"season":2,"episode":3,"fileName":"Other.Series.2026.S02E03.torrent"}]}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		result := dupe.NewAdapter(New(), "ANT", antDupeTestConfig(t), client, api.NopLogger{}).Search(t.Context(), api.DuplicateSubject{Identity: identity})
		entries := result.Entries()
		wantEntries := 1
		if useTVDB {
			wantEntries = 2
		}
		if result.Cause() != nil || !result.SearchEvidence().EffectiveComplete() || len(entries) != wantEntries ||
			entries[0].Category != "tv" || entries[0].Pack || entries[0].Season != 2 || entries[0].Episode != 0 || entries[0].TrackerSlot != "" {
			t.Fatalf("wrong TV evidence: entries=%+v search=%+v err=%v", entries, result.SearchEvidence(), result.Cause())
		}
	}
}

func TestANTTVWorkflowOmitsImagesAndMovieSlotAuthority(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	cfg := antDupeTestConfig(t)
	cfg.Trackers.Trackers["ANT"] = config.TrackerConfig{APIKey: "test-key", ImageCount: 4}
	projector, err := trackers.NewWorkflowProjector(registry, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range []api.CanonicalCategory{api.CanonicalCategoryMovie, api.CanonicalCategoryTV} {
		meta := api.UploadSubject{
			Identity: api.ExternalIdentity{Category: category, TMDBID: 200001},
			ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
				TMDBID: 200001,
				Title:  "Example Release",
				Genres: "Drama",
			}},
			ReleaseName:       "Example.Release.2026.1080p.WEB-DL.x264-GRP",
			MediaInfoTextPath: "prepared-media",
			LanguageFacts:     api.LanguageFacts{ProgrammeLanguages: []string{"English"}, ProgrammeStatus: api.MetadataEvidenceStatusComplete},
		}
		_, _, _, projections, err := projector.Build(t.Context(), api.ReleaseSnapshot{}, meta, []api.TrackerID{"ANT"}, nil, nil, api.WorkflowExecutionModeNormal)
		if err != nil || len(projections.Projections) != 1 {
			t.Fatalf("project: %v %+v", err, projections)
		}
		projection := projections.Projections[0]
		policy, _ := registry.LookupDupePolicyForCategory("ANT", category)
		if category == api.CanonicalCategoryTV {
			if projection.Artifacts.ScreenshotCount != 0 || projection.Artifacts.DVDMenuCount != 0 || projection.Artifacts.Description ||
				policy.StaffTokenOverride || policy.TargetSlot != nil || policy.CompareSlots != nil || projection.DuplicatePolicyID != policy.ID {
				t.Fatalf("movie policy leaked into TV: projection=%+v policy=%+v", projection, policy)
			}
			service := trackers.NewServiceWithRegistry(cfg, nil, nil, registry)
			if preview, err := service.BuildPreparation(t.Context(), api.NewDescriptionSubject(meta), []string{"ANT"}); err != nil || len(preview.Descriptions) != 0 {
				t.Fatalf("TV preparation must bypass description/image services: %+v %v", preview, err)
			}
		} else if projection.Artifacts.ScreenshotCount != 4 || !projection.Artifacts.Description || !policy.StaffTokenOverride || policy.TargetSlot == nil {
			t.Fatalf("movie workflow changed: projection=%+v policy=%+v", projection, policy)
		}
	}
}

func TestANTCombinedBannedGroupsReachSharedGate(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	checker := trackers.NewBannedGroupCheckerWithRegistry(filepath.Join(t.TempDir(), "upbrr.db"), registry)
	for _, group := range []string{"QxR", "YakuboEncodes", "ZMNT"} {
		if banned, err := checker.IsBanned("ANT", group); err != nil || !banned {
			t.Fatalf("combined ANT/NBL ban %s did not reach shared gate: banned=%v err=%v", group, banned, err)
		}
	}
}
