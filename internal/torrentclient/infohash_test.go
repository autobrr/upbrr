// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	qbittorrent "github.com/autobrr/go-qbittorrent"
	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"
	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestInfoHashSelectsExactClientTorrentBeforeOrdinaryDiscovery(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "Example.Release.2026.mkv")
	hash, data := createTestTorrent(t, dir, filepath.Base(source), 22)
	if err := os.Remove(source + ".torrent"); err != nil {
		t.Fatal(err)
	}
	var order []string
	var orderMu sync.Mutex
	newClient := func(name string, found bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/auth/login":
				_, _ = w.Write([]byte("Ok."))
			case "/api/v2/torrents/search", "/api/v2/torrents/info":
				if r.URL.Path != "/api/v2/torrents/info" {
					t.Error("exact lookup did not use the hash-capable info endpoint")
				}
				orderMu.Lock()
				order = append(order, name)
				orderMu.Unlock()
				if (r.URL.Query().Get("hashes") != hash && name != "recheck") || r.URL.Query().Get("filter") != "" || r.URL.Query().Get("search") != "" {
					t.Error("lookup did not request the exact hash without name/status filters")
				}
				items := []qbittorrent.Torrent{}
				if found {
					items = append(items, qbittorrent.Torrent{
						Hash:        hash,
						Name:        "Client display label",
						ContentPath: source,
						Progress:    1,
						State:       qbittorrent.TorrentStateStalledUp,
						Size:        123,
						TotalSize:   123,
						Comment:     "https://blutopia.cc/torrents/2222",
					})
				}
				_ = json.NewEncoder(w).Encode(items)
			case "/api/v2/torrents/properties":
				_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{})
			case "/api/v2/torrents/export":
				_, _ = w.Write(data)
			case "/api/v2/torrents/recheck":
				w.WriteHeader(http.StatusServiceUnavailable)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
	}
	first, second, unused := newClient("default", false), newClient("other", true), newClient("unused", true)
	defer first.Close()
	defer second.Close()
	defer unused.Close()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(dir, "db.sqlite")},
		ClientSetup:  config.ClientSetupConfig{DefaultClient: "z-default"},
		TorrentClients: map[string]config.TorrentClientConfig{
			"z-default": {Type: "qbit", URL: first.URL},
			"a-other":   {Type: "qui", QuiProxyURL: second.URL},
			"b-unused":  {Type: "qui", QuiProxyURL: unused.URL},
		},
	}
	result, err := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t)).SearchPathedTorrents(t.Context(), api.ClientSubject{
		InfoHash:   strings.ToUpper(hash),
		SourcePath: source,
		FileList:   []string{source},
	})
	if err != nil || result.InfoHash != hash || result.TorrentPath == "" || result.TrackerIDs["blu"] != "2222" {
		t.Fatalf("exact selection failed: hash=%s path=%t metadata=%s err=%v", result.InfoHash, result.TorrentPath != "", result.TrackerIDs["blu"], err)
	}
	orderMu.Lock()
	if strings.Join(order, ",") != "default,other" {
		t.Fatalf("client order=%v", order)
	}
	order = nil
	orderMu.Unlock()
	for _, test := range []struct {
		name          string
		defaultClient string
		searchClients config.CSVList
		wantOrder     string
	}{
		{
			name:          "disabled search list",
			defaultClient: "z-default",
			searchClients: config.CSVList{"none"},
			wantOrder:     "default,other",
		},
		{
			name:          "restricted search list",
			defaultClient: "z-default",
			searchClients: config.CSVList{"b-unused"},
			wantOrder:     "default,other",
		},
		{
			name:          "disabled default",
			defaultClient: "none",
			searchClients: config.CSVList{"none"},
			wantOrder:     "other",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			configured := cfg
			configured.ClientSetup = config.ClientSetupConfig{DefaultClient: test.defaultClient, SearchClients: test.searchClients}
			selected, err := NewServiceWithRegistry(configured, api.NopLogger{}, trackerPatternRegistry(t)).SearchPathedTorrents(t.Context(), api.ClientSubject{
				InfoHash:   hash,
				SourcePath: source,
				FileList:   []string{source},
			})
			if err != nil || selected.InfoHash != hash || selected.TorrentPath == "" || selected.TrackerIDs["blu"] != "2222" {
				t.Fatalf("search settings blocked exact selection: result=%+v err=%v", selected, err)
			}
			orderMu.Lock()
			defer orderMu.Unlock()
			if strings.Join(order, ",") != test.wantOrder {
				t.Errorf("client order=%v, want %s", order, test.wantOrder)
			}
			order = nil
		})
	}
	for _, selection := range []string{"b-unused", "none"} {
		selected, err := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t)).SearchPathedTorrents(t.Context(), api.ClientSubject{
			InfoHash:        hash,
			SourcePath:      source,
			FileList:        []string{source},
			ClientOverrides: api.ClientOverrides{Client: new(selection)},
		})
		if err != nil || selected.TorrentPath == "" || selected.InfoHash != hash {
			t.Fatalf("explicit client selection %s failed: artifact=%t err=%v", selection, selected.TorrentPath != "", err)
		}
		orderMu.Lock()
		want := "unused"
		if selection == "none" {
			want = "default,other"
		}
		if strings.Join(order, ",") != want {
			t.Errorf("explicit selection searched other clients: %v", order)
		}
		order = nil
		orderMu.Unlock()
	}
	recheckClient := newClient("recheck", true)
	defer recheckClient.Close()
	cfg.TorrentClients["z-default"] = config.TorrentClientConfig{Type: "qbit", URL: recheckClient.URL}
	for _, alternatives := range []bool{false, true} {
		_, err = NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t)).SearchPathedTorrents(t.Context(), api.ClientSubject{
			InfoHash:          hash,
			SourcePath:        source,
			FileList:          []string{source},
			FindAllCandidates: alternatives,
			ClientOverrides:   api.ClientOverrides{ForceRecheck: new(true)},
		})
		orderMu.Lock()
		if !errors.Is(err, internalerrors.ErrClientRecheckFailed) || strings.Join(order, ",") != "recheck" {
			t.Errorf("failed explicit recheck was ignored: alternatives=%t order=%v err=%v", alternatives, order, err)
		}
		order = nil
		orderMu.Unlock()
	}
}

func TestInfoHashMatchesContentIndependentlyOfClientLocation(t *testing.T) {
	for _, test := range []struct {
		name         string
		clientType   string
		inputKind    string
		mismatch     string
		exportStatus int
		emptyExport  bool
		wrapped      bool
	}{
		{name: "direct client different location", clientType: "qbit"},
		{name: "proxy client different location", clientType: "qui"},
		{
			name:       "convertible wrapper",
			clientType: "qui",
			wrapped:    true,
		},
		{
			name:       "wrapper wrong size",
			clientType: "qui",
			wrapped:    true,
			mismatch:   "size",
		},
		{
			name:       "single file selected from directory",
			clientType: "qui",
			inputKind:  "selected file",
		},
		{
			name:       "folder with nested files",
			clientType: "qui",
			inputKind:  "folder",
		},
		{
			name:       "single file wrong size",
			clientType: "qui",
			mismatch:   "size",
		},
		{
			name:       "single file wrong name",
			clientType: "qui",
			mismatch:   "name",
		},
		{
			name:       "folder wrong root",
			clientType: "qui",
			inputKind:  "folder",
			mismatch:   "root",
		},
		{
			name:       "folder wrong file set",
			clientType: "qui",
			inputKind:  "folder",
			mismatch:   "files",
		},
		{
			name:       "folder wrong file size",
			clientType: "qui",
			inputKind:  "folder",
			mismatch:   "size",
		},
		{
			name:         "unavailable export",
			clientType:   "qui",
			exportStatus: http.StatusServiceUnavailable,
		},
		{
			name:        "empty export",
			clientType:  "qui",
			emptyExport: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "Example.Release.2026")
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "Example.Release.2026.mkv")
			if err := os.WriteFile(source, []byte("synthetic media bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			sourcePath := source
			files := []string{source}
			if test.inputKind != "" {
				sourcePath = root
			}
			if test.inputKind == "folder" {
				if err := os.MkdirAll(filepath.Join(root, "Extras"), 0o700); err != nil {
					t.Fatal(err)
				}
				extra := filepath.Join(root, "Extras", "Example.Extra.mkv")
				if err := os.WriteFile(extra, []byte("synthetic extra bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
				files = append(files, extra)
			}
			fixture := filepath.Join(dir, "fixture.torrent")
			fixtureSource := sourcePath
			if test.inputKind == "selected file" {
				fixtureSource = source
			}
			if test.wrapped {
				fixtureSource = root
			}
			writeQbitTestTorrentForSource(t, fixture, fixtureSource)
			metaInfo, err := metainfo.LoadFromFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			info, err := metaInfo.UnmarshalInfo()
			if err != nil {
				t.Fatal(err)
			}
			// A structurally valid piece table deliberately differs from the media
			// bytes, so successful selection also protects against full hashing.
			info.Pieces = make([]byte, metainfo.HashSize)
			metaInfo.InfoBytes, err = bencode.Marshal(info)
			if err != nil {
				t.Fatal(err)
			}
			var encoded bytes.Buffer
			if err := metaInfo.Write(&encoded); err != nil {
				t.Fatal(err)
			}
			data := encoded.Bytes()
			hash := metaInfo.HashInfoBytes().String()
			switch test.mismatch {
			case "size":
				if err := os.Truncate(source, 1); err != nil {
					t.Fatal(err)
				}
			case "name":
				renamed := filepath.Join(root, "Different.Release.2026.mkv")
				if err := os.Rename(source, renamed); err != nil {
					t.Fatal(err)
				}
				sourcePath, files = renamed, []string{renamed}
			case "root":
				renamed := filepath.Join(dir, "Different.Release.2026")
				if err := os.Rename(root, renamed); err != nil {
					t.Fatal(err)
				}
				sourcePath = renamed
				files = []string{filepath.Join(renamed, filepath.Base(source)), filepath.Join(renamed, "Extras", "Example.Extra.mkv")}
			case "files":
				files = files[:1]
			}
			var ordinary atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					_, _ = w.Write([]byte("Ok."))
				case "/api/v2/torrents/search", "/api/v2/torrents/info":
					if r.URL.Query().Get("hashes") == "" {
						ordinary.Add(1)
						_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{})
						return
					}
					_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{{
						Hash:        hash,
						Name:        "Client display label",
						ContentPath: filepath.Join(dir, "client", "Unrelated.Location"),
						Comment:     "https://blutopia.cc/torrents/2222",
					}})
				case "/api/v2/torrents/properties":
					_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{})
				case "/api/v2/torrents/export":
					if test.exportStatus != 0 {
						w.WriteHeader(test.exportStatus)
					} else if !test.emptyExport {
						_, _ = w.Write(data)
					}
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			logger := &captureLogger{}
			cfg := config.Config{
				MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(dir, "db.sqlite")},
				ClientSetup:  config.ClientSetupConfig{DefaultClient: "client"},
				TorrentClients: map[string]config.TorrentClientConfig{
					"client": {
						Type:        test.clientType,
						URL:         server.URL,
						QuiProxyURL: server.URL,
					},
				},
			}
			result, err := NewServiceWithRegistry(cfg, logger, trackerPatternRegistry(t)).SearchPathedTorrents(t.Context(), api.ClientSubject{
				InfoHash:   hash,
				SourcePath: sourcePath,
				FileList:   files,
				KeepFolder: test.inputKind == "folder",
			})
			if err != nil {
				t.Fatal(err)
			}
			if test.mismatch != "" || test.exportStatus != 0 || test.emptyExport {
				if result.TorrentPath != "" || len(result.TorrentCandidates) != 0 || len(result.TrackerIDs) != 0 || ordinary.Load() == 0 || len(logger.errors) != 1 {
					t.Fatalf("unmatched content was selected: result=%+v ordinary=%d errors=%v", result, ordinary.Load(), logger.errors)
				}
				return
			}
			if test.wrapped {
				if result.InfoHash != hash || result.TorrentPath != "" || len(result.TorrentCandidates) != 1 ||
					result.TorrentCandidates[0].InfoHash != hash || result.TorrentCandidates[0].ContentSHA256 == "" ||
					result.TrackerIDs["blu"] != "2222" || ordinary.Load() != 0 || len(logger.errors) != 0 {
					t.Fatalf("convertible requested torrent was lost: result=%+v ordinary=%d errors=%v", result, ordinary.Load(), logger.errors)
				}
				return
			}
			if result.InfoHash != hash || result.TorrentPath == "" || result.TrackerIDs["blu"] != "2222" || ordinary.Load() != 0 || len(logger.errors) != 0 {
				t.Fatalf("matching content was not selected: result=%+v ordinary=%d errors=%v", result, ordinary.Load(), logger.errors)
			}
		})
	}
}

func TestInfoHashMissAndUnsafeExportsFallBackWithDiagnostic(t *testing.T) {
	for _, scenario := range []string{"missing", "wrong hash", "wrong export"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "Example.Release.2026.mkv")
			hash, data := createTestTorrent(t, dir, filepath.Base(source), 22)
			if err := os.Remove(source + ".torrent"); err != nil {
				t.Fatal(err)
			}
			requested := strings.Repeat("a", 40)
			var lookup, ordinary atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/search", "/api/v2/torrents/info":
					items := []qbittorrent.Torrent{}
					if r.URL.Query().Get("hashes") != "" {
						lookup.Add(1)
						candidate := qbittorrent.Torrent{
							Hash:        requested,
							Name:        filepath.Base(source),
							ContentPath: source,
							Comment:     "https://blutopia.cc/torrents/9999",
						}

						if scenario == "wrong hash" {
							candidate.Hash = hash
						}
						if scenario != "missing" {
							items = append(items, candidate)
						}
					} else {
						ordinary.Add(1)
						items = append(items, qbittorrent.Torrent{
							Hash:        hash,
							Name:        filepath.Base(source),
							ContentPath: source,
							Comment:     "https://blutopia.cc/torrents/2222",
						})
					}
					_ = json.NewEncoder(w).Encode(items)
				case "/api/v2/torrents/properties":
					_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{})
				case "/api/v2/torrents/export":
					_, _ = w.Write(data)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			logger := &captureLogger{}
			cfg := config.Config{
				MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(dir, "db.sqlite")},
				ClientSetup:  config.ClientSetupConfig{DefaultClient: "qbit"},
				TorrentClients: map[string]config.TorrentClientConfig{
					"qbit":  {Type: "qui", QuiProxyURL: server.URL},
					"other": {Type: "qui", QuiProxyURL: server.URL},
				},
			}
			result, err := NewServiceWithRegistry(cfg, logger, trackerPatternRegistry(t)).SearchPathedTorrents(t.Context(), api.ClientSubject{
				InfoHash:        requested,
				SourcePath:      source,
				FileList:        []string{source},
				ClientOverrides: api.ClientOverrides{Client: new("qbit")},
			})
			if err != nil || result.InfoHash != hash || result.TorrentPath == "" || result.TrackerIDs["blu"] != "2222" {
				t.Fatalf("ordinary fallback failed: hash=%s artifact=%t metadata=%s err=%v", result.InfoHash, result.TorrentPath != "", result.TrackerIDs["blu"], err)
			}
			if lookup.Load() != 1 || ordinary.Load() == 0 || len(logger.errors) != 1 || !strings.Contains(logger.errors[0], "continuing ordinary discovery") ||
				!strings.Contains(logger.errors[0], `selected client "qbit"`) || strings.Contains(logger.errors[0], server.URL) {
				t.Fatalf("missing bounded fallback diagnostic: lookup=%d ordinary=%d errors=%v", lookup.Load(), ordinary.Load(), logger.errors)
			}
		})
	}
}

func TestInfoHashPolicyRetryFindsOrdinaryAlternatives(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "Example.Release.2026.mkv")
	requested, firstData := createTestTorrent(t, dir, filepath.Base(source), 25)
	alternative, secondData := createTestTorrent(t, dir, filepath.Base(source), 22)
	if err := os.Remove(source + ".torrent"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/search":
			if r.URL.Query().Get("hashes") != "" {
				t.Error("policy retry was restricted to the supplied hash")
			}
			_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{
				{
					Hash:        requested,
					Name:        filepath.Base(source),
					ContentPath: source,
				},
				{
					Hash:        alternative,
					Name:        filepath.Base(source),
					ContentPath: source,
				},
			})
		case "/api/v2/torrents/properties":
			_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{})
		case "/api/v2/torrents/export":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("hash") == requested {
				_, _ = w.Write(firstData)
			} else {
				_, _ = w.Write(secondData)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	var defaultUnavailable atomic.Bool
	defaultClient := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if defaultUnavailable.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path != "/api/v2/torrents/search" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{})
	}))
	defer defaultClient.Close()
	unavailableClient := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unavailableClient.Close()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(dir, "db.sqlite")},
		ClientSetup:  config.ClientSetupConfig{DefaultClient: "default", SearchClients: config.CSVList{"none"}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"default":       {Type: "qui", QuiProxyURL: defaultClient.URL},
			"other":         {Type: "qui", QuiProxyURL: server.URL},
			"z-unavailable": {Type: "qui", QuiProxyURL: unavailableClient.URL},
		},
	}
	subject := api.ClientSubject{
		InfoHash:          requested,
		SourcePath:        source,
		FileList:          []string{source},
		FindAllCandidates: true,
	}
	clients := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t))
	result, err := clients.SearchPathedTorrents(t.Context(), subject)
	if err != nil || len(result.TorrentCandidates) != 2 {
		t.Fatalf("policy retry did not capture alternatives: candidates=%d err=%v", len(result.TorrentCandidates), err)
	}
	found := false
	for _, candidate := range result.TorrentCandidates {
		found = found || candidate.InfoHash == alternative
	}
	if !found {
		t.Fatal("compatible ordinary candidate was lost")
	}
	defaultUnavailable.Store(true)
	selected, err := clients.SearchPathedTorrents(t.Context(), subject)
	if err != nil || len(selected.TorrentCandidates) != 2 {
		t.Fatalf("unavailable default blocked compatible alternatives: candidates=%d err=%v", len(selected.TorrentCandidates), err)
	}
	for _, selection := range []string{"default", "none"} {
		subject.ClientOverrides.Client = new(selection)
		selected, err := clients.SearchPathedTorrents(t.Context(), subject)
		want := 0
		if selection == "none" {
			want = 2
		}
		if err != nil || len(selected.TorrentCandidates) != want {
			t.Fatalf("policy retry ignored client selection %s: candidates=%d err=%v", selection, len(selected.TorrentCandidates), err)
		}
	}
	subject.InfoHash = ""
	subject.ClientOverrides.Client = new("default")
	if _, err := clients.SearchPathedTorrents(t.Context(), subject); err == nil {
		t.Fatal("ordinary discovery ignored the selected client's failure")
	}
}
