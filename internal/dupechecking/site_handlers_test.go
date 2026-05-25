// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSiteHandlersSearch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tracker  string
		meta     api.PreparedMetadata
		setup    func(t *testing.T, baseURL string, dbPath string)
		handler  func(cfg config.Config, client *http.Client) searchHandler
		validate func(t *testing.T, entries []api.DupeEntry)
	}{
		{
			name:    "BT",
			tracker: "BT",
			meta:    api.PreparedMetadata{ExternalIDs: api.ExternalIDs{IMDBID: 123}, Release: api.ReleaseInfo{Title: "Movie"}, SourcePath: "x"},
			setup: func(t *testing.T, baseURL string, dbPath string) {
				writeTextCookie(t, dbPath, "BT", hostFromBaseURL(t, baseURL))
			},
			handler: func(cfg config.Config, client *http.Client) searchHandler {
				return btHandler{cfg: cfg, http: client, logger: api.NopLogger{}}
			},
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].Name != "Movie.2024.1080p.WEB-DL-GRP" {
					t.Fatalf("unexpected BT entries: %#v", entries)
				}
			},
		},
		{
			name:    "FL",
			tracker: "FL",
			meta:    api.PreparedMetadata{ExternalIDs: api.ExternalIDs{IMDBID: 123}, Release: api.ReleaseInfo{Resolution: "1080p"}, SourcePath: "x"},
			setup: func(t *testing.T, baseURL string, dbPath string) {
				writeJSONCookie(t, dbPath, "FL", `{"sid":"cookie"}`)
			},
			handler: func(cfg config.Config, client *http.Client) searchHandler { return flHandler{cfg: cfg, http: client} },
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].ID != "1234" {
					t.Fatalf("unexpected FL entries: %#v", entries)
				}
			},
		},
		{
			name:    "FF",
			tracker: "FF",
			meta:    api.PreparedMetadata{ExternalIDs: api.ExternalIDs{IMDBID: 123}, SourcePath: "x"},
			setup: func(t *testing.T, baseURL string, dbPath string) {
				writeTextCookie(t, dbPath, "FF", hostFromBaseURL(t, baseURL))
			},
			handler: func(cfg config.Config, client *http.Client) searchHandler { return ffHandler{cfg: cfg, http: client} },
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].Name != "Fun.Movie.2024.1080p.BluRay.x264-GRP" {
					t.Fatalf("unexpected FF entries: %#v", entries)
				}
			},
		},
		{
			name:    "BJS",
			tracker: "BJS",
			meta:    api.PreparedMetadata{ExternalIDs: api.ExternalIDs{IMDBID: 123}, SourcePath: "x"},
			setup: func(t *testing.T, baseURL string, dbPath string) {
				writeTextCookie(t, dbPath, "BJS", hostFromBaseURL(t, baseURL))
			},
			handler: func(cfg config.Config, client *http.Client) searchHandler { return bjsHandler{cfg: cfg, http: client} },
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].ID != "44" {
					t.Fatalf("unexpected BJS entries: %#v", entries)
				}
			},
		},
		{
			name:    "HDS",
			tracker: "HDS",
			meta:    api.PreparedMetadata{ExternalIDs: api.ExternalIDs{IMDBID: 123}, Release: api.ReleaseInfo{Resolution: "1080p"}, SourcePath: "x"},
			setup: func(t *testing.T, baseURL string, dbPath string) {
				writeTextCookie(t, dbPath, "HDS", hostFromBaseURL(t, baseURL))
			},
			handler: func(cfg config.Config, client *http.Client) searchHandler { return hdsHandler{cfg: cfg, http: client} },
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || !entries[0].SizeKnown {
					t.Fatalf("unexpected HDS entries: %#v", entries)
				}
			},
		},
		{
			name:    "HDT",
			tracker: "HDT",
			meta:    api.PreparedMetadata{ExternalIDs: api.ExternalIDs{IMDBID: 123}, Release: api.ReleaseInfo{Resolution: "1080p"}, SourcePath: "x"},
			setup: func(t *testing.T, baseURL string, dbPath string) {
				writeTextCookie(t, dbPath, "HDT", hostFromBaseURL(t, baseURL))
			},
			handler: func(cfg config.Config, client *http.Client) searchHandler { return hdtHandler{cfg: cfg, http: client} },
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].Name != "Movie.2024.1080p.REMUX-GRP" {
					t.Fatalf("unexpected HDT entries: %#v", entries)
				}
			},
		},
		{
			name:    "IS",
			tracker: "IS",
			meta:    api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "MOVIE", IMDBID: 123}, SourcePath: "x"},
			setup: func(t *testing.T, baseURL string, dbPath string) {
				writeTextCookie(t, dbPath, "IS", hostFromBaseURL(t, baseURL))
			},
			handler: func(cfg config.Config, client *http.Client) searchHandler { return isHandler{cfg: cfg, http: client} },
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].Name != "Movie.2024.1080p.WEB-DL-GRP" {
					t.Fatalf("unexpected IS entries: %#v", entries)
				}
			},
		},
		{
			name:    "PTS",
			tracker: "PTS",
			meta:    api.PreparedMetadata{ExternalIDs: api.ExternalIDs{IMDBID: 123}, SourcePath: "x"},
			setup: func(t *testing.T, baseURL string, dbPath string) {
				writeTextCookie(t, dbPath, "PTS", hostFromBaseURL(t, baseURL))
			},
			handler: func(cfg config.Config, client *http.Client) searchHandler { return ptsHandler{cfg: cfg, http: client} },
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].Name != "PTS.Movie.2024.1080p.WEB-DL-GRP" {
					t.Fatalf("unexpected PTS entries: %#v", entries)
				}
			},
		},
		{
			name:    "THR",
			tracker: "THR",
			meta:    api.PreparedMetadata{ExternalIDs: api.ExternalIDs{IMDBID: 123}, SourcePath: "x"},
			setup:   func(_ *testing.T, _ string, _ string) {},
			handler: func(cfg config.Config, client *http.Client) searchHandler { return thrHandler{cfg: cfg, http: client} },
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].Name != "THR.Movie.2024.1080p.BluRay-GRP" {
					t.Fatalf("unexpected THR entries: %#v", entries)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			dbPath := filepath.Join(tmp, "ua.db")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch tc.tracker {
				case "BT":
					if r.URL.Path == "/torrents.php" {
						if r.URL.RawQuery == "id=99" {
							_, _ = w.Write([]byte(`<html><body><div id="files_99"><div class="filelist_path">Movie.2024.1080p.WEB-DL-GRP</div></div><table id="torrent_table"><tr id="torrent99"><td><a onclick="gtoggle()">m2ts</a></td></tr></table></body></html>`))
							return
						}
						if strings.Contains(r.URL.RawQuery, "searchstr=") {
							_, _ = w.Write([]byte(`<html><body><table id="torrent_table"><tr><td><a href="torrents.php?id=99">group</a></td></tr></table></body></html>`))
							return
						}
					}
				case "FL":
					if r.URL.Path == "/browse.php" {
						_, _ = w.Write([]byte(`<a href="details.php?id=1234" title="FileList.Movie.2024.1080p.BluRay-GRP">x</a>`))
						return
					}
				case "FF":
					if r.URL.Path == "/torrents.php" && r.URL.RawQuery == "id=55" {
						_, _ = w.Write([]byte(`<html><body><table><tr id="torrent123"><td><a onclick="gtoggle()">Fun.Movie.2024.1080p.BluRay.x264-GRP</a></td></tr></table></body></html>`))
						return
					}
					if r.URL.Path == "/torrents.php" {
						_, _ = w.Write([]byte(`<html><body><table id="torrent_table"><tr><td><a href="torrents.php?id=55">group</a></td></tr></table></body></html>`))
						return
					}
				case "BJS":
					if r.URL.Path == "/torrents.php" {
						_, _ = w.Write([]byte(`<html><body><div class="main_column"><table><tr id="torrent44"><td><a onclick="loadIfNeeded('44', '99')">BJS Movie 2024 1080p WEB-DL</a></td></tr></table></div></body></html>`))
						return
					}
				case "HDS":
					if r.URL.Path == "/index.php" {
						_, _ = w.Write([]byte(`Show/Hide Categories<table><tr><td class="lista"><a href="index.php?page=torrent-details&id=7">HDS.Movie.2024.1080p.BluRay-GRP</a></td><td class="lista">10.5 GB</td></tr></table>`))
						return
					}
				case "HDT":
					if r.URL.Path == "/torrents.php" {
						_, _ = w.Write([]byte(`<html><body><table><tr><td class="mainblockcontent"><a href="details.php?id=77">Movie.2024.1080p.REMUX-GRP</a></td><td class="mainblockcontent">15.2 GiB</td></tr></table></body></html>`))
						return
					}
				case "IS":
					if r.URL.Path == "/browse.php" {
						_, _ = w.Write([]byte(`<table id="sortabletable"><tbody><tr><td></td><td><a href="details.php?id=12">Movie.2024.1080p.WEB-DL-GRP</a></td><td></td><td></td><td>8.2 GB</td></tr></tbody></table>`))
						return
					}
				case "PTS":
					if r.URL.Path == "/torrents.php" {
						_, _ = w.Write([]byte(`<table class="torrents"><table class="torrentname"><b>PTS.Movie.2024.1080p.WEB-DL-GRP</b></table></table>`))
						return
					}
				case "THR":
					if r.URL.Path == "/login.php" {
						_, _ = w.Write([]byte(`<form><input type="hidden" name="returnto" value="/browse.php"></form>`))
						return
					}
					if r.URL.Path == "/takelogin.php" {
						http.SetCookie(w, &http.Cookie{Name: "session", Value: "cookie", Path: "/"})
						_, _ = w.Write([]byte("ok"))
						return
					}
					if r.URL.Path == "/browse.php" {
						_, _ = w.Write([]byte(`<a href="details.php?id=91" onmousemove="return overlibImage('THR.Movie.2024.1080p.BluRay-GRP','/images/test.png')">link</a>`))
						return
					}
				}
				http.NotFound(w, r)
			}))
			defer server.Close()

			tc.setup(t, server.URL, dbPath)
			cfg := config.Config{
				MainSettings: config.MainSettingsConfig{DBPath: dbPath},
				Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
					tc.tracker: {URL: server.URL, Username: "user", Password: "pass"},
				}},
			}
			entries, notes, err := tc.handler(cfg, server.Client()).Search(context.Background(), tc.meta, tc.tracker)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(notes) != 0 {
				t.Fatalf("unexpected notes: %v", notes)
			}
			tc.validate(t, entries)
		})
	}
}

func writeTextCookie(t *testing.T, dbPath string, tracker string, domain string) {
	t.Helper()
	cookieDir := filepath.Join(filepath.Dir(dbPath), "cookies")
	if err := os.MkdirAll(cookieDir, 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	line := domain + "\tTRUE\t/\tFALSE\t0\tsession\tcookie\n"
	if err := os.WriteFile(filepath.Join(cookieDir, tracker+".txt"), []byte(line), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
}

func writeJSONCookie(t *testing.T, dbPath string, tracker string, payload string) {
	t.Helper()
	cookieDir := filepath.Join(filepath.Dir(dbPath), "cookies")
	if err := os.MkdirAll(cookieDir, 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cookieDir, tracker+".json"), []byte(payload), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
}

func hostFromBaseURL(t *testing.T, baseURL string) string {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	return parsed.Hostname()
}
