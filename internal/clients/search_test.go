// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/unit3dmeta"
	"github.com/autobrr/upbrr/pkg/api"

	"github.com/anacrolix/torrent/metainfo"
	qbittorrent "github.com/autobrr/go-qbittorrent"
	mkbrr "github.com/autobrr/mkbrr/torrent"
)

type captureLogger struct {
	debug []string
}

func (l *captureLogger) Tracef(string, ...any) {}

func (l *captureLogger) Debugf(format string, args ...any) {
	l.debug = append(l.debug, fmt.Sprintf(format, args...))
}

func (l *captureLogger) Infof(string, ...any) {}

func (l *captureLogger) Warnf(string, ...any) {}

func (l *captureLogger) Errorf(string, ...any) {}

func TestSearchPathedTorrentsProxyPrefersPieceSize(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	hashLarge, dataLarge := createTestTorrent(t, dir, "Movie.Title.2024.large.mkv", 25)
	hashSmall, dataSmall := createTestTorrent(t, dir, "Movie.Title.2024.mkv", 22)
	dataByHash := map[string][]byte{
		hashLarge: dataLarge,
		hashSmall: dataSmall,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/search":
			items := []qbittorrent.Torrent{
				{
					Hash:        hashLarge,
					Name:        "Movie.Title.2024",
					SavePath:    "/data",
					Size:        123,
					Category:    "movies",
					NumComplete: 5,
					Tracker:     "https://blutopia.cc/announce",
					Comment:     "https://blutopia.cc/torrents/1234",
					Trackers:    []qbittorrent.TorrentTracker{{Url: "https://blutopia.cc/announce", Status: qbittorrent.TrackerStatusOK}},
				},
				{
					Hash:        hashSmall,
					Name:        "Movie.Title.2024",
					SavePath:    "/data",
					Size:        123,
					Category:    "movies",
					NumComplete: 8,
					Tracker:     "https://blutopia.cc/announce",
					Comment:     "https://blutopia.cc/torrents/9999",
					Trackers:    []qbittorrent.TorrentTracker{{Url: "https://blutopia.cc/announce", Status: qbittorrent.TrackerStatusOK}},
				},
			}
			_ = json.NewEncoder(w).Encode(items)
		case "/api/v2/torrents/properties":
			hash := r.URL.Query().Get("hash")
			props := qbittorrent.TorrentProperties{}
			if hash == hashLarge {
				props.Comment = "https://blutopia.cc/torrents/1234"
				props.PieceSize = 32 * 1024 * 1024
			} else {
				props.Comment = "https://blutopia.cc/torrents/9999"
				props.PieceSize = 4 * 1024 * 1024
			}
			_ = json.NewEncoder(w).Encode(props)
		case "/api/v2/torrents/export":
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			hash := r.FormValue("hash")
			data, ok := dataByHash[hash]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(data)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{
			TMDBAPI: "x",
			DBPath:  filepath.Join(dir, "db.sqlite"),
		},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "qbit",
			SearchClients: config.CSVList{"qbit"},
		},
		TorrentCreation: config.TorrentCreationConfig{PreferMax16: true},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:        "qui",
				QuiProxyURL: server.URL,
			},
		},
	}

	svc := NewService(cfg, api.NopLogger{})
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/Movie.Title.2024",
		FileList:   []string{"/tmp/Movie.Title.2024.mkv"},
	}

	result, err := svc.SearchPathedTorrents(context.Background(), meta)
	if err != nil {
		t.Fatalf("search pathed torrents: %v", err)
	}
	if result.InfoHash != hashSmall {
		t.Fatalf("expected preferred hash-small, got %q", result.InfoHash)
	}
	if result.FoundPreferredPiece != "16MiB" {
		t.Fatalf("expected preferred piece size 16MiB, got %q", result.FoundPreferredPiece)
	}
	if result.PieceSizeConstraint != "16MiB" {
		t.Fatalf("expected piece constraint 16MiB, got %q", result.PieceSizeConstraint)
	}
	if len(result.TorrentComments) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(result.TorrentComments))
	}
	if result.TrackerIDs["blu"] != "9999" {
		t.Fatalf("expected tracker ID from best match, got %q", result.TrackerIDs["blu"])
	}
	if !containsString(result.MatchedTrackers, "BLU") {
		t.Fatalf("expected BLU in matched trackers, got %v", result.MatchedTrackers)
	}
	if result.TorrentPath == "" {
		t.Fatalf("expected torrent path to be set")
	}
	if _, err := os.Stat(result.TorrentPath); err != nil {
		t.Fatalf("expected torrent file to exist, got %v", err)
	}
}

func TestSearchPathedTorrentsProxyStripsSymbolsFromSearch(t *testing.T) {
	t.Parallel()

	searchQueries := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/search":
			search := r.URL.Query().Get("search")
			searchQueries = append(searchQueries, search)
			if strings.Contains(search, "\u2122") {
				_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{})
				return
			}
			items := []qbittorrent.Torrent{
				{
					Hash:        strings.Repeat("a", 40),
					Name:        "Fixture Title 2024 2160p AUS UHD Blu-ray DV HDR HEVC DTS-HD MA 5.1-FixtureGroup",
					Tracker:     "https://tracker.beyond-hd.me/announce/redacted",
					Comment:     "https://beyond-hd.me/details/10001",
					Trackers:    []qbittorrent.TorrentTracker{{Url: "https://tracker.beyond-hd.me/announce/redacted", Status: qbittorrent.TrackerStatusOK}},
					NumComplete: 2,
				},
				{
					Hash:        strings.Repeat("b", 40),
					Name:        "Fixture Title 2024 2160p AUS UHD Blu-ray DV HDR HEVC DTS-HD MA 5.1-FixtureGroup",
					Tracker:     "https://passthepopcorn.me/announce/redacted",
					Comment:     "https://passthepopcorn.me/torrents.php?id=100&torrentid=10002",
					Trackers:    []qbittorrent.TorrentTracker{{Url: "https://passthepopcorn.me/announce/redacted", Status: qbittorrent.TrackerStatusOK}},
					NumComplete: 1,
				},
			}
			_ = json.NewEncoder(w).Encode(items)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{TMDBAPI: "x", DBPath: t.TempDir()},
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "qbit",
			SearchClients: config.CSVList{"qbit"},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:        "qui",
				QuiProxyURL: server.URL,
			},
		},
	}

	svc := NewService(cfg, api.NopLogger{})
	result, err := svc.SearchPathedTorrents(context.Background(), api.PreparedMetadata{
		SourcePath: `D:\Movies\Fixture Title 2024 2160p AUS UHD Blu-ray DV HDR HEVC DTS-HD MA 5.1-FixtureGroup` + "\u2122",
		DiscType:   "BDMV",
	})
	if err != nil {
		t.Fatalf("search pathed torrents: %v", err)
	}
	if len(searchQueries) != 1 {
		t.Fatalf("expected one proxy search, got %d", len(searchQueries))
	}
	if strings.Contains(searchQueries[0], "\u2122") {
		t.Fatalf("expected proxy search term without trademark symbol, got %q", searchQueries[0])
	}
	if result.TrackerIDs["bhd"] != "10001" {
		t.Fatalf("expected BHD tracker id, got %q", result.TrackerIDs["bhd"])
	}
	if result.TrackerIDs["ptp"] != "10002" {
		t.Fatalf("expected PTP tracker id, got %q", result.TrackerIDs["ptp"])
	}
	if !containsString(result.MatchedTrackers, "BHD") {
		t.Fatalf("expected BHD in matched trackers, got %v", result.MatchedTrackers)
	}
	if !containsString(result.MatchedTrackers, "PTP") {
		t.Fatalf("expected PTP in matched trackers, got %v", result.MatchedTrackers)
	}
}

func TestLogPathedSearchMatchesRedactsTrackerURLs(t *testing.T) {
	t.Parallel()

	logger := &captureLogger{}
	logPathedSearchMatches(logger, []api.TorrentMatch{{
		Hash:              strings.Repeat("A", 40),
		Name:              "Fixture.Title.2024",
		SavePath:          "/data",
		ContentPath:       "/data/Fixture.Title.2024",
		Size:              123,
		Category:          "movies",
		Seeders:           7,
		Tracker:           "https://tracker.beyond-hd.me/announce/passkey",
		HasWorkingTracker: true,
		TrackerURLsRaw:    []string{"https://tracker.beyond-hd.me/announce/passkey"},
		TrackerURLs:       []api.TrackerMatch{{ID: "bhd", TrackerID: "10001"}},
	}})

	joined := strings.Join(logger.debug, "\n")
	if !strings.Contains(joined, "Fixture.Title.2024") || !strings.Contains(joined, "BHD:10001") {
		t.Fatalf("expected match details in debug log, got %q", joined)
	}
	if strings.Contains(joined, "announce") || strings.Contains(joined, "passkey") || strings.Contains(joined, "beyond-hd.me") {
		t.Fatalf("expected tracker URLs redacted from debug log, got %q", joined)
	}
}

func TestMatchTrackerURLsMatchesBTNLandOfTVAnnounce(t *testing.T) {
	t.Parallel()

	matched := matchTrackerURLs([]string{"https://landof.tv/redacted/announce"})
	if !containsString(matched, "BTN") {
		t.Fatalf("expected BTN in matched trackers, got %v", matched)
	}
}

func TestEnsureMatchedTrackersForKnownIDsAddsBTN(t *testing.T) {
	t.Parallel()

	matched := ensureMatchedTrackersForKnownIDs(nil, map[string]string{"btn": "2202392"})
	if !containsString(matched, "BTN") {
		t.Fatalf("expected BTN in matched trackers, got %v", matched)
	}
}

func TestExtractTrackerMatchesHandlesReelFlixAliasComment(t *testing.T) {
	t.Parallel()

	matches, found := extractTrackerMatches(
		"https://reelflix.xyz/torrents/10003",
		[]string{"https://reelflix.cc/announce/redacted"},
		true,
		[]string{"rf"},
	)

	if !found {
		t.Fatalf("expected RF tracker match")
	}
	if len(matches) != 1 || matches[0].ID != "rf" || matches[0].TrackerID != "10003" {
		t.Fatalf("expected RF tracker id 10003, got %#v", matches)
	}
}

func TestExtractTrackerMatchesHandlesRetroFlixBrowseComment(t *testing.T) {
	t.Parallel()

	matches, found := extractTrackerMatches(
		"https://retroflix.club/browse/t/10004",
		[]string{"http://peer.retroflix.club/announce.php?passkey=redacted"},
		true,
		[]string{"rtf"},
	)

	if !found {
		t.Fatalf("expected RTF tracker match")
	}
	if len(matches) != 1 || matches[0].ID != "rtf" || matches[0].TrackerID != "10004" {
		t.Fatalf("expected RTF tracker id 10004, got %#v", matches)
	}
}

func TestExtractTrackerMatchesIncludesPatternsOutsidePriority(t *testing.T) {
	t.Parallel()

	matches, found := extractTrackerMatches(
		"https://retroflix.club/browse/t/10004",
		[]string{"http://peer.retroflix.club/announce.php?passkey=redacted"},
		true,
		trackers.TrackerPriority(),
	)

	if !found {
		t.Fatalf("expected RTF tracker match")
	}
	if len(matches) != 1 || matches[0].ID != "rtf" || matches[0].TrackerID != "10004" {
		t.Fatalf("expected RTF tracker id 10004, got %#v", matches)
	}
}

func TestTorrentMatchesMetaAllowsSymbolDrift(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		SourcePath: `D:\Movies\Fixture Title 2024 2160p AUS UHD Blu-ray DV HDR HEVC DTS-HD MA 5.1-FixtureGroup` + "\u2122",
		DiscType:   "BDMV",
	}
	torrent := qbittorrent.Torrent{
		Name: "Fixture Title 2024 2160p AUS UHD Blu-ray DV HDR HEVC DTS-HD MA 5.1-FixtureGroup",
	}

	if !torrentMatchesMeta(torrent, meta) {
		t.Fatalf("expected trademark-only name drift to match")
	}
}

func TestTorrentMatchesMetaAllowsSeparatorDrift(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		SourcePath: `/tmp/Movie.Title.2024`,
		FileList:   []string{`/tmp/Movie.Title.2024.mkv`},
	}
	torrent := qbittorrent.Torrent{Name: "Movie Title 2024.mkv"}

	if !torrentMatchesMeta(torrent, meta) {
		t.Fatalf("expected separator-only file name drift to match")
	}
}

func TestTorrentMatchesMetaRejectsExtraTokens(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{SourcePath: `/tmp/Movie.Title.2024`}
	torrent := qbittorrent.Torrent{Name: "Movie Title 2024 Remux"}

	if torrentMatchesMeta(torrent, meta) {
		t.Fatalf("expected extra torrent name tokens to be rejected")
	}
}

func TestTorrentMatchesMetaUsesContentPathBasename(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{SourcePath: `/tmp/Movie.Title.2024`}
	torrent := qbittorrent.Torrent{
		Name:        "Tracker renamed folder",
		ContentPath: `/downloads/Movie Title 2024`,
	}

	if !torrentMatchesMeta(torrent, meta) {
		t.Fatalf("expected content path basename to match")
	}
}

func TestResolveSearchClientsUsesClientOverride(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "default-client",
			SearchClients: config.CSVList{"default-client"},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"default-client":  {Type: "qbit"},
			"override-client": {Type: "qbit"},
		},
	}

	override := "override-client"
	clients, usedFallback := resolveSearchClients(cfg, api.ClientOverrides{Client: &override})
	if usedFallback {
		t.Fatalf("expected explicit client override to avoid fallback")
	}
	if len(clients) != 1 || clients[0] != "override-client" {
		t.Fatalf("expected override search client, got %v", clients)
	}
}

func TestBuildTrackerIDPatternsIncludesUnit3DBaseURLs(t *testing.T) {
	for _, tracker := range trackers.Unit3DTrackers() {
		baseURL, ok := unit3dmeta.BaseURL(tracker)
		if !ok {
			t.Fatalf("expected base URL for %s", tracker)
		}
		key := strings.ToLower(tracker)
		pattern, found := trackerIDPatterns[key]
		if !found {
			t.Fatalf("expected unit3d tracker pattern for %s", key)
		}
		if pattern.url != strings.ToLower(baseURL) {
			t.Fatalf("expected %s URL %q, got %q", key, strings.ToLower(baseURL), pattern.url)
		}
		match := pattern.pattern.FindStringSubmatch(strings.ToLower(baseURL) + "/torrents/12345")
		if len(match) != 2 || match[1] != "12345" {
			t.Fatalf("expected ID extraction for %s, got %v", key, match)
		}
	}
}

func TestTrackerPriorityPlacesPreferredTrackersBeforeRemainingUnit3D(t *testing.T) {
	result := trackers.TrackerPriority()
	expectedPrefix := []string{"aither", "ulcx", "lst", "blu", "oe", "btn", "bhd", "hdb", "ant", "rf", "otw", "yus", "dp", "sp", "ptp"}

	prevIdx := -1
	for _, tracker := range expectedPrefix {
		idx := indexOfValue(result, tracker)
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
		if hasValue(expectedPrefix, lower) {
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

func TestApplyPreferredTrackerPriorityMovesToFront(t *testing.T) {
	result := applyPreferredTrackerPriority(trackers.TrackerPriority(), "PTP")
	if len(result) == 0 {
		t.Fatalf("expected non-empty priority list")
	}
	if result[0] != "ptp" {
		t.Fatalf("expected ptp at index 0, got %q", result[0])
	}
}

func TestApplyPreferredTrackerPriorityNoopForUnknown(t *testing.T) {
	priority := trackers.TrackerPriority()
	result := applyPreferredTrackerPriority(priority, "UNKNOWN")
	if len(result) != len(priority) {
		t.Fatalf("expected unchanged list length")
	}
	for idx := range priority {
		if result[idx] != priority[idx] {
			t.Fatalf("expected no ordering changes for unknown preferred tracker")
		}
	}
}

func indexOfValue(values []string, target string) int {
	for idx, value := range values {
		if strings.EqualFold(value, target) {
			return idx
		}
	}
	return -1
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func hasValue(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func createTestTorrent(t *testing.T, dir, name string, pieceExp uint) (string, []byte) {
	t.Helper()

	source := filepath.Join(dir, name)
	if err := os.WriteFile(source, bytes.Repeat([]byte("a"), 5*1024*1024), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	torrentPath := filepath.Join(dir, name+".torrent")
	_, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:           source,
		OutputPath:     torrentPath,
		IsPrivate:      true,
		PieceLengthExp: &pieceExp,
	})
	if err != nil {
		t.Fatalf("create torrent: %v", err)
	}

	data, err := os.ReadFile(torrentPath)
	if err != nil {
		t.Fatalf("read torrent: %v", err)
	}
	metaInfo, err := metainfo.LoadFromFile(torrentPath)
	if err != nil {
		t.Fatalf("load torrent: %v", err)
	}

	return metaInfo.HashInfoBytes().String(), data
}
