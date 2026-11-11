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
	"sync/atomic"
	"testing"

	qbittorrent "github.com/autobrr/go-qbittorrent"
	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"
	"github.com/autobrr/upbrr/internal/config"
	torrentservice "github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestMetadataSurvivesUnavailableExport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "Movie.Title.2024.mkv")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/search":
			_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{{
				Hash:        strings.Repeat("a", 40),
				Name:        "Movie.Title.2024",
				ContentPath: source,
				Comment:     "https://blutopia.cc/torrents/2222",
				Tracker:     "https://blutopia.cc/announce/redacted",
			}})
		case "/api/v2/torrents/properties":
			_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	cfg := config.Config{
		MainSettings:   config.MainSettingsConfig{DBPath: filepath.Join(dir, "db.sqlite")},
		ClientSetup:    config.ClientSetupConfig{DefaultClient: "qbit"},
		TorrentClients: map[string]config.TorrentClientConfig{"qbit": {Type: "qui", QuiProxyURL: server.URL}},
	}
	svc := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t))
	result, err := svc.SearchPathedTorrents(context.Background(), api.ClientSubject{SourcePath: source, FileList: []string{source}})
	if err != nil || result.TrackerIDs["blu"] != "2222" || result.TorrentPath != "" {
		t.Fatalf("metadata should survive unavailable export: result=%+v err=%v", result, err)
	}
}

func TestDiscoveryRetainsSingleFileForEffectiveFolderPolicy(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached=%t", cached), func(t *testing.T) {
			dir := t.TempDir()
			release := filepath.Join(dir, "Example.Show.S01")
			if err := os.MkdirAll(release, 0o700); err != nil {
				t.Fatal(err)
			}
			name := "Example.Show.S01E01.mkv"
			source := filepath.Join(release, name)
			hash, data := createTestTorrent(t, release, name, 22)
			if err := os.Remove(source + ".torrent"); err != nil {
				t.Fatal(err)
			}
			var exports atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/search":
					_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{{
						Hash:        hash,
						Name:        name,
						ContentPath: source,
					}})
				case "/api/v2/torrents/properties":
					_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{})
				case "/api/v2/torrents/export":
					exports.Add(1)
					_, _ = w.Write(data)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			stateDir := filepath.Join(dir, "state")
			cfg := config.Config{
				MainSettings:   config.MainSettingsConfig{DBPath: filepath.Join(stateDir, "db.sqlite")},
				ClientSetup:    config.ClientSetupConfig{DefaultClient: "qbit"},
				TorrentClients: map[string]config.TorrentClientConfig{"qbit": {Type: "qui", QuiProxyURL: server.URL}},
			}
			if cached {
				path, err := torrentservice.TempTorrentPath(filepath.Join(stateDir, "tmp"), release)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			found, err := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t)).SearchPathedTorrents(
				t.Context(), api.ClientSubject{
					SourcePath: release,
					FileList:   []string{source},
					KeepFolder: true,
				},
			)
			if err != nil || found.TorrentPath != "" || len(found.TorrentCandidates) != 1 {
				t.Fatalf("single-file alternate was not retained: result=%+v err=%v", found, err)
			}
			wantExports := int64(1)
			if cached {
				wantExports = 0
			}
			if exports.Load() != wantExports {
				t.Fatalf("exports=%d, want %d", exports.Load(), wantExports)
			}
			var pieceWork atomic.Int64
			ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
				if update.TotalPieces > 0 {
					pieceWork.Add(1)
				}
			})
			service := torrentservice.NewService(nil, t.TempDir())
			subject := api.TorrentSubject{
				SourcePath:              release,
				FileList:                []string{source},
				ClientTorrentCandidates: found.TorrentCandidates,
				TorrentOverrides:        api.TorrentOverrides{NoHash: new(true)},
			}
			reused, err := service.Create(ctx, subject)
			if err != nil || reused.InfoHash != hash || pieceWork.Load() != 0 {
				t.Fatalf("effective single-file layout should reuse without hashing: result=%+v work=%d err=%v", reused, pieceWork.Load(), err)
			}
			subject.KeepFolder = true
			if _, err := service.Create(t.Context(), subject); err == nil {
				t.Fatal("single-file alternate must not satisfy an effective folder layout")
			}
			subject.KeepFolder = false
			if err := os.Truncate(source, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Create(t.Context(), subject); err == nil {
				t.Fatal("alternate must not be reused after its source length changes")
			}
		})
	}
}

func TestMetadataPreferenceDoesNotSelectArtifact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "Movie.Title.2024.mkv")
	firstHash, firstData := createTestTorrent(t, dir, filepath.Base(source), 22)
	if err := os.Remove(source + ".torrent"); err != nil {
		t.Fatal(err)
	}
	mi, err := metainfo.Load(bytes.NewReader(firstData))
	if err != nil {
		t.Fatal(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	info.Source = "alternative"
	mi.InfoBytes, err = bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	secondHash := mi.HashInfoBytes().String()
	var secondData bytes.Buffer
	if err := mi.Write(&secondData); err != nil {
		t.Fatal(err)
	}
	var exports atomic.Int64
	var alternativeOnly atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/search":
			items := []qbittorrent.Torrent{
				{
					Hash:        firstHash,
					Name:        "Movie.Title.2024",
					ContentPath: source,
					Comment:     "https://blutopia.cc/torrents/2222",
					Tracker:     "https://blutopia.cc/announce/redacted",
				},
				{
					Hash:        secondHash,
					Name:        "Movie.Title.2024",
					ContentPath: source,
					Comment:     "https://beyond-hd.me/details/1111",
					Tracker:     "https://tracker.beyond-hd.me/announce/redacted",
				},
			}
			if alternativeOnly.Load() {
				items = items[1:]
			}
			_ = json.NewEncoder(w).Encode(items)
		case "/api/v2/torrents/properties":
			_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{})
		case "/api/v2/torrents/export":
			exports.Add(1)
			if r.FormValue("hash") == firstHash {
				_, _ = w.Write(firstData)
			} else {
				_, _ = w.Write(secondData.Bytes())
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, preferred := range []string{"BLU", "BHD"} {
		before := exports.Load()
		cfg := config.Config{
			MainSettings:   config.MainSettingsConfig{DBPath: filepath.Join(dir, preferred, "db.sqlite")},
			ClientSetup:    config.ClientSetupConfig{DefaultClient: "qbit"},
			Trackers:       config.TrackersConfig{PreferredTracker: preferred},
			TorrentClients: map[string]config.TorrentClientConfig{"qbit": {Type: "qui", QuiProxyURL: server.URL}},
		}
		result, err := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t)).SearchPathedTorrents(
			context.Background(), api.ClientSubject{SourcePath: source, FileList: []string{source}},
		)
		if err != nil || result.InfoHash != firstHash || len(result.TorrentComments) != 2 || !strings.EqualFold(result.TorrentComments[0].TrackerURLs[0].ID, preferred) || exports.Load()-before != 1 {
			t.Fatalf("metadata preference %s affected artifact selection: result=%+v err=%v", preferred, result, err)
		}
	}
	if err := os.WriteFile(source+".torrent", firstData, 0o600); err != nil {
		t.Fatal(err)
	}
	alternativeOnly.Store(true)
	before := exports.Load()
	cfg := config.Config{
		MainSettings:   config.MainSettingsConfig{DBPath: filepath.Join(dir, "local", "db.sqlite")},
		ClientSetup:    config.ClientSetupConfig{DefaultClient: "qbit"},
		TorrentClients: map[string]config.TorrentClientConfig{"qbit": {Type: "qui", QuiProxyURL: server.URL}},
	}
	result, err := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t)).SearchPathedTorrents(
		context.Background(), api.ClientSubject{SourcePath: source, FileList: []string{source}},
	)
	if err != nil || result.TrackerIDs["bhd"] != "1111" || result.TorrentPath != "" || len(result.TorrentComments) != 1 || exports.Load() != before {
		t.Fatalf("local artifact should avoid exports: result=%+v exports=%d err=%v", result, exports.Load()-before, err)
	}
	selected, err := torrentservice.NewService(nil, t.TempDir()).Create(context.Background(), api.TorrentSubject{
		SourcePath: source, TorrentOverrides: api.TorrentOverrides{NoHash: new(true)},
	})
	if err != nil || selected.InfoHash != firstHash {
		t.Fatalf("expected the available artifact despite a different client infohash: result=%+v err=%v", selected, err)
	}
}

func TestMetadataAccumulatesAcrossClientsWithoutRebindingArtifact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "Movie.Title.2024.mkv")
	hash, data := createTestTorrent(t, dir, filepath.Base(source), 25)
	server := func(torrentHash, comment, tracker string, exported []byte) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/torrents/search":
				_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{{
					Hash:        torrentHash,
					Name:        "Movie.Title.2024",
					ContentPath: source,
					Comment:     comment,
					Tracker:     tracker,
				}})
			case "/api/v2/torrents/properties":
				_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{})
			case "/api/v2/torrents/export":
				if exported != nil {
					_, _ = w.Write(exported)
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
	}
	first := server(hash, "https://blutopia.cc/torrents/2222", "https://blutopia.cc/announce/redacted", data)
	defer first.Close()
	second := server(hash, "https://beyond-hd.me/details/1111", "https://tracker.beyond-hd.me/announce/redacted", nil)
	defer second.Close()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(dir, "db.sqlite")},
		ClientSetup:  config.ClientSetupConfig{SearchClients: config.CSVList{"first", "second"}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"first": {Type: "qui", QuiProxyURL: first.URL}, "second": {Type: "qui", QuiProxyURL: second.URL},
		},
	}
	result, err := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t)).SearchPathedTorrents(
		context.Background(), api.ClientSubject{SourcePath: source, FileList: []string{source}},
	)
	if err != nil || result.TrackerIDs["blu"] != "2222" || result.TrackerIDs["bhd"] != "1111" || result.InfoHash != hash || result.TorrentPath == "" {
		t.Fatalf("metadata or artifact identity lost across clients: result=%+v err=%v", result, err)
	}
	cfg.MainSettings.DBPath = filepath.Join(dir, "preferred", "db.sqlite")
	cfg.Trackers.PreferredTracker = "BHD"
	result, err = NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t)).SearchPathedTorrents(
		context.Background(), api.ClientSubject{SourcePath: source, FileList: []string{source}},
	)
	if err != nil || result.TrackerIDs["bhd"] != "1111" || result.InfoHash != hash {
		t.Fatalf("preferred metadata in the next client was not discovered: result=%+v err=%v", result, err)
	}
	largerHash, largerData := createTestTorrent(t, dir, filepath.Base(source), 26)
	larger := server(largerHash, "https://beyond-hd.me/details/1111", "https://tracker.beyond-hd.me/announce/redacted", largerData)
	defer larger.Close()
	cfg.MainSettings.DBPath = filepath.Join(dir, "larger", "db.sqlite")
	cfg.Trackers.PreferredTracker = ""
	cfg.TorrentClients["second"] = config.TorrentClientConfig{Type: "qui", QuiProxyURL: larger.URL}
	result, err = NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t)).SearchPathedTorrents(
		context.Background(), api.ClientSubject{SourcePath: source, FileList: []string{source}},
	)
	if err != nil || result.InfoHash != hash {
		t.Fatalf("expected best available existing piece size: result=%+v err=%v", result, err)
	}
}

func TestDiscoveryRetainsSidecarTorrentForSingleFileExtraction(t *testing.T) {
	t.Parallel()
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{true: "requested infohash", false: "ordinary discovery"}[explicit], func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "Example.Video.2026")
			source := filepath.Join(root, "Example.Video.2026.mkv")
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte("synthetic media bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "readme.nfo"), []byte("sidecar"), 0o600); err != nil {
				t.Fatal(err)
			}
			fixture := filepath.Join(t.TempDir(), "client.torrent")
			if _, err := mkbrr.Create(mkbrr.CreateOptions{
				Path:       root,
				OutputPath: fixture,
				Quiet:      true,
			}); err != nil {
				t.Fatal(err)
			}
			mi, err := metainfo.LoadFromFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			hash := mi.HashInfoBytes().String()
			var exactLookups, ordinarySearches atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info", "/api/v2/torrents/search":
					if r.URL.Path == "/api/v2/torrents/info" {
						exactLookups.Add(1)
						if !explicit || r.URL.Query().Get("hashes") != hash {
							t.Error("unexpected exact lookup or missing requested infohash")
							w.WriteHeader(http.StatusBadRequest)
							return
						}
					} else {
						ordinarySearches.Add(1)
						if explicit {
							t.Error("requested-infohash discovery fell back to ordinary search")
							_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{})
							return
						}
					}
					_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{{
						Hash:        hash,
						Name:        filepath.Base(root),
						ContentPath: root,
					}})
				case "/api/v2/torrents/properties":
					_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{})
				case "/api/v2/torrents/export":
					_, _ = w.Write(data)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			cfg := config.Config{
				MainSettings:   config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "db.sqlite")},
				ClientSetup:    config.ClientSetupConfig{DefaultClient: "client"},
				TorrentClients: map[string]config.TorrentClientConfig{"client": {Type: "qui", QuiProxyURL: server.URL}},
			}
			subject := api.ClientSubject{SourcePath: source, FileList: []string{source}}
			if explicit {
				subject.InfoHash = hash
			}
			var work atomic.Int64
			ctx := api.WithUploadProgressReporter(t.Context(), func(u api.UploadProgressUpdate) {
				if u.TotalPieces > 0 && u.CompletedPieces == u.TotalPieces {
					work.Add(1)
				}
			})
			found, err := NewService(cfg, api.NopLogger{}).SearchPathedTorrents(ctx, subject)
			if err != nil || found.TorrentPath != "" || len(found.TorrentCandidates) != 1 || found.TorrentCandidates[0].InfoHash != hash || found.TorrentCandidates[0].ContentSHA256 == "" || work.Load() != 0 {
				t.Fatalf("discovery lost extraction authority or hashed media: candidates=%d path=%q work=%d err=%v", len(found.TorrentCandidates), found.TorrentPath, work.Load(), err)
			}
			if explicit && (exactLookups.Load() != 1 || ordinarySearches.Load() != 0 || found.InfoHash != hash) {
				t.Fatalf("requested infohash was not selected directly: lookups=%d searches=%d matched=%t", exactLookups.Load(), ordinarySearches.Load(), found.InfoHash == hash)
			}
			extracted, err := torrentservice.NewService(nil, t.TempDir()).Create(ctx, api.TorrentSubject{SourcePath: source, ClientTorrentCandidates: found.TorrentCandidates})
			if err != nil || extracted.Path == "" || work.Load() != 1 {
				t.Fatalf("captured sidecar torrent was not extracted: work=%d err=%v", work.Load(), err)
			}
		})
	}
}
