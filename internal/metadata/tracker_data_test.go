// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	dbsvc "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	btnimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/btn"
	"github.com/autobrr/upbrr/pkg/api"
)

const minTrackerTokenLen = 25

func TestTrackerImageURLsFromResultKeepsPreviewPairs(t *testing.T) {
	t.Parallel()
	result := trackerdata.Result{Images: []bbcode.Image{
		{RawURL: "https://images.example.invalid/full-one.png", ImgURL: "https://images.example.invalid/preview-one.png"},
		{RawURL: "https://images.example.invalid/full-two.png", ImgURL: "javascript:alert(1)"},
	}}
	urls, previews := trackerImageURLsFromResult(result, []string{"https://images.example.invalid/full-one.png", ""}, true)
	if len(urls) != 2 || urls[0] != result.Images[0].RawURL || urls[1] != "" ||
		previews[result.Images[0].RawURL] != result.Images[0].ImgURL || previews[result.Images[1].RawURL] != "" {
		t.Fatalf("tracker image URLs = %#v, previews = %#v", urls, previews)
	}
	urls, previews = trackerImageURLsFromResult(result, nil, false)
	if urls != nil || previews[result.Images[0].RawURL] != result.Images[0].ImgURL {
		t.Fatalf("description previews lost when image downloads are disabled: urls=%#v previews=%#v", urls, previews)
	}
}

type stubTrackerLookup struct {
	results     map[string]trackerdata.Result
	calls       []string
	searchNames []string
	ids         map[string]string
	delays      map[string]time.Duration
	mu          sync.Mutex
}

type coolingTrackerRepo struct {
	*fakeRepo
	timestamp      time.Time
	assetTimestamp time.Time
}

func (r *coolingTrackerRepo) GetTrackerTimestamp(_ context.Context, tracker string) (time.Time, error) {
	if tracker == trackerAssetTimestampKey("AITHER") {
		return r.assetTimestamp, nil
	}
	return r.timestamp, nil
}

func (r *coolingTrackerRepo) SaveTrackerTimestamp(ctx context.Context, timestamp api.TrackerTimestamp) error {
	if timestamp.Tracker == trackerAssetTimestampKey("AITHER") {
		r.assetTimestamp = timestamp.UpdatedAt
	}
	return r.fakeRepo.SaveTrackerTimestamp(ctx, timestamp)
}

func (s *stubTrackerLookup) Lookup(
	ctx context.Context,
	tracker string,
	trackerID string,
	_ api.UploadSubject,
	searchName string,
	_ bool,
	_ bool,
) (trackerdata.Result, error) {
	s.mu.Lock()
	s.calls = append(s.calls, tracker)
	s.searchNames = append(s.searchNames, searchName)
	if s.ids == nil {
		s.ids = make(map[string]string)
	}
	s.ids[tracker] = trackerID
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

func (s *stubTrackerLookup) SearchNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.searchNames...)
}

func (s *stubTrackerLookup) TrackerIDFor(tracker string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ids[tracker]
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

func TestTrackerLookupKeepsExactPageWithoutTorrentID(t *testing.T) {
	t.Parallel()
	const torrentPage = "https://anthelion.me/torrents.php?id=42"
	lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
		"ANT": {TorrentURL: torrentPage, IMDBID: 1234567},
	}}
	svc := NewService(&fakeRepo{}, WithTrackerDataLookup(lookup))
	record, persistable, _, err := svc.lookupTrackerData(t.Context(), preparationstate.State{SourcePath: filepath.Join(t.TempDir(), "Example.Release.mkv")}, "ANT", time.Now())
	if err != nil || !persistable || record.TorrentURL != torrentPage || record.TrackerID != "" {
		t.Fatalf("exact torrent page was not retained: record=%+v persistable=%t err=%v", record, persistable, err)
	}
}

func TestTrackerLookupANTWorkingIDStillUsesFilename(t *testing.T) {
	t.Parallel()
	const torrentPage = "https://anthelion.me/torrents.php?id=42"
	lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
		"ANT": {TorrentURL: torrentPage, IMDBID: 1234567},
	}}
	svc := NewService(&fakeRepo{}, WithTrackerDataLookup(lookup))
	meta := preparationstate.State{
		SourcePath: filepath.Join(t.TempDir(), "Example.Release.mkv"),
		TrackerIDs: map[string]string{"ant": "1"},
	}
	record, persistable, _, err := svc.lookupTrackerData(t.Context(), meta, "ANT", time.Now())
	if err != nil || !persistable || record.TorrentURL != torrentPage || record.TrackerID != "1" {
		t.Fatalf("ANT working tracker lost exact page: record=%+v persistable=%t err=%v", record, persistable, err)
	}
	if got := lookup.SearchNames(); len(got) != 1 || got[0] != "Example.Release.mkv" {
		t.Fatalf("ANT search filenames = %v", got)
	}
}

func TestTrackerLookupFileNameHonorsSkipWithoutTrackerID(t *testing.T) {
	meta := preparationstate.State{
		SourcePath: `D:\Movies\Example.Show.S04E01.2160p.WEB.h265-GRP.mkv`,
	}

	if got := trackerLookupFileName(meta, true); got != "" {
		t.Fatalf("expected filename lookup to be skipped, got %q", got)
	}
}

func TestTrackerLookupFileNameKeepsFilenameWhenDefaultEnabled(t *testing.T) {
	meta := preparationstate.State{
		SourcePath: `D:\Movies\Example.Show.S04E01.2160p.WEB.h265-GRP.mkv`,
	}

	if got := trackerLookupFileName(meta, false); got != "Example.Show.S04E01.2160p.WEB.h265-GRP.mkv" {
		t.Fatalf("expected filename lookup to remain enabled by default, got %q", got)
	}
}

func TestTrackerLookupWithKnownIDPassesFilename(t *testing.T) {
	t.Parallel()
	lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{"BLU": {TrackerID: "42"}}}
	svc := NewService(&fakeRepo{}, WithTrackerDataLookup(lookup))
	meta := preparationstate.State{
		SourcePath: filepath.Join(t.TempDir(), "Example.Release.mkv"),
		TrackerIDs: map[string]string{"blu": "42"},
	}
	_, _, _, err := svc.lookupTrackerData(t.Context(), meta, "BLU", time.Now())
	if err != nil {
		t.Fatalf("tracker lookup: %v", err)
	}
	if got := lookup.SearchNames(); len(got) != 1 || got[0] != "Example.Release.mkv" {
		t.Fatalf("lookup filenames with known ID = %v", got)
	}
}

func TestTrackerLookupFileNameHonorsSkipForANT(t *testing.T) {
	t.Parallel()
	lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{"ANT": {TrackerID: "1"}}}
	svc := NewService(&fakeRepo{}, WithConfig(config.Config{Metadata: config.MetadataConfig{SkipTrackerFilenameLookup: true}}), WithTrackerDataLookup(lookup))
	meta := preparationstate.State{
		SourcePath: filepath.Join(t.TempDir(), "Example.Release.mkv"),
		TrackerIDs: map[string]string{"ant": "1"},
	}
	_, _, _, err := svc.lookupTrackerData(t.Context(), meta, "ANT", time.Now())
	if err != nil {
		t.Fatalf("tracker lookup: %v", err)
	}
	if got := lookup.SearchNames(); len(got) != 1 || got[0] != "" {
		t.Fatalf("expected ANT filename lookup to be skipped by config, got %v", got)
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

func TestLoadBTNClaimedTitlesRefreshesFreshLegacyCache(t *testing.T) {
	tempDir := t.TempDir()
	cachePath := filepath.Join(tempDir, "cache", "banned", "BTN_claimed_releases.json")
	cached := map[string]struct{}{normalizeBTNTitle("Cached Show"): {}}
	if err := writeBTNClaimedCacheFixture(cachePath, time.Now().Add(-47*time.Hour).Unix(), cached); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	clientCalls := 0
	restore := swapDefaultTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		clientCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`
				<table id="post1405482">
				  <tr><td><div id="content1405482" class="postcontent">
				    <strong>Current Shows:</strong><br>
				    Fresh Show -- HDB | BTN -- TBN -- AMZN<br>
				  </div></td></tr>
				</table>`)),
			Header:  make(http.Header),
			Request: req,
		}, nil
	}))
	defer restore()

	svc := NewService(&fakeRepo{}, WithConfig(config.Config{}))
	claimed, err := svc.loadBTNClaimedTitles(context.Background(), cachePath, 48*time.Hour)
	if err != nil {
		t.Fatalf("load btn claimed titles: %v", err)
	}
	if clientCalls != 2 {
		t.Fatalf("expected legacy cache to trigger session validation and fetch, got %d requests", clientCalls)
	}
	if _, ok := claimed[normalizeBTNTitle("Fresh Show")]; !ok {
		t.Fatalf("expected refreshed title, got %#v", claimed)
	}

	cacheData, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var migrated btnClaimedShowsCache
	if err := json.Unmarshal(cacheData, &migrated); err != nil {
		t.Fatalf("decode migrated cache: %v", err)
	}
	if migrated.Version != 2 || len(migrated.Claims) != 1 {
		t.Fatalf("expected v2 structured cache, got version=%d claims=%d", migrated.Version, len(migrated.Claims))
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
			    Fresh Show -- BTN -- GRP -- AMZN<br>
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

func TestLoadBTNClaimedTitlesFallsBackToStaleCacheAfterFetchFailure(t *testing.T) {
	tempDir := t.TempDir()
	cachePath := filepath.Join(tempDir, "cache", "banned", "BTN_claimed_releases.json")
	cached := map[string]struct{}{normalizeBTNTitle("Cached Show"): {}}
	if err := writeBTNClaimedCacheFixture(cachePath, time.Now().Add(-49*time.Hour).Unix(), cached); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	clientCalls := 0
	restore := swapDefaultTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		clientCalls++
		if clientCalls == 1 {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("session valid")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		return nil, errors.New("synthetic fetch failure")
	}))
	defer restore()

	svc := NewService(&fakeRepo{}, WithConfig(config.Config{}))
	claimed, err := svc.loadBTNClaimedTitles(context.Background(), cachePath, 48*time.Hour)
	if err != nil {
		t.Fatalf("load stale BTN claimed titles: %v", err)
	}
	if clientCalls != 2 {
		t.Fatalf("expected session validation and failed claimed-title fetch, got %d requests", clientCalls)
	}
	if _, ok := claimed[normalizeBTNTitle("Cached Show")]; !ok {
		t.Fatalf("expected stale cached title after fetch failure, got %#v", claimed)
	}
}

func TestFetchBTNClaimedTitlesRequiresExistingSessionWithoutLogin(t *testing.T) {
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
	if len(requests) != 1 {
		t.Fatalf("expected only session validation, got %d requests: %v", len(requests), requests)
	}
	if strings.Contains(strings.Join(requests, " "), "forums.php") {
		t.Fatalf("did not expect claimed-thread fetch after login failure, got %v", requests)
	}
}

func TestFreshSnapshotUsesTrackerIDsBeforeProviderNameSearch(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Example.Movie.2026.1080p-GRP")
	const trackerID = "1234"
	const infoHash = "abc123"
	for _, test := range []struct {
		name        string
		stored      []api.TrackerMetadata
		wantLookups int
	}{
		{
			name: "matching saved tracker IDs",
			stored: []api.TrackerMetadata{{
				SourcePath: sourcePath,
				Tracker:    "ANT",
				TrackerID:  trackerID,
				InfoHash:   infoHash,
				TMDBID:     42,
				IMDBID:     24,
			}},
		},
		{name: "missing saved tracker IDs", wantLookups: 1},
		{
			name: "saved tracker record without metadata IDs",
			stored: []api.TrackerMetadata{{
				SourcePath: sourcePath,
				Tracker:    "ANT",
				TrackerID:  trackerID,
				InfoHash:   infoHash,
			}},
			wantLookups: 1,
		},
		{
			name: "saved tracker record for another torrent",
			stored: []api.TrackerMetadata{{
				SourcePath: sourcePath,
				Tracker:    "ANT",
				TrackerID:  "old",
				InfoHash:   "old",
				TMDBID:     99,
				IMDBID:     99,
			}},
			wantLookups: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &fakeRepo{trackerMetadata: test.stored}
			lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
				"ANT": {
					TrackerID: trackerID,
					TMDBID:    42,
					IMDBID:    24,
				},
			}}
			tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{Title: "Example Movie", Year: 2026}}
			imdbClient := &stubIMDB{info: imdb.Info{
				IMDbID: "tt0000024",
				Title:  "Example Movie",
				Year:   2026,
			}}
			svc := NewService(repo,
				WithConfig(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
					"ANT": {APIKey: "ant-key"},
				}}}),
				WithTrackerDataLookup(lookup),
				WithTrackerRegistry(trackerDataTestRegistry(t)),
				WithTMDBClient(tmdbClient),
				WithIMDBClient(imdbClient),
			)
			state := preparationstate.State{
				SourcePath:        sourcePath,
				StoredDataFresh:   true,
				InfoHash:          infoHash,
				TrackerIDs:        map[string]string{"ant": trackerID},
				EvidenceTrackers:  []string{"ANT"},
				Policy:            preparationstate.CollectionPolicy{OnlyID: true},
				ExternalFreshness: api.ExternalFreshnessRefresh,
				MediaInfoCategory: "MOVIE",
				Release: api.ReleaseInfo{
					Category: "MOVIE",
					Title:    "Example Movie",
					Year:     2026,
				},
			}

			state, err := svc.collectTrackerEvidence(t.Context(), state)
			if err != nil {
				t.Fatalf("collect tracker evidence: %v", err)
			}
			if got := len(lookup.Calls()); got != test.wantLookups {
				t.Fatalf("tracker lookups = %d, want %d", got, test.wantLookups)
			}
			state, err = svc.collectExternalIdentityEvidence(t.Context(), state)
			if err != nil {
				t.Fatalf("collect external identity: %v", err)
			}
			if state.Identity.TMDBID != 42 || state.Identity.IMDBID != 24 ||
				state.Identity.Provenance.TMDB != api.IdentityProvenanceTracker ||
				state.Identity.Provenance.IMDB != api.IdentityProvenanceTracker {
				t.Fatalf("external identity did not use tracker IDs: %#v", state.Identity)
			}
			if tmdbClient.searchCalls != 0 || imdbClient.searchCalls != 0 {
				t.Fatalf("name searches: tmdb=%d imdb=%d", tmdbClient.searchCalls, imdbClient.searchCalls)
			}
		})
	}
}

func TestFreshAitherSnapshotRefreshesRequestedDescriptionAndImages(t *testing.T) {
	imageURL, _, cfg, sourcePath, _ := trackerDuplicateImageFixture(t)
	cfg.Trackers.Trackers["AITHER"] = config.TrackerConfig{APIKey: "aither-key"}
	stored := api.TrackerMetadata{
		SourcePath: sourcePath,
		Tracker:    "AITHER",
		TrackerID:  "72677",
		InfoHash:   "example-hash",
		TMDBID:     42,
		IMDBID:     24,
	}
	for _, test := range []struct {
		name       string
		stored     api.TrackerMetadata
		policy     preparationstate.CollectionPolicy
		wantLookup bool
		provenance bool
	}{
		{
			name:   "IDs only",
			stored: stored,
			policy: preparationstate.CollectionPolicy{OnlyID: true},
		},
		{
			name:       "description requested",
			stored:     stored,
			wantLookup: true,
		},
		{
			name:       "description and images requested",
			stored:     stored,
			policy:     preparationstate.CollectionPolicy{KeepImages: true},
			wantLookup: true,
		},
		{
			name: "images missing from stored description",
			stored: api.TrackerMetadata{
				SourcePath:  sourcePath,
				Tracker:     "AITHER",
				TrackerID:   "72677",
				InfoHash:    "example-hash",
				TMDBID:      42,
				IMDBID:      24,
				Description: "stored description",
			},
			policy:     preparationstate.CollectionPolicy{KeepImages: true},
			wantLookup: true,
		},
		{
			name: "complete stored assets",
			stored: api.TrackerMetadata{
				SourcePath:  sourcePath,
				Tracker:     "AITHER",
				TrackerID:   "72677",
				InfoHash:    "example-hash",
				TMDBID:      42,
				IMDBID:      24,
				Description: "stored description",
				ImageURLs:   []string{imageURL},
			},
			policy:     preparationstate.CollectionPolicy{KeepImages: true},
			provenance: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &fakeRepo{trackerMetadata: []api.TrackerMetadata{test.stored}}
			if test.provenance {
				repo.trackerTimestamps = append(repo.trackerTimestamps, api.TrackerTimestamp{
					Tracker:   trackers.TrackerAssetProvenanceKey(test.stored),
					UpdatedAt: time.Now(),
				})
			}
			lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
				"AITHER": {
					TrackerID:   "72677",
					Description: "fetched description",
					Images:      []bbcode.Image{{RawURL: imageURL}},
				},
			}}
			svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))
			result, err := svc.collectTrackerEvidence(t.Context(), preparationstate.State{
				SourcePath:      sourcePath,
				StoredDataFresh: true,
				InfoHash:        "example-hash",
				TrackerIDs:      map[string]string{"aither": "72677"},
				Policy:          test.policy,
			})
			if err != nil {
				t.Fatalf("collect tracker evidence: %v", err)
			}
			if got := len(lookup.Calls()); (got == 1) != test.wantLookup {
				t.Fatalf("lookup calls = %d, want lookup %t", got, test.wantLookup)
			}
			record, found := trackerRecordFor(result.TrackerData, "AITHER")
			if !found || record.TMDBID != 42 || record.IMDBID != 24 {
				t.Fatalf("stored IDs were lost: %#v", result.TrackerData)
			}
			if test.wantLookup && record.Description != "fetched description" {
				t.Fatalf("description = %q, want fetched description", record.Description)
			}
			if test.policy.KeepImages && (len(record.ImageURLs) != 1 || record.ImageURLs[0] != imageURL) {
				t.Fatalf("image URLs = %v, want %q", record.ImageURLs, imageURL)
			}
		})
	}
}

func TestFreshSnapshotRefreshesDescriptionAndImagesForOtherUnit3DAndBHD(t *testing.T) {
	for _, tracker := range []string{"BLU", "BHD"} {
		t.Run(tracker, func(t *testing.T) {
			imageURL, _, cfg, sourcePath, _ := trackerDuplicateImageFixture(t)
			if tracker == "BLU" {
				cfg.Trackers.Trackers[tracker] = config.TrackerConfig{APIKey: "blu-key"}
			}
			stored := api.TrackerMetadata{
				SourcePath: sourcePath,
				Tracker:    tracker,
				TrackerID:  "72677",
				InfoHash:   "example-hash",
				TMDBID:     42,
			}
			repo := &fakeRepo{trackerMetadata: []api.TrackerMetadata{stored}}
			lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
				tracker: {
					TrackerID:   "72677",
					Description: "fetched description",
					Images:      []bbcode.Image{{RawURL: imageURL}},
				},
			}}
			svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))
			result, err := svc.collectTrackerEvidence(t.Context(), preparationstate.State{
				SourcePath:      sourcePath,
				StoredDataFresh: true,
				InfoHash:        "example-hash",
				TrackerIDs:      map[string]string{strings.ToLower(tracker): "72677"},
				Policy:          preparationstate.CollectionPolicy{KeepImages: true},
			})
			if err != nil {
				t.Fatalf("collect tracker evidence: %v", err)
			}
			if got := lookup.Calls(); !slices.Equal(got, []string{tracker}) {
				t.Fatalf("asset refresh calls=%v", got)
			}
			record, found := trackerRecordFor(result.TrackerData, tracker)
			if !found || record.TrackerID != stored.TrackerID || record.Description != "fetched description" ||
				!slices.Equal(record.ImageURLs, []string{imageURL}) {
				t.Fatalf("tracker asset refresh incomplete: %#v", result.TrackerData)
			}
		})
	}
}

func TestFreshLegacyComparisonCacheRefreshesImageProvenance(t *testing.T) {
	for _, tracker := range []string{"AITHER", "BHD"} {
		t.Run(tracker, func(t *testing.T) {
			freshURL, _, cfg, sourcePath, _ := trackerDuplicateImageFixture(t)
			if tracker == "AITHER" {
				cfg.Trackers.Trackers[tracker] = config.TrackerConfig{APIKey: "aither-key"}
			}
			oldURL := "https://img.example/legacy-comparison.png"
			legacyDescription := "Legacy notes without image provenance"
			if tracker == "BHD" {
				legacyDescription = ""
			}
			stored := api.TrackerMetadata{
				SourcePath:  sourcePath,
				Tracker:     tracker,
				TrackerID:   "72677",
				InfoHash:    "example-hash",
				TMDBID:      42,
				Description: legacyDescription,
				ImageURLs:   []string{oldURL},
			}
			repo := &fakeRepo{trackerMetadata: []api.TrackerMetadata{stored}}
			lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
				tracker: {
					TrackerID:   stored.TrackerID,
					Description: "[spoiler=Comparisons][img]" + oldURL + "[/img][/spoiler]\n\nCurrent notes",
					Images:      []bbcode.Image{{RawURL: freshURL}},
				},
			}}
			svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))
			state := preparationstate.State{
				SourcePath:      sourcePath,
				StoredDataFresh: true,
				InfoHash:        stored.InfoHash,
				TrackerIDs:      map[string]string{strings.ToLower(tracker): stored.TrackerID},
				Policy:          preparationstate.CollectionPolicy{KeepImages: true},
			}
			result, err := svc.collectTrackerEvidence(t.Context(), state)
			if err != nil {
				t.Fatalf("refresh legacy assets: %v", err)
			}
			record, found := trackerRecordFor(result.TrackerData, tracker)
			if !found || record.Description != lookup.results[tracker].Description ||
				!slices.Equal(record.ImageURLs, []string{freshURL}) || !slices.Equal(lookup.Calls(), []string{tracker}) {
				t.Fatalf("legacy comparison URL was reused: record=%#v calls=%#v", record, lookup.Calls())
			}
			if _, err := repo.GetTrackerTimestamp(t.Context(), trackers.TrackerAssetProvenanceKey(record)); err != nil {
				t.Fatalf("refreshed record has no asset provenance: %v", err)
			}
			if _, err := svc.collectTrackerEvidence(t.Context(), state); err != nil || len(lookup.Calls()) != 1 {
				t.Fatalf("verified assets were refetched: calls=%#v err=%v", lookup.Calls(), err)
			}
		})
	}
}

func TestLegacyComparisonCacheWithFailedRefreshWithholdsUnverifiedImages(t *testing.T) {
	_, _, cfg, sourcePath, _ := trackerDuplicateImageFixture(t)
	stored := api.TrackerMetadata{
		SourcePath:  sourcePath,
		Tracker:     "BHD",
		TrackerID:   "72677",
		InfoHash:    "example-hash",
		Description: "Legacy notes",
		ImageURLs:   []string{"https://img.example/legacy-comparison.png"},
	}
	repo := &fakeRepo{trackerMetadata: []api.TrackerMetadata{stored}}
	lookup := &stubTrackerLookup{}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))
	result, err := svc.collectTrackerEvidence(t.Context(), preparationstate.State{
		SourcePath:      sourcePath,
		StoredDataFresh: true,
		InfoHash:        stored.InfoHash,
		TrackerIDs:      map[string]string{"bhd": stored.TrackerID},
		Policy:          preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil {
		t.Fatalf("refresh legacy assets: %v", err)
	}
	record, found := trackerRecordFor(result.TrackerData, "BHD")
	if !found || len(record.ImageURLs) != 0 || len(repo.trackerMetadata) != 1 || len(repo.trackerMetadata[0].ImageURLs) != 0 {
		t.Fatalf("unverified legacy comparison image remained eligible: result=%#v stored=%#v", record, repo.trackerMetadata)
	}
}

func TestFreshSnapshotClearsLegacyImagesForEveryStoredTracker(t *testing.T) {
	_, _, cfg, sourcePath, _ := trackerDuplicateImageFixture(t)
	cfg.Trackers.Trackers["AITHER"] = config.TrackerConfig{APIKey: "aither-key"}
	stored := []api.TrackerMetadata{
		{
			SourcePath:  sourcePath,
			Tracker:     "AITHER",
			TrackerID:   "101",
			InfoHash:    "example-hash",
			TMDBID:      42,
			Description: "Aither notes",
			ImageURLs:   []string{"https://img.example/old-aither.png"},
		},
		{
			SourcePath:  sourcePath,
			Tracker:     "BHD",
			TrackerID:   "202",
			InfoHash:    "example-hash",
			TMDBID:      43,
			Description: "BHD notes",
			ImageURLs:   []string{"https://img.example/old-bhd.png"},
		},
	}
	repo := &fakeRepo{trackerMetadata: stored}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(&stubTrackerLookup{}), WithTrackerRegistry(trackerDataTestRegistry(t)))
	_, err := svc.collectTrackerEvidence(t.Context(), preparationstate.State{
		SourcePath:      sourcePath,
		StoredDataFresh: true,
		InfoHash:        "example-hash",
		TrackerIDs:      map[string]string{"aither": "101"},
		Policy:          preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil {
		t.Fatalf("collect tracker evidence: %v", err)
	}
	for _, record := range repo.trackerMetadata {
		if len(record.ImageURLs) != 0 {
			t.Fatalf("legacy images survived for %s: %#v", record.Tracker, record.ImageURLs)
		}
	}
}

func TestFreshSnapshotRespectsDeletedTrackerImages(t *testing.T) {
	_, _, cfg, sourcePath, _ := trackerDuplicateImageFixture(t)
	cfg.Trackers.Trackers["AITHER"] = config.TrackerConfig{APIKey: "aither-key"}
	for _, remaining := range [][]string{{"https://img.example/remaining.png"}, nil} {
		record := api.TrackerMetadata{
			SourcePath:  sourcePath,
			Tracker:     "AITHER",
			TrackerID:   "101",
			InfoHash:    "example-hash",
			TMDBID:      42,
			Description: "Aither notes",
			ImageURLs:   remaining,
		}
		repo := &fakeRepo{
			trackerMetadata: []api.TrackerMetadata{record},
			trackerTimestamps: []api.TrackerTimestamp{
				{Tracker: trackers.TrackerAssetProvenanceKey(record), UpdatedAt: time.Now()},
				{Tracker: trackers.TrackerImageDeletionKey(record), UpdatedAt: time.Now()},
			},
		}
		if _, err := repo.GetTrackerTimestamp(t.Context(), trackers.TrackerImageDeletionKey(repo.trackerMetadata[0])); err != nil {
			t.Fatalf("image deletion marker missing before collection: %v", err)
		}
		if _, err := repo.GetTrackerTimestamp(t.Context(), trackers.TrackerAssetProvenanceKey(repo.trackerMetadata[0])); err != nil {
			t.Fatalf("asset provenance missing before collection: %v", err)
		}
		lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
			"AITHER": {
				TrackerID:   "101",
				Description: "Aither notes",
				Images:      []bbcode.Image{{RawURL: "https://img.example/deleted.png"}},
			},
		}}
		svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))
		result, err := svc.collectTrackerEvidence(t.Context(), preparationstate.State{
			SourcePath:      sourcePath,
			StoredDataFresh: true,
			InfoHash:        "example-hash",
			TrackerIDs:      map[string]string{"aither": "101"},
			Policy:          preparationstate.CollectionPolicy{KeepImages: true},
		})
		if err != nil {
			t.Fatalf("collect tracker evidence: %v", err)
		}
		if len(lookup.Calls()) != 0 || len(result.TrackerData) != 1 || !slices.Equal(result.TrackerData[0].ImageURLs, remaining) {
			t.Fatalf("deleted images were refetched: remaining=%v calls=%v records=%#v", remaining, lookup.Calls(), result.TrackerData)
		}
	}
}

func TestFreshAitherSnapshotKeepsStoredIDsWhenRefreshIsEmpty(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Example.Movie.2026.1080p-GRP")
	stored := api.TrackerMetadata{
		SourcePath:  sourcePath,
		Tracker:     "AITHER",
		TrackerID:   "72677",
		InfoHash:    "example-hash",
		TMDBID:      42,
		IMDBID:      24,
		Description: "stored description",
	}
	repo := &fakeRepo{trackerMetadata: []api.TrackerMetadata{stored}}
	lookup := &stubTrackerLookup{}
	svc := NewService(repo, WithConfig(config.Config{Trackers: config.TrackersConfig{
		Trackers: map[string]config.TrackerConfig{"AITHER": {APIKey: "aither-key"}},
	}}), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))
	result, err := svc.collectTrackerEvidence(t.Context(), preparationstate.State{
		SourcePath:      sourcePath,
		StoredDataFresh: true,
		InfoHash:        "example-hash",
		TrackerIDs:      map[string]string{"aither": "72677"},
		Policy:          preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil {
		t.Fatalf("collect tracker evidence: %v", err)
	}
	if got := lookup.Calls(); !slices.Equal(got, []string{"AITHER"}) {
		t.Fatalf("lookup calls = %v, want AITHER refresh", got)
	}
	if len(result.TrackerData) != 1 || !reflect.DeepEqual(result.TrackerData[0], stored) {
		t.Fatalf("expected stored record after empty refresh, got %#v", result.TrackerData)
	}
	if len(repo.trackerMetadata) != 1 {
		t.Fatalf("empty refresh overwrote stored tracker metadata: %#v", repo.trackerMetadata)
	}
}

func TestFreshAitherPartialRefreshRetainsStoredImagePreviews(t *testing.T) {
	t.Parallel()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026-GRP.mkv")
	full := "https://images.example.invalid/full.png"
	preview := "https://images.example.invalid/preview.png"
	stored := api.TrackerMetadata{
		SourcePath: sourcePath,
		Tracker:    "AITHER",
		TrackerID:  "72677",
		InfoHash:   "example-hash",
		TMDBID:     42,
		Description: "[img]" + full + "[/img]",
		ImageURLs: []string{full},
		ImagePreviews: map[string]string{full: preview},
	}
	repo := &fakeRepo{
		trackerMetadata: []api.TrackerMetadata{stored},
		trackerTimestamps: []api.TrackerTimestamp{{
			Tracker:   trackers.TrackerAssetProvenanceKey(stored),
			UpdatedAt: time.Now(),
		}},
	}
	lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
		"AITHER": {TrackerID: stored.TrackerID, TMDBID: stored.TMDBID},
	}}
	svc := NewService(repo, WithConfig(config.Config{Trackers: config.TrackersConfig{
		Trackers: map[string]config.TrackerConfig{"AITHER": {APIKey: "aither-key"}},
	}}), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))
	result, err := svc.collectTrackerEvidence(t.Context(), preparationstate.State{
		SourcePath: sourcePath,
		StoredDataFresh: true,
		InfoHash: stored.InfoHash,
		TrackerIDs: map[string]string{"aither": stored.TrackerID},
		Policy:     preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil || len(result.TrackerData) != 1 || len(repo.trackerMetadata) != 1 ||
		result.TrackerData[0].ImagePreviews[full] != preview || repo.trackerMetadata[0].ImagePreviews[full] != preview {
		t.Fatalf("partial refresh lost saved preview: result=%#v stored=%#v err=%v", result.TrackerData, repo.trackerMetadata, err)
	}
}

func TestFreshAitherSnapshotDoesNotPairNewDescriptionWithOldImages(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Example.Movie.2026.1080p-GRP")
	stored := api.TrackerMetadata{
		SourcePath:  sourcePath,
		Tracker:     "AITHER",
		TrackerID:   "72677",
		InfoHash:    "example-hash",
		TMDBID:      42,
		Description: "old description",
		ImageURLs:   []string{"https://images.example/old.png", ""},
	}
	repo := &fakeRepo{trackerMetadata: []api.TrackerMetadata{stored}}
	lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
		"AITHER": {Description: "new description"},
	}}
	svc := NewService(repo, WithConfig(config.Config{Trackers: config.TrackersConfig{
		Trackers: map[string]config.TrackerConfig{"AITHER": {APIKey: "aither-key"}},
	}}), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))
	result, err := svc.collectTrackerEvidence(t.Context(), preparationstate.State{
		SourcePath:      sourcePath,
		StoredDataFresh: true,
		InfoHash:        "example-hash",
		TrackerIDs:      map[string]string{"aither": "72677"},
		Policy:          preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil {
		t.Fatalf("collect tracker evidence: %v", err)
	}
	if len(lookup.Calls()) != 1 || len(result.TrackerData) != 1 ||
		result.TrackerData[0].Description != "new description" ||
		len(result.TrackerData[0].ImageURLs) != 0 {
		t.Fatalf("failed image refresh should keep the new description without old screenshots: %#v", result.TrackerData)
	}
}

func TestFreshAitherSnapshotRefreshesByStoredTrackerIDWhenOnlyInfoHashIsPathed(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Example.Movie.2026.1080p-GRP")
	stored := api.TrackerMetadata{
		SourcePath: sourcePath,
		Tracker:    "AITHER",
		TrackerID:  "72677",
		InfoHash:   "example-hash",
		TMDBID:     42,
	}
	repo := &fakeRepo{trackerMetadata: []api.TrackerMetadata{stored}}
	lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
		"AITHER": {TMDBID: 42, Description: "fetched description"},
	}}
	svc := NewService(repo, WithConfig(config.Config{Trackers: config.TrackersConfig{
		Trackers: map[string]config.TrackerConfig{"AITHER": {APIKey: "aither-key"}},
	}}), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))
	result, err := svc.collectTrackerEvidence(t.Context(), preparationstate.State{
		SourcePath:       sourcePath,
		StoredDataFresh:  true,
		InfoHash:         "example-hash",
		EvidenceTrackers: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("collect tracker evidence: %v", err)
	}
	if got := lookup.TrackerIDFor("AITHER"); got != "72677" {
		t.Fatalf("tracker lookup ID = %q, want stored Aither ID", got)
	}
	if len(result.TrackerData) != 1 || result.TrackerData[0].TrackerID != "72677" || result.TrackerData[0].Description != "fetched description" {
		t.Fatalf("refreshed tracker record lost identity or description: %#v", result.TrackerData)
	}
	if got := repo.trackerMetadata[len(repo.trackerMetadata)-1].TrackerID; got != "72677" {
		t.Fatalf("stored tracker ID overwritten with %q", got)
	}
}

func TestFreshAitherSnapshotCompletesAssetsDuringIDCooldown(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Example.Movie.2026.1080p-GRP")
	repo := &coolingTrackerRepo{
		fakeRepo: &fakeRepo{trackerMetadata: []api.TrackerMetadata{{
			SourcePath: sourcePath,
			Tracker:    "AITHER",
			TrackerID:  "72677",
			InfoHash:   "example-hash",
			TMDBID:     42,
		}}},
		timestamp: time.Now().UTC(),
	}
	lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
		"AITHER": {TrackerID: "72677", Description: "fetched description"},
	}}
	svc := NewService(repo, WithConfig(config.Config{Trackers: config.TrackersConfig{
		Trackers: map[string]config.TrackerConfig{"AITHER": {APIKey: "aither-key"}},
	}}), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))
	result, err := svc.collectTrackerEvidence(t.Context(), preparationstate.State{
		SourcePath:      sourcePath,
		StoredDataFresh: true,
		InfoHash:        "example-hash",
		TrackerIDs:      map[string]string{"aither": "72677"},
		Policy:          preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil {
		t.Fatalf("collect tracker evidence: %v", err)
	}
	if got := lookup.Calls(); !slices.Equal(got, []string{"AITHER"}) {
		t.Fatalf("asset completion was blocked by ID cooldown: calls=%v", got)
	}
	if len(result.TrackerData) != 1 || result.TrackerData[0].Description != "fetched description" {
		t.Fatalf("asset completion result = %#v", result.TrackerData)
	}
	if repo.assetTimestamp.IsZero() {
		t.Fatal("asset refresh attempt did not record a separate cooldown")
	}
	_, err = svc.collectTrackerEvidence(t.Context(), preparationstate.State{
		SourcePath:      sourcePath,
		StoredDataFresh: true,
		InfoHash:        "example-hash",
		TrackerIDs:      map[string]string{"aither": "72677"},
		Policy:          preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil {
		t.Fatalf("collect during asset cooldown: %v", err)
	}
	if got := lookup.Calls(); !slices.Equal(got, []string{"AITHER"}) {
		t.Fatalf("repeated incomplete asset refresh bypassed cooldown: calls=%v", got)
	}
}

func TestFreshAitherSnapshotMergesSparseImageRefreshAndStoredHash(t *testing.T) {
	imageURL, middleURL, cfg, sourcePath, _ := trackerDuplicateImageFixture(t)
	lastURL := "http://93.184.216.34/last.png"
	cfg.Trackers.Trackers["AITHER"] = config.TrackerConfig{APIKey: "aither-key"}
	stored := api.TrackerMetadata{
		SourcePath: sourcePath,
		Tracker:    "AITHER",
		TrackerID:  "72677",
		InfoHash:   "example-hash",
		TMDBID:     42,
		ImageURLs:  []string{"https://images.example/old-1.png", "https://images.example/old-2.png", "https://images.example/old-3.png"},
	}
	repo := &fakeRepo{trackerMetadata: []api.TrackerMetadata{stored}}
	lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
		"AITHER": {
			Description: "fetched description",
			Images: []bbcode.Image{
				{RawURL: imageURL},
				{},
				{RawURL: lastURL},
			},
		},
	}}
	svc := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))
	result, err := svc.collectTrackerEvidence(t.Context(), preparationstate.State{
		SourcePath:      sourcePath,
		StoredDataFresh: true,
		TrackerIDs:      map[string]string{"aither": "72677"},
		Policy:          preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil {
		t.Fatalf("collect tracker evidence: %v", err)
	}
	if len(result.TrackerData) != 1 {
		t.Fatalf("tracker metadata = %#v", result.TrackerData)
	}
	record := result.TrackerData[0]
	if record.InfoHash != "example-hash" || len(record.ImageURLs) != 3 || record.ImageURLs[0] != imageURL ||
		record.ImageURLs[1] != "" || record.ImageURLs[2] != lastURL {
		t.Fatalf("sparse refresh lost stored hash or image position: %#v", record)
	}
	repo.trackerMetadata = []api.TrackerMetadata{record}
	repo.trackerTimestamps = slices.DeleteFunc(repo.trackerTimestamps, func(timestamp api.TrackerTimestamp) bool {
		return timestamp.Tracker == trackerAssetTimestampKey("AITHER")
	})
	lookup.results["AITHER"] = trackerdata.Result{
		Description: "fetched description",
		Images: []bbcode.Image{
			{RawURL: imageURL},
			{RawURL: middleURL},
			{RawURL: lastURL},
		},
	}
	retried, err := svc.collectTrackerEvidence(t.Context(), preparationstate.State{
		SourcePath:      sourcePath,
		StoredDataFresh: true,
		TrackerIDs:      map[string]string{"aither": "72677"},
		Policy:          preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil {
		t.Fatalf("retry tracker evidence: %v", err)
	}
	if len(lookup.Calls()) != 2 || len(retried.TrackerData) != 1 ||
		!slices.Equal(retried.TrackerData[0].ImageURLs, []string{imageURL, middleURL, lastURL}) {
		t.Fatalf("later retry did not fill missing middle image: calls=%v data=%#v", lookup.Calls(), retried.TrackerData)
	}
}

func TestFreshSnapshotPreservesPreferredTrackerIDPriority(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Example.Movie.2026.1080p-GRP")
	const infoHash = "abc123"
	cachedANT := api.TrackerMetadata{
		SourcePath: sourcePath,
		Tracker:    "ANT",
		TrackerID:  "101",
		InfoHash:   infoHash,
		TMDBID:     42,
		IMDBID:     24,
	}
	cachedHDB := api.TrackerMetadata{
		SourcePath: sourcePath,
		Tracker:    "HDB",
		TrackerID:  "202",
		InfoHash:   infoHash,
		TMDBID:     84,
		IMDBID:     48,
	}
	for _, test := range []struct {
		name        string
		stored      []api.TrackerMetadata
		hdbResult   trackerdata.Result
		wantTMDBID  int
		wantIMDBID  int
		wantLookups []string
	}{
		{
			name:       "preferred record cached after lower-priority record",
			stored:     []api.TrackerMetadata{cachedANT, cachedHDB},
			wantTMDBID: 84,
			wantIMDBID: 48,
		},
		{
			name:   "preferred record missing and lookup has IDs",
			stored: []api.TrackerMetadata{cachedANT},
			hdbResult: trackerdata.Result{
				TrackerID: "202",
				TMDBID:    84,
				IMDBID:    48,
			},
			wantTMDBID:  84,
			wantIMDBID:  48,
			wantLookups: []string{"HDB"},
		},
		{
			name:        "preferred lookup has no IDs and lower-priority record is cached",
			stored:      []api.TrackerMetadata{cachedANT},
			wantTMDBID:  42,
			wantIMDBID:  24,
			wantLookups: []string{"HDB"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &fakeRepo{trackerMetadata: test.stored}
			lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{"HDB": test.hdbResult}}
			tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{Title: "Example Movie", Year: 2026}}
			imdbClient := &stubIMDB{infoFn: func(id string) imdb.Info {
				return imdb.Info{
					IMDbID: id,
					Title:  "Example Movie",
					Year:   2026,
				}
			}}
			svc := NewService(repo,
				WithConfig(config.Config{Trackers: config.TrackersConfig{
					PreferredTracker: "HDB",
					Trackers: map[string]config.TrackerConfig{
						"ANT": {APIKey: "ant-key"},
						"HDB": {Username: "user", Passkey: "pass"},
					},
				}}),
				WithTrackerDataLookup(lookup),
				WithTrackerRegistry(trackerDataTestRegistry(t)),
				WithTMDBClient(tmdbClient),
				WithIMDBClient(imdbClient),
			)
			state := preparationstate.State{
				SourcePath:        sourcePath,
				StoredDataFresh:   true,
				InfoHash:          infoHash,
				TrackerIDs:        map[string]string{"ant": "101", "hdb": "202"},
				Policy:            preparationstate.CollectionPolicy{OnlyID: true},
				ExternalFreshness: api.ExternalFreshnessRefresh,
				MediaInfoCategory: "MOVIE",
				Release: api.ReleaseInfo{
					Category: "MOVIE",
					Title:    "Example Movie",
					Year:     2026,
				},
			}
			state, err := svc.collectTrackerEvidence(t.Context(), state)
			if err != nil {
				t.Fatalf("collect tracker evidence: %v", err)
			}
			if got := lookup.Calls(); !slices.Equal(got, test.wantLookups) {
				t.Fatalf("tracker lookups = %v, want %v", got, test.wantLookups)
			}
			state, err = svc.collectExternalIdentityEvidence(t.Context(), state)
			if err != nil {
				t.Fatalf("collect external identity: %v", err)
			}
			if state.Identity.TMDBID != test.wantTMDBID || state.Identity.IMDBID != test.wantIMDBID ||
				state.Identity.Provenance.TMDB != api.IdentityProvenanceTracker ||
				state.Identity.Provenance.IMDB != api.IdentityProvenanceTracker {
				t.Fatalf("external identity did not use preferred tracker IDs: %#v", state.Identity)
			}
			if tmdbClient.searchCalls != 0 || imdbClient.searchCalls != 0 {
				t.Fatalf("name searches: tmdb=%d imdb=%d", tmdbClient.searchCalls, imdbClient.searchCalls)
			}
		})
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
	<table id="post1405482">
	  <tr><td><div id="content1405482" class="postcontent">
	    <strong>Current Shows:</strong><br>
	    Example Show -- BTN -- GRP -- AMZN<br>
	    Another Show (aka: Alt Name) -- BTN -- GRP -- AMZN<br>
	    Upcoming Shows:<br>
	    Ignored Show -- BTN -- GRP -- AMZN
	  </div></td></tr>
	</table>`

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
	  Wrong Show -- BTN -- GRP -- AMZN<br>
	</div>
	<table id="post1405482">
	  <tr>
	    <td>
	      <div id="content1405482" class="postcontent">
	        <strong>Current Shows:</strong><br>
	        Example Show -- BTN -- GRP -- AMZN<br>
	        Another Show (aka: Alt Name) -- BTN -- GRP -- AMZN<br>
	        Upcoming Shows:<br>
	        Ignored Show -- BTN -- GRP -- AMZN
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
	            Example Show (aka: Alt Name) -- BTN -- GRP -- AMZN<br>
	            <div class="note">Some nested wrapper</div>
	            Another Show -- BTN -- GRP -- AMZN<br>
	          </div>
	          <div>
	            <strong>Upcoming Shows:</strong><br>
	            Future Show -- BTN -- GRP -- AMZN<br>
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
