// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerdata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type rewriteHostTransport struct {
	base *url.URL
	rt   http.RoundTripper
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.base.Scheme
	clone.URL.Host = t.base.Host
	clone.Host = t.base.Host
	return t.rt.RoundTrip(clone)
}

func TestLookupBTN(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/btn" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"torrents": map[string]any{
					"1": map[string]any{"ImdbID": 1234567, "TvdbID": 76543},
				},
			},
		})
	}))
	defer server.Close()

	cfg := config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BTN": {APIKey: strings.Repeat("a", 30)},
		}},
	}
	client := NewClient(cfg, api.NopLogger{}, server.Client())
	client.btnURL = server.URL + "/btn"

	result, err := client.Lookup(context.Background(), "BTN", "42", api.PreparedMetadata{}, "", true, false)
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if result.IMDBID != 1234567 || result.TVDBID != 76543 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestLookupBHD(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bhd/") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status_code": 1,
			"success":     true,
			"results": []any{
				map[string]any{
					"id":          "99",
					"name":        "Example.Release",
					"imdb_id":     "tt1234567",
					"tmdb_id":     "movie/765",
					"description": "hello\n[url=https://ptpimg.me/full/example][img]https://ptpimg.me/example.png[/img][/url]",
				},
			},
		})
	}))
	defer server.Close()

	cfg := config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BHD": {APIKey: strings.Repeat("a", 30), BhdRSSKey: strings.Repeat("b", 30)},
		}},
	}
	client := NewClient(cfg, api.NopLogger{}, server.Client())
	client.bhdBaseURL = server.URL + "/bhd"

	result, err := client.Lookup(context.Background(), "BHD", "", api.PreparedMetadata{SourcePath: "/tmp/release"}, "release.mkv", false, true)
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if result.TrackerID != "99" || result.IMDBID != 1234567 || result.TMDBID != 765 || result.Category != "MOVIE" {
		t.Fatalf("unexpected ids: %+v", result)
	}
	if result.Description != "hello" {
		t.Fatalf("unexpected description: %q", result.Description)
	}
	if len(result.Images) != 1 {
		t.Fatalf("expected image extraction, got %d", len(result.Images))
	}
	if result.Images[0].ImgURL != "https://ptpimg.me/example.png" {
		t.Fatalf("unexpected image data: %+v", result.Images[0])
	}
}

func TestLookupPTPAndHDB(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ptp":
			switch {
			case r.URL.Query().Get("torrentid") != "":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ImdbId": "1122334",
					"Torrents": []any{
						map[string]any{"Id": "777", "InfoHash": "abc123"},
					},
				})
			case r.URL.Query().Get("action") == "get_description":
				_, _ = w.Write([]byte("Desc\nhttps://ptpimg.me/abc.png"))
			default:
				http.NotFound(w, r)
			}
		case "/hdb":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": 0,
				"data": []any{
					map[string]any{
						"id":    "321",
						"hash":  "deadbeef",
						"imdb":  map[string]any{"id": "998877"},
						"tvdb":  map[string]any{"id": "5544"},
						"descr": "Text\n[url=https://imgbox.com/abc][img]https://thumbs2.imgbox.com/abc_t.png[/img][/url]",
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"PTP": {ApiUser: "user", ApiKey: "key"},
			"HDB": {Username: "user", Passkey: "pass"},
		}},
	}
	client := NewClient(cfg, api.NopLogger{}, server.Client())
	client.ptpURL = server.URL + "/ptp"
	client.hdbURL = server.URL + "/hdb"

	ptpResult, err := client.Lookup(context.Background(), "PTP", "777", api.PreparedMetadata{}, "release.mkv", false, true)
	if err != nil {
		t.Fatalf("ptp lookup failed: %v", err)
	}
	if ptpResult.IMDBID != 1122334 || ptpResult.TrackerID != "777" || ptpResult.InfoHash != "abc123" {
		t.Fatalf("unexpected ptp result: %+v", ptpResult)
	}
	if ptpResult.Description != "Desc" || len(ptpResult.Images) != 1 {
		t.Fatalf("unexpected ptp description/images: %+v", ptpResult)
	}

	hdbResult, err := client.Lookup(context.Background(), "HDB", "321", api.PreparedMetadata{}, "release.mkv", false, true)
	if err != nil {
		t.Fatalf("hdb lookup failed: %v", err)
	}
	if hdbResult.IMDBID != 998877 || hdbResult.TVDBID != 5544 || hdbResult.InfoHash != "deadbeef" {
		t.Fatalf("unexpected hdb ids: %+v", hdbResult)
	}
	if hdbResult.Description != "Text" || len(hdbResult.Images) != 1 {
		t.Fatalf("unexpected hdb description/images: %+v", hdbResult)
	}
}

func TestLookupUnit3DOnlyIDKeepsImages(t *testing.T) {
	t.Parallel()

	pngBytes := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41,
		0x54, 0x08, 0x99, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
		0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/torrents/"):
			imageURL := server.URL + "/images/shot.png"
			description := "[url=https://example.com/view][img]" + imageURL + "[/img][/url]"
			_ = json.NewEncoder(w).Encode(map[string]any{
				"attributes": map[string]any{
					"tmdb_id":     100,
					"description": description,
				},
			})
		case r.URL.Path == "/images/shot.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}

	httpClient := server.Client()
	transport := httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	httpClient.Transport = rewriteHostTransport{base: baseURL, rt: transport}

	client := NewClient(config.Config{}, api.NopLogger{}, httpClient)

	result, err := client.Lookup(context.Background(), "BLU", "777", api.PreparedMetadata{}, "release.mkv", true, true)
	if err != nil {
		t.Fatalf("unit3d lookup failed: %v", err)
	}
	if result.TMDBID != 100 {
		t.Fatalf("expected tmdb id, got %+v", result)
	}
	if result.Description != "" {
		t.Fatalf("expected onlyID to clear description, got %q", result.Description)
	}
	if len(result.Images) != 1 {
		t.Fatalf("expected keepImages to retain images with onlyID=true, got %d", len(result.Images))
	}
}
