// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/unit3dmeta"
	"github.com/autobrr/upbrr/pkg/api"

	"github.com/anacrolix/torrent/metainfo"
	qbittorrent "github.com/autobrr/go-qbittorrent"
	mkbrr "github.com/autobrr/mkbrr/torrent"
)

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
	for _, tracker := range unit3dmeta.Trackers() {
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

func TestTrackerPriorityInsertsMissingUnit3DBeforeBTN(t *testing.T) {
	result := trackerPriority
	oeIdx := indexOfValue(result, "oe")
	btnIdx := indexOfValue(result, "btn")
	if oeIdx < 0 || btnIdx < 0 || oeIdx >= btnIdx {
		t.Fatalf("invalid OE/BTN ordering: oe=%d btn=%d list=%v", oeIdx, btnIdx, result)
	}

	inserted := []string{"a4k", "cbr", "emuw", "fnp", "friki", "hhd", "ihd", "itt", "lcd", "ldu", "lt", "pt", "ptt", "r4e", "ras", "sam", "shri", "stc", "tik", "tlz", "tos", "ttr", "utp"}
	for _, tracker := range inserted {
		idx := indexOfValue(result, tracker)
		if idx < 0 {
			t.Fatalf("expected inserted tracker %s in %v", tracker, result)
		}
		if idx <= oeIdx || idx >= btnIdx {
			t.Fatalf("expected %s between OE and BTN, got idx=%d oe=%d btn=%d", tracker, idx, oeIdx, btnIdx)
		}
	}
}

func TestApplyPreferredTrackerPriorityMovesToFront(t *testing.T) {
	result := applyPreferredTrackerPriority(trackerPriority, "PTP")
	if len(result) == 0 {
		t.Fatalf("expected non-empty priority list")
	}
	if result[0] != "ptp" {
		t.Fatalf("expected ptp at index 0, got %q", result[0])
	}
}

func TestApplyPreferredTrackerPriorityNoopForUnknown(t *testing.T) {
	result := applyPreferredTrackerPriority(trackerPriority, "UNKNOWN")
	if len(result) != len(trackerPriority) {
		t.Fatalf("expected unchanged list length")
	}
	for idx := range trackerPriority {
		if result[idx] != trackerPriority[idx] {
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
