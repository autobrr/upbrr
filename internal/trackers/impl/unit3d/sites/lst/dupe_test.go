// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLSTTitleSearchIncludesEverySeasonAndVariant(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	var normalSeen atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		query := r.URL.Query()
		if query.Get("seasonNumber") == "2" {
			normalSeen.Store(true)
			if query.Get("name") != " S02" {
				t.Errorf("ordinary/full-disc season name changed: %v", query)
			}
			_, _ = w.Write([]byte(`{"data":[],"links":{"next":null}}`))
			return
		}
		if query.Get("page") == "1" {
			if query.Get("tmdbId") != "123" || len(query["categories[]"]) == 0 {
				t.Errorf("missing work/category binding: %v", query)
			}
			for _, key := range []string{"name", "seasonNumber", "episodeNumber", "types[]", "resolutions[]"} {
				if _, ok := query[key]; ok {
					t.Errorf("narrow title lookup %s: %v", key, query)
				}
			}
			_, _ = w.Write([]byte(`{"data":[{"id":1,"attributes":{"name":"Example.S01.2160p.REMUX-GRP","tmdb_id":123,"type":"Remux","resolution":"2160p"}}],"links":{"next":"?page=2"}}`))
		} else {
			_, _ = w.Write([]byte(`{"data":[{"id":2,"attributes":{"name":"Example.S03.720p.WEB-DL-GRP","tmdb_id":123,"type":"WEBDL","resolution":"720p"}}],"links":{"next":null}}`))
		}
	}))
	t.Cleanup(server.Close)
	profile := Profile()
	profile.BaseURL = server.URL
	definition := unit3d.NewWithProfile(profile)
	registry := trackers.NewRegistry()
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"LST": {APIKey: "test-key"}}}}
	adapter := dupe.NewAdapter(definition, "LST", cfg, server.Client(), api.NopLogger{}, registry)
	titleAdapter, ok := adapter.(dupe.TitleSearchAdapter)
	if !ok {
		t.Fatal("title search adapter unavailable")
	}
	result := titleAdapter.SearchTitle(t.Context(), api.DuplicateSubject{
		Identity:    api.ExternalIdentity{TMDBID: 123, Category: api.CanonicalCategoryTV},
		ReleaseName: "Example.S02E03.1080p.WEB-DL-GRP",
		SeasonInt:   2,
		EpisodeInt:  3,
	})
	if requests.Load() != 2 || !result.SearchEvidence().EffectiveComplete() || len(result.Entries()) != 2 {
		t.Fatalf("whole-title result=%v rows=%v requests=%d", result.SearchEvidence(), result.Entries(), requests.Load())
	}
	ordinary := adapter.Search(t.Context(), api.DuplicateSubject{
		Identity:    api.ExternalIdentity{TMDBID: 123, Category: api.CanonicalCategoryTV},
		ReleaseName: "Example.S02.1080p.BluRay-GRP",
		SeasonInt:   2,
		DiscType:    "BDMV",
	})
	if !normalSeen.Load() || !ordinary.SearchEvidence().EffectiveComplete() || requests.Load() != 3 {
		t.Fatal("ordinary full-disc search no longer retains season scope")
	}
}

func TestLSTTitleSearchApplicability(t *testing.T) {
	t.Parallel()
	base := api.TrackerValidationSubject{LanguageFacts: api.LanguageFacts{
		OriginalLanguages:  []string{"Japanese"},
		ProgrammeLanguages: []string{"Japanese"},
		AudioStatus:        api.MetadataEvidenceStatusComplete,
		SubtitleStatus:     api.MetadataEvidenceStatusComplete,
	}}
	policy := titleSearchPolicy()
	if !policy.Required(base) {
		t.Fatal("missing subtitles did not request title search")
	}
	for _, disc := range []string{"BDMV", "DVD", "HDDVD"} {
		t.Run(disc, func(t *testing.T) {
			subject := base
			subject.DiscType = disc
			if policy.Required(subject) {
				t.Fatal("full disc requested language search")
			}
		})
	}
	for index, change := range []func(*api.TrackerValidationSubject){
		func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.ProgrammeLanguages = []string{"Japanese", "English"}
		},
		func(s *api.TrackerValidationSubject) { s.LanguageFacts.SubtitleLanguages = []string{"English"} },
		func(s *api.TrackerValidationSubject) { s.LanguageFacts.OriginalLanguages = []string{"English"} },
		func(s *api.TrackerValidationSubject) { s.LanguageFacts.OriginalLanguages = []string{"ZXX"} },
		func(s *api.TrackerValidationSubject) { s.LanguageFacts.AudioStatus = api.MetadataEvidenceStatusPartial },
		func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.SubtitleStatus = api.MetadataEvidenceStatusPartial
		},
	} {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			subject := base
			change(&subject)
			if policy.Required(subject) {
				t.Fatal("inapplicable facts requested title search")
			}
		})
	}
	remux := base
	remux.Type = "REMUX"
	remux.Source = "BluRay"
	if !policy.Required(remux) || !slices.Equal(remux.LanguageFacts.ProgrammeLanguages, base.LanguageFacts.ProgrammeLanguages) {
		t.Fatal("disc-sourced remux excluded")
	}
}
