// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/azfamily"
	antimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/ant"
	ascimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/asc"
	bhdimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/bhd"
	bjsimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/bjs"
	btimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/bt"
	btnimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/btn"
	cztimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/czt"
	dcimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/dc"
	ffimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/ff"
	flimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/fl"
	gpwimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/gpw"
	hdbimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/hdb"
	hdsimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/hds"
	hdtimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/hdt"
	isimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/is"
	nblimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/nbl"
	ptpimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/ptp"
	ptsimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/pts"
	rtfimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/rtf"
	spdimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/spd"
	thrimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/thr"
	tlimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/tl"
	unit3dimpl "github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	aitherimpl "github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/aither"
	"github.com/autobrr/upbrr/pkg/api"
)

type handlerRewriteTransport struct {
	base *url.URL
	rt   http.RoundTripper
}

func (t handlerRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.base.Scheme
	clone.URL.Host = t.base.Host
	clone.Host = t.base.Host
	response, err := t.rt.RoundTrip(clone)
	if err != nil {
		return response, fmt.Errorf("rewrite tracker request: %w", err)
	}
	return response, nil
}

func adapterEvidence(result dupe.AdapterResult) ([]api.DupeEntry, []string, error) {
	return result.Entries(), result.Notes(), result.Cause()
}

func TestSiteHandlersSearch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		tracker    string
		meta       api.DuplicateSubject
		setup      func(t *testing.T, baseURL string, dbPath string)
		handler    func(cfg config.Config, client *http.Client) dupe.Adapter
		validate   func(t *testing.T, entries []api.DupeEntry)
		scope      dupe.WorkScope
		enumerated bool
		effective  bool
	}{
		{
			name:    "ASC",
			tracker: "ASC",
			meta: api.DuplicateSubject{
				Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryTV, IMDBID: 1234567},
				Release:    api.ReleaseInfo{Title: "Example Release 2026"},
				SeasonInt:  1,
				EpisodeInt: 2,
				SourcePath: "x",
			},
			setup: func(t *testing.T, _ string, dbPath string) {
				writeTextCookie(t, dbPath, "ASC", hostFromBaseURL(t, "https://cliente.amigos-share.club"))
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(ascimpl.New(), "ASC", cfg, client, api.NopLogger{})
			},
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 0 {
					t.Fatalf("unexpected ASC entries: %#v", entries)
				}
			},
			scope: dupe.WorkScopeProviderID,
		},
		{
			name:    "BT",
			tracker: "BT",
			meta: api.DuplicateSubject{
				Identity:   api.ExternalIdentity{IMDBID: 123},
				Release:    api.ReleaseInfo{Title: "Movie"},
				SourcePath: "x",
			},
			setup: func(t *testing.T, _ string, dbPath string) {
				writeTextCookie(t, dbPath, "BT", hostFromBaseURL(t, "https://brasiltracker.org"))
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(btimpl.New(), "BT", cfg, client, api.NopLogger{})
			},
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].Name != "Movie.2024.1080p.WEB-DL-GRP" {
					t.Fatalf("unexpected BT entries: %#v", entries)
				}
			},
			scope: dupe.WorkScopeProviderID,
		},
		{
			name:    "FL",
			tracker: "FL",
			meta: api.DuplicateSubject{
				Identity:   api.ExternalIdentity{IMDBID: 123},
				Release:    api.ReleaseInfo{Resolution: "1080p"},
				SourcePath: "x",
			},
			setup: func(t *testing.T, _ string, dbPath string) {
				writeJSONCookie(t, dbPath, "FL", `{"sid":"cookie"}`)
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(flimpl.New(), "FL", cfg, client, api.NopLogger{})
			},
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].ID != "1234" {
					t.Fatalf("unexpected FL entries: %#v", entries)
				}
			},
			scope: dupe.WorkScopeProviderID,
		},
		{
			name:    "FF",
			tracker: "FF",
			meta:    api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 123}, SourcePath: "x"},
			setup: func(t *testing.T, _ string, dbPath string) {
				writeTextCookie(t, dbPath, "FF", hostFromBaseURL(t, "https://www.funfile.org"))
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(ffimpl.New(), "FF", cfg, client, api.NopLogger{})
			},
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].Name != "Fun.Movie.2024.1080p.BluRay.x264-GRP" {
					t.Fatalf("unexpected FF entries: %#v", entries)
				}
			},
			scope: dupe.WorkScopeProviderID,
		},
		{
			name:    "BJS",
			tracker: "BJS",
			meta:    api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 123}, SourcePath: "x"},
			setup: func(t *testing.T, _ string, dbPath string) {
				writeTextCookie(t, dbPath, "BJS", hostFromBaseURL(t, "https://bj-share.info"))
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(bjsimpl.New(), "BJS", cfg, client, api.NopLogger{})
			},
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].ID != "44" {
					t.Fatalf("unexpected BJS entries: %#v", entries)
				}
			},
			scope: dupe.WorkScopeProviderID,
		},
		{
			name:    "HDS",
			tracker: "HDS",
			meta: api.DuplicateSubject{
				Identity:   api.ExternalIdentity{IMDBID: 123},
				Release:    api.ReleaseInfo{Resolution: "1080p"},
				SourcePath: "x",
			},
			setup: func(t *testing.T, _ string, dbPath string) {
				writeTextCookie(t, dbPath, "HDS", hostFromBaseURL(t, "https://hd-space.org"))
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(hdsimpl.New(), "HDS", cfg, client, api.NopLogger{})
			},
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || !entries[0].SizeKnown {
					t.Fatalf("unexpected HDS entries: %#v", entries)
				}
			},
			scope:      dupe.WorkScopeProviderID,
			enumerated: true,
			effective:  true,
		},
		{
			name:    "HDT",
			tracker: "HDT",
			meta: api.DuplicateSubject{
				Identity:   api.ExternalIdentity{IMDBID: 123},
				Release:    api.ReleaseInfo{Resolution: "1080p"},
				SourcePath: "x",
			},
			setup: func(t *testing.T, _ string, dbPath string) {
				writeTextCookie(t, dbPath, "HDT", hostFromBaseURL(t, "https://hd-torrents.me"))
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(hdtimpl.New(), "HDT", cfg, client, api.NopLogger{})
			},
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].Name != "Movie.2024.1080p.REMUX-GRP" {
					t.Fatalf("unexpected HDT entries: %#v", entries)
				}
			},
			scope: dupe.WorkScopeProviderID,
		},
		{
			name:    "IS",
			tracker: "IS",
			meta:    api.DuplicateSubject{Identity: api.ExternalIdentity{Category: "MOVIE", IMDBID: 123}, SourcePath: "x"},
			setup: func(t *testing.T, _ string, dbPath string) {
				writeTextCookie(t, dbPath, "IS", hostFromBaseURL(t, "https://immortalseed.me"))
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(isimpl.New(), "IS", cfg, client, api.NopLogger{})
			},
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].Name != "Movie.2024.1080p.WEB-DL-GRP" {
					t.Fatalf("unexpected IS entries: %#v", entries)
				}
			},
			scope: dupe.WorkScopeProviderID,
		},
		{
			name:    "PTS",
			tracker: "PTS",
			meta:    api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 123}, SourcePath: "x"},
			setup: func(t *testing.T, _ string, dbPath string) {
				writeTextCookie(t, dbPath, "PTS", hostFromBaseURL(t, "https://www.ptskit.org"))
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(ptsimpl.New(), "PTS", cfg, client, api.NopLogger{})
			},
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].Name != "PTS.Movie.2024.1080p.WEB-DL-GRP" {
					t.Fatalf("unexpected PTS entries: %#v", entries)
				}
			},
			scope: dupe.WorkScopeProviderID,
		},
		{
			name:    "THR",
			tracker: "THR",
			meta:    api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 123}, SourcePath: "x"},
			setup:   func(_ *testing.T, _ string, _ string) {},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(thrimpl.New(), "THR", cfg, client, api.NopLogger{})
			},
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 1 || entries[0].Name != "THR.Movie.2024.1080p.BluRay-GRP" {
					t.Fatalf("unexpected THR entries: %#v", entries)
				}
			},
			scope:      dupe.WorkScopeProviderID,
			enumerated: true,
			effective:  true,
		},
		{
			name:    "TL",
			tracker: "TL",
			meta: api.DuplicateSubject{
				Release:     api.ReleaseInfo{Title: "Example Release 2026"},
				ReleaseName: "Example.Release.2026.1080p-GRP",
				SourcePath:  "x",
			},
			setup: func(_ *testing.T, _ string, _ string) {},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(tlimpl.New(), "TL", cfg, client, api.NopLogger{})
			},
			validate: func(t *testing.T, entries []api.DupeEntry) {
				if len(entries) != 0 {
					t.Fatalf("unexpected TL entries: %#v", entries)
				}
			},
			scope:      dupe.WorkScopeTitle,
			enumerated: true,
		},
		{
			name:    "ANT",
			tracker: "ANT",
			meta:    api.DuplicateSubject{Identity: api.ExternalIdentity{TMDBID: 123}, SourcePath: "x"},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(antimpl.New(), "ANT", cfg, client, api.NopLogger{})
			},
			validate:   validateNoEntries,
			scope:      dupe.WorkScopeProviderID,
			enumerated: true,
			effective:  true,
		},
		{
			name:    "AZ",
			tracker: "AZ",
			meta: api.DuplicateSubject{
				Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 123},
				Release:    api.ReleaseInfo{Title: "Example Release 2026"},
				SourcePath: "x",
			},
			setup: func(t *testing.T, _ string, dbPath string) {
				writeTextCookie(t, dbPath, "AZ", hostFromBaseURL(t, "https://avistaz.to"))
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(azfamily.New("AZ"), "AZ", cfg, client, api.NopLogger{})
			},
			validate:   validateNoEntries,
			scope:      dupe.WorkScopeTrackerGroup,
			enumerated: true,
			effective:  true,
		},
		{
			name:    "BHD",
			tracker: "BHD",
			meta:    api.DuplicateSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123}, SourcePath: "x"},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(bhdimpl.New(), "BHD", cfg, client, api.NopLogger{})
			},
			validate:   validateNoEntries,
			scope:      dupe.WorkScopeProviderID,
			enumerated: true,
			effective:  true,
		},
		{
			name:    "BTN",
			tracker: "BTN",
			meta: api.DuplicateSubject{
				Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryTV, TVDBID: 123},
				SourcePath: "x",
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(btnimpl.New(), "BTN", cfg, client, api.NopLogger{})
			},
			validate:   validateNoEntries,
			scope:      dupe.WorkScopeProviderID,
			enumerated: true,
			effective:  true,
		},
		{
			name:    "CZT",
			tracker: "CZT",
			meta:    api.DuplicateSubject{Release: api.ReleaseInfo{Title: "Example Release 2026"}, SourcePath: "x"},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(cztimpl.New(), "CZT", cfg, client, api.NopLogger{})
			},
			validate:   validateNoEntries,
			scope:      dupe.WorkScopeTitle,
			enumerated: true,
		},
		{
			name:    "DC",
			tracker: "DC",
			meta:    api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 123}, SourcePath: "x"},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(dcimpl.New(), "DC", cfg, client, api.NopLogger{})
			},
			validate:   validateNoEntries,
			scope:      dupe.WorkScopeProviderID,
			enumerated: true,
			effective:  true,
		},
		{
			name:    "GPW",
			tracker: "GPW",
			meta:    api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 123}, SourcePath: "x"},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(gpwimpl.New(), "GPW", cfg, client, api.NopLogger{})
			},
			validate:   validateNoEntries,
			scope:      dupe.WorkScopeProviderID,
			enumerated: true,
			effective:  true,
		},
		{
			name:    "HDB",
			tracker: "HDB",
			meta: api.DuplicateSubject{
				Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 123},
				SourcePath: "x",
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(hdbimpl.New(), "HDB", cfg, client, api.NopLogger{})
			},
			validate:   validateNoEntries,
			scope:      dupe.WorkScopeProviderID,
			enumerated: true,
			effective:  true,
		},
		{
			name:    "NBL",
			tracker: "NBL",
			meta:    api.DuplicateSubject{Identity: api.ExternalIdentity{TVmazeID: 81}, SourcePath: "x"},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(nblimpl.New(), "NBL", cfg, client, api.NopLogger{})
			},
			validate:   validateNoEntries,
			scope:      dupe.WorkScopeProviderID,
			enumerated: true,
			effective:  true,
		},
		{
			name:    "PTP",
			tracker: "PTP",
			meta:    api.DuplicateSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 123}, SourcePath: "x"},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(ptpimpl.New(), "PTP", cfg, client, api.NopLogger{})
			},
			validate:   validateNoEntries,
			scope:      dupe.WorkScopeTrackerGroup,
			enumerated: true,
			effective:  true,
		},
		{
			name:    "RTF",
			tracker: "RTF",
			meta: api.DuplicateSubject{
				Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 123},
				Release:    api.ReleaseInfo{Year: 1990},
				SourcePath: "x",
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(rtfimpl.New(), "RTF", cfg, client, api.NopLogger{})
			},
			validate:   validateNoEntries,
			scope:      dupe.WorkScopeProviderID,
			enumerated: true,
			effective:  true,
		},
		{
			name:    "SPD",
			tracker: "SPD",
			meta:    api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 123}, SourcePath: "x"},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(spdimpl.New(), "SPD", cfg, client, api.NopLogger{})
			},
			validate:   validateNoEntries,
			scope:      dupe.WorkScopeProviderID,
			enumerated: true,
			effective:  true,
		},
		{
			name:    "Unit3D",
			tracker: "AITHER",
			meta: api.DuplicateSubject{
				Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
				SourcePath: "x",
			},
			handler: func(cfg config.Config, client *http.Client) dupe.Adapter {
				return dupe.NewAdapter(unit3dimpl.NewWithProfile(aitherimpl.Profile()), "AITHER", cfg, client, api.NopLogger{}, MustNewRegistry())
			},
			validate:   validateNoEntries,
			scope:      dupe.WorkScopeProviderID,
			enumerated: true,
			effective:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			dbPath := filepath.Join(tmp, "ua.db")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch tc.tracker {
				case "ANT":
					_, _ = w.Write([]byte(`{"item":[],"response":{"offset":0,"total":0}}`))
					return
				case "AZ":
					switch r.URL.Path {
					case "/ajax/movies/1":
						_, _ = w.Write([]byte(`{"data":[{"id":"77","imdb":"tt0000123"}]}`))
						return
					case "/movies/torrents/77":
						_, _ = w.Write([]byte(`<table class="table-bordered"><tbody></tbody></table>`))
						return
					}
				case "BHD":
					_, _ = w.Write([]byte(`{"status_code":1,"page":1,"total_pages":0,"total_results":0,"results":[]}`))
					return
				case "BTN":
					if r.Method != http.MethodPost || r.URL.Path != "/" {
						http.Error(w, "unexpected BTN search route", http.StatusBadRequest)
						return
					}
					var request struct {
						JSONRPC string            `json:"jsonrpc"`
						ID      string            `json:"id"`
						Method  string            `json:"method"`
						Params  []json.RawMessage `json:"params"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.JSONRPC != "2.0" ||
						request.ID != "upbrr-btn-search" || request.Method != "getTorrents" || len(request.Params) != 4 {
						http.Error(w, "invalid BTN search request", http.StatusBadRequest)
						return
					}
					var filter map[string]string
					if err := json.Unmarshal(request.Params[1], &filter); err != nil || filter["tvdb"] != "123" {
						http.Error(w, "missing BTN TVDB provider ID", http.StatusBadRequest)
						return
					}
					_, _ = w.Write([]byte(`{"result":{"results":"0","torrents":{}}}`))
					return
				case "DC":
					_, _ = w.Write([]byte(`{"results":[],"index":0,"limit":100,"count":0,"total":0,"includesPending":true}`))
					return
				case "CZT", "RTF", "SPD":
					_, _ = w.Write([]byte(`[]`))
					return
				case "GPW":
					_, _ = w.Write([]byte(`{"status":200,"response":[]}`))
					return
				case "HDB":
					_, _ = w.Write([]byte(`{"status":0,"data":[]}`))
					return
				case "NBL":
					_, _ = w.Write([]byte(`{"current_page":0,"total_pages":0,"count":0,"total_results":0,"items":[]}`))
					return
				case "PTP":
					_, _ = w.Write([]byte(`{"TotalResults":"0","Movies":[],"Page":"1"}`))
					return
				case "AITHER":
					_, _ = w.Write([]byte(`{"data":[],"links":{"next":null}}`))
					return
				case "ASC":
					if r.URL.Path == "/busca-series.php" && r.URL.Query().Get("search") == "" && r.URL.Query().Get("imdb") == "tt1234567" {
						_, _ = w.Write([]byte(`<html><body></body></html>`))
						return
					}
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
						http.SetCookie(w, &http.Cookie{
							Name:  "session",
							Value: "cookie",
							Path:  "/",
						})
						_, _ = w.Write([]byte("ok"))
						return
					}
					if r.URL.Path == "/browse.php" {
						_, _ = w.Write([]byte(`<a href="details.php?id=91" onmousemove="return overlibImage('THR.Movie.2024.1080p.BluRay-GRP','/images/test.png')">link</a>`))
						return
					}
				case "TL":
					if r.URL.EscapedPath() == "/torrents/browse/list/query/Example%20Release%202026" {
						_, _ = w.Write([]byte(`{"torrentList":[]}`))
						return
					}
				}
				http.NotFound(w, r)
			}))
			defer server.Close()

			if tc.setup != nil {
				tc.setup(t, server.URL, dbPath)
			}
			cfg := config.Config{
				MainSettings: config.MainSettingsConfig{DBPath: dbPath},
				Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
					tc.tracker: {
						Username:   "user",
						Password:   "pass",
						Passkey:    "passkey",
						APIKey:     "api-key",
						PTPAPIUser: "api-user",
						PTPAPIKey:  "api-key",
					},
				}},
			}
			base, err := url.Parse(server.URL)
			if err != nil {
				t.Fatalf("parse test server URL: %v", err)
			}
			client := &http.Client{Transport: handlerRewriteTransport{base: base, rt: server.Client().Transport}}
			result := tc.handler(cfg, client).Search(context.Background(), tc.meta)
			entries, notes, err := adapterEvidence(result)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(notes) != 0 {
				t.Fatalf("unexpected notes: %v", notes)
			}
			search := result.SearchEvidence()
			if search.WorkScope != tc.scope || search.Complete != tc.enumerated || search.EffectiveComplete() != tc.effective {
				t.Fatalf("search evidence = %#v, want scope=%q enumerated=%t effective=%t", search, tc.scope, tc.enumerated, tc.effective)
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

func validateNoEntries(t *testing.T, entries []api.DupeEntry) {
	t.Helper()
	if len(entries) != 0 {
		t.Fatalf("unexpected entries: %#v", entries)
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
