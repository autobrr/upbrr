// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

const hdbClaimList = `
<div><strong>ALL titles on the list are also claimed for HDB.</strong><br>
<strong>Show</strong> -- <strong>Site(s) Uploaded To</strong> -- <strong>Group</strong> -- Notes<br>
Harbor Watch -- <span>HDB</span> | <strong>BTN</strong> -- NTb -- AMZN (aka: Harbour Watch (US))<br>
Orbital Station -- BTN -- BTNGRP -- NF<br>
Harbor Watch -- HDB -- Other -- conflicting owner<br>
Winter &amp; Summer -- HDB -- HDBGRP -- WEBRip<br></div>
<div>Reply from another user<br>
Other Show -- HDB -- GRP -- ignored reply<br></div>`

func TestExtractHDBClaimRecordsScopesListAndPreservesOwners(t *testing.T) {
	t.Parallel()
	records, complete := extractHDBClaimRecords(hdbClaimList)
	if !complete || len(records) != 4 {
		t.Fatalf("extractHDBClaimRecords() = %#v, complete=%t", records, complete)
	}
	if records[0].Title != "Harbor Watch" || records[0].Group != "NTb" || !slices.Equal(records[0].Sites, []string{"HDB", "BTN"}) {
		t.Fatalf("direct HDB record = %#v", records[0])
	}
	if !slices.Equal(records[0].Aliases, []string{"Harbour Watch (US)"}) {
		t.Fatalf("HDB alias = %#v", records[0].Aliases)
	}
	if records[1].Title != "Orbital Station" || !records[1].RelayedFromBTN {
		t.Fatalf("BTN relay record = %#v", records[1])
	}
	if records[2].Group != "Other" {
		t.Fatalf("conflicting owner was not preserved: %#v", records[2])
	}
	if records[3].Title != "Winter & Summer" {
		t.Fatalf("HTML entity was not preserved: %#v", records[3])
	}
	if _, complete := extractHDBClaimRecords(`<form><input name="username"></form>`); complete {
		t.Fatal("login response was accepted as a claim list")
	}
	if records, complete := extractHDBClaimRecords(`Show -- Site(s) Uploaded To -- Group<br>Only BTN -- BTN -- GRP<br>`); !complete || len(records) != 0 {
		t.Fatalf("HDB-empty list = %#v, complete=%t", records, complete)
	}
	if records, complete := extractHDBClaimRecords(`Show -- Site(s) Uploaded To -- Group<br>Harbor Watch -- HDB -- NTb<br>Harbor Watch -- ??? -- Other<br>`); complete || records != nil {
		t.Fatalf("partial list was accepted: %#v, complete=%t", records, complete)
	}
}

func TestHDBClaimsUseOnlyHDBSessionAndCache(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.RequestURI() != hdbClaimsPath {
			t.Errorf("request URI = %q", request.URL.RequestURI())
		}
		cookie, err := request.Cookie("session")
		if err != nil || cookie.Value != "hdb-test" {
			t.Errorf("HDB session cookie = %v, %v", cookie, err)
		}
		_, _ = w.Write([]byte(hdbClaimList))
	}))
	defer server.Close()

	cfg := hdbClaimConfig(t)
	d := New()
	d.baseURL, d.httpClient = server.URL, server.Client()
	checker := d.NewClaimChecker(cfg, nil)
	meta := hdbClaimSubject()
	for range 2 {
		claimed, err := checker.HasClaim(t.Context(), meta)
		if err != nil || !claimed {
			t.Fatalf("HasClaim() = %t, %v", claimed, err)
		}
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one fetch followed by a cache hit", requests)
	}
	cachePath := hdbClaimCachePathForTest(t, cfg)
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cachePath), "BTN_claimed_releases.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected BTN cache: %v", err)
	}
}

func TestHDBClaimsUseCanonicalTitlesAndProviderOverrides(t *testing.T) {
	t.Parallel()
	cfg := hdbConfigWithCache(t, []hdbClaimRecord{{
		Title:   "Harbor Watch",
		Aliases: []string{"Harbour Watch (US)"},
		Sites:   []string{"HDB"},
		Group:   "GRP",
	}})
	checker := New().NewClaimChecker(cfg, nil)

	meta := hdbClaimSubject()
	meta.ReleaseName = "Harbor.Watch.S01E01.1080p.WEB-DL-GRP"
	meta.Release.Title = "Different Canonical Title"
	claimed, err := checker.HasClaim(t.Context(), meta)
	if err != nil || claimed {
		t.Fatalf("release-name-only match = %t, %v", claimed, err)
	}
	meta.ProviderMetadata.TVDB = &api.TVDBMetadata{NameEnglish: "Harbor Watch"}
	claimed, err = checker.HasClaim(t.Context(), meta)
	if err != nil || !claimed {
		t.Fatalf("provider override match = %t, %v", claimed, err)
	}
	meta.ProviderMetadata.TVDB = nil
	meta.AlternateTitle = "AKA Harbour Watch (US)"
	claimed, err = checker.HasClaim(t.Context(), meta)
	if err != nil || !claimed {
		t.Fatalf("HDB list AKA match = %t, %v", claimed, err)
	}
}

func TestHDBYearQualifiedClaims(t *testing.T) {
	t.Parallel()
	claims := hdbClaimData{Records: []hdbClaimRecord{{
		Title: "Harbor Watch (2022)",
		Sites: []string{"HDB"},
		Group: "GRP",
	}}}
	for _, tt := range []struct {
		name      string
		release   api.ReleaseInfo
		alternate string
		providers api.SourceScopedMetadata
		want      bool
	}{
		{
			name:    "prepared year",
			release: api.ReleaseInfo{Title: "Harbor Watch", Year: 2022},
			want:    true,
		},
		{name: "wrong year", release: api.ReleaseInfo{Title: "Harbor Watch", Year: 2018}},
		{name: "missing year", release: api.ReleaseInfo{Title: "Harbor Watch"}},
		{
			name:      "resolved AKA year",
			release:   api.ReleaseInfo{Title: "Different Show", Year: 2022},
			alternate: "AKA Harbor Watch",
			want:      true,
		},
		{
			name:      "resolved AKA wrong year",
			release:   api.ReleaseInfo{Title: "Different Show", Year: 2018},
			alternate: "AKA Harbor Watch",
		},
		{
			name:      "IMDb year with omitted naming year",
			release:   api.ReleaseInfo{Title: "Harbor Watch"},
			providers: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{Title: "Harbor Watch", Year: 2022}},
			want:      true,
		},
		{
			name:      "IMDb AKA year",
			alternate: "AKA Harbor Watch",
			providers: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{
				Title: "Different Show",
				AKA:   "Harbor Watch",
				Year:  2022,
			}},
			want: true,
		},
		{
			name:      "TMDB AKA year",
			alternate: "AKA Harbor Watch",
			providers: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
				Title:        "Different Show",
				RetrievedAKA: "aka Harbor Watch",
				Year:         2022,
			}},
			want: true,
		},
		{name: "no cross-provider AKA year", providers: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{AKA: "Harbor Watch", Year: 2018}, TMDB: &api.TMDBMetadata{Title: "Different Show", Year: 2022}}},
		{
			name:      "TVDB pair",
			providers: api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{NameEnglish: "Harbor Watch", Year: 2022}},
			want:      true,
		},
		{
			name:      "TMDB pair",
			providers: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalTitle: "Harbor Watch", Year: 2022}},
			want:      true,
		},
		{
			name:      "TVmaze premiere",
			providers: api.SourceScopedMetadata{TVmaze: &api.TVmazeMetadata{Name: "Harbor Watch", Premiered: "2022-04-03"}},
			want:      true,
		},
		{
			name:      "no cross-provider year",
			release:   api.ReleaseInfo{Title: "Harbor Watch", Year: 2018},
			providers: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Title: "Different Show", Year: 2022}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			matches, _ := matchHDBClaimRecords(api.UploadSubject{
				Release:          tt.release,
				AlternateTitle:   tt.alternate,
				ProviderMetadata: tt.providers,
			}, claims)
			if got := len(matches) > 0; got != tt.want {
				t.Fatalf("matched = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestHDBAlternateClaimConflictBlocksOwnPrimaryClaim(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		alternate string
		providers api.SourceScopedMetadata
	}{
		{name: "resolved alternate", alternate: "AKA Portside Patrol (2022)"},
		{name: "IMDb AKA with omitted naming year", providers: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{
			Title: "Harbor Watch",
			AKA:   "Portside Patrol",
			Year:  2022,
		}}},
		{name: "TMDB AKA with omitted naming year", providers: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
			Title:        "Harbor Watch",
			RetrievedAKA: "Portside Patrol",
			Year:         2022,
		}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := hdbConfigWithCache(t, []hdbClaimRecord{
				{
					Title: "Harbor Watch",
					Sites: []string{"HDB"},
					Group: "NTb",
				},
				{
					Title:   "Localized Show",
					Aliases: []string{"Portside Patrol (2022)"},
					Sites:   []string{"HDB"},
					Group:   "Other",
				},
			})
			cfg.Trackers.Trackers = map[string]config.TrackerConfig{"HDB": {InternalGroups: config.CSVList{"NTb"}}}
			meta := hdbClaimSubject()
			meta.Tag = "NTb"
			meta.AlternateTitle = tt.alternate
			meta.ProviderMetadata = tt.providers
			if claimed, err := New().NewClaimChecker(cfg, nil).HasClaim(t.Context(), meta); err != nil || !claimed {
				t.Fatalf("conflicting alias = %t, %v; want blocked", claimed, err)
			}
		})
	}
}

func TestHDBInterruptedListPreservesStaleClaims(t *testing.T) {
	t.Parallel()
	interrupted := `<div>Show -- Site(s) Uploaded To -- Group<br>Harbor Watch -- HDB -- NTb<br>Unexpected content<br>Harbor Watch -- HDB -- Other<br></div>`
	if records, complete := extractHDBClaimRecords(interrupted); complete || records != nil {
		t.Fatalf("interrupted list accepted: %#v, complete=%t", records, complete)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(interrupted))
	}))
	defer server.Close()
	cfg := hdbClaimConfig(t)
	cfg.Trackers.Trackers = map[string]config.TrackerConfig{"HDB": {InternalGroups: config.CSVList{"NTb"}}}
	d := New()
	d.baseURL, d.httpClient = server.URL, server.Client()
	path := hdbClaimCachePathForTest(t, cfg)
	writeHDBClaimCacheForTest(t, path, server.URL+hdbClaimsPath,
		[]hdbClaimRecord{{
			Title: "Harbor Watch",
			Sites: []string{"HDB"},
			Group: "NTb",
		}}, time.Now().Add(-49*time.Hour))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	meta := hdbClaimSubject()
	meta.Tag = "NTb"
	if claimed, err := d.NewClaimChecker(cfg, nil).HasClaim(t.Context(), meta); err != nil || !claimed {
		t.Fatalf("stale claim = %t, %v; want blocked", claimed, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("incomplete fetch replaced stale cache")
	}
}

func TestHDBCacheReplacement(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "claims.json")
	for _, payload := range []string{"old cache", "new cache"} {
		if err := writeHDBClaimCacheFile(path, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != payload {
			t.Fatalf("cache = %q, %v", got, err)
		}
	}
	if err := writeHDBClaimCacheFile(dir, []byte("replacement")); err == nil {
		t.Fatal("directory replacement succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new cache" {
		t.Fatalf("failed replacement changed existing cache: %q, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary cache files remain: %v", entries)
	}
}

func TestHDBInternalGroupBypassRequiresFreshDirectUnambiguousClaim(t *testing.T) {
	t.Parallel()
	base := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"HDB": {InternalGroups: config.CSVList{"NTb"}},
	}}}
	meta := hdbClaimSubject()
	meta.Tag = "-NTb"
	cases := []struct {
		name    string
		records []hdbClaimRecord
		fresh   bool
		want    bool
		aliases bool
	}{
		{
			name:  "fresh direct own group bypasses",
			fresh: true,
			records: []hdbClaimRecord{{
				Title: "Harbor Watch",
				Sites: []string{"HDB"},
				Group: "NTb",
			}},
		},
		{
			name: "stale direct own group blocks",
			records: []hdbClaimRecord{{
				Title: "Harbor Watch",
				Sites: []string{"HDB"},
				Group: "NTb",
			}},
			want: true,
		},
		{
			name:  "conflicting owner blocks",
			fresh: true,
			records: []hdbClaimRecord{{
				Title: "Harbor Watch",
				Sites: []string{"HDB"},
				Group: "NTb",
			}, {
				Title: "Harbor Watch",
				Sites: []string{"HDB"},
				Group: "Other",
			}},
			want: true,
		},
		{
			name:    "conflicting alias owners block",
			fresh:   true,
			aliases: true,
			records: []hdbClaimRecord{{
				Title:   "Localized One",
				Aliases: []string{"Harbor Watch"},
				Sites:   []string{"HDB"},
				Group:   "NTb",
			}, {
				Title:   "Localized Two",
				Aliases: []string{"Harbor Watch"},
				Sites:   []string{"HDB"},
				Group:   "Other",
			}},
			want: true,
		},
		{
			name:  "relay cannot prove HDB owner",
			fresh: true,
			records: []hdbClaimRecord{{
				Title:          "Harbor Watch",
				Sites:          []string{"BTN"},
				Group:          "NTb",
				RelayedFromBTN: true,
			}},
			want: true,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			cfg.MainSettings.DBPath = filepath.Join(t.TempDir(), "upbrr.db")
			checker := &claimChecker{
				cfg:    cfg,
				logger: api.NopLogger{},
				fetchOverride: func(context.Context) (hdbClaimData, error) {
					if !tt.fresh {
						return hdbClaimData{}, errors.New("HDB unavailable")
					}
					return hdbClaimData{Records: tt.records, Complete: true}, nil
				},
			}
			caseMeta := meta
			if tt.aliases {
				caseMeta.Release.Title = "Different Canonical Title"
				caseMeta.AlternateTitle = "AKA Harbor Watch"
			}
			if !tt.fresh {
				checker.endpoint = "https://hdb.example" + hdbClaimsPath
				writeHDBClaimCacheForTest(t, hdbClaimCachePathForTest(t, checker.cfg), checker.endpoint, tt.records, time.Now().Add(-49*time.Hour))
			}
			claimed, err := checker.HasClaim(t.Context(), caseMeta)
			if err != nil || claimed != tt.want {
				t.Fatalf("HasClaim() = %t, %v; want %t", claimed, err, tt.want)
			}
		})
	}
}

func TestHDBClaimsFailOpenButPropagateCancellation(t *testing.T) {
	t.Parallel()
	checker := &claimChecker{
		cfg:           config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")}},
		logger:        api.NopLogger{},
		fetchOverride: func(context.Context) (hdbClaimData, error) { return hdbClaimData{}, errors.New("HDB unavailable") },
	}
	claimed, err := checker.HasClaim(t.Context(), hdbClaimSubject())
	if err != nil || claimed {
		t.Fatalf("unavailable claims = %t, %v", claimed, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := checker.HasClaim(canceled, hdbClaimSubject()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestInvalidHDBClaimCacheCannotAuthorizeBypass(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		mutate func(*hdbClaimedShowsCache)
	}{
		{name: "wrong source", mutate: func(cache *hdbClaimedShowsCache) { cache.SourceURL = "https://other.example" }},
		{name: "future timestamp", mutate: func(cache *hdbClaimedShowsCache) { cache.FetchedAt = time.Now().Add(time.Hour).Unix() }},
		{name: "invalid record", mutate: func(cache *hdbClaimedShowsCache) {
			cache.Claims = append(cache.Claims, hdbClaimRecord{Title: "Harbor Watch", Sites: []string{"unknown"}})
		}},
		{name: "corrupt JSON", mutate: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "claims.json")
			endpoint := "https://hdb.example" + hdbClaimsPath
			if tt.mutate == nil {
				if err := os.WriteFile(path, []byte(`{bad json`), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				writeHDBClaimCacheForTest(t, path, endpoint, []hdbClaimRecord{{
					Title: "Harbor Watch",
					Sites: []string{"HDB"},
					Group: "NTb",
				}}, time.Now())
				payload, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var cache hdbClaimedShowsCache
				if err := json.Unmarshal(payload, &cache); err != nil {
					t.Fatal(err)
				}
				tt.mutate(&cache)
				payload, err = json.Marshal(cache)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, payload, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			checker := &claimChecker{
				endpoint: endpoint,
				logger:   api.NopLogger{},
				fetchOverride: func(context.Context) (hdbClaimData, error) {
					return hdbClaimData{}, errors.New("HDB unavailable")
				},
			}
			if claims, err := checker.loadHDBClaims(t.Context(), path, hdbClaimsCacheTTL); err == nil || claims.FreshStructured {
				t.Fatalf("invalid cache authorized fresh data: %#v, %v", claims, err)
			}
		})
	}
}

func TestHDBClaimWindowAndApplicability(t *testing.T) {
	t.Parallel()
	aired := time.Now().UTC().Add(-49 * time.Hour)
	meta := hdbClaimSubject()
	meta.TVDBAiredDate, meta.TVDBAirsTime, meta.TVDBAirsTimezone = aired.Format("2006-01-02"), aired.Format("15:04"), "UTC"
	if expired, _, _ := hdbClaimWindowExpired(meta, hdbClaimDefaultGrace); !expired {
		t.Fatal("precise airtime outside 48-hour window remained active")
	}
	meta.TVDBAirsTime, meta.TVDBAirsTimezone = "", ""
	meta.TVDBAiredDate = time.Now().UTC().Add(-36 * time.Hour).Format("2006-01-02")
	if expired, _, _ := hdbClaimWindowExpired(meta, hdbClaimDefaultGrace); expired {
		t.Fatal("date-only grace was not applied")
	}
	if applies, known := hdbClaimPolicyApplies(api.UploadSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV}, Source: "WEB-DL"}); !applies || !known {
		t.Fatalf("WEB applicability = (%t, %t)", applies, known)
	}
	if applies, known := hdbClaimPolicyApplies(api.UploadSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV}, Source: "HDTV"}); applies || !known {
		t.Fatalf("non-WEB applicability = (%t, %t)", applies, known)
	}
}

func hdbClaimSubject() api.UploadSubject {
	return api.UploadSubject{
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		Type:     "WEB-DL",
		Release: api.ReleaseInfo{
			Title:   "Harbor Watch",
			Season:  1,
			Episode: 1,
		},
		SeasonInt:  1,
		EpisodeInt: 1,
	}
}

func hdbClaimConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")}}
	cookiePath, err := db.CookiePath(cfg.MainSettings.DBPath, "HDB.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cookiePath, []byte(`{"session":"hdb-test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func hdbConfigWithCache(t *testing.T, records []hdbClaimRecord) config.Config {
	t.Helper()
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")}}
	writeHDBClaimCacheForTest(t, hdbClaimCachePathForTest(t, cfg), hdbBaseURL+hdbClaimsPath, records, time.Now())
	return cfg
}

func hdbClaimCachePathForTest(t *testing.T, cfg config.Config) string {
	t.Helper()
	path, err := hdbClaimsCachePath(cfg.MainSettings.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func writeHDBClaimCacheForTest(t *testing.T, path, endpoint string, records []hdbClaimRecord, fetchedAt time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(hdbClaimedShowsCache{
		Version:   hdbClaimsCacheVersion,
		FetchedAt: fetchedAt.Unix(),
		SourceURL: endpoint,
		Complete:  true,
		Claims:    records,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}
