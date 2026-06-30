// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerdata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
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
	resp, err := t.rt.RoundTrip(clone)
	if err != nil {
		return resp, fmt.Errorf("rewrite host round trip: %w", err)
	}
	return resp, nil
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
					"description": "hello\n[url=https://pixhost.to/full/example][img]https://pixhost.to/example.png[/img][/url]",
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
	if result.Images[0].ImgURL != "https://pixhost.to/example.png" {
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
				_, _ = w.Write([]byte("Desc\nhttps://pixhost.to/abc.png"))
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
			"PTP": {PTPAPIUser: "user", PTPAPIKey: "key"},
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

func TestLookupANTSendsAPIKeyHeader(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if got := query.Get("apikey"); got != "" {
			t.Fatal("apikey should not be sent as a query parameter")
		}
		if got := query.Get("t"); got != "search" {
			t.Fatalf("unexpected t query value: got %q want %q", got, "search")
		}
		if got := query.Get("filename"); got != "Example.Release.mkv" {
			t.Fatalf("unexpected filename query value: got %q", got)
		}
		if got := r.Header.Get("X-Api-Key"); got != "token" {
			t.Fatal("unexpected X-API-Key header")
		}
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Fatal("expected User-Agent header")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"item": []map[string]any{
				{"imdb": "tt1234567", "tmdb": 765},
			},
		})
	}))
	defer server.Close()

	client := NewClient(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"ANT": {APIKey: "token"},
		}},
	}, api.NopLogger{}, server.Client())
	client.antURL = server.URL

	result, err := client.Lookup(context.Background(), "ANT", "", api.PreparedMetadata{}, "Example.Release.mkv", false, true)
	if err != nil {
		t.Fatalf("ant lookup failed: %v", err)
	}
	if result.IMDBID != 1234567 || result.TMDBID != 765 {
		t.Fatalf("unexpected ant result: %+v", result)
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/torrents/"):
			imageURL := "http://93.184.216.34/images/shot.png"
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

func TestLookupUnit3DRejectsLinkedPrivateRawURLBeforeFetch(t *testing.T) {
	t.Parallel()

	var privateRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/torrents/"):
			description := "[url=http://127.0.0.1/private.png][img]http://93.184.216.34/thumb.png[/img][/url]"
			_ = json.NewEncoder(w).Encode(map[string]any{
				"attributes": map[string]any{
					"tmdb_id":     100,
					"description": description,
				},
			})
		case r.URL.Path == "/private.png":
			privateRequests.Add(1)
			http.Error(w, "private image should not be fetched", http.StatusInternalServerError)
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
	result, err := client.Lookup(context.Background(), "BLU", "777", api.PreparedMetadata{}, "release.mkv", false, true)
	if err != nil {
		t.Fatalf("unit3d lookup failed: %v", err)
	}
	if len(result.Images) != 0 || len(result.Validated) != 0 {
		t.Fatalf("expected private linked image to be rejected, got images=%+v validated=%+v", result.Images, result.Validated)
	}
	if got := privateRequests.Load(); got != 0 {
		t.Fatalf("expected private raw URL not to be fetched, got %d request(s)", got)
	}
}

func TestLookupUnit3DAllowsPublicLinkedRawURL(t *testing.T) {
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

	var fullRequests atomic.Int32
	var thumbRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/torrents/"):
			description := "[url=http://93.184.216.34/images/full.png][img]http://93.184.216.34/images/thumb.png[/img][/url]"
			_ = json.NewEncoder(w).Encode(map[string]any{
				"attributes": map[string]any{
					"tmdb_id":     100,
					"description": description,
				},
			})
		case r.URL.Path == "/images/full.png":
			fullRequests.Add(1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		case r.URL.Path == "/images/thumb.png":
			thumbRequests.Add(1)
			http.Error(w, "thumbnail should not be validated when linked full-size URL is public", http.StatusInternalServerError)
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
	result, err := client.Lookup(context.Background(), "BLU", "777", api.PreparedMetadata{}, "release.mkv", false, true)
	if err != nil {
		t.Fatalf("unit3d lookup failed: %v", err)
	}
	if len(result.Images) != 1 || len(result.Validated) != 1 {
		t.Fatalf("expected public linked image to validate, got images=%+v validated=%+v", result.Images, result.Validated)
	}
	if got := result.Images[0].RawURL; got != "http://93.184.216.34/images/full.png" {
		t.Fatalf("expected linked full-size raw URL, got %q", got)
	}
	if got := result.Images[0].ImgURL; got != "http://93.184.216.34/images/thumb.png" {
		t.Fatalf("expected thumbnail image URL preserved, got %q", got)
	}
	if got := fullRequests.Load(); got != 1 {
		t.Fatalf("expected one public full-size fetch, got %d", got)
	}
	if got := thumbRequests.Load(); got != 0 {
		t.Fatalf("expected thumbnail not to be fetched, got %d request(s)", got)
	}
}

func TestLookupUnit3DDescriptionFlagsGateDescriptionAndImages(t *testing.T) {
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/torrents/"):
			imageURL := "http://93.184.216.34/images/shot.png"
			description := "[center]Fetched tracker body[/center]\n\n[center][url=https://example.com/view][img]" + imageURL + "[/img][/url][/center]"
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
	ctx := context.Background()

	result, err := client.Lookup(ctx, "BLU", "777", api.PreparedMetadata{}, "release.mkv", false, false)
	if err != nil {
		t.Fatalf("unit3d lookup failed: %v", err)
	}
	if result.Description != "[center]Fetched tracker body[/center]" {
		t.Fatalf("expected cleaned description when onlyID=false, got %q", result.Description)
	}
	if len(result.Images) != 0 {
		t.Fatalf("expected keepImages=false to skip images, got %d", len(result.Images))
	}

	result, err = client.Lookup(ctx, "BLU", "777", api.PreparedMetadata{}, "release.mkv", true, false)
	if err != nil {
		t.Fatalf("unit3d lookup failed: %v", err)
	}
	if result.Description != "" {
		t.Fatalf("expected onlyID=true to clear description, got %q", result.Description)
	}
	if len(result.Images) != 0 {
		t.Fatalf("expected keepImages=false to skip images with onlyID=true, got %d", len(result.Images))
	}

	result, err = client.Lookup(ctx, "BLU", "777", api.PreparedMetadata{}, "release.mkv", false, true)
	if err != nil {
		t.Fatalf("unit3d lookup failed: %v", err)
	}
	if result.Description != "[center]Fetched tracker body[/center]" {
		t.Fatalf("expected cleaned description with keepImages=true, got %q", result.Description)
	}
	if len(result.Images) != 1 {
		t.Fatalf("expected keepImages=true to retain images, got %d", len(result.Images))
	}
}

func TestLookupHDBSkipsUnfilteredSearch(t *testing.T) {
	t.Parallel()

	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requested = true
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"HDB": {Username: "user", Passkey: "pass"},
		}},
	}, api.NopLogger{}, server.Client())
	client.hdbURL = server.URL + "/hdb"

	result, err := client.Lookup(
		context.Background(),
		"HDB",
		"",
		api.PreparedMetadata{SourcePath: `D:\TV\From.S04E01.2160p.WEB.h265-ETHEL.mkv`, FileList: []string{"From.S04E01.2160p.WEB.h265-ETHEL.mkv"}},
		"",
		false,
		true,
	)
	if err != nil {
		t.Fatalf("hdb lookup failed: %v", err)
	}
	if result.HasData() {
		t.Fatalf("expected empty result for unfiltered HDB lookup, got %+v", result)
	}
	if requested {
		t.Fatalf("expected HDB lookup to skip request without id or filename")
	}
}

func TestLookupBHDSkipsUnfilteredSearch(t *testing.T) {
	t.Parallel()

	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requested = true
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	token := strings.Repeat("a", minTokenLength)
	client := NewClient(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BHD": {APIKey: token, BhdRSSKey: token},
		}},
	}, api.NopLogger{}, server.Client())
	client.bhdBaseURL = server.URL + "/api/torrents"

	result, err := client.Lookup(
		context.Background(),
		"BHD",
		"",
		api.PreparedMetadata{SourcePath: `D:\TV\From.S04E01.2160p.WEB.h265-ETHEL.mkv`, FileList: []string{"From.S04E01.2160p.WEB.h265-ETHEL.mkv"}},
		"",
		false,
		true,
	)
	if err != nil {
		t.Fatalf("bhd lookup failed: %v", err)
	}
	if result.HasData() {
		t.Fatalf("expected empty result for unfiltered BHD lookup, got %+v", result)
	}
	if requested {
		t.Fatalf("expected BHD lookup to skip request without id or filename")
	}
}
