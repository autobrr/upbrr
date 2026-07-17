// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/config"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	dbsvc "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	btnimpl "github.com/autobrr/upbrr/internal/trackers/impl/btn"
	"github.com/autobrr/upbrr/pkg/api"
)

const minTrackerTokenLen = 25

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
	_ api.UploadSubject,
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
			return trackerdata.Result{}, fmt.Errorf("context canceled: %w", ctx.Err())
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

func trackerDataTestRegistry(t *testing.T) *trackers.Registry {
	t.Helper()
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		t.Fatalf("create tracker registry: %v", err)
	}
	return registry
}

func TestTrackerLookupFileNameHonorsSkipWithoutTrackerID(t *testing.T) {
	meta := preparationstate.State{
		SourcePath: `D:\Movies\Example.Show.S04E01.2160p.WEB.h265-GRP.mkv`,
	}

	if got := trackerLookupFileName(meta, "", true); got != "" {
		t.Fatalf("expected filename lookup to be skipped, got %q", got)
	}
}

func TestTrackerLookupFileNameKeepsFilenameWhenDefaultEnabled(t *testing.T) {
	meta := preparationstate.State{
		SourcePath: `D:\Movies\Example.Show.S04E01.2160p.WEB.h265-GRP.mkv`,
	}

	if got := trackerLookupFileName(meta, "", false); got != "Example.Show.S04E01.2160p.WEB.h265-GRP.mkv" {
		t.Fatalf("expected filename lookup to remain enabled by default, got %q", got)
	}
}

func TestTrackerLookupFileNameKeepsFilenameWithTrackerID(t *testing.T) {
	meta := preparationstate.State{
		SourcePath: `D:\Movies\Example.Show.S04E01.2160p.WEB.h265-GRP.mkv`,
	}

	if got := trackerLookupFileName(meta, "12345", true); got != "Example.Show.S04E01.2160p.WEB.h265-GRP.mkv" {
		t.Fatalf("expected filename to remain available with tracker id, got %q", got)
	}
}

func TestEnrichTrackerDataStopsAfterFirstPriorityIDWinner(t *testing.T) {
	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{
		results: map[string]trackerdata.Result{
			"ANT": {
				TMDBID:    123,
				IMDBID:    456,
				TrackerID: "101",
			},
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
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))

	meta := preparationstate.State{
		SourcePath: `D:\Movies\Example.Movie.2026.BluRay.1080p.DTS.x264-GRP`,
		TrackerIDs: map[string]string{
			"ant": "101",
			"hdb": "202",
		},
	}

	result, err := svc.collectTrackerEvidence(context.Background(), meta)
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
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))

	meta := preparationstate.State{
		SourcePath: `D:\Movies\Example.Movie.2026.BluRay.1080p.DTS.x264-GRP`,
		TrackerIDs: map[string]string{
			"ant": "101",
			"hdb": "202",
		},
	}

	result, err := svc.collectTrackerEvidence(context.Background(), meta)
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
			"ANT": {
				TMDBID:    123,
				IMDBID:    456,
				TrackerID: "101",
			},
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
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))

	meta := preparationstate.State{
		SourcePath:       `D:\Movies\Example.Movie.2026.BluRay.1080p.DTS.x264-GRP`,
		EvidenceTrackers: []string{"ANT", "HDB"},
	}

	result, err := svc.collectTrackerEvidence(context.Background(), meta)
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

func TestEnrichTrackerDataPreferredTrackerIsSourceOfTruthWithoutClientTrackerIDs(t *testing.T) {
	repo := &fakeRepo{}
	installTestArtifactImageHTTPClient(t, trackerDataPNG1x1())
	antImageURL := "http://93.184.216.34/ant.png"
	hdbImageURL := "http://93.184.216.34/hdb.png"
	lookup := &stubTrackerLookup{
		results: map[string]trackerdata.Result{
			"ANT": {
				TMDBID:      123,
				IMDBID:      456,
				Description: "ant description",
				Images:      []bbcode.Image{{RawURL: antImageURL}},
			},
			"HDB": {
				TMDBID:      999,
				IMDBID:      888,
				Description: "hdb description",
				Images:      []bbcode.Image{{RawURL: hdbImageURL}},
			},
		},
		delays: map[string]time.Duration{
			"ANT": 40 * time.Millisecond,
			"HDB": 5 * time.Millisecond,
		},
	}
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "db.sqlite")},
		Trackers: config.TrackersConfig{
			PreferredTracker: "ANT",
			Trackers: map[string]config.TrackerConfig{
				"ANT": {APIKey: "ant-key"},
				"HDB": {Username: "user", Passkey: "pass"},
			},
		},
	}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))

	meta := preparationstate.State{
		SourcePath:       `D:\Movies\Example.Movie.2026.BluRay.1080p.DTS.x264-GRP`,
		EvidenceTrackers: []string{"ANT", "HDB"},
		Policy:           preparationstate.CollectionPolicy{KeepImages: true},
	}

	result, err := svc.collectTrackerEvidence(context.Background(), meta)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	calls := lookup.Calls()
	if len(calls) != 1 || !strings.EqualFold(calls[0], "ANT") {
		t.Fatalf("expected preferred tracker ANT to be queried first and stop as source of truth, calls=%v", calls)
	}
	winner, found := trackerRecordFor(result.TrackerData, "ANT")
	if !found {
		t.Fatalf("expected ANT tracker winner record, got %v", result.TrackerData)
	}
	if winner.TMDBID != 123 || winner.IMDBID != 456 {
		t.Fatalf("expected ANT ids, got tmdb=%d imdb=%d", winner.TMDBID, winner.IMDBID)
	}
	if winner.Description != "ant description" {
		t.Fatalf("expected ANT description, got %q", winner.Description)
	}
	if len(winner.ImageURLs) != 1 || winner.ImageURLs[0] != antImageURL {
		t.Fatalf("expected ANT image urls, got %v", winner.ImageURLs)
	}
	if _, found := trackerRecordFor(result.TrackerData, "HDB"); found {
		t.Fatalf("expected non-preferred HDB not to supply tracker data, got %v", result.TrackerData)
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
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))

	meta := preparationstate.State{
		SourcePath: `D:\Movies\Example.Movie.2026.BluRay.1080p.DTS.x264-GRP`,
		TrackerIDs: map[string]string{
			"ant": "101",
			"hdb": "202",
			"ptp": "303",
		},
	}

	result, err := svc.collectTrackerEvidence(context.Background(), meta)
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

func TestResolveTrackerClaimProviderSupportsKnownTrackers(t *testing.T) {
	t.Parallel()
	service := &Service{registry: newClaimTestRegistry(t)}

	btnProvider, ok := service.resolveTrackerClaimProvider("btn", api.NopLogger{})
	if !ok {
		t.Fatalf("expected BTN provider")
	}
	if _, ok := btnProvider.(registryTrackerClaimProvider); !ok {
		t.Fatalf("expected BTN provider type, got %T", btnProvider)
	}

	aitherProvider, ok := service.resolveTrackerClaimProvider("AITHER", api.NopLogger{})
	if !ok {
		t.Fatalf("expected AITHER provider")
	}
	if _, ok := aitherProvider.(apiTrackerClaimProvider); !ok {
		t.Fatalf("expected API provider type, got %T", aitherProvider)
	}

	if _, ok := service.resolveTrackerClaimProvider("PTP", api.NopLogger{}); ok {
		t.Fatalf("did not expect provider for unsupported tracker")
	}
}

func newClaimTestRegistry(t *testing.T) *trackers.Registry {
	t.Helper()
	registry := trackers.NewRegistry()
	if err := registry.Register(btnimpl.New()); err != nil {
		t.Fatalf("register claim provider: %v", err)
	}
	if err := registry.Register(aitherRuleDefinition{}); err != nil {
		t.Fatalf("register AITHER claim policy: %v", err)
	}
	return registry
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
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))

	meta := preparationstate.State{
		SourcePath:       `D:\Movies\Example.Movie.2026.BluRay.1080p.DTS.x264-GRP`,
		StoredDataFresh:  true,
		EvidenceTrackers: []string{"ANT"},
	}

	result, err := svc.collectTrackerEvidence(context.Background(), meta)
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
			"BHD": {
				TMDBID:      513053,
				TrackerID:   "513053",
				Description: "desc",
				Images:      []bbcode.Image{{RawURL: "https://img.example/a.jpg"}},
			},
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
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(newClaimTestRegistry(t)))

	meta := preparationstate.State{
		SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.S01.1080p.WEB-DL-GRP"),
		TrackerIDs: map[string]string{
			"btn": "2167358",
			"bhd": "513053",
		},
		Policy: preparationstate.CollectionPolicy{KeepImages: true},
	}

	result, err := svc.collectTrackerEvidence(context.Background(), meta)
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
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(newClaimTestRegistry(t)))

	meta := preparationstate.State{
		SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.S01.1080p.WEB-DL-GRP"),
		TrackerIDs: map[string]string{
			"btn": "2167358",
			"bhd": "513053",
		},
		Policy: preparationstate.CollectionPolicy{KeepImages: true},
	}

	result, err := svc.collectTrackerEvidence(context.Background(), meta)
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
			"HDB": {
				Description: "hdb description",
				IMDBID:      1554091,
				TrackerID:   "202",
			},
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
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))

	meta := preparationstate.State{
		SourcePath: `D:\Movies\Example.Movie.2026.BluRay.1080p.DTS.x264-GRP`,
		TrackerIDs: map[string]string{
			"ant": "101",
			"hdb": "202",
		},
	}

	result, err := svc.collectTrackerEvidence(context.Background(), meta)
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

func TestEnrichTrackerDataDropsImageURLsWhenArtifactDownloadRejected(t *testing.T) {
	png1x1 := []byte{
		137, 80, 78, 71, 13, 10, 26, 10,
		0, 0, 0, 13, 73, 72, 68, 82,
		0, 0, 0, 1, 0, 0, 0, 1,
		8, 6, 0, 0, 0, 31, 21, 196,
		137, 0, 0, 0, 13, 73, 68, 65,
		84, 120, 156, 99, 248, 15, 4, 0,
		9, 251, 3, 253, 160, 158, 134, 129,
		0, 0, 0, 0, 73, 69, 78, 68,
		174, 66, 96, 130,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png1x1)
	}))
	defer server.Close()

	imageURL := server.URL + "/screen.png"
	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{
		results: map[string]trackerdata.Result{
			"BHD": {
				TrackerID:   "513053",
				Description: "tracker description",
				Images:      []bbcode.Image{{RawURL: imageURL}},
			},
		},
	}
	longToken := strings.Repeat("a", minTrackerTokenLen)
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "db.sqlite")},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"BHD": {APIKey: longToken, BhdRSSKey: longToken},
			},
		},
	}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))

	meta := preparationstate.State{
		SourcePath: filepath.Join(t.TempDir(), "Movie.2026.2160p.mkv"),
		Release:    api.ReleaseInfo{Resolution: "2160p"},
		TrackerIDs: map[string]string{"bhd": "513053"},
		Policy:     preparationstate.CollectionPolicy{KeepImages: true},
	}

	result, err := svc.collectTrackerEvidence(context.Background(), meta)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	record, found := trackerRecordFor(result.TrackerData, "BHD")
	if !found {
		t.Fatalf("expected BHD tracker record, got %v", result.TrackerData)
	}
	if len(record.ImageURLs) != 0 {
		t.Fatalf("expected rejected image URL to be dropped, got %#v", record.ImageURLs)
	}
}

func TestEnrichTrackerDataRejectedImageURLsDoNotChooseAssetSource(t *testing.T) {
	png1x1 := []byte{
		137, 80, 78, 71, 13, 10, 26, 10,
		0, 0, 0, 13, 73, 72, 68, 82,
		0, 0, 0, 1, 0, 0, 0, 1,
		8, 6, 0, 0, 0, 31, 21, 196,
		137, 0, 0, 0, 13, 73, 68, 65,
		84, 120, 156, 99, 248, 15, 4, 0,
		9, 251, 3, 253, 160, 158, 134, 129,
		0, 0, 0, 0, 73, 69, 78, 68,
		174, 66, 96, 130,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png1x1)
	}))
	defer server.Close()

	imageURL := server.URL + "/too-small.png"
	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{
		results: map[string]trackerdata.Result{
			"BHD": {
				Images: []bbcode.Image{{RawURL: imageURL}},
			},
			"HDB": {
				Description: "usable hdb description",
			},
		},
		delays: map[string]time.Duration{"HDB": 10 * time.Millisecond},
	}
	longToken := strings.Repeat("a", minTrackerTokenLen)
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "db.sqlite")},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"BHD": {APIKey: longToken, BhdRSSKey: longToken},
				"HDB": {Username: "user", Passkey: "pass"},
			},
		},
	}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))

	result, err := svc.collectTrackerEvidence(context.Background(), preparationstate.State{
		SourcePath:       filepath.Join(t.TempDir(), "Movie.2026.2160p.mkv"),
		Release:          api.ReleaseInfo{Resolution: "2160p"},
		EvidenceTrackers: []string{"BHD", "HDB"},
		Policy:           preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	bhd, found := trackerRecordFor(result.TrackerData, "BHD")
	if !found {
		t.Fatalf("expected BHD tracker record, got %v", result.TrackerData)
	}
	if len(bhd.ImageURLs) != 0 {
		t.Fatalf("expected rejected BHD image URL to be dropped, got %#v", bhd.ImageURLs)
	}
	hdb, found := trackerRecordFor(result.TrackerData, "HDB")
	if !found {
		t.Fatalf("expected HDB tracker record, got %v", result.TrackerData)
	}
	if hdb.Description != "usable hdb description" {
		t.Fatalf("expected HDB description to remain selected, got %q", hdb.Description)
	}
}

func TestEnrichTrackerDataPreservesDuplicateImageURLPositionsForArtifactLinks(t *testing.T) {
	firstURL, laterURL, cfg, sourcePath, closeServer := trackerDuplicateImageFixture(t)
	defer closeServer()

	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{
		results: map[string]trackerdata.Result{
			"BHD": {
				TrackerID:   "513053",
				Description: "tracker description",
				Images: []bbcode.Image{
					{RawURL: firstURL},
					{RawURL: firstURL},
					{RawURL: laterURL},
				},
			},
		},
	}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))

	result, err := svc.collectTrackerEvidence(context.Background(), preparationstate.State{
		SourcePath: sourcePath,
		TrackerIDs: map[string]string{"bhd": "513053"},
		Policy:     preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	record, found := trackerRecordFor(result.TrackerData, "BHD")
	if !found {
		t.Fatalf("expected BHD tracker record, got %v", result.TrackerData)
	}
	expectedURLs := []string{firstURL, firstURL, laterURL}
	if !reflect.DeepEqual(record.ImageURLs, expectedURLs) {
		t.Fatalf("expected positional image URLs, got %#v", record.ImageURLs)
	}
	assertTrackerArtifactExists(t, cfg, sourcePath, "BHD", laterURL, 2)
}

func TestEnrichTrackerDataPreservesDuplicateImageURLPositionsForLocalScreenshots(t *testing.T) {
	firstURL, laterURL, cfg, sourcePath, closeServer := trackerDuplicateImageFixture(t)
	defer closeServer()

	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{
		results: map[string]trackerdata.Result{
			"BHD": {
				TrackerID:   "513053",
				Description: "tracker description",
				Images: []bbcode.Image{
					{RawURL: firstURL},
					{RawURL: firstURL},
					{RawURL: laterURL},
				},
			},
		},
	}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))

	result, err := svc.collectTrackerEvidence(context.Background(), preparationstate.State{
		SourcePath: sourcePath,
		TrackerIDs: map[string]string{"bhd": "513053"},
		Policy:     preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	record, found := trackerRecordFor(result.TrackerData, "BHD")
	if !found {
		t.Fatalf("expected BHD tracker record, got %v", result.TrackerData)
	}
	if len(record.ImageURLs) != 3 || record.ImageURLs[2] != laterURL {
		t.Fatalf("expected later URL to keep original index 2, got %#v", record.ImageURLs)
	}
	assertTrackerArtifactExists(t, cfg, sourcePath, "BHD", laterURL, 2)
}

func TestMetadataTrackerPriorityPlacesPreferredTrackersBeforeRemainingUnit3D(t *testing.T) {
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		t.Fatalf("create tracker registry: %v", err)
	}
	result := registry.Priority()
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
	for _, tracker := range registry.NamesByFamily(trackers.FamilyUnit3D) {
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

func installTestArtifactImageHTTPClient(t *testing.T, payload []byte) {
	t.Helper()

	previousHTTPClient := newUnit3DArtifactImageHTTPClient
	newUnit3DArtifactImageHTTPClient = func() *http.Client {
		return &http.Client{Transport: artifactRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"image/png"}},
				Body:          io.NopCloser(bytes.NewReader(payload)),
				ContentLength: int64(len(payload)),
				Request:       req,
			}, nil
		})}
	}
	t.Cleanup(func() {
		newUnit3DArtifactImageHTTPClient = previousHTTPClient
	})
}

func trackerDataPNG1x1() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0x18, 0xdd, 0x8d,
		0xb0, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}
}

func trackerDuplicateImageFixture(t *testing.T) (string, string, config.Config, string, func()) {
	t.Helper()

	installTestArtifactImageHTTPClient(t, trackerDataPNG1x1())

	tempDir := t.TempDir()
	longToken := strings.Repeat("a", minTrackerTokenLen)
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "db.sqlite")},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"BHD": {APIKey: longToken, BhdRSSKey: longToken},
			},
		},
	}
	sourcePath := filepath.Join(tempDir, "Movie.2026.1080p.mkv")

	return "http://93.184.216.34/duplicate.png", "http://93.184.216.34/later.png", cfg, sourcePath, func() {}
}

func assertTrackerArtifactExists(t *testing.T, cfg config.Config, sourcePath string, tracker string, rawURL string, index int) {
	t.Helper()

	tmpRoot, err := dbsvc.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatalf("tmp dir: %v", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, preparationstate.State{SourcePath: sourcePath}, sourcePath)
	if err != nil {
		t.Fatalf("release temp dir: %v", err)
	}
	artifactPath := filepath.Join(tmpDir, sanitizeFilename(strings.ToLower(tracker)), buildImageFilename(rawURL, index))
	info, err := os.Stat(artifactPath)
	if err != nil {
		t.Fatalf("expected tracker artifact at %s: %v", artifactPath, err)
	}
	if info.IsDir() {
		t.Fatalf("expected tracker artifact file at %s", artifactPath)
	}
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

	meta := preparationstate.State{
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
		return fmt.Errorf("create BTN claimed cache fixture dir: %w", err)
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
		return fmt.Errorf("marshal BTN claimed cache fixture: %w", err)
	}

	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("write BTN claimed cache fixture: %w", err)
	}
	return nil
}
