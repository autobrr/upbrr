// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl_test

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
	"github.com/autobrr/upbrr/internal/trackers"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	ptpimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/ptp"
	"github.com/autobrr/upbrr/pkg/api"
)

func unit3DTestConfig() config.Config {
	return config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"BLU": {AnnounceURL: "https://blutopia.cc/announce"},
	}}}
}

func newUnit3DDataClient(t *testing.T, httpClient *http.Client) *trackerdata.Client {
	t.Helper()
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		t.Fatalf("create tracker registry: %v", err)
	}
	return trackerdata.NewClientWithRegistry(unit3DTestConfig(), api.NopLogger{}, httpClient, registry)
}

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

func TestLookupPTP(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
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
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"PTP": {PTPAPIUser: "user", PTPAPIKey: "key"},
		}},
	}
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	httpClient := server.Client()
	httpClient.Transport = rewriteHostTransport{base: baseURL, rt: httpClient.Transport}
	registry := trackers.NewRegistry()
	if err := registry.Register(ptpimpl.New()); err != nil {
		t.Fatalf("register PTP: %v", err)
	}
	client := trackerdata.NewClientWithRegistry(cfg, api.NopLogger{}, httpClient, registry)

	ptpResult, err := client.Lookup(context.Background(), "PTP", "777", api.UploadSubject{}, "release.mkv", false, true)
	if err != nil {
		t.Fatalf("ptp lookup failed: %v", err)
	}
	if ptpResult.IMDBID != 1122334 || ptpResult.TrackerID != "777" || ptpResult.InfoHash != "abc123" {
		t.Fatalf("unexpected ptp result: %+v", ptpResult)
	}
	if ptpResult.Description != "Desc" || len(ptpResult.Images) != 1 {
		t.Fatalf("unexpected ptp description/images: %+v", ptpResult)
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

	client := newUnit3DDataClient(t, httpClient)

	result, err := client.Lookup(context.Background(), "BLU", "777", api.UploadSubject{}, "release.mkv", true, true)
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

	client := newUnit3DDataClient(t, httpClient)
	result, err := client.Lookup(context.Background(), "BLU", "777", api.UploadSubject{}, "release.mkv", false, true)
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

	client := newUnit3DDataClient(t, httpClient)
	result, err := client.Lookup(context.Background(), "BLU", "777", api.UploadSubject{}, "release.mkv", false, true)
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

	client := newUnit3DDataClient(t, httpClient)
	ctx := context.Background()

	result, err := client.Lookup(ctx, "BLU", "777", api.UploadSubject{}, "release.mkv", false, false)
	if err != nil {
		t.Fatalf("unit3d lookup failed: %v", err)
	}
	if result.Description != "[center]Fetched tracker body[/center]" {
		t.Fatalf("expected cleaned description when onlyID=false, got %q", result.Description)
	}
	if len(result.Images) != 0 {
		t.Fatalf("expected keepImages=false to skip images, got %d", len(result.Images))
	}

	result, err = client.Lookup(ctx, "BLU", "777", api.UploadSubject{}, "release.mkv", true, false)
	if err != nil {
		t.Fatalf("unit3d lookup failed: %v", err)
	}
	if result.Description != "" {
		t.Fatalf("expected onlyID=true to clear description, got %q", result.Description)
	}
	if len(result.Images) != 0 {
		t.Fatalf("expected keepImages=false to skip images with onlyID=true, got %d", len(result.Images))
	}

	result, err = client.Lookup(ctx, "BLU", "777", api.UploadSubject{}, "release.mkv", false, true)
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
