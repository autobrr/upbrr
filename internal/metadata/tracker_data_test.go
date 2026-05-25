// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/trackerdata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type stubTrackerLookup struct {
	results map[string]trackerdata.Result
	calls   []string
	delays  map[string]time.Duration
	mu      sync.Mutex
}

func (s *stubTrackerLookup) Lookup(
	ctx context.Context,
	tracker string,
	_ string,
	_ api.PreparedMetadata,
	_ string,
	_ bool,
	_ bool,
) (trackerdata.Result, error) {
	s.mu.Lock()
	s.calls = append(s.calls, tracker)
	delay := s.delays[tracker]
	s.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			return trackerdata.Result{}, ctx.Err()
		case <-time.After(delay):
		}
	}

	if value, ok := s.results[tracker]; ok {
		return value, nil
	}
	return trackerdata.Result{}, nil
}

func (s *stubTrackerLookup) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := make([]string, len(s.calls))
	copy(cloned, s.calls)
	return cloned
}

func trackerRecordFor(trackerData []api.TrackerMetadata, tracker string) (api.TrackerMetadata, bool) {
	for _, record := range trackerData {
		if strings.EqualFold(record.Tracker, tracker) {
			return record, true
		}
	}
	return api.TrackerMetadata{}, false
}

func TestTrackerLookupFileNameHonorsSkipWithoutTrackerID(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath: `D:\Movies\From.S04E01.2160p.WEB.h265-ETHEL.mkv`,
	}

	if got := trackerLookupFileName(meta, "", true); got != "" {
		t.Fatalf("expected filename lookup to be skipped, got %q", got)
	}
}

func TestTrackerLookupFileNameKeepsFilenameWhenDefaultEnabled(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath: `D:\Movies\From.S04E01.2160p.WEB.h265-ETHEL.mkv`,
	}

	if got := trackerLookupFileName(meta, "", false); got != "From.S04E01.2160p.WEB.h265-ETHEL.mkv" {
		t.Fatalf("expected filename lookup to remain enabled by default, got %q", got)
	}
}

func TestTrackerLookupFileNameKeepsFilenameWithTrackerID(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath: `D:\Movies\From.S04E01.2160p.WEB.h265-ETHEL.mkv`,
	}

	if got := trackerLookupFileName(meta, "12345", true); got != "From.S04E01.2160p.WEB.h265-ETHEL.mkv" {
		t.Fatalf("expected filename to remain available with tracker id, got %q", got)
	}
}

func TestEnrichTrackerDataStopsAfterFirstPriorityIDWinner(t *testing.T) {
	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{
		results: map[string]trackerdata.Result{
			"ANT": {TMDBID: 123, IMDBID: 456, TrackerID: "101"},
			"HDB": {TMDBID: 999, TrackerID: "202"},
		},
		delays: map[string]time.Duration{
			"ANT": 60 * time.Millisecond,
			"HDB": 5 * time.Millisecond,
		},
	}
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"ANT": {APIKey: "ant-key"},
				"HDB": {Username: "user", Passkey: "pass"},
			},
		},
	}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup))

	meta := api.PreparedMetadata{
		SourcePath: `D:\Movies\A.Better.Life.2011.BluRay.1080p.DTS.x264-CHD`,
		TrackerIDs: map[string]string{
			"ant": "101",
			"hdb": "202",
		},
	}

	result, err := svc.EnrichTrackerData(context.Background(), meta)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	calls := lookup.Calls()
	if len(calls) == 0 {
		t.Fatalf("expected at least one lookup call")
	}
	winner, found := trackerRecordFor(result.TrackerData, "HDB")
	if !found {
		t.Fatalf("expected HDB tracker winner record, got %v", result.TrackerData)
	}
	if winner.TMDBID == 0 && winner.IMDBID == 0 && winner.TVDBID == 0 {
		t.Fatalf("expected metadata ids on winner record")
	}
	if len(calls) != 1 || !strings.EqualFold(calls[0], "HDB") {
		t.Fatalf("expected strict priority stop after HDB, calls=%v", calls)
	}
	if len(repo.trackerMetadata) == 0 {
		t.Fatalf("expected persisted tracker records")
	}
}

func TestEnrichTrackerDataPreferredTrackerOverridesStaticPriority(t *testing.T) {
	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{
		results: map[string]trackerdata.Result{
			"ANT": {TMDBID: 123, TrackerID: "101"},
			"HDB": {TMDBID: 999, TrackerID: "202"},
		},
		delays: map[string]time.Duration{
			"ANT": 40 * time.Millisecond,
			"HDB": 5 * time.Millisecond,
		},
	}
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			PreferredTracker: "HDB",
			Trackers: map[string]config.TrackerConfig{
				"ANT": {APIKey: "ant-key"},
				"HDB": {Username: "user", Passkey: "pass"},
			},
		},
	}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup))

	meta := api.PreparedMetadata{
		SourcePath: `D:\Movies\A.Better.Life.2011.BluRay.1080p.DTS.x264-CHD`,
		TrackerIDs: map[string]string{
			"ant": "101",
			"hdb": "202",
		},
	}

	result, err := svc.EnrichTrackerData(context.Background(), meta)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	calls := lookup.Calls()
	if len(calls) != 1 || !strings.EqualFold(calls[0], "HDB") {
		t.Fatalf("expected preferred tracker HDB queried first and to stop after winner, calls=%v", calls)
	}
	winner, found := trackerRecordFor(result.TrackerData, "HDB")
	if !found || winner.TMDBID == 0 {
		t.Fatalf("expected HDB winner with metadata ids, got %v", result.TrackerData)
	}
}

func TestEnrichTrackerDataUsesConcurrentWinnerWithoutClientTrackerIDs(t *testing.T) {
	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{
		results: map[string]trackerdata.Result{
			"ANT": {TMDBID: 123, IMDBID: 456, TrackerID: "101"},
			"HDB": {TMDBID: 999, TrackerID: "202"},
		},
		delays: map[string]time.Duration{
			"ANT": 60 * time.Millisecond,
			"HDB": 5 * time.Millisecond,
		},
	}
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"ANT": {APIKey: "ant-key"},
				"HDB": {Username: "user", Passkey: "pass"},
			},
		},
	}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup))

	meta := api.PreparedMetadata{
		SourcePath: `D:\Movies\A.Better.Life.2011.BluRay.1080p.DTS.x264-CHD`,
		Trackers:   []string{"ANT", "HDB"},
	}

	result, err := svc.EnrichTrackerData(context.Background(), meta)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	winner, found := trackerRecordFor(result.TrackerData, "HDB")
	if !found {
		t.Fatalf("expected HDB tracker winner record from fastest concurrent lookup, got %v", result.TrackerData)
	}
	if winner.TMDBID == 0 && winner.IMDBID == 0 && winner.TVDBID == 0 {
		t.Fatalf("expected metadata ids on winner record")
	}
}

func TestEnrichTrackerDataContinuesUntilIDsFound(t *testing.T) {
	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{
		results: map[string]trackerdata.Result{
			"ANT": {Description: "desc only", TrackerID: "101"},
			"HDB": {IMDBID: 1554091, TrackerID: "202"},
			"PTP": {TMDBID: 55720, TrackerID: "303"},
		},
		delays: map[string]time.Duration{
			"ANT": 5 * time.Millisecond,
			"HDB": 40 * time.Millisecond,
			"PTP": 75 * time.Millisecond,
		},
	}
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"ANT": {APIKey: "ant-key"},
				"HDB": {Username: "user", Passkey: "pass"},
				"PTP": {PTPAPIUser: "user", PTPAPIKey: "key"},
			},
		},
	}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup))

	meta := api.PreparedMetadata{
		SourcePath: `D:\Movies\A.Better.Life.2011.BluRay.1080p.DTS.x264-CHD`,
		TrackerIDs: map[string]string{
			"ant": "101",
			"hdb": "202",
			"ptp": "303",
		},
	}

	result, err := svc.EnrichTrackerData(context.Background(), meta)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	calls := lookup.Calls()
	if len(calls) == 0 {
		t.Fatalf("expected lookup calls")
	}
	winner, found := trackerRecordFor(result.TrackerData, "HDB")
	if !found {
		t.Fatalf("expected HDB id winner record, got %v", result.TrackerData)
	}
	if winner.IMDBID == 0 {
		t.Fatalf("expected HDB imdb id to be set")
	}
}

func TestApplyTrackerClaimsBlocksAitherAndCachesClaims(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.URL.Path; got != "/api/internals/claim" {
			t.Fatalf("unexpected path %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer aither-key" {
			t.Fatalf("unexpected auth header %q", got)
		}
		_, _ = w.Write([]byte(`{
			"data":[
				{"attributes":{"title":"Example Show","season":2,"tmdb_id":4242,"categories":["2"],"resolutions":["3"],"types":["4"]}}
			],
			"meta":{"next_cursor":""}
		}`))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "db.sqlite")},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {
					APIKey:      "aither-key",
					AnnounceURL: server.URL + "/announce",
				},
			},
		},
	}

	svc := NewService(&fakeRepo{}, WithConfig(cfg))
	meta := api.PreparedMetadata{
		SourcePath: "/media/Example.Show.S02E03.mkv",
		Trackers:   []string{"AITHER"},
		Type:       "WEBDL",
		Release:    api.ReleaseInfo{Resolution: "1080p", Season: 2},
		SeasonInt:  2,
		ExternalIDs: api.ExternalIDs{
			TMDBID: 4242,
		},
	}

	result, err := svc.applyTrackerClaims(context.Background(), meta)
	if err != nil {
		t.Fatalf("apply tracker claims: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected one claim fetch, got %d", requests)
	}
	if got := result.BlockedTrackers["AITHER"]; len(got) != 1 || got[0] != api.TrackerBlockReasonClaim {
		t.Fatalf("expected AITHER claim block, got %#v", result.BlockedTrackers)
	}

	cachePath := filepath.Join(tempDir, "cache", "banned", "AITHER_claimed_releases.json")
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("expected claims cache file at %s: %v", cachePath, err)
	}

	cacheData, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read claims cache: %v", err)
	}
	if !strings.Contains(string(cacheData), `"resolutions": [`) || !strings.Contains(string(cacheData), `"1080P"`) {
		t.Fatalf("expected semantic resolutions in cache, got %s", string(cacheData))
	}
	if !strings.Contains(string(cacheData), `"types": [`) || !strings.Contains(string(cacheData), `"WEBDL"`) {
		t.Fatalf("expected semantic types in cache, got %s", string(cacheData))
	}

	result, err = svc.applyTrackerClaims(context.Background(), meta)
	if err != nil {
		t.Fatalf("apply tracker claims from cache: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected cached claims to avoid refetch, got %d requests", requests)
	}
	if got := result.BlockedTrackers["AITHER"]; len(got) != 1 || got[0] != api.TrackerBlockReasonClaim {
		t.Fatalf("expected cached AITHER claim block, got %#v", result.BlockedTrackers)
	}
}

func TestApplyTrackerClaimsDoesNotBlockOnSemanticMismatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"data":[
				{"attributes":{"title":"Example Show","season":2,"tmdb_id":4242,"resolutions":["2"],"types":["4"]}}
			],
			"meta":{"next_cursor":""}
		}`))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "db.sqlite")},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {
					APIKey:      "aither-key",
					AnnounceURL: server.URL + "/announce",
				},
			},
		},
	}

	svc := NewService(&fakeRepo{}, WithConfig(cfg))
	meta := api.PreparedMetadata{
		SourcePath: "/media/Example.Show.S02E03.mkv",
		Trackers:   []string{"AITHER"},
		Type:       "WEBDL",
		Release:    api.ReleaseInfo{Resolution: "1080p", Season: 2},
		SeasonInt:  2,
		ExternalIDs: api.ExternalIDs{
			TMDBID: 4242,
		},
	}

	result, err := svc.applyTrackerClaims(context.Background(), meta)
	if err != nil {
		t.Fatalf("apply tracker claims: %v", err)
	}
	if len(result.BlockedTrackers["AITHER"]) != 0 {
		t.Fatalf("expected no claim block for mismatched semantic resolution, got %#v", result.BlockedTrackers)
	}
}

func TestApplyTrackerClaimsUsesRequestedBTNWhenTrackerIDsContainDifferentTracker(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "db.sqlite")},
	}

	svc := NewService(&fakeRepo{}, WithConfig(cfg))

	cachePath := filepath.Join(tempDir, "cache", "banned", "BTN_claimed_releases.json")
	if err := writeBTNClaimedCacheFixture(cachePath, time.Now().Unix(), map[string]struct{}{
		normalizeBTNTitle("Australian Survivor"): {},
	}); err != nil {
		t.Fatalf("write btn claims cache: %v", err)
	}

	meta := api.PreparedMetadata{
		SourcePath:       `D:\TV\Australian.Survivor.S14E19.1080p.WEB-DL.AAC2.0.H.264-WH.mkv`,
		Trackers:         []string{"BTN"},
		TrackerIDs:       map[string]string{"tvv": "12345"},
		TVDBAiredDate:    time.Now().Add(-12 * time.Hour).UTC().Format("2006-01-02"),
		TVDBAirsTime:     "20:00",
		TVDBAirsTimezone: "UTC",
		SeasonInt:        14,
		EpisodeInt:       19,
		ReleaseName:      "Australian Survivor S14E19 Sold the Dream 1080p WEB-DL AAC 2.0-WH",
		Filename:         "Australian.Survivor.S14E19.1080p.WEB-DL.AAC2.0.H.264-WH.mkv",
		ExternalMetadata: api.ExternalMetadata{
			TVDB: &api.TVDBMetadata{Name: "Australian Survivor"},
		},
	}

	result, err := svc.applyTrackerClaims(context.Background(), meta)
	if err != nil {
		t.Fatalf("apply tracker claims: %v", err)
	}
	if got := result.BlockedTrackers["BTN"]; len(got) != 1 || got[0] != api.TrackerBlockReasonClaim {
		t.Fatalf("expected BTN claim block, got %#v", result.BlockedTrackers)
	}
	failures := result.TrackerRuleFailures["BTN"]
	if len(failures) != 1 {
		t.Fatalf("expected BTN claim rule failure, got %#v", result.TrackerRuleFailures)
	}
	if failures[0].Rule != trackerClaimRuleActive {
		t.Fatalf("expected BTN claim rule %q, got %#v", trackerClaimRuleActive, failures)
	}
	if !strings.Contains(strings.ToLower(failures[0].Reason), "hours remain") {
		t.Fatalf("expected BTN claim failure reason to include hours remaining, got %#v", failures)
	}
}

func TestResolveTrackerClaimProviderSupportsKnownTrackers(t *testing.T) {
	t.Parallel()

	btnProvider, ok := resolveTrackerClaimProvider("btn")
	if !ok {
		t.Fatalf("expected BTN provider")
	}
	if _, ok := btnProvider.(btnTrackerClaimProvider); !ok {
		t.Fatalf("expected BTN provider type, got %T", btnProvider)
	}

	aitherProvider, ok := resolveTrackerClaimProvider("AITHER")
	if !ok {
		t.Fatalf("expected AITHER provider")
	}
	if _, ok := aitherProvider.(apiTrackerClaimProvider); !ok {
		t.Fatalf("expected API provider type, got %T", aitherProvider)
	}

	if _, ok := resolveTrackerClaimProvider("PTP"); ok {
		t.Fatalf("did not expect provider for unsupported tracker")
	}
}

func TestBTNTrackerClaimProviderUsesSharedCachePathAnd48HourTTL(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	provider := btnTrackerClaimProvider{}

	cachePath, err := provider.cachePath(filepath.Join(tempDir, "db.sqlite"), "BTN")
	if err != nil {
		t.Fatalf("cache path: %v", err)
	}

	expected := filepath.Join(tempDir, "cache", "banned", "BTN_claimed_releases.json")
	if cachePath != expected {
		t.Fatalf("expected cache path %q, got %q", expected, cachePath)
	}
	if provider.cacheTTL() != 48*time.Hour {
		t.Fatalf("expected BTN cache ttl 48h, got %s", provider.cacheTTL())
	}
}

func TestLoadBTNClaimedTitlesUsesFreshCacheWithin48Hours(t *testing.T) {
	tempDir := t.TempDir()
	cachePath := filepath.Join(tempDir, "cache", "banned", "BTN_claimed_releases.json")
	cached := map[string]struct{}{normalizeBTNTitle("Cached Show"): {}}
	if err := writeBTNClaimedCacheFixture(cachePath, time.Now().Add(-47*time.Hour).Unix(), cached); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	clientCalls := 0
	restore := swapDefaultTransport(roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		clientCalls++
		return nil, context.Canceled
	}))
	defer restore()

	svc := NewService(&fakeRepo{}, WithConfig(config.Config{}))
	claimed, err := svc.loadBTNClaimedTitles(context.Background(), cachePath, 48*time.Hour)
	if err != nil {
		t.Fatalf("load btn claimed titles: %v", err)
	}
	if clientCalls != 0 {
		t.Fatalf("expected fresh cache to avoid fetch, got %d requests", clientCalls)
	}
	if _, ok := claimed[normalizeBTNTitle("Cached Show")]; !ok {
		t.Fatalf("expected cached title, got %#v", claimed)
	}
}

func TestLoadBTNClaimedTitlesRefetchesAfter48Hours(t *testing.T) {
	tempDir := t.TempDir()
	cachePath := filepath.Join(tempDir, "cache", "banned", "BTN_claimed_releases.json")
	cached := map[string]struct{}{normalizeBTNTitle("Cached Show"): {}}
	if err := writeBTNClaimedCacheFixture(cachePath, time.Now().Add(-49*time.Hour).Unix(), cached); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	clientCalls := 0
	restore := swapDefaultTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		clientCalls++
		body := io.NopCloser(strings.NewReader(`
			<table id="post1405482">
			  <tr><td><div id="content1405482" class="postcontent">
			    <strong>Current Shows:</strong><br>
			    Fresh Show -- BTN<br>
			  </div></td></tr>
			</table>`))
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       body,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}))
	defer restore()

	svc := NewService(&fakeRepo{}, WithConfig(config.Config{}))
	claimed, err := svc.loadBTNClaimedTitles(context.Background(), cachePath, 48*time.Hour)
	if err != nil {
		t.Fatalf("load btn claimed titles: %v", err)
	}
	if clientCalls != 2 {
		t.Fatalf("expected stale cache to trigger session validation and fetch, got %d requests", clientCalls)
	}
	if _, ok := claimed[normalizeBTNTitle("Fresh Show")]; !ok {
		t.Fatalf("expected refetched title, got %#v", claimed)
	}

	cacheData, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if !strings.Contains(string(cacheData), "fresh show") {
		t.Fatalf("expected refreshed cache data, got %s", string(cacheData))
	}
}

func TestFetchBTNClaimedTitlesStopsAfterLoginFailure(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "db.sqlite")},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"BTN": {
					Username: "user",
					Password: "pass",
				},
			},
		},
	}

	requests := make([]string, 0, 3)
	restore := swapDefaultTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.String())
		switch {
		case strings.Contains(req.URL.String(), "/user.php"):
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("login required")),
				Header:     make(http.Header),
				Request: &http.Request{
					URL: mustParseURL(t, "https://broadcasthe.net/login.php"),
				},
			}, nil
		case strings.Contains(req.URL.String(), "/login.php"):
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader("forbidden")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		default:
			t.Fatalf("unexpected request to %s", req.URL.String())
			return nil, nil
		}
	}))
	defer restore()

	svc := NewService(&fakeRepo{}, WithConfig(cfg))
	claimed, err := svc.fetchBTNClaimedTitles(context.Background())
	if err == nil {
		t.Fatalf("expected login failure")
	}
	if len(claimed) != 0 {
		t.Fatalf("expected no claimed titles, got %#v", claimed)
	}
	if len(requests) != 2 {
		t.Fatalf("expected session validation and login request only, got %d requests: %v", len(requests), requests)
	}
	if strings.Contains(strings.Join(requests, " "), "forums.php") {
		t.Fatalf("did not expect claimed-thread fetch after login failure, got %v", requests)
	}
}

func TestEnrichTrackerDataSkipsLookupWhenStoredFresh(t *testing.T) {
	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{}
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"ANT": {APIKey: "ant-key"},
			},
		},
	}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup))

	meta := api.PreparedMetadata{
		SourcePath:      `D:\Movies\A.Better.Life.2011.BluRay.1080p.DTS.x264-CHD`,
		StoredDataFresh: true,
		Trackers:        []string{"ANT"},
	}

	result, err := svc.EnrichTrackerData(context.Background(), meta)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if len(lookup.Calls()) != 0 {
		t.Fatalf("expected no tracker lookups, got %v", lookup.Calls())
	}
	if len(result.TrackerData) != 0 {
		t.Fatalf("expected no tracker data changes, got %d records", len(result.TrackerData))
	}
}

func TestEnrichTrackerDataDeprioritizesBTNWhenKeepingImages(t *testing.T) {
	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{
		results: map[string]trackerdata.Result{
			"BHD": {TMDBID: 513053, TrackerID: "513053", Description: "desc", Images: []bbcode.Image{{RawURL: "https://img.example/a.jpg"}}},
			"BTN": {IMDBID: 39050141, TrackerID: "2167358"},
		},
		delays: map[string]time.Duration{
			"BHD": 5 * time.Millisecond,
			"BTN": 40 * time.Millisecond,
		},
	}
	longToken := strings.Repeat("a", minTrackerTokenLen)
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"BTN": {APIKey: strings.Repeat("b", minTrackerTokenLen)},
				"BHD": {APIKey: longToken, BhdRSSKey: longToken},
			},
		},
	}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup))

	meta := api.PreparedMetadata{
		SourcePath: `D:\temp\Love.Through.A.Prism.S01.1080p.NF.WEB-DL.DDP5.1.DV.H.265-ppkhoa`,
		TrackerIDs: map[string]string{
			"btn": "2167358",
			"bhd": "513053",
		},
		Options: api.UploadOptions{KeepImages: true},
	}

	result, err := svc.EnrichTrackerData(context.Background(), meta)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	winner, found := trackerRecordFor(result.TrackerData, "BHD")
	if !found {
		t.Fatalf("expected BHD tracker winner record, got %v", result.TrackerData)
	}
	if winner.TMDBID == 0 {
		t.Fatalf("expected BHD winner metadata id")
	}
}

func TestEnrichTrackerDataKeepsBTNAsFallbackWhenKeepingImages(t *testing.T) {
	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{
		results: map[string]trackerdata.Result{
			"BHD": {Description: "desc only", TrackerID: "513053"},
			"BTN": {IMDBID: 39050141, TrackerID: "2167358"},
		},
		delays: map[string]time.Duration{
			"BHD": 5 * time.Millisecond,
			"BTN": 35 * time.Millisecond,
		},
	}
	longToken := strings.Repeat("a", minTrackerTokenLen)
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"BTN": {APIKey: strings.Repeat("b", minTrackerTokenLen)},
				"BHD": {APIKey: longToken, BhdRSSKey: longToken},
			},
		},
	}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup))

	meta := api.PreparedMetadata{
		SourcePath: `D:\temp\Love.Through.A.Prism.S01.1080p.NF.WEB-DL.DDP5.1.DV.H.265-ppkhoa`,
		TrackerIDs: map[string]string{
			"btn": "2167358",
			"bhd": "513053",
		},
		Options: api.UploadOptions{KeepImages: true},
	}

	result, err := svc.EnrichTrackerData(context.Background(), meta)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	winner, found := trackerRecordFor(result.TrackerData, "BTN")
	if !found {
		t.Fatalf("expected BTN fallback id winner record, got %v", result.TrackerData)
	}
	if winner.IMDBID == 0 {
		t.Fatalf("expected BTN imdb id to be set")
	}
}

func TestEnrichTrackerDataKeepsDescriptionFromSingleTracker(t *testing.T) {
	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{
		results: map[string]trackerdata.Result{
			"ANT": {Description: "ant description", TrackerID: "101"},
			"HDB": {Description: "hdb description", IMDBID: 1554091, TrackerID: "202"},
		},
		delays: map[string]time.Duration{
			"ANT": 5 * time.Millisecond,
			"HDB": 40 * time.Millisecond,
		},
	}
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"ANT": {APIKey: "ant-key"},
				"HDB": {Username: "user", Passkey: "pass"},
			},
		},
	}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup))

	meta := api.PreparedMetadata{
		SourcePath: `D:\Movies\A.Better.Life.2011.BluRay.1080p.DTS.x264-CHD`,
		TrackerIDs: map[string]string{
			"ant": "101",
			"hdb": "202",
		},
	}

	result, err := svc.EnrichTrackerData(context.Background(), meta)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	descriptionTrackers := make([]string, 0)
	for _, record := range result.TrackerData {
		if strings.TrimSpace(record.Description) == "" && len(record.ImageURLs) == 0 {
			continue
		}
		descriptionTrackers = append(descriptionTrackers, strings.ToUpper(record.Tracker))
	}
	if len(descriptionTrackers) != 1 {
		t.Fatalf("expected exactly one tracker with description/images, got %d (%v)", len(descriptionTrackers), descriptionTrackers)
	}
	if descriptionTrackers[0] != "HDB" {
		t.Fatalf("expected highest-priority tracker to keep description/images, got %v", descriptionTrackers)
	}
}

func TestMetadataTrackerPriorityPlacesPreferredTrackersBeforeRemainingUnit3D(t *testing.T) {
	result := trackers.TrackerPriority()
	expectedPrefix := []string{"aither", "ulcx", "lst", "blu", "oe", "btn", "bhd", "hdb", "ant", "rf", "otw", "yus", "dp", "sp", "ptp"}

	prevIdx := -1
	for _, tracker := range expectedPrefix {
		idx := indexOfTracker(result, tracker)
		if idx < 0 {
			t.Fatalf("expected preferred tracker %s in %v", tracker, result)
		}
		if idx <= prevIdx {
			t.Fatalf("expected preferred trackers in order %v, got %v", expectedPrefix, result)
		}
		prevIdx = idx
	}

	remaining := make([]string, 0)
	for _, tracker := range trackers.Unit3DTrackers() {
		lower := strings.ToLower(tracker)
		if hasTracker(expectedPrefix, lower) {
			continue
		}
		remaining = append(remaining, lower)
	}

	if len(result) != len(expectedPrefix)+len(remaining) {
		t.Fatalf("expected preferred + remaining unit3d trackers only, got %v", result)
	}

	for idx, tracker := range remaining {
		gotIdx := len(expectedPrefix) + idx
		if result[gotIdx] != tracker {
			t.Fatalf("expected remaining unit3d trackers appended at end in sorted order %v, got %v", remaining, result)
		}
	}
}

func TestApplyPreferredTrackerMovesTrackerToFront(t *testing.T) {
	t.Parallel()

	trackers := []string{"BHD", "AITHER", "PTP"}
	result := applyPreferredTracker(trackers, "ptp")
	expected := []string{"PTP", "BHD", "AITHER"}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
}

func TestApplyPreferredTrackerNoopForUnknown(t *testing.T) {
	t.Parallel()

	trackers := []string{"BHD", "AITHER", "PTP"}
	result := applyPreferredTracker(trackers, "BLU")
	expected := []string{"BHD", "AITHER", "PTP"}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
}

func indexOfTracker(values []string, target string) int {
	for idx, value := range values {
		if strings.EqualFold(value, target) {
			return idx
		}
	}
	return -1
}

func hasTracker(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func TestExtractBTNClaimedShowsParsesCurrentSection(t *testing.T) {
	t.Parallel()

	html := `
	<div>
	  <strong>Current Shows:</strong><br>
	  Example Show -- BTN<br>
	  Another Show (aka: Alt Name) -- BTN<br>
	  Upcoming Shows:<br>
	  Ignored Show -- BTN
	</div>`

	claimed := extractBTNClaimedShows(html)
	if _, ok := claimed[normalizeBTNTitle("Example Show")]; !ok {
		t.Fatalf("expected example show to be extracted, got %#v", claimed)
	}
	if _, ok := claimed[normalizeBTNTitle("Another Show")]; !ok {
		t.Fatalf("expected canonical title to be extracted, got %#v", claimed)
	}
	if _, ok := claimed[normalizeBTNTitle("Alt Name")]; !ok {
		t.Fatalf("expected AKA alias to be extracted, got %#v", claimed)
	}
	if _, ok := claimed[normalizeBTNTitle("Ignored Show")]; ok {
		t.Fatalf("did not expect shows after upcoming section to be extracted, got %#v", claimed)
	}
}

func TestExtractBTNClaimedShowsScopesToClaimedPost(t *testing.T) {
	t.Parallel()

	html := `
	<div>
	  <strong>Current Shows:</strong><br>
	  Wrong Show -- BTN<br>
	</div>
	<table id="post1405482">
	  <tr>
	    <td>
	      <div id="content1405482" class="postcontent">
	        <strong>Current Shows:</strong><br>
	        Example Show -- BTN<br>
	        Another Show (aka: Alt Name) -- BTN<br>
	        Upcoming Shows:<br>
	        Ignored Show -- BTN
	      </div>
	    </td>
	  </tr>
	</table>`

	claimed := extractBTNClaimedShows(html)
	if _, ok := claimed[normalizeBTNTitle("Example Show")]; !ok {
		t.Fatalf("expected scoped example show to be extracted, got %#v", claimed)
	}
	if _, ok := claimed[normalizeBTNTitle("Alt Name")]; !ok {
		t.Fatalf("expected scoped AKA alias to be extracted, got %#v", claimed)
	}
	if _, ok := claimed[normalizeBTNTitle("Wrong Show")]; ok {
		t.Fatalf("did not expect out-of-post show to be extracted, got %#v", claimed)
	}
}

func TestExtractBTNClaimedShowsParsesNestedClaimedPostContent(t *testing.T) {
	t.Parallel()

	html := `
	<div>
	  <table id="post1405482">
	    <tr>
	      <td>
	        <div id="content1405482" class="postcontent">
	          <div>
	            <strong>Current Shows:</strong><br>
	            Example Show (aka: Alt Name) -- BTN<br>
	            <div class="note">Some nested wrapper</div>
	            Another Show -- BTN<br>
	          </div>
	          <div>
	            <strong>Upcoming Shows:</strong><br>
	            Future Show -- BTN<br>
	          </div>
	        </div>
	      </td>
	    </tr>
	  </table>
	</div>`

	claimed := extractBTNClaimedShows(html)
	if _, ok := claimed[normalizeBTNTitle("Example Show")]; !ok {
		t.Fatalf("expected example show to be extracted, got %#v", claimed)
	}
	if _, ok := claimed[normalizeBTNTitle("Alt Name")]; !ok {
		t.Fatalf("expected alias to be extracted, got %#v", claimed)
	}
	if _, ok := claimed[normalizeBTNTitle("Another Show")]; !ok {
		t.Fatalf("expected nested-row show to be extracted, got %#v", claimed)
	}
	if _, ok := claimed[normalizeBTNTitle("Future Show")]; ok {
		t.Fatalf("did not expect upcoming show to be extracted, got %#v", claimed)
	}
}

func TestMirrorBTNCookiesForClaimedThreadCopiesBackupDomainSession(t *testing.T) {
	t.Parallel()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	backupURL := mustParseURL(t, "https://backup.landof.tv/")
	broadcastURL := mustParseURL(t, "https://broadcasthe.net/")
	client.Jar.SetCookies(backupURL, []*http.Cookie{{
		Name:   "session",
		Value:  "abc123",
		Domain: "backup.landof.tv",
		Path:   "/",
	}})

	mirrorBTNCookiesForClaimedThread(client)

	broadcastCookies := client.Jar.Cookies(broadcastURL)
	if len(broadcastCookies) == 0 {
		t.Fatalf("expected mirrored cookies for broadcasthe.net")
	}
	found := false
	for _, cookie := range broadcastCookies {
		if cookie.Name == "session" && cookie.Value == "abc123" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected mirrored session cookie, got %#v", broadcastCookies)
	}
}

func TestMirrorBTNCookiesForClaimedThreadKeepsDistinctCookies(t *testing.T) {
	t.Parallel()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	backupURL := mustParseURL(t, "https://backup.landof.tv/")
	broadcastURL := mustParseURL(t, "https://broadcasthe.net/")
	client.Jar.SetCookies(backupURL, []*http.Cookie{
		{
			Name:   "session",
			Value:  "abc123",
			Domain: "backup.landof.tv",
			Path:   "/",
		},
		{
			Name:   "authkey",
			Value:  "xyz789",
			Domain: "backup.landof.tv",
			Path:   "/",
		},
	})

	mirrorBTNCookiesForClaimedThread(client)

	broadcastCookies := client.Jar.Cookies(broadcastURL)
	if len(broadcastCookies) < 2 {
		t.Fatalf("expected mirrored cookies for broadcasthe.net, got %#v", broadcastCookies)
	}

	valuesByName := make(map[string]string, len(broadcastCookies))
	for _, cookie := range broadcastCookies {
		valuesByName[cookie.Name] = cookie.Value
	}

	if valuesByName["session"] != "abc123" {
		t.Fatalf("expected mirrored session cookie, got %#v", valuesByName)
	}
	if valuesByName["authkey"] != "xyz789" {
		t.Fatalf("expected mirrored authkey cookie, got %#v", valuesByName)
	}
	if len(valuesByName) < 2 {
		t.Fatalf("expected distinct mirrored cookies, got %#v", valuesByName)
	}
}

func TestBTNClaimWindowExpiredUsesAiredDateAndTimezone(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		TVDBAiredDate:    time.Now().Add(-96 * time.Hour).UTC().Format("2006-01-02"),
		TVDBAirsTime:     "20:00",
		TVDBAirsTimezone: "UTC",
	}

	expired, threshold, _ := btnClaimWindowExpired(meta, 24)
	if !expired {
		t.Fatalf("expected claim window to be expired")
	}
	if threshold != 48 {
		t.Fatalf("expected threshold 48h when explicit air time is present, got %d", threshold)
	}

	meta.TVDBAiredDate = time.Now().Add(-12 * time.Hour).UTC().Format("2006-01-02")
	expired, threshold, _ = btnClaimWindowExpired(meta, 24)
	if expired {
		t.Fatalf("expected claim window to still be active")
	}
	if threshold != 48 {
		t.Fatalf("expected threshold 48h when explicit air time is present, got %d", threshold)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return parsed
}

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func swapDefaultTransport(transport http.RoundTripper) func() {
	original := http.DefaultTransport
	http.DefaultTransport = transport
	return func() {
		http.DefaultTransport = original
	}
}

func writeBTNClaimedCacheFixture(path string, fetchedAt int64, titles map[string]struct{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	serializedTitles := make([]string, 0, len(titles))
	for title := range titles {
		serializedTitles = append(serializedTitles, title)
	}

	payload, err := json.MarshalIndent(btnClaimedShowsCache{
		FetchedAt: fetchedAt,
		SourceURL: btnClaimedShowsURL,
		PostID:    btnClaimedShowsPostID,
		Titles:    serializedTitles,
	}, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, payload, 0o600)
}
