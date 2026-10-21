// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	qbittorrent "github.com/autobrr/go-qbittorrent"
	"github.com/autobrr/go-torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"
	"github.com/autobrr/upbrr/internal/clientdiscovery"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	torrentservice "github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/pkg/api"
)

// TestDiscoveryRetainsCandidateSnapshot checks that unrelated discovery cannot
// replace captured metainfo or make qualified reuse read source bytes again.
func TestDiscoveryRetainsCandidateSnapshot(t *testing.T) {
	t.Parallel()

	const name = "Example.Collision.2026.mkv"
	const sourceSize = 5 * 1024 * 1024
	fixtures := make([]struct {
		source string
		hash   string
		data   []byte
	}, 2)
	for idx := range fixtures {
		source := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(source, bytes.Repeat([]byte{byte('a' + idx)}, sourceSize), 0o600); err != nil {
			t.Fatalf("write source %d: %v", idx, err)
		}
		// Keep fixture metainfo outside candidate search paths so that it cannot
		// mask the collision by providing an adjacent reusable torrent.
		torrentPath := filepath.Join(t.TempDir(), "fixture.torrent")
		pieceExp := uint(18)
		if _, err := mkbrr.Create(mkbrr.CreateOptions{
			Path:           source,
			OutputPath:     torrentPath,
			IsPrivate:      true,
			PieceLengthExp: &pieceExp,
		}); err != nil {
			t.Fatalf("create fixture torrent %d: %v", idx, err)
		}
		data, err := os.ReadFile(torrentPath)
		if err != nil {
			t.Fatalf("read fixture torrent %d: %v", idx, err)
		}
		info, err := metainfo.Load(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("load fixture torrent %d: %v", idx, err)
		}
		fixtures[idx].source = source
		fixtures[idx].hash = info.HashInfoBytes().String()
		fixtures[idx].data = data
	}
	if fixtures[0].hash == fixtures[1].hash {
		t.Fatal("different source bytes must produce different infohashes")
	}

	// Each discovery sees one complete client record. Both use the same DB
	// temporary root, as independent preparations in one runtime would.
	var active atomic.Int32
	var exportUnavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture := fixtures[active.Load()]
		switch r.URL.Path {
		case "/api/v2/torrents/search":
			_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{{
				Hash:        fixture.hash,
				Name:        name,
				ContentPath: fixture.source,
				Progress:    1,
				Size:        sourceSize,
				TotalSize:   sourceSize,
				State:       qbittorrent.TorrentStateStalledUp,
			}})
		case "/api/v2/torrents/properties":
			_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{})
		case "/api/v2/torrents/export":
			if exportUnavailable.Load() || r.FormValue("hash") != fixture.hash {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(fixture.data)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "db.sqlite")},
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "qbit",
			SearchClients: config.CSVList{"qbit"},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {Type: "qui", QuiProxyURL: server.URL},
		},
	}
	clients := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t))
	discovery := clientdiscovery.New(clients, api.NopLogger{})
	discover := func(idx int32) clientdiscovery.Evidence {
		t.Helper()
		active.Store(idx)
		fixture := fixtures[idx]
		evidence, err := discovery.Discover(t.Context(), clientdiscovery.SearchInput{
			SourcePath: fixture.source,
			FileList:   []string{fixture.source},
		})
		if err != nil {
			t.Fatalf("discover source %d: %v", idx, err)
		}
		if evidence.TorrentPath == "" || evidence.InfoHash != fixture.hash || !evidence.TorrentDataVerified {
			t.Fatalf("source %d did not return its complete exact-path evidence: %#v", idx, evidence)
		}
		return evidence
	}
	logger := &captureLogger{}
	service := torrentservice.NewService(logger, t.TempDir())
	reuseOnly := true
	create := func(evidence clientdiscovery.Evidence) (api.TorrentResult, error) {
		return service.Create(t.Context(), api.TorrentSubject{
			SourcePath:                fixtures[0].source,
			FileList:                  []string{fixtures[0].source},
			ClientTorrentPath:         evidence.TorrentPath,
			ClientTorrentInfoHash:     evidence.InfoHash,
			ClientTorrentDataVerified: evidence.TorrentDataVerified,
			TorrentOverrides:          api.TorrentOverrides{NoHash: &reuseOnly},
		})
	}
	// A full piece check must fail, making successful reuse prove that the
	// exact complete-data evidence was preserved.
	if err := os.WriteFile(fixtures[0].source, bytes.Repeat([]byte("z"), sourceSize), 0o600); err != nil {
		t.Fatal(err)
	}
	evidenceA := discover(0)
	result, err := create(evidenceA)
	if err != nil || result.Path != evidenceA.TorrentPath || result.InfoHash != fixtures[0].hash {
		t.Fatalf("initial source A must reuse its candidate: result=%#v err=%v", result, err)
	}

	// A matching legacy cache is sufficient even when the export endpoint fails.
	exportUnavailable.Store(true)
	replayed := discover(0)
	exportUnavailable.Store(false)
	if result, err := create(replayed); err != nil || result.InfoHash != fixtures[0].hash {
		t.Fatalf("cached export must retain qualifying reuse: result=%#v err=%v", result, err)
	}

	evidenceB := discover(1)
	if evidenceA.TorrentPath == evidenceB.TorrentPath {
		t.Fatal("different metainfo must not share a retained candidate path")
	}
	onDisk, err := metainfo.LoadFromFile(evidenceA.TorrentPath)
	if err != nil {
		t.Fatalf("load retained candidate: %v", err)
	}
	if onDisk.HashInfoBytes().String() != evidenceA.InfoHash {
		t.Fatal("source B discovery replaced source A's retained candidate")
	}
	logger.debug = nil
	result, err = create(evidenceA)
	if err != nil || result.Path != evidenceA.TorrentPath || result.InfoHash != fixtures[0].hash {
		t.Fatalf("retained source A must still reuse without hashing: result=%#v err=%v", result, err)
	}
	if strings.Contains(strings.Join(logger.debug, "\n"), "stage=piece_hash") {
		t.Fatalf("qualified retained evidence reached piece verification: %v", logger.debug)
	}

	// The generic export remains available to untrusted fallback after discovery
	// is unavailable. Source B matches it and must pass ordinary verification.
	tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := torrentservice.NewService(api.NopLogger{}, tmpRoot).Create(t.Context(), api.TorrentSubject{
		SourcePath:       fixtures[1].source,
		FileList:         []string{fixtures[1].source},
		TorrentOverrides: api.TorrentOverrides{NoHash: &reuseOnly},
	})
	if err != nil || fallback.InfoHash != fixtures[1].hash || fallback.Path == evidenceB.TorrentPath {
		t.Fatalf("generic fallback must remain usable without client trust: result=%#v err=%v", fallback, err)
	}

	restored := discover(0)
	result, err = create(restored)
	if err != nil || result.InfoHash != fixtures[0].hash {
		t.Fatalf("same-infohash rediscovery must preserve qualifying reuse: result=%#v err=%v", result, err)
	}
	if restored.TorrentPath != evidenceA.TorrentPath {
		t.Fatal("repeated identical metadata must reuse its snapshot path")
	}
}

func TestWriteTorrentFileRetainsSanitizedSnapshot(t *testing.T) {
	t.Parallel()

	hash, data := createTestTorrent(t, t.TempDir(), "Example.Snapshot.2026.mkv", 18)
	meta, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	meta.Announce = "https://tracker.example/announce/secret"
	meta.AnnounceList = [][]string{{meta.Announce}}
	meta.Comment = "private comment"
	meta.UrlList = []string{"https://seed.example/secret"}
	var raw bytes.Buffer
	if err := meta.Write(&raw); err != nil {
		t.Fatal(err)
	}
	// A hash-like generic filename must still live outside the snapshot namespace.
	generic := filepath.Join(t.TempDir(), hash+".torrent")
	snapshot, err := writeTorrentFile(generic, raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == generic || filepath.Dir(snapshot) == filepath.Dir(generic) {
		t.Fatal("retained snapshot must be separate from generic export filenames")
	}
	check := func(t *testing.T, path string) {
		t.Helper()
		retained, err := metainfo.LoadFromFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if retained.HashInfoBytes().String() != hash || retained.Announce != "" || len(retained.AnnounceList) != 0 ||
			retained.Comment != "" || len(retained.UrlList) != 0 {
			t.Fatal("saved export must preserve infohash and remove private tracker metadata")
		}
	}
	check(t, generic)
	check(t, snapshot)
	reader, err := os.Open(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	beforeReplay, err := reader.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeTorrentFile(generic, raw.Bytes()); err != nil {
		t.Fatalf("identical replay with an open reader must succeed: %v", err)
	}
	afterReplay, err := os.Stat(snapshot)
	if err != nil || !os.SameFile(beforeReplay, afterReplay) {
		t.Fatalf("identical replay must reuse the existing file without replacement: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	// Concurrent equal-infohash publication must preserve the retained path and
	// expose complete metainfo to readers, rather than truncate the selected file.
	t.Run("concurrent replay", func(t *testing.T) {
		for range 12 {
			t.Run("writer", func(t *testing.T) {
				t.Parallel()
				path, err := writeTorrentFile(generic, raw.Bytes())
				if err != nil {
					t.Fatal(err)
				}
				if path != snapshot {
					t.Fatal("identical exports must reuse the retained snapshot path")
				}
				check(t, snapshot)
				check(t, generic)
			})
		}
	})
	entries, err := os.ReadDir(filepath.Dir(snapshot))
	if err != nil || len(entries) != 1 {
		t.Fatalf("replay must not leave staging files or duplicate snapshots: count=%d err=%v", len(entries), err)
	}
	// Equal infohashes need not mean equal top-level metadata. Preserve the exact
	// captured snapshot because tracker policy also checks metainfo file size.
	before, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	meta.CreatedBy = "Different synthetic exporter"
	raw.Reset()
	if err := meta.Write(&raw); err != nil {
		t.Fatal(err)
	}
	otherSnapshot, err := writeTorrentFile(generic, raw.Bytes())
	if err != nil || otherSnapshot == snapshot {
		t.Fatalf("different sanitized metadata must have its own snapshot: path=%s err=%v", otherSnapshot, err)
	}
	after, err := os.ReadFile(snapshot)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("same-infohash metadata replacement changed the retained snapshot: %v", err)
	}
	check(t, otherSnapshot)
	// Release-directory cleanup already owns both the generic file and snapshots.
	if err := os.RemoveAll(filepath.Dir(generic)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("release cleanup left retained snapshot: %v", err)
	}
}

func TestWriteTorrentBytesCleansFailedPublication(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "occupied.torrent")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeTorrentBytes(path, []byte("synthetic metainfo")); err == nil {
		t.Fatal("publication onto a directory must fail")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("failed publication must preserve destination and remove staged files: entries=%v err=%v", entries, err)
	}
}
