// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func newTestBannedGroupChecker(t *testing.T, dbPath string) *BannedGroupChecker {
	t.Helper()
	return NewBannedGroupCheckerWithRegistry(dbPath, newTestBannedPolicyRegistry(t))
}

func newTestBannedPolicyRegistry(t *testing.T) *Registry {
	t.Helper()
	registry := NewRegistry()
	policies := map[string]struct {
		baseURL string
		policy  BannedGroupPolicy
	}{
		"AITHER": {baseURL: "https://aither.cc", policy: BannedGroupPolicy{EndpointPath: "/api/blacklists/releasegroups", RequireAPIKey: true}},
		"LST":    {baseURL: "https://lst.gg", policy: BannedGroupPolicy{EndpointPath: "/api/bannedReleaseGroups", RequireAPIKey: true}},
		"LUME":   {baseURL: "https://luminarr.me", policy: BannedGroupPolicy{TRaSHGuideURL: trashGuideBannedGroupsURL}},
		"SPD": {policy: BannedGroupPolicy{
			DefaultEndpoint: "https://speedapp.io/api/torrent/release-group/blacklist",
			EndpointPath:    "/api/torrent/release-group/blacklist",
			RequireAPIKey:   true,
		}},
	}
	for name, item := range policies {
		policy := item.policy
		if err := registry.RegisterDescriptor(Descriptor{
			Name:         name,
			BaseURL:      item.baseURL,
			Definition:   stubDefinition{name: name},
			BannedPolicy: &policy,
		}); err != nil {
			t.Fatalf("register %s banned policy: %v", name, err)
		}
	}
	static := map[string][]string{
		"A4K":  {"TEKNO3D"},
		"BLU":  {"TheFarm"},
		"CBR":  {"YTS.MX"},
		"DP":   {"FGT", "PSA", "HorribleSubs", "Subsplease", "SyncUp", "Trix"},
		"GPW":  {"MOMOWEB"},
		"HHD":  {"EVO"},
		"LT":   {"EVO"},
		"MTV":  {"PandaRG"},
		"NBL":  {"YakuboEncodes"},
		"OE":   {"VipapkSudios"},
		"OTW":  {"Sync0rdi"},
		"PHD":  {"VisionXpert"},
		"PTP":  {"WORLD"},
		"PTT":  {"M@RTiNU$"},
		"RAS":  {"INFINITY"},
		"RHD":  {"MagicX"},
		"TOS":  {"FL3ER"},
		"ULCX": {"EDGE2020", "NuBz", "Ralphy"},
		"YUS":  {"YOLAND"},
	}
	for name, groups := range static {
		if err := registry.RegisterDescriptor(Descriptor{
			Name:         name,
			Definition:   stubDefinition{name: name},
			BannedGroups: groups,
		}); err != nil {
			t.Fatalf("register %s static banned groups: %v", name, err)
		}
	}
	return registry
}

type bannedRefreshTestLogger struct {
	debugMessages []string
}

func (l *bannedRefreshTestLogger) Tracef(string, ...any) {}

func (l *bannedRefreshTestLogger) Debugf(format string, args ...any) {
	l.debugMessages = append(l.debugMessages, fmt.Sprintf(format, args...))
}

func (l *bannedRefreshTestLogger) Infof(string, ...any) {}

func (l *bannedRefreshTestLogger) Warnf(string, ...any) {}

func (l *bannedRefreshTestLogger) Errorf(string, ...any) {}

func TestNewBannedGroupCheckerFromDBPath(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	checker := newTestBannedGroupChecker(t, filepath.Join(tempDir, "db.sqlite"))
	if checker == nil {
		t.Fatalf("expected checker, got nil")
	}
	bannedDir := filepath.Join(tempDir, "cache", "banned")
	if checker.basePath != bannedDir {
		t.Fatalf("expected base path %q, got %q", bannedDir, checker.basePath)
	}
}
func TestNewBannedGroupCheckerNoPathUsesDefaultRoot(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	checker := newTestBannedGroupChecker(t, " ")
	if checker == nil {
		t.Fatalf("expected checker")
	}
	expected := filepath.Join(home, ".upbrr", "cache", "banned")
	if checker.basePath != expected {
		t.Fatalf("expected base path %q, got %q", expected, checker.basePath)
	}
}

func TestBannedGroupCheckerStaticBuiltins(t *testing.T) {
	t.Parallel()

	checker := newTestBannedGroupChecker(t, filepath.Join(t.TempDir(), "db.sqlite"))
	cases := map[string][]string{
		"A4K":  {"TEKNO3D"},
		"BLU":  {"TheFarm"},
		"CBR":  {"YTS.MX"},
		"DP":   {"FGT", "PSA", "HorribleSubs", "Subsplease", "SyncUp", "Trix"},
		"GPW":  {"MOMOWEB"},
		"HHD":  {"EVO"},
		"LT":   {"EVO"},
		"MTV":  {"PandaRG"},
		"NBL":  {"YakuboEncodes"},
		"OE":   {"VipapkSudios"},
		"OTW":  {"Sync0rdi"},
		"PHD":  {"VisionXpert"},
		"PTP":  {"WORLD"},
		"PTT":  {"M@RTiNU$"},
		"RAS":  {"INFINITY"},
		"ULCX": {"EDGE2020", "NuBz", "Ralphy"},
		"YUS":  {"YOLAND"},
	}
	for tracker, groups := range cases {
		for _, group := range groups {
			banned, err := checker.IsBanned(tracker, group)
			if err != nil {
				t.Fatalf("check %s/%s: %v", tracker, group, err)
			}
			if !banned {
				t.Fatalf("expected %s to be banned on %s", group, tracker)
			}
		}
	}
}

func TestBannedGroupCheckerMergesBuiltinsWithCacheFile(t *testing.T) {
	t.Parallel()

	checker := newTestBannedGroupChecker(t, filepath.Join(t.TempDir(), "db.sqlite"))
	if checker == nil {
		t.Fatalf("expected checker")
	}
	if err := os.MkdirAll(checker.basePath, 0o700); err != nil {
		t.Fatalf("create banned cache dir: %v", err)
	}
	filePath := filepath.Join(checker.basePath, "RHD_banned_groups.json")
	if err := os.WriteFile(filePath, []byte(`{"banned_groups":"CustomRHD, Another.Custom"}`), 0o600); err != nil {
		t.Fatalf("write banned groups: %v", err)
	}

	for _, group := range []string{"MagicX", "CustomRHD", "another.custom"} {
		banned, err := checker.IsBanned("RHD", group)
		if err != nil {
			t.Fatalf("check %s: %v", group, err)
		}
		if !banned {
			t.Fatalf("expected %s to be banned on RHD", group)
		}
	}
}

func TestBannedGroupCheckerUnreadableCacheFallsBackToRhdBuiltins(t *testing.T) {
	t.Parallel()

	checker := newTestBannedGroupChecker(t, filepath.Join(t.TempDir(), "db.sqlite"))
	if checker == nil {
		t.Fatalf("expected checker")
	}
	if err := os.MkdirAll(checker.basePath, 0o700); err != nil {
		t.Fatalf("create banned cache dir: %v", err)
	}
	filePath := filepath.Join(checker.basePath, "RHD_banned_groups.json")
	if err := os.Mkdir(filePath, 0o700); err != nil {
		t.Fatalf("create unreadable banned groups path: %v", err)
	}

	banned, err := checker.IsBanned(" rhd ", " MagicX ")
	if err != nil {
		t.Fatalf("check builtin after unreadable cache: %v", err)
	}
	if !banned {
		t.Fatalf("expected MagicX to be banned on RHD")
	}

	banned, err = checker.IsBanned("RHD", "CustomRHD")
	if err == nil {
		t.Fatalf("expected unreadable cache error")
	}
	if !strings.Contains(err.Error(), "RHD_banned_groups.json") {
		t.Fatalf("expected error to include cache path, got %v", err)
	}
	if banned {
		t.Fatalf("expected unread custom group not to be banned without readable cache")
	}

	if err := os.Remove(filePath); err != nil {
		t.Fatalf("remove unreadable banned groups path: %v", err)
	}
	if err := os.WriteFile(filePath, []byte(`{"banned_groups":"CustomRHD"}`), 0o600); err != nil {
		t.Fatalf("write banned groups: %v", err)
	}
	banned, err = checker.IsBanned("RHD", "CustomRHD")
	if err != nil {
		t.Fatalf("check custom group after readable cache: %v", err)
	}
	if !banned {
		t.Fatalf("expected CustomRHD to be banned after readable cache")
	}
}

func TestBannedGroupCheckerDPDoesNotIncludeRemovedHDT(t *testing.T) {
	t.Parallel()

	checker := newTestBannedGroupChecker(t, filepath.Join(t.TempDir(), "db.sqlite"))
	banned, err := checker.IsBanned("DP", "HDT")
	if err != nil {
		t.Fatalf("check HDT: %v", err)
	}
	if banned {
		t.Fatalf("expected HDT not to be banned on DP")
	}
}

func TestRefreshDynamicBannedGroupsCachesAither(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.URL.Path; got != "/api/blacklists/releasegroups" {
			t.Errorf("unexpected path %q", got)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer aither-key" {
			t.Error("unexpected auth header")
			return
		}
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("unexpected per_page %q", got)
			return
		}
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{
				"data":[{"attributes":{"name":"GRP"}},{"attributes":{"release_group":"AnotherGRP"}}],
				"meta":{"next_cursor":"next"}
			}`))
		case "next":
			_, _ = w.Write([]byte(`{
				"data":[{"name":"ThirdGRP"}],
				"meta":{"next_cursor":""}
			}`))
		default:
			t.Errorf("unexpected cursor")
		}
	}))
	defer server.Close()

	tempDir := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "upbrr.db")},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {APIKey: "aither-key", URL: server.URL},
			},
		},
	}
	checker := newTestBannedGroupChecker(t, cfg.MainSettings.DBPath)

	if err := checker.RefreshDynamic(context.Background(), cfg, []string{"AITHER"}, api.NopLogger{}); err != nil {
		t.Fatalf("refresh banned groups: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("expected paginated fetch, got %d requests", got)
	}

	cachePath := filepath.Join(tempDir, "cache", "banned", "AITHER_banned_groups.json")
	payload, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read banned groups cache: %v", err)
	}
	var cache bannedGroupsFile
	if err := json.Unmarshal(payload, &cache); err != nil {
		t.Fatalf("unmarshal banned groups cache: %v", err)
	}
	for _, group := range []string{"AnotherGRP", "GRP", "ThirdGRP"} {
		if !strings.Contains(cache.BannedGroups, group) {
			t.Fatalf("expected cached banned group %q in %q", group, cache.BannedGroups)
		}
	}
	if len(cache.RawData) != 3 {
		t.Fatalf("expected raw cache data for 3 groups, got %d", len(cache.RawData))
	}

	banned, err := checker.IsBanned("AITHER", "grp")
	if err != nil {
		t.Fatalf("check refreshed banned group: %v", err)
	}
	if !banned {
		t.Fatalf("expected refreshed group to be banned")
	}

	if err := checker.RefreshDynamic(context.Background(), cfg, []string{"AITHER"}, api.NopLogger{}); err != nil {
		t.Fatalf("refresh fresh banned groups: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("expected fresh cache to avoid refetch, got %d requests", got)
	}
}

func TestRefreshDynamicBannedGroupsRejectsTrailingJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"name":"BadGRP"}],"meta":{"next_cursor":""}} true`))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "upbrr.db")},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {APIKey: "aither-key", URL: server.URL},
			},
		},
	}

	_, _, err := fetchDynamicBannedGroups(context.Background(), cfg, "AITHER", newTestBannedPolicyRegistry(t))
	if err == nil {
		t.Fatalf("expected trailing JSON error")
	}
	if !strings.Contains(err.Error(), "trailing JSON value") {
		t.Fatalf("expected trailing JSON error, got %v", err)
	}

	checker := newTestBannedGroupChecker(t, cfg.MainSettings.DBPath)
	if err := checker.RefreshDynamic(context.Background(), cfg, []string{"AITHER"}, api.NopLogger{}); err != nil {
		t.Fatalf("refresh banned groups with malformed response: %v", err)
	}
	cachePath := filepath.Join(tempDir, "cache", "banned", "AITHER_banned_groups.json")
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("expected malformed response not to write cache, stat err=%v", err)
	}
}

func TestRefreshDynamicDoesNotLogUnsupportedTrackers(t *testing.T) {
	t.Parallel()

	checker := newTestBannedGroupChecker(t, filepath.Join(t.TempDir(), "db.sqlite"))
	logger := &bannedRefreshTestLogger{}

	if err := checker.RefreshDynamic(context.Background(), config.Config{}, []string{"DP", "BHD", "MTV"}, logger); err != nil {
		t.Fatalf("refresh unsupported banned groups: %v", err)
	}
	if len(logger.debugMessages) != 0 {
		t.Fatalf("expected unsupported trackers not to log banned refresh skip")
	}
}

func TestFetchDynamicBannedGroupsRejectsRepeatedCursor(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{
				"data":[{"name":"FirstGRP"}],
				"meta":{"next_cursor":"again"}
			}`))
		case "again":
			_, _ = w.Write([]byte(`{
				"data":[{"name":"SecondGRP"}],
				"meta":{"next_cursor":"again"}
			}`))
		default:
			t.Errorf("unexpected cursor %q", r.URL.Query().Get("cursor"))
		}
	}))
	defer server.Close()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {APIKey: "aither-key", URL: server.URL},
			},
		},
	}
	_, _, err := fetchDynamicBannedGroups(context.Background(), cfg, "AITHER", newTestBannedPolicyRegistry(t))
	if err == nil {
		t.Fatalf("expected repeated cursor error")
	}
	if !strings.Contains(err.Error(), `repeated cursor "again"`) {
		t.Fatalf("expected repeated cursor error, got %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("expected bounded repeated cursor fetches, got %d", got)
	}
}

func TestFetchDynamicBannedGroupsPageRetriesSPDRawKeyOnBearerAuthFailure(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			authValues := make(chan string, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				authValues <- r.Header.Get("Authorization")
				if len(authValues) == 1 {
					w.WriteHeader(status)
					return
				}
				_, _ = w.Write([]byte(`{"data":[{"name":"SPDGROUP"}],"meta":{"next_cursor":""}}`))
			}))
			defer server.Close()

			groups, _, nextCursor, err := fetchDynamicBannedGroupsPage(context.Background(), server.Client(), server.URL, "SPD", "spd-key", "", true)
			if err != nil {
				t.Fatalf("fetch SPD banned groups: %v", err)
			}
			if nextCursor != "" {
				t.Fatalf("expected empty next cursor, got %q", nextCursor)
			}
			if !slices.Equal(groups, []string{"SPDGROUP"}) {
				t.Fatalf("expected fetched SPD group, got %#v", groups)
			}
			close(authValues)
			got := []string{}
			for value := range authValues {
				got = append(got, value)
			}
			if !slices.Equal(got, []string{"Bearer spd-key", "spd-key"}) {
				t.Fatalf("expected bearer then raw-key fallback authorization sequence")
			}
		})
	}
}

func TestFetchDynamicBannedGroupsPageDoesNotRetrySPDRawKeyOnNonAuthFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer spd-key" {
			t.Errorf("expected bearer authorization only")
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, _, _, err := fetchDynamicBannedGroupsPage(context.Background(), server.Client(), server.URL, "SPD", "spd-key", "", true)
	if err == nil {
		t.Fatalf("expected non-auth status error")
	}
	if !strings.Contains(err.Error(), "status 429") {
		t.Fatalf("expected status error, got %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("expected no raw-key retry for non-auth status, got %d request(s)", got)
	}
}

func TestFetchDynamicBannedGroupsRejectsOversizedSuccessBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte{' '}, int(maxBannedGroupsResponseBytes)+1))
	}))
	defer server.Close()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {APIKey: "aither-key", URL: server.URL},
			},
		},
	}
	_, _, err := fetchDynamicBannedGroups(context.Background(), cfg, "AITHER", newTestBannedPolicyRegistry(t))
	if err == nil {
		t.Fatalf("expected oversized response error")
	}
	if !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}

func TestFetchTRaSHGuideBannedGroupsRejectsOversizedSuccessBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte{' '}, int(maxBannedGroupsResponseBytes)+1))
	}))
	defer server.Close()

	_, _, err := fetchTRaSHGuideBannedGroups(context.Background(), server.URL)
	if err == nil {
		t.Fatalf("expected oversized response error")
	}
	if !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}

func TestTrashGuideReleaseGroupNamesExpandsCurrentPatternShapes(t *testing.T) {
	cases := map[string][]string{
		`^(MTeam|MT)$`:                {"MTeam", "MT"},
		`^(jennaortega(UHD)?)$`:       {"jennaortega", "jennaortegaUHD"},
		`NoGr(ou)?p`:                  {"NoGrp", "NoGroup"},
		`Pahe(\.(ph|in))?\b`:          {"Pahe", "Pahe.in", "Pahe.ph"},
		`^(VISIONPLUSHDR(-X|1000)?)$`: {"VISIONPLUSHDR", "VISIONPLUSHDR-X", "VISIONPLUSHDR1000"},
		`^(YTS(.(MX|LT|AG))?)$`:       {"YTS", "YTS.AG", "YTS.LT", "YTS.MX"},
	}

	for pattern, expected := range cases {
		got := trashGuideReleaseGroupNames(pattern)
		for _, group := range expected {
			if !containsString(got, group) {
				t.Fatalf("pattern %q missing %q in %v", pattern, group, got)
			}
		}
		if len(got) != len(expected) {
			t.Fatalf("pattern %q expected %d groups, got %d: %v", pattern, len(expected), len(got), got)
		}
	}
}

func containsString(values []string, target string) bool {
	return slices.Contains(values, target)
}

func TestRefreshDynamicFreshCacheInvalidatesLoadedGroups(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "upbrr.db")},
	}
	checker := newTestBannedGroupChecker(t, cfg.MainSettings.DBPath)
	cachePath := filepath.Join(tempDir, "cache", "banned", "AITHER_banned_groups.json")
	if err := writeBannedGroupsCache(cachePath, []string{"OldGRP"}, nil); err != nil {
		t.Fatalf("write old banned groups cache: %v", err)
	}

	banned, err := checker.IsBanned("AITHER", "OldGRP")
	if err != nil {
		t.Fatalf("check old group: %v", err)
	}
	if !banned {
		t.Fatalf("expected old group to be loaded before refresh")
	}

	if err := writeBannedGroupsCache(cachePath, []string{"NewGRP"}, nil); err != nil {
		t.Fatalf("write new banned groups cache: %v", err)
	}
	if err := checker.RefreshDynamic(context.Background(), cfg, []string{"AITHER"}, api.NopLogger{}); err != nil {
		t.Fatalf("refresh fresh banned groups: %v", err)
	}

	banned, err = checker.IsBanned("AITHER", "NewGRP")
	if err != nil {
		t.Fatalf("check new group: %v", err)
	}
	if !banned {
		t.Fatalf("expected fresh cache file to replace loaded groups")
	}
	banned, err = checker.IsBanned("AITHER", "OldGRP")
	if err != nil {
		t.Fatalf("check old group after refresh: %v", err)
	}
	if banned {
		t.Fatalf("expected stale loaded group to be invalidated")
	}
}

func TestRefreshDynamicCanceledAfterFetchDoesNotWriteOrInvalidate(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "upbrr.db")},
	}
	checker := newTestBannedGroupChecker(t, cfg.MainSettings.DBPath)
	cachePath := filepath.Join(tempDir, "cache", "banned", "AITHER_banned_groups.json")
	if err := writeBannedGroupsCache(cachePath, []string{"OldGRP"}, nil); err != nil {
		t.Fatalf("write old banned groups cache: %v", err)
	}

	banned, err := checker.IsBanned("AITHER", "OldGRP")
	if err != nil {
		t.Fatalf("load old banned group: %v", err)
	}
	if !banned {
		t.Fatalf("expected old group to be loaded before refresh")
	}
	stale := time.Now().Add(-bannedGroupsCacheTTL - time.Minute)
	if err := os.Chtimes(cachePath, stale, stale); err != nil {
		t.Fatalf("make banned groups cache stale: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	fetch := func(context.Context, config.Config, string) ([]string, []json.RawMessage, error) {
		cancel()
		return []string{"NewGRP"}, []json.RawMessage{json.RawMessage(`{"name":"NewGRP"}`)}, nil
	}
	err = checker.refreshDynamic(ctx, cfg, []string{"AITHER"}, api.NopLogger{}, fetch)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled refresh error, got %v", err)
	}

	payload, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read banned groups cache: %v", err)
	}
	if strings.Contains(string(payload), "NewGRP") {
		t.Fatalf("expected canceled refresh not to write fetched group")
	}
	if !strings.Contains(string(payload), "OldGRP") {
		t.Fatalf("expected canceled refresh to keep old cache")
	}

	banned, err = checker.IsBanned("AITHER", "OldGRP")
	if err != nil {
		t.Fatalf("check old group after canceled refresh: %v", err)
	}
	if !banned {
		t.Fatalf("expected canceled refresh not to invalidate loaded groups")
	}
	banned, err = checker.IsBanned("AITHER", "NewGRP")
	if err != nil {
		t.Fatalf("check new group after canceled refresh: %v", err)
	}
	if banned {
		t.Fatalf("expected canceled refresh not to load fetched group")
	}
}

func TestWriteBannedGroupsCacheReplacesExistingFile(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "AITHER_banned_groups.json")
	if err := writeBannedGroupsCache(cachePath, []string{"OldGRP"}, nil); err != nil {
		t.Fatalf("write old banned groups cache: %v", err)
	}
	if err := writeBannedGroupsCache(cachePath, []string{"NewGRP"}, nil); err != nil {
		t.Fatalf("replace banned groups cache: %v", err)
	}

	payload, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read replaced banned groups cache: %v", err)
	}
	if strings.Contains(string(payload), "OldGRP") {
		t.Fatalf("expected old group to be replaced")
	}
	if !strings.Contains(string(payload), "NewGRP") {
		t.Fatalf("expected new group in replaced cache")
	}
}

func TestReplaceBannedGroupsCacheFileRestoresExistingOnFailure(t *testing.T) {
	tempDir := t.TempDir()
	cachePath := filepath.Join(tempDir, "AITHER_banned_groups.json")
	original := []byte(`{"banned_groups":"OldGRP"}`)
	if err := os.WriteFile(cachePath, original, 0o600); err != nil {
		t.Fatalf("write original banned groups cache: %v", err)
	}

	err := replaceBannedGroupsCacheFile(filepath.Join(tempDir, "missing.tmp"), cachePath)
	if err == nil {
		t.Fatalf("expected replacement failure")
	}
	payload, readErr := os.ReadFile(cachePath)
	if readErr != nil {
		t.Fatalf("read restored banned groups cache: %v", readErr)
	}
	if string(payload) != string(original) {
		t.Fatalf("expected original cache to be restored, got %q", payload)
	}
}

func TestReplaceBannedGroupsCacheFileIgnoresBackupCleanupAfterCommit(t *testing.T) {
	tempDir := t.TempDir()
	cachePath := filepath.Join(tempDir, "AITHER_banned_groups.json")
	tmpPath := filepath.Join(tempDir, "AITHER_banned_groups.json.tmp")
	original := []byte(`{"banned_groups":"OldGRP"}`)
	replacement := []byte(`{"banned_groups":"NewGRP"}`)
	if err := os.WriteFile(cachePath, original, 0o600); err != nil {
		t.Fatalf("write original banned groups cache: %v", err)
	}
	if err := os.WriteFile(tmpPath, replacement, 0o600); err != nil {
		t.Fatalf("write replacement banned groups cache: %v", err)
	}

	renameCalls := 0
	rename := func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 1 {
			return errors.New("direct replacement unsupported")
		}
		return os.Rename(oldPath, newPath)
	}
	var cleanupPath string
	remove := func(path string) error {
		if strings.HasPrefix(filepath.Base(path), filepath.Base(cachePath)+".backup-") {
			cleanupPath = path
			return errors.New("cleanup failed")
		}
		return os.Remove(path)
	}

	if err := replaceBannedGroupsCacheFileWithOps(tmpPath, cachePath, rename, remove); err != nil {
		t.Fatalf("replace committed cache despite backup cleanup failure: %v", err)
	}
	payload, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read replaced banned groups cache: %v", err)
	}
	if string(payload) != string(replacement) {
		t.Fatalf("expected replacement cache to be committed, got %q", payload)
	}
	if cleanupPath == "" {
		t.Fatalf("expected backup cleanup to be attempted")
	}
}

func TestRefreshDynamicBannedGroupsCachesLUMEFromTRaSHGuide(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.URL.Path; got != "/trash/lq.json" {
			t.Errorf("unexpected path %q", got)
			return
		}
		_, _ = w.Write([]byte(`{
			"specifications":[
				{"implementation":"ReleaseGroupSpecification","fields":{"value":"^(AlphaGRP|BetaGRP)$"}},
				{"implementation":"ReleaseGroupSpecification","fields":{"value":"\\b(GammaGRP)\\b"}},
				{"implementation":"ReleaseGroupSpecification","fields":{"value":"^DeltaGRP$"}},
				{"implementation":"OtherSpecification","fields":{"value":"^(IgnoredGRP)$"}}
			]
		}`))
	}))
	defer server.Close()

	originalURL := trashGuideBannedGroupsURL
	trashGuideBannedGroupsURL = server.URL + "/trash/lq.json"
	t.Cleanup(func() {
		trashGuideBannedGroupsURL = originalURL
	})

	tempDir := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "upbrr.db")},
	}
	checker := newTestBannedGroupChecker(t, cfg.MainSettings.DBPath)

	if err := checker.RefreshDynamic(context.Background(), cfg, []string{"LUME"}, api.NopLogger{}); err != nil {
		t.Fatalf("refresh banned groups: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("expected TRaSH fetch, got %d requests", got)
	}

	cachePath := filepath.Join(tempDir, "cache", "banned", "LUME_banned_groups.json")
	payload, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read LUME banned groups cache: %v", err)
	}
	var cache bannedGroupsFile
	if err := json.Unmarshal(payload, &cache); err != nil {
		t.Fatalf("unmarshal LUME banned groups cache: %v", err)
	}
	for _, group := range []string{"AlphaGRP", "BetaGRP", "DeltaGRP", "GammaGRP"} {
		if !strings.Contains(cache.BannedGroups, group) {
			t.Fatalf("expected cached LUME banned group %q in %q", group, cache.BannedGroups)
		}
	}
	if strings.Contains(cache.BannedGroups, "IgnoredGRP") {
		t.Fatalf("expected non-release-group TRaSH spec to be ignored")
	}

	banned, err := checker.IsBanned("LUME", "betagrp")
	if err != nil {
		t.Fatalf("check refreshed LUME banned group: %v", err)
	}
	if !banned {
		t.Fatalf("expected refreshed LUME group to be banned")
	}

	if err := checker.RefreshDynamic(context.Background(), cfg, []string{"LUME"}, api.NopLogger{}); err != nil {
		t.Fatalf("refresh fresh LUME banned groups: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("expected fresh LUME cache to avoid refetch, got %d requests", got)
	}
}

func TestRefreshDynamicBannedGroupsMissingAPIKeyDoesNotWriteEmptyCache(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "upbrr.db")},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {URL: "https://aither.example.invalid"},
			},
		},
	}
	checker := newTestBannedGroupChecker(t, cfg.MainSettings.DBPath)

	if err := checker.RefreshDynamic(context.Background(), cfg, []string{"AITHER"}, api.NopLogger{}); err != nil {
		t.Fatalf("refresh banned groups: %v", err)
	}
	cachePath := filepath.Join(tempDir, "cache", "banned", "AITHER_banned_groups.json")
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("expected no empty banned groups cache, stat err=%v", err)
	}
}
