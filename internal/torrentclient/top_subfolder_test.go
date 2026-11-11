// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	qbittorrent "github.com/autobrr/go-qbittorrent"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildSearchTermsIncludesOnlyTopSubfolder(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source string
		want         []string
	}{
		{"Windows nested file", `D:\Library\Example.Release.2026\Part.One\short.mkv`, []string{"short.mkv", "short", "Example.Release.2026"}},
		{"Windows slash paths", `D:/Library/Example.Release.2026/Part.One/short.mkv`, []string{"short.mkv", "short", "Example.Release.2026"}},
		{"POSIX nested file", `/library/Example.Release.2026/Part.One/short.mkv`, []string{"short.mkv", "short", "Example.Release.2026"}},
		{"UNC volume", `\\server\share\Library\Example.Release.2026\Part.One\short.mkv`, []string{"short.mkv", "short", "Example.Release.2026"}},
		{"first directory only", `D:\Library\short.mkv`, []string{"short.mkv", "short"}},
		{"POSIX first directory only", `/library/short.mkv`, []string{"short.mkv", "short"}},
		{"drive root file", `D:\short.mkv`, []string{"short.mkv", "short"}},
		{"relative path", `Library/Example.Release.2026/short.mkv`, []string{"short.mkv", "short"}},
		{"duplicate release name", `/library/Example.Release.2026/Example.Release.2026.mkv`, []string{"Example.Release.2026.mkv", "Example.Release.2026"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := api.ClientSubject{SourcePath: tc.source, FileList: []string{tc.source}}
			if got := buildSearchTerms(meta); !slices.Equal(got, tc.want) {
				t.Fatalf("search terms = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTopSubfolderMatchesRequireValidatedContent(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{
		"wrong content", "unavailable export", "empty export", "cached release", "release selected first",
		"valid cached release", "valid release selected first",
	} {
		t.Run(scenario, func(t *testing.T) {
			validFolder := strings.HasPrefix(scenario, "valid ")
			mode := strings.TrimPrefix(scenario, "valid ")
			dir := t.TempDir()
			source := filepath.Join(dir, "Example.Video.2026.mkv")
			validHash, validData := createTestTorrent(t, dir, filepath.Base(source), 22)
			folderFile := "Other.Video.2026.mkv"
			if validFolder {
				folderFile = filepath.Base(source)
			}
			folderHash, folderData := createTestTorrent(t, t.TempDir(), folderFile, 23)
			if mode != "cached release" {
				if err := os.Remove(source + ".torrent"); err != nil {
					t.Fatal(err)
				}
			}
			root := filepath.Join(filepath.VolumeName(source), string(filepath.Separator))
			relative, err := filepath.Rel(root, source)
			if err != nil {
				t.Fatal(err)
			}
			folder := strings.Split(filepath.ToSlash(relative), "/")[1]
			valid := qbittorrent.Torrent{
				Hash:    validHash,
				Name:    filepath.Base(source),
				Comment: "https://blutopia.cc/torrents/2222",
			}
			folderMatch := qbittorrent.Torrent{
				Hash:    folderHash,
				Name:    folder,
				Comment: "https://beyond-hd.me/details/1111",
				Tracker: "https://tracker.beyond-hd.me/announce/redacted",
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/search":
					// Return both to also cover fuzzy folder matches and the
					// paths that finish before exporting every candidate.
					items := []qbittorrent.Torrent{folderMatch, valid}
					if mode == "release selected first" {
						items = []qbittorrent.Torrent{valid, folderMatch}
					}
					_ = json.NewEncoder(w).Encode(items)
				case "/api/v2/torrents/properties":
					_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{})
				case "/api/v2/torrents/export":
					if err := r.ParseForm(); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if r.FormValue("hash") == validHash {
						_, _ = w.Write(validData)
					} else {
						switch mode {
						case "unavailable export":
							w.WriteHeader(http.StatusNotFound)
						case "empty export":
							w.WriteHeader(http.StatusOK)
						default:
							_, _ = w.Write(folderData)
						}
					}
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
			result, err := NewServiceWithRegistry(cfg, api.NopLogger{}, trackerPatternRegistry(t)).SearchPathedTorrents(t.Context(), api.ClientSubject{
				SourcePath: source,
				FileList:   []string{source},
			})
			if err != nil || result.InfoHash != validHash || result.TorrentPath == "" || result.TrackerIDs["blu"] != "2222" {
				t.Fatalf("valid release lost: hash matched=%t path=%t tracker=%q err=%v", result.InfoHash == validHash, result.TorrentPath != "", result.TrackerIDs["blu"], err)
			}
			if validFolder {
				if result.TrackerIDs["bhd"] != "1111" || !containsString(result.MatchedTrackers, "BHD") {
					t.Fatal("validated folder hint lost its tracker metadata")
				}
			} else if result.TrackerIDs["bhd"] != "" || containsString(result.MatchedTrackers, "BHD") {
				t.Fatal("unvalidated folder hint contributed tracker metadata")
			}
		})
	}
}
