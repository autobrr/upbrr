// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

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
	"sync"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"

	"github.com/anacrolix/torrent/metainfo"
	qbittorrent "github.com/autobrr/go-qbittorrent"
	mkbrr "github.com/autobrr/mkbrr/torrent"
)

type captureLogger struct {
	debug []string
}

func trackerPatternRegistry(t *testing.T) *trackers.Registry {
	t.Helper()
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		t.Fatalf("create tracker registry: %v", err)
	}
	return registry
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
	hashLarge, dataLarge := createTestTorrent(t, dir, "Movie.Title.2024.mkv", 25)
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

	svc := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t))
	meta := api.ClientSubject{
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

	svc := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t))
	result, err := svc.SearchPathedTorrents(context.Background(), api.ClientSubject{
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
	if len(result.TrackerIDs) != 0 {
		t.Fatalf("expected unvalidated tracker ids to be ignored, got %#v", result.TrackerIDs)
	}
	if len(result.MatchedTrackers) != 0 {
		t.Fatalf("expected unvalidated tracker matches to be ignored, got %v", result.MatchedTrackers)
	}
}

func TestSearchPathedTorrentsProxySearchesExtensionlessFileName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	hash, data := createTestTorrent(t, dir, "Movie.Title.2024.mkv", 22)
	searchQueries := make([]string, 0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/search":
			search := r.URL.Query().Get("search")
			searchQueries = append(searchQueries, search)
			if search != "Movie.Title.2024" {
				_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{})
				return
			}
			_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{{
				Hash:        hash,
				Name:        "Movie.Title.2024",
				Tracker:     "https://blutopia.cc/announce/redacted",
				Comment:     "https://blutopia.cc/torrents/7788",
				Trackers:    []qbittorrent.TorrentTracker{{Url: "https://blutopia.cc/announce/redacted", Status: qbittorrent.TrackerStatusOK}},
				NumComplete: 4,
			}})
		case "/api/v2/torrents/properties":
			_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{
				Comment:   "https://blutopia.cc/torrents/7788",
				PieceSize: 4 * 1024 * 1024,
			})
		case "/api/v2/torrents/export":
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.FormValue("hash") != hash {
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
		MainSettings: config.MainSettingsConfig{TMDBAPI: "x", DBPath: filepath.Join(dir, "db.sqlite")},
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

	svc := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t))
	result, err := svc.SearchPathedTorrents(context.Background(), api.ClientSubject{
		SourcePath: filepath.Join(dir, "Movie.Title.2024.mkv"),
		FileList:   []string{filepath.Join(dir, "Movie.Title.2024.mkv")},
	})
	if err != nil {
		t.Fatalf("search pathed torrents: %v", err)
	}
	if !containsString(searchQueries, "Movie.Title.2024.mkv") || !containsString(searchQueries, "Movie.Title.2024") {
		t.Fatalf("expected original and extensionless search terms, got %v", searchQueries)
	}
	if result.InfoHash != hash {
		t.Fatalf("expected validated infohash %q, got %q", hash, result.InfoHash)
	}
	if result.TrackerIDs["blu"] != "7788" {
		t.Fatalf("expected validated BLU tracker id, got %q", result.TrackerIDs["blu"])
	}
	if !containsString(result.MatchedTrackers, "BLU") {
		t.Fatalf("expected validated BLU match, got %v", result.MatchedTrackers)
	}
}

func TestSearchPathedTorrentsProxyRejectsIDsFromInvalidExtensionlessMatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	validHash, validData := createTestTorrent(t, dir, "Movie.Title.2024.mkv", 22)
	wrongHash, wrongData := createTestTorrent(t, dir, "Different.Title.2024.mkv", 22)
	dataByHash := map[string][]byte{
		validHash: validData,
		wrongHash: wrongData,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/search":
			if r.URL.Query().Get("search") != "Movie.Title.2024" {
				_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{})
				return
			}
			_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{
				{
					Hash:        wrongHash,
					Name:        "Movie.Title.2024",
					Tracker:     "https://tracker.beyond-hd.me/announce/redacted",
					Comment:     "https://beyond-hd.me/details/1111",
					Trackers:    []qbittorrent.TorrentTracker{{Url: "https://tracker.beyond-hd.me/announce/redacted", Status: qbittorrent.TrackerStatusOK}},
					NumComplete: 9,
				},
				{
					Hash:        validHash,
					Name:        "Movie.Title.2024",
					Tracker:     "https://blutopia.cc/announce/redacted",
					Comment:     "https://blutopia.cc/torrents/2222",
					Trackers:    []qbittorrent.TorrentTracker{{Url: "https://blutopia.cc/announce/redacted", Status: qbittorrent.TrackerStatusOK}},
					NumComplete: 5,
				},
			})
		case "/api/v2/torrents/properties":
			hash := r.URL.Query().Get("hash")
			props := qbittorrent.TorrentProperties{PieceSize: 4 * 1024 * 1024}
			if hash == wrongHash {
				props.Comment = "https://beyond-hd.me/details/1111"
			} else {
				props.Comment = "https://blutopia.cc/torrents/2222"
			}
			_ = json.NewEncoder(w).Encode(props)
		case "/api/v2/torrents/export":
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			data, ok := dataByHash[r.FormValue("hash")]
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
		MainSettings: config.MainSettingsConfig{TMDBAPI: "x", DBPath: filepath.Join(dir, "db.sqlite")},
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

	svc := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t))
	result, err := svc.SearchPathedTorrents(context.Background(), api.ClientSubject{
		SourcePath: filepath.Join(dir, "Movie.Title.2024.mkv"),
		FileList:   []string{filepath.Join(dir, "Movie.Title.2024.mkv")},
	})
	if err != nil {
		t.Fatalf("search pathed torrents: %v", err)
	}
	if result.InfoHash != validHash {
		t.Fatalf("expected validated infohash %q, got %q", validHash, result.InfoHash)
	}
	if result.TrackerIDs["blu"] != "2222" {
		t.Fatalf("expected validated BLU tracker id, got %q", result.TrackerIDs["blu"])
	}
	if got := result.TrackerIDs["bhd"]; got != "" {
		t.Fatalf("expected invalid BHD tracker id to be ignored, got %q", got)
	}
	if containsString(result.MatchedTrackers, "BHD") {
		t.Fatalf("expected invalid BHD match to be ignored, got %v", result.MatchedTrackers)
	}
}

func TestSearchPathedTorrentsProxyUsesFolderWrappedSingleFileAsTrackerSource(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	exactHash, exactData := createTestTorrent(t, dir, "Movie.Title.2024.mkv", 22)
	folderHash, folderData := createTestFolderTorrent(t, dir, "Movie.Title.2024.Release", "Movie.Title.2024.mkv", 22)
	dataByHash := map[string][]byte{
		exactHash:  exactData,
		folderHash: folderData,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/search":
			if r.URL.Query().Get("search") != "Movie.Title.2024" {
				_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{})
				return
			}
			_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{
				{
					Hash:        folderHash,
					Name:        "Movie.Title.2024",
					Tracker:     "https://tracker.beyond-hd.me/announce/redacted",
					Comment:     "https://beyond-hd.me/details/3333",
					Trackers:    []qbittorrent.TorrentTracker{{Url: "https://tracker.beyond-hd.me/announce/redacted", Status: qbittorrent.TrackerStatusOK}},
					NumComplete: 8,
				},
				{
					Hash:        exactHash,
					Name:        "Movie.Title.2024",
					Tracker:     "https://blutopia.cc/announce/redacted",
					Comment:     "https://blutopia.cc/torrents/4444",
					Trackers:    []qbittorrent.TorrentTracker{{Url: "https://blutopia.cc/announce/redacted", Status: qbittorrent.TrackerStatusOK}},
					NumComplete: 4,
				},
			})
		case "/api/v2/torrents/properties":
			hash := r.URL.Query().Get("hash")
			props := qbittorrent.TorrentProperties{PieceSize: 4 * 1024 * 1024}
			if hash == folderHash {
				props.Comment = "https://beyond-hd.me/details/3333"
			} else {
				props.Comment = "https://blutopia.cc/torrents/4444"
			}
			_ = json.NewEncoder(w).Encode(props)
		case "/api/v2/torrents/export":
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			data, ok := dataByHash[r.FormValue("hash")]
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
		MainSettings: config.MainSettingsConfig{TMDBAPI: "x", DBPath: filepath.Join(dir, "db.sqlite")},
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

	svc := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t))
	result, err := svc.SearchPathedTorrents(context.Background(), api.ClientSubject{
		SourcePath: filepath.Join(dir, "Movie.Title.2024.mkv"),
		FileList:   []string{filepath.Join(dir, "Movie.Title.2024.mkv")},
	})
	if err != nil {
		t.Fatalf("search pathed torrents: %v", err)
	}
	if result.InfoHash != exactHash {
		t.Fatalf("expected reusable exact-file infohash %q, got %q", exactHash, result.InfoHash)
	}
	if result.InfoHash == folderHash {
		t.Fatalf("expected folder-wrapped torrent not to be selected as reusable infohash")
	}
	if result.TorrentPath == "" {
		t.Fatalf("expected exact-file torrent path to be selected")
	}
	if result.TrackerIDs["bhd"] != "3333" {
		t.Fatalf("expected folder-wrapped BHD tracker id, got %q", result.TrackerIDs["bhd"])
	}
	if result.TrackerIDs["blu"] != "4444" {
		t.Fatalf("expected exact BLU tracker id, got %q", result.TrackerIDs["blu"])
	}
	if !containsString(result.MatchedTrackers, "BHD") || !containsString(result.MatchedTrackers, "BLU") {
		t.Fatalf("expected both folder-wrapped and exact matches reported in client, got %v", result.MatchedTrackers)
	}
	if len(result.TorrentComments) != 2 {
		t.Fatalf("expected both client matches in torrent comments, got %d", len(result.TorrentComments))
	}
}

func TestSearchPathedTorrentsProxyKeepsPieceConstrainedTrackerData(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	hash, data := createTestTorrent(t, dir, "Movie.Title.2024.mkv", 16)
	data = padTorrentData(t, data, 251*1024, hash)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/search":
			search := r.URL.Query().Get("search")
			if search != "Movie.Title.2024.mkv" && search != "Movie.Title.2024" {
				_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{})
				return
			}
			_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{{
				Hash:        hash,
				Name:        "Movie.Title.2024",
				Tracker:     "https://blutopia.cc/announce/redacted",
				Comment:     "https://blutopia.cc/torrents/5555",
				Trackers:    []qbittorrent.TorrentTracker{{Url: "https://blutopia.cc/announce/redacted", Status: qbittorrent.TrackerStatusOK}},
				NumComplete: 6,
			}})
		case "/api/v2/torrents/properties":
			_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{
				Comment:   "https://blutopia.cc/torrents/5555",
				PieceSize: 64 * 1024,
			})
		case "/api/v2/torrents/export":
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.FormValue("hash") != hash {
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
		MainSettings: config.MainSettingsConfig{TMDBAPI: "x", DBPath: filepath.Join(dir, "db.sqlite")},
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

	svc := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t))
	result, err := svc.SearchPathedTorrents(context.Background(), api.ClientSubject{
		SourcePath: filepath.Join(dir, "Movie.Title.2024.mkv"),
		FileList:   []string{filepath.Join(dir, "Movie.Title.2024.mkv")},
	})
	if err != nil {
		t.Fatalf("search pathed torrents: %v", err)
	}
	if result.InfoHash != "" {
		t.Fatalf("expected piece-constrained torrent not to be selected as reusable infohash, got %q", result.InfoHash)
	}
	if result.TorrentPath != "" {
		t.Fatalf("expected piece-constrained torrent not to be saved as reusable torrent path, got %q", result.TorrentPath)
	}
	if result.TrackerIDs["blu"] != "5555" {
		t.Fatalf("expected piece-constrained BLU tracker id, got %q", result.TrackerIDs["blu"])
	}
	if !result.FoundTrackerMatch {
		t.Fatal("expected piece-constrained client match to report tracker match")
	}
	if !containsString(result.MatchedTrackers, "BLU") {
		t.Fatalf("expected piece-constrained BLU match, got %v", result.MatchedTrackers)
	}
	if len(result.TorrentComments) != 1 {
		t.Fatalf("expected piece-constrained client match in torrent comments, got %d", len(result.TorrentComments))
	}
}

func TestSearchPathedTorrentsQbitForceRecheckAfterMetadataValidation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	validHash, validData := createTestTorrent(t, dir, "Movie.Title.2024.mkv", 22)
	wrongHash, wrongData := createTestTorrent(t, dir, "Different.Title.2024.mkv", 22)
	dataByHash := map[string][]byte{
		validHash: validData,
		wrongHash: wrongData,
	}

	var mu sync.Mutex
	calls := make([]string, 0)
	record := func(call string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			record("login")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			hashes := r.URL.Query().Get("hashes")
			if hashes != "" {
				record("info:" + hashes)
				_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{{Hash: hashes}})
				return
			}
			record("list")
			_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{
				{
					Hash:        wrongHash,
					Name:        "Movie.Title.2024",
					Tracker:     "https://tracker.beyond-hd.me/announce/redacted",
					Comment:     "https://beyond-hd.me/details/6666",
					NumComplete: 9,
				},
				{
					Hash:        validHash,
					Name:        "Movie.Title.2024",
					Tracker:     "https://blutopia.cc/announce/redacted",
					Comment:     "https://blutopia.cc/torrents/7777",
					NumComplete: 5,
				},
			})
		case "/api/v2/torrents/properties":
			hash := r.URL.Query().Get("hash")
			record("properties:" + hash)
			props := qbittorrent.TorrentProperties{PieceSize: 4 * 1024 * 1024}
			if hash == wrongHash {
				props.Comment = "https://beyond-hd.me/details/6666"
			} else {
				props.Comment = "https://blutopia.cc/torrents/7777"
			}
			_ = json.NewEncoder(w).Encode(props)
		case "/api/v2/torrents/trackers":
			hash := r.URL.Query().Get("hash")
			record("trackers:" + hash)
			trackerURL := "https://blutopia.cc/announce/redacted"
			if hash == wrongHash {
				trackerURL = "https://tracker.beyond-hd.me/announce/redacted"
			}
			_ = json.NewEncoder(w).Encode([]qbittorrent.TorrentTracker{{Url: trackerURL, Status: qbittorrent.TrackerStatusOK}})
		case "/api/v2/torrents/export":
			hash := r.URL.Query().Get("hash")
			record("export:" + hash)
			data, ok := dataByHash[hash]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(data)
		case "/api/v2/torrents/recheck":
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			record("recheck:" + r.FormValue("hashes"))
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{TMDBAPI: "x", DBPath: filepath.Join(dir, "db.sqlite")},
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "qbit",
			SearchClients: config.CSVList{"qbit"},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}

	forceRecheck := true
	svc := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t))
	result, err := svc.SearchPathedTorrents(context.Background(), api.ClientSubject{
		SourcePath: filepath.Join(dir, "Movie.Title.2024.mkv"),
		FileList:   []string{filepath.Join(dir, "Movie.Title.2024.mkv")},
		ClientOverrides: api.ClientOverrides{
			ForceRecheck: &forceRecheck,
		},
	})
	if err != nil {
		t.Fatalf("search pathed torrents: %v", err)
	}
	if result.InfoHash != validHash {
		t.Fatalf("expected validated infohash %q, got %q", validHash, result.InfoHash)
	}

	mu.Lock()
	gotCalls := append([]string(nil), calls...)
	mu.Unlock()

	if containsString(gotCalls, "recheck:"+wrongHash) {
		t.Fatalf("did not expect invalid metadata candidate to be rechecked; calls=%v", gotCalls)
	}
	validExportIdx := indexOfValue(gotCalls, "export:"+validHash)
	validRecheckIdx := indexOfValue(gotCalls, "recheck:"+validHash)
	if validExportIdx == -1 || validRecheckIdx == -1 {
		t.Fatalf("expected valid candidate export and recheck calls, got %v", gotCalls)
	}
	if validRecheckIdx < validExportIdx {
		t.Fatalf("expected valid candidate recheck after metadata export, got %v", gotCalls)
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

func TestCommonPathDoesNotFoldCaseDistinctSegments(t *testing.T) {
	t.Parallel()

	got := commonPath([]string{
		"Release/BDMV/STREAM/00001.m2ts",
		"Release/bdmv/STREAM/00002.m2ts",
	})
	if got != "Release" {
		t.Fatalf("expected exact shared root only, got %q", got)
	}
}

func TestMatchTrackerURLsMatchesBTNLandOfTVAnnounce(t *testing.T) {
	t.Parallel()

	matched := matchTrackerURLs(
		[]string{"https://landof.tv/redacted/announce"},
		buildTrackerIDPatterns(trackerPatternRegistry(t)),
	)
	if !containsString(matched, "BTN") {
		t.Fatalf("expected BTN in matched trackers, got %v", matched)
	}
}

func TestMatchTrackerURLsMatchesCZTAnnounce(t *testing.T) {
	t.Parallel()

	matched := matchTrackerURLs(
		[]string{"https://czteam.me/announce.php?passkey=redacted"},
		buildTrackerIDPatterns(trackerPatternRegistry(t)),
	)
	if !containsString(matched, "CZT") {
		t.Fatalf("expected CZT in matched trackers, got %v", matched)
	}
}

func TestEnsureMatchedTrackersForKnownIDsAddsBTN(t *testing.T) {
	t.Parallel()

	matched := ensureMatchedTrackersForKnownIDs(nil, map[string]string{"btn": "2202392"}, map[string]trackerPattern{
		"btn": {inferMatchFromID: true},
	})
	if !containsString(matched, "BTN") {
		t.Fatalf("expected BTN in matched trackers, got %v", matched)
	}
}

func TestExtractTrackerMatchesHandlesReelFlixAliasComment(t *testing.T) {
	t.Parallel()

	matches, found := extractTrackerMatchesWithPatterns(
		"https://reelflix.xyz/torrents/10003",
		[]string{"https://reelflix.cc/announce/redacted"},
		true,
		[]string{"rf"},
		buildTrackerIDPatterns(trackerPatternRegistry(t)),
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

	registry := trackerPatternRegistry(t)
	matches, found := extractTrackerMatchesWithPatterns(
		"https://retroflix.club/browse/t/10004",
		[]string{"http://peer.retroflix.club/announce.php?passkey=redacted"},
		true,
		[]string{"rtf"},
		buildTrackerIDPatterns(registry),
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

	registry := trackerPatternRegistry(t)
	matches, found := extractTrackerMatchesWithPatterns(
		"https://retroflix.club/browse/t/10004",
		[]string{"http://peer.retroflix.club/announce.php?passkey=redacted"},
		true,
		registry.Priority(),
		buildTrackerIDPatterns(registry),
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

	meta := api.ClientSubject{
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

	meta := api.ClientSubject{
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

	meta := api.ClientSubject{SourcePath: `/tmp/Movie.Title.2024`}
	torrent := qbittorrent.Torrent{Name: "Movie Title 2024 Remux"}

	if torrentMatchesMeta(torrent, meta) {
		t.Fatalf("expected extra torrent name tokens to be rejected")
	}
}

func TestTorrentMatchesMetaUsesContentPathBasename(t *testing.T) {
	t.Parallel()

	meta := api.ClientSubject{SourcePath: `/tmp/Movie.Title.2024`}
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

func TestResolveSearchClientsSkipsFallbackWhenDefaultClientUnknown(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "missing",
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {Type: "qbit"},
		},
	}

	clients, usedFallback := resolveSearchClients(cfg, api.ClientOverrides{})
	if usedFallback {
		t.Fatalf("expected unknown default selector to suppress fallback")
	}
	if len(clients) != 0 {
		t.Fatalf("expected no search clients, got %v", clients)
	}
}

func TestResolveSearchClientsSkipsFallbackWhenSearchClientUnknown(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "qbit",
			SearchClients: config.CSVList{"missing"},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {Type: "qbit"},
		},
	}

	clients, usedFallback := resolveSearchClients(cfg, api.ClientOverrides{})
	if usedFallback {
		t.Fatalf("expected unknown search selector to suppress fallback")
	}
	if len(clients) != 0 {
		t.Fatalf("expected no search clients, got %v", clients)
	}
}

func TestResolveSearchClientsNoneSearchListDisablesFallback(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "qbit",
			SearchClients: config.CSVList{" none "},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {Type: "qbit"},
		},
	}

	clients, usedFallback := resolveSearchClients(cfg, api.ClientOverrides{})
	if usedFallback {
		t.Fatalf("expected none search selector to suppress fallback")
	}
	if len(clients) != 0 {
		t.Fatalf("expected no search clients, got %v", clients)
	}
}

func TestResolveSearchClientsBlankSearchListUsesDefaultClient(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "qbit",
			SearchClients: config.CSVList{" ", ""},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit":  {Type: "qbit"},
			"other": {Type: "qbit"},
		},
	}

	clients, usedFallback := resolveSearchClients(cfg, api.ClientOverrides{})
	if usedFallback {
		t.Fatalf("expected blank search list to use default without fallback")
	}
	if len(clients) != 1 || clients[0] != "qbit" {
		t.Fatalf("expected default qbit selector, got %v", clients)
	}
}

func TestResolveSearchClientsBlankSearchListWithoutDefaultUsesFallback(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		ClientSetup: config.ClientSetupConfig{
			SearchClients: config.CSVList{" "},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit":  {Type: "qbit"},
			"watch": {Type: "watch"},
		},
	}

	clients, usedFallback := resolveSearchClients(cfg, api.ClientOverrides{})
	if !usedFallback {
		t.Fatalf("expected blank search list without default to use implicit fallback")
	}
	if len(clients) != 1 || clients[0] != "qbit" {
		t.Fatalf("expected qbit fallback only, got %v", clients)
	}
}

func TestResolveSearchClientsNormalizesSelectorCase(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		ClientSetup: config.ClientSetupConfig{
			SearchClients: config.CSVList{" QBIT ", "qbit"},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {Type: "qbit"},
		},
	}

	clients, usedFallback := resolveSearchClients(cfg, api.ClientOverrides{})
	if usedFallback {
		t.Fatalf("expected configured selector to avoid fallback")
	}
	if len(clients) != 1 || clients[0] != "qbit" {
		t.Fatalf("expected normalized qbit selector, got %v", clients)
	}
}

func TestBuildTrackerIDPatternsIncludesUnit3DBaseURLs(t *testing.T) {
	registry := trackerPatternRegistry(t)
	patterns := buildTrackerIDPatterns(registry)
	for _, tracker := range registry.NamesByFamily(trackers.FamilyUnit3D) {
		baseURL, ok := registry.LookupBaseURL(tracker)
		if !ok {
			t.Fatalf("expected base URL for %s", tracker)
		}
		key := strings.ToLower(tracker)
		pattern, found := patterns[key]
		if !found {
			t.Fatalf("expected unit3d tracker pattern for %s", key)
		}
		foundBaseURL := false
		for _, trackerURL := range pattern.trackerURLs {
			if strings.EqualFold(trackerURL, baseURL) {
				foundBaseURL = true
				break
			}
		}
		if !foundBaseURL {
			t.Fatalf("expected %s URL %q, got %q", key, baseURL, pattern.trackerURLs)
		}
		match := pattern.pattern.FindStringSubmatch(strings.ToLower(baseURL) + "/torrents/12345")
		if len(match) != 2 || match[1] != "12345" {
			t.Fatalf("expected ID extraction for %s, got %v", key, match)
		}
	}
}

func TestTrackerPriorityPlacesPreferredTrackersBeforeRemainingUnit3D(t *testing.T) {
	registry := trackerPatternRegistry(t)
	result := registry.Priority()
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
	for _, tracker := range registry.NamesByFamily(trackers.FamilyUnit3D) {
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
	result := applyPreferredTrackerPriority(trackerPatternRegistry(t).Priority(), "PTP")
	if len(result) == 0 {
		t.Fatalf("expected non-empty priority list")
	}
	if result[0] != "ptp" {
		t.Fatalf("expected ptp at index 0, got %q", result[0])
	}
}

func TestApplyPreferredTrackerPriorityNoopForUnknown(t *testing.T) {
	priority := trackerPatternRegistry(t).Priority()
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

// padTorrentData appends a top-level bencoded padding field without changing
// the infohash.
func padTorrentData(t *testing.T, data []byte, minSize int, expectedHash string) []byte {
	t.Helper()

	if len(data) >= minSize {
		return data
	}
	if len(data) == 0 || data[len(data)-1] != 'e' {
		t.Fatalf("expected top-level torrent dictionary ending")
	}

	padding := bytes.Repeat([]byte("p"), minSize-len(data)+32)
	entry := fmt.Appendf(nil, "7:padding%d:", len(padding))
	padded := make([]byte, 0, len(data)+len(entry)+len(padding))
	padded = append(padded, data[:len(data)-1]...)
	padded = append(padded, entry...)
	padded = append(padded, padding...)
	padded = append(padded, 'e')

	metaInfo, err := metainfo.Load(bytes.NewReader(padded))
	if err != nil {
		t.Fatalf("load padded torrent: %v", err)
	}
	if got := metaInfo.HashInfoBytes().String(); got != expectedHash {
		t.Fatalf("padded torrent changed infohash: got %q want %q", got, expectedHash)
	}
	if len(padded) < minSize {
		t.Fatalf("padded torrent size = %d, want at least %d", len(padded), minSize)
	}
	return padded
}

// createTestFolderTorrent creates a folder-wrapped single-file torrent and
// returns its infohash with the raw torrent data.
func createTestFolderTorrent(t *testing.T, dir, folderName, fileName string, pieceExp uint) (string, []byte) {
	t.Helper()

	sourceDir := filepath.Join(dir, folderName)
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("create source dir: %v", err)
	}
	source := filepath.Join(sourceDir, fileName)
	if err := os.WriteFile(source, bytes.Repeat([]byte("a"), 5*1024*1024), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	torrentPath := filepath.Join(dir, folderName+".torrent")
	_, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:           sourceDir,
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
