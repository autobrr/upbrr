// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/blu"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/cbr"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ihd"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ldu"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/r4e"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/sam"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/tik"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/tos"
	"github.com/autobrr/upbrr/pkg/api"
)

type categorySearchTraceLogger struct {
	api.NopLogger
	trace []string
}

func (l *categorySearchTraceLogger) Tracef(format string, args ...any) {
	l.trace = append(l.trace, fmt.Sprintf(format, args...))
}

func TestUnit3DCategorySearchIncludesUploadAndRelatedCategories(t *testing.T) {
	t.Parallel()

	profiles := map[string]unit3d.Profile{
		"BLU": blu.Profile(),
		"CBR": cbr.Profile(),
		"IHD": ihd.Profile(),
		"LDU": ldu.Profile(),
		"R4E": r4e.Profile(),
		"SAM": sam.Profile(),
		"TIK": tik.Profile(),
		"TOS": tos.Profile(),
	}
	scopes := map[string]map[api.CanonicalCategory][]string{
		"BLU": {api.CanonicalCategoryMovie: {"1", "3"}, api.CanonicalCategoryTV: {"2", "3"}},
		"CBR": {api.CanonicalCategoryMovie: {"1"}, api.CanonicalCategoryTV: {"2", "4"}},
		"IHD": {api.CanonicalCategoryMovie: {"1", "4"}, api.CanonicalCategoryTV: {"2", "3"}},
		"LDU": {
			api.CanonicalCategoryMovie: {"1", "6", "8", "12", "17", "21", "22", "25", "27", "45"},
			api.CanonicalCategoryTV:    {"2", "9", "29", "31", "40", "41"},
		},
		"R4E": {api.CanonicalCategoryMovie: {"66", "70"}, api.CanonicalCategoryTV: {"2", "79"}},
		"SAM": {api.CanonicalCategoryMovie: {"1"}, api.CanonicalCategoryTV: {"2", "3"}},
		"TIK": {api.CanonicalCategoryMovie: {"1", "3", "5", "6"}, api.CanonicalCategoryTV: {"2", "4", "5"}},
		"TOS": {api.CanonicalCategoryMovie: {"1", "6"}, api.CanonicalCategoryTV: {"2", "7", "8", "9"}},
	}
	anime := func(m *api.UploadSubject) { m.Anime = true }
	pack := func(m *api.UploadSubject) { m.TVPack = true; m.EpisodeInt = 0 }
	foreign := func(m *api.UploadSubject) { m.AudioLanguages = nil; m.SubtitleLanguages = nil }
	manualGenre := func(value string) func(*api.UploadSubject) {
		return func(m *api.UploadSubject) {
			m.EffectiveMetadata.Genres = []string{value}
			m.EffectiveMetadata.GenresProvenance = api.FactProvenanceManual
			m.ProviderMetadata.TMDB = &api.TMDBMetadata{Genres: "Drama"}
		}
	}
	doc := func(m *api.UploadSubject) { m.ProviderMetadata.TMDB = &api.TMDBMetadata{GenreIDs: "18,99"} }
	subFrench := func(m *api.UploadSubject) { m.Tag = "GRP-VOSTFR" }

	tests := []struct {
		tracker    string
		name       string
		category   api.CanonicalCategory
		modify     func(*api.UploadSubject)
		wantUpload string
	}{
		{"BLU", "movie", api.CanonicalCategoryMovie, nil, "1"},
		{"BLU", "TV", api.CanonicalCategoryTV, nil, "2"},
		{"BLU", "manual fan restoration", api.CanonicalCategoryMovie, func(m *api.UploadSubject) { m.Edition = "Fanres" }, "3"},
		{"BLU", "TV fan restoration", api.CanonicalCategoryTV, func(m *api.UploadSubject) { m.Edition = "Fanres" }, "3"},
		{"CBR", "movie", api.CanonicalCategoryMovie, nil, "1"},
		{"CBR", "TV", api.CanonicalCategoryTV, nil, "2"},
		{"CBR", "anime TV", api.CanonicalCategoryTV, anime, "4"},
		{"IHD", "movie", api.CanonicalCategoryMovie, nil, "1"},
		{"IHD", "TV", api.CanonicalCategoryTV, nil, "2"},
		{"IHD", "anime movie", api.CanonicalCategoryMovie, anime, "4"},
		{"IHD", "anime TV", api.CanonicalCategoryTV, anime, "3"},
		{"LDU", "movie", api.CanonicalCategoryMovie, nil, "1"},
		{"LDU", "TV episode", api.CanonicalCategoryTV, nil, "41"},
		{"LDU", "TV pack", api.CanonicalCategoryTV, pack, "2"},
		{"LDU", "anime movie", api.CanonicalCategoryMovie, anime, "8"},
		{"LDU", "MAL-bound TV", api.CanonicalCategoryTV, func(m *api.UploadSubject) { m.Identity.MALID = 321 }, "9"},
		{"LDU", "fan edit", api.CanonicalCategoryMovie, func(m *api.UploadSubject) { m.Edition = "Fanedit" }, "12"},
		{"LDU", "3D movie", api.CanonicalCategoryMovie, func(m *api.UploadSubject) { m.Is3D = "3D" }, "21"},
		{"LDU", "adult movie", api.CanonicalCategoryMovie, manualGenre("Adult"), "6"},
		{"LDU", "foreign adult movie", api.CanonicalCategoryMovie, func(m *api.UploadSubject) { manualGenre("Adult")(m); foreign(m) }, "45"},
		{"LDU", "manual documentary movie", api.CanonicalCategoryMovie, manualGenre("Documentary"), "17"},
		{"LDU", "manual documentary TV", api.CanonicalCategoryTV, manualGenre("Documentary"), "40"},
		{"LDU", "musical movie", api.CanonicalCategoryMovie, manualGenre("Musical"), "25"},
		{"LDU", "foreign movie", api.CanonicalCategoryMovie, foreign, "22"},
		{"LDU", "foreign TV", api.CanonicalCategoryTV, foreign, "29"},
		{"LDU", "dubbed movie", api.CanonicalCategoryMovie, func(m *api.UploadSubject) { m.Audio = "Dubbed" }, "27"},
		{"LDU", "dubbed TV", api.CanonicalCategoryTV, func(m *api.UploadSubject) { m.Audio = "Dubbed" }, "31"},
		{"R4E", "movie", api.CanonicalCategoryMovie, nil, "70"},
		{"R4E", "TV", api.CanonicalCategoryTV, nil, "79"},
		{"R4E", "provider documentary movie", api.CanonicalCategoryMovie, doc, "66"},
		{"R4E", "provider documentary TV", api.CanonicalCategoryTV, doc, "2"},
		{"SAM", "movie", api.CanonicalCategoryMovie, nil, "1"},
		{"SAM", "TV", api.CanonicalCategoryTV, nil, "2"},
		{"SAM", "anime TV", api.CanonicalCategoryTV, anime, "3"},
		{"SAM", "anime TV pack", api.CanonicalCategoryTV, func(m *api.UploadSubject) { anime(m); pack(m) }, "3"},
		{"TIK", "movie", api.CanonicalCategoryMovie, nil, "1"},
		{"TIK", "TV", api.CanonicalCategoryTV, nil, "2"},
		{"TIK", "manual foreign movie", api.CanonicalCategoryMovie, func(m *api.UploadSubject) {
			m.EffectiveMetadata.OriginalLanguage = "French"
			m.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceManual
			m.ProviderMetadata.TMDB = &api.TMDBMetadata{OriginalLanguage: "en"}
		}, "3"},
		{"TIK", "foreign TV override", api.CanonicalCategoryTV, func(m *api.UploadSubject) { m.TrackerSiteOverrides.TIK.Foreign = new(true) }, "4"},
		{"TIK", "opera override", api.CanonicalCategoryTV, func(m *api.UploadSubject) { m.TrackerSiteOverrides.TIK.Opera = new(true) }, "5"},
		{"TIK", "Asian override", api.CanonicalCategoryMovie, func(m *api.UploadSubject) { m.TrackerSiteOverrides.TIK.Asian = new(true) }, "6"},
		{"TIK", "foreign override disabled", api.CanonicalCategoryMovie, func(m *api.UploadSubject) {
			m.ProviderMetadata.TMDB = &api.TMDBMetadata{OriginalLanguage: "fr"}
			m.TrackerSiteOverrides.TIK.Foreign = new(false)
		}, "1"},
		{"TOS", "movie", api.CanonicalCategoryMovie, nil, "1"},
		{"TOS", "TV episode", api.CanonicalCategoryTV, nil, "2"},
		{"TOS", "French-subtitled movie", api.CanonicalCategoryMovie, subFrench, "6"},
		{"TOS", "French-subtitled episode", api.CanonicalCategoryTV, subFrench, "7"},
		{"TOS", "TV pack", api.CanonicalCategoryTV, pack, "8"},
		{"TOS", "French-subtitled pack", api.CanonicalCategoryTV, func(m *api.UploadSubject) { subFrench(m); pack(m) }, "9"},
	}
	for _, test := range tests {
		t.Run(test.tracker+"/"+test.name, func(t *testing.T) {
			t.Parallel()
			meta := unit3DCategoryScopeSubject(t, test.category)
			if test.modify != nil {
				test.modify(&meta)
			}
			wantScope := scopes[test.tracker][test.category]
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				if got := query["categories[]"]; !slices.Equal(got, wantScope) {
					t.Errorf("search categories = %v, want %v", got, wantScope)
				}
				if query.Get("tmdbId") != "123" || query.Get("perPage") != "100" {
					t.Errorf("lost provider-bound pagination: %v", query)
				}
				for _, key := range []string{"types[]", "resolutions[]", "episodeNumber"} {
					if _, exists := query[key]; exists {
						t.Errorf("unsafe narrowing %s = %v", key, query[key])
					}
				}
				_, _ = w.Write([]byte(`{"data":[],"meta":{"current_page":1,"last_page":1},"links":{"next":null}}`))
			}))
			defer server.Close()
			profile := profiles[test.tracker]
			profile.BaseURL = server.URL
			definition := unit3d.NewWithProfile(profile)
			plan, failure := definition.Prepare(t.Context(), trackers.PreparationInput{
				Intent:        trackers.PreparationIntentDryRun,
				Tracker:       test.tracker,
				Meta:          meta,
				TrackerConfig: config.TrackerConfig{APIKey: "test-key"},
				Logger:        api.NopLogger{},
				Assets:        &trackers.DescriptionAssets{Description: "Synthetic description", Final: true},
			})
			if failure != nil {
				t.Fatalf("prepare upload: %v", failure)
			}
			t.Cleanup(func() {
				if err := plan.Release(); err != nil {
					t.Errorf("release plan: %v", err)
				}
			})
			if got := plan.DryRun().Payload["category_id"]; got != test.wantUpload || !slices.Contains(wantScope, got) {
				t.Fatalf("upload category = %q, want %q within search scope %v", got, test.wantUpload, wantScope)
			}
			registry := trackers.NewRegistry()
			if err := registry.Register(definition); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
				test.tracker: {APIKey: "test-key"},
			}}}
			logger := &categorySearchTraceLogger{}
			adapter := dupe.NewAdapter(definition, test.tracker, cfg, server.Client(), logger, registry)
			result := adapter.Search(t.Context(), api.DuplicateSubject{
				Identity:    meta.Identity,
				ReleaseName: meta.ReleaseName,
				SeasonInt:   meta.SeasonInt,
				Anime:       meta.Anime,
			})
			if result.Disposition() != dupe.DispositionResolved || !result.SearchEvidence().EffectiveComplete() {
				t.Fatalf("search = %v (%s), evidence = %+v", result.Disposition(), result.SafeMessage(), result.SearchEvidence())
			}
			categories, err := json.Marshal(wantScope)
			if err != nil {
				t.Fatal(err)
			}
			logs := strings.Join(logger.trace, "\n")
			for _, want := range []string{
				"dupechecking: request tracker=" + test.tracker + " method=GET endpoint=/api/torrents/filter",
				`"tmdbId":["123"]`, `"categories[]":` + string(categories), `"page":["1"]`, `"perPage":["100"]`,
			} {
				if !strings.Contains(logs, want) {
					t.Fatalf("request TRACE missing %q", want)
				}
			}
			if strings.Contains(logs, "test-key") || strings.Contains(logs, server.URL) {
				t.Fatal("request TRACE exposed credentials or the configured URL")
			}
		})
	}
}

func unit3DCategoryScopeSubject(t *testing.T, category api.CanonicalCategory) api.UploadSubject {
	t.Helper()
	tempDir := t.TempDir()
	mediaInfoPath := filepath.Join(tempDir, "mediainfo.txt")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nComplete name: Example Release"), 0o600); err != nil {
		t.Fatal(err)
	}
	torrentPath := filepath.Join(tempDir, "example.torrent")
	if err := os.WriteFile(torrentPath, []byte("synthetic torrent preview"), 0o600); err != nil {
		t.Fatal(err)
	}
	return api.UploadSubject{
		SourcePath:        filepath.Join(tempDir, "Example.Release.2026.1080p.WEB-DL.H264-GRP.mkv"),
		ReleaseName:       "Example.Release.2026.1080p.WEB-DL.H264-GRP",
		MediaInfoTextPath: mediaInfoPath,
		TorrentPath:       torrentPath,
		Identity:          api.ExternalIdentity{Category: category, TMDBID: 123},
		Release: api.ReleaseInfo{
			Title:      "Example Release",
			Year:       2026,
			Resolution: "1080p",
			Genre:      "Drama",
		},
		EffectiveMetadata:    api.EffectiveMetadata{Title: "Example Release", Year: 2026},
		Type:                 "WEBDL",
		Source:               "WEB-DL",
		VideoCodec:           "H264",
		Tag:                  "GRP",
		AudioLanguages:       []string{"English"},
		SubtitleLanguages:    []string{"English"},
		SeasonInt:            1,
		EpisodeInt:           2,
		Assessments:          api.ReleaseAssessments{MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent},
		TrackerSiteOverrides: api.TrackerSiteOverrides{TIK: api.TIKOverrides{DiscType: new("BD50")}},
	}
}

func TestUnit3DCategorySearchCollectsOrdinaryAndSpecialPages(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if !slices.Equal(query["categories[]"], []string{"2", "3"}) {
			t.Errorf("pagination lost broad category scope: %v", query)
		}
		switch query.Get("page") {
		case "1":
			next := query.Clone()
			next.Set("page", "2")
			_, _ = fmt.Fprintf(w, `{"data":[{"id":101,"attributes":{"name":"Example.Show.S01E02.1080p.WEB-DL-GRP","tmdb":123}}],"links":{"next":"https://samaritano.cc/api/torrents/filter?%s"}}`, next.Encode())
		case "2":
			_, _ = w.Write([]byte(`{"data":[{"id":102,"attributes":{"name":"Example.Show.S01E02.2160p.WEB-DL-GRP","tmdb":123}}],"links":{"next":null}}`))
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: handlerRewriteTransport{base: base, rt: server.Client().Transport}}
	registry := MustNewRegistry()
	definition, ok := registry.Lookup("SAM")
	if !ok {
		t.Fatal("SAM is not registered")
	}
	factory, ok := definition.(dupe.Factory)
	if !ok {
		t.Fatal("SAM lacks a duplicate adapter")
	}
	cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"SAM": {APIKey: "test-key"},
	}}}
	result := dupe.NewAdapter(factory, "SAM", cfg, client, api.NopLogger{}, registry).Search(t.Context(), api.DuplicateSubject{
		Identity:    api.ExternalIdentity{Category: api.CanonicalCategoryTV, TMDBID: 123},
		ReleaseName: "Example.Show.S01E02.1080p.WEB-DL-GRP",
		SeasonInt:   1,
		Anime:       true,
	})
	entries, _, searchErr := adapterEvidence(result)
	if searchErr != nil {
		t.Fatal(searchErr)
	}
	if len(entries) != 2 || entries[0].ID != "101" || entries[1].ID != "102" ||
		!result.SearchEvidence().EffectiveComplete() || result.SearchEvidence().Pages != 2 {
		t.Fatalf("broad paginated collection = %+v, evidence=%+v", entries, result.SearchEvidence())
	}
}

func TestUnit3DCategoryScopeInvalidatesNarrowSearchAuthority(t *testing.T) {
	t.Parallel()

	registry := MustNewRegistry()
	projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{
		Tracker: "SAM", Meta: unit3DCategoryScopeSubject(t, api.CanonicalCategoryTV),
	}, "", "", "")
	if failure != nil {
		t.Fatal(failure)
	}
	legacy, err := api.CanonicalWorkflowFingerprint(struct {
		Contract string
		Criteria api.TrackerDuplicateCriteria
		Scope    trackers.DupeSearchScope
	}{
		Contract: "duplicate-search/work-scope/v1",
		Criteria: projection.DuplicateCriteria,
		Scope:    trackers.DupeSearchScope{MaxPages: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(projection.DuplicateSearchFingerprint)) == "" || projection.DuplicateSearchFingerprint == legacy {
		t.Fatal("broad search retained the fingerprint of the previous narrow search")
	}
}
