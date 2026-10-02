// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBHDDataLookupPolicyPreservesCooldown(t *testing.T) {
	policy := New().DataLookupPolicy()
	if policy == nil || policy.Cooldown != 15*time.Second || !policy.LegacyImageAssetsNeedProvenance {
		t.Fatalf("BHD lookup policy lost cooldown or image provenance: %#v", policy)
	}
}

type bhdRecordingLogger struct {
	api.NopLogger
	debug []string
}

func (l *bhdRecordingLogger) Debugf(format string, args ...any) {
	l.debug = append(l.debug, fmt.Sprintf(format, args...))
}

func bhdImageTestClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	transport := server.Client().Transport
	return &http.Client{Transport: bhdRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == base.Host {
			return transport.RoundTrip(req)
		}
		cloned := req.Clone(req.Context())
		cloned.URL.Scheme = base.Scheme
		cloned.URL.Host = base.Host
		return transport.RoundTrip(cloned)
	})}
}

func TestDataLookup(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken.png" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path == "/full.png" {
			w.Header().Set("Content-Type", "image/png")
			if err := png.Encode(w, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
				t.Errorf("encode test image: %v", err)
			}
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/bhd/") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status_code": 1,
			"success":     true,
			"results": []any{map[string]any{
				"id":      "99",
				"imdb_id": "tt1234567",
				"tmdb_id": "movie/765",
				"description": "hello\n[url=https://93.184.216.34/full.png][img]https://93.184.216.34/full.png[/img][/url]" +
					"\n[url=https://93.184.216.34/broken.png][img]https://93.184.216.34/broken.png[/img][/url]",
			}},
		})
	}))
	defer server.Close()

	token := strings.Repeat("a", minDataTokenLength)
	logger := &bhdRecordingLogger{}
	lookup, ok := New().NewDataLookup(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"BHD": {APIKey: token, BhdRSSKey: token}}}}, bhdImageTestClient(t, server), logger).(*dataLookup)
	if !ok {
		t.Fatal("expected BHD data lookup")
	}
	lookup.baseURL = server.URL + "/bhd"
	result, err := lookup.Lookup(context.Background(), trackers.DataLookupRequest{
		Meta:       api.UploadSubject{SourcePath: "/tmp/release"},
		SearchName: "release.mkv",
		KeepImages: true,
	})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if result.TrackerID != "99" || result.TorrentURL != "https://beyond-hd.me/details/99" || result.IMDBID != 1234567 ||
		result.TMDBID != 765 || result.Category != "MOVIE" || result.Description != "hello" ||
		len(result.Images) != 2 || result.Images[0].RawURL != "https://93.184.216.34/full.png" || result.Images[1].RawURL != "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	debug := strings.Join(logger.debug, "\n")
	if !strings.Contains(debug, "image validation rejected tracker=BHD index=2 reason=http_status_403") || strings.Contains(debug, "broken.png") {
		t.Fatalf("missing URL-free BHD image failure reason: %q", debug)
	}
	idsOnly, err := lookup.Lookup(t.Context(), trackers.DataLookupRequest{
		Meta:       api.UploadSubject{SourcePath: "/tmp/release"},
		SearchName: "release.mkv",
		OnlyID:     true,
	})
	if err != nil || idsOnly.TrackerID != "99" || idsOnly.TorrentURL != "https://beyond-hd.me/details/99" ||
		len(idsOnly.Images) != 0 || idsOnly.Description != "" {
		t.Fatalf("ID-only lookup lost discovered tracker data: result=%+v err=%v", idsOnly, err)
	}
}

func TestDataLookupFetchesSeparateDescriptionAndImages(t *testing.T) {
	t.Parallel()
	actions := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/full.png" {
			w.Header().Set("Content-Type", "image/png")
			if err := png.Encode(w, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
				t.Errorf("encode test image: %v", err)
			}
			return
		}
		var payload struct {
			Action    string `json:"action"`
			TorrentID string `json:"torrent_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode BHD request: %v", err)
			return
		}
		actions <- payload.Action
		if payload.TorrentID != "99" {
			t.Errorf("torrent ID = %q", payload.TorrentID)
		}
		w.Header().Set("Content-Type", "application/json")
		switch payload.Action {
		case "details":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status_code": 1,
				"success":     true,
				"result":      map[string]any{"id": "99", "description": "1"},
			})
		case "description":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status_code": 1,
				"success":     true,
				"result":      "BHD notes\n[img]https://93.184.216.34/full.png[/img]",
			})
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	token := strings.Repeat("a", minDataTokenLength)
	lookup, ok := New().NewDataLookup(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"BHD": {APIKey: token, BhdRSSKey: token},
	}}}, bhdImageTestClient(t, server), api.NopLogger{}).(*dataLookup)
	if !ok {
		t.Fatal("expected BHD data lookup")
	}
	lookup.baseURL = server.URL + "/bhd"
	result, err := lookup.Lookup(t.Context(), trackers.DataLookupRequest{TrackerID: "99", KeepImages: true})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if result.TrackerID != "99" || result.Description != "BHD notes" || len(result.Images) != 1 ||
		result.Images[0].RawURL != "https://93.184.216.34/full.png" {
		t.Fatalf("separate BHD description/images not imported: %#v", result)
	}
	if first, second := <-actions, <-actions; first != "details" || second != "description" {
		t.Fatalf("BHD actions = %q, %q", first, second)
	}
}

func TestDataLookupSkipsUnfilteredSearch(t *testing.T) {
	t.Parallel()
	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requested = true
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()
	token := strings.Repeat("a", minDataTokenLength)
	lookup, ok := New().NewDataLookup(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"BHD": {APIKey: token, BhdRSSKey: token}}}}, server.Client(), nil).(*dataLookup)
	if !ok {
		t.Fatal("expected BHD data lookup")
	}
	lookup.baseURL = server.URL
	result, err := lookup.Lookup(context.Background(), trackers.DataLookupRequest{Meta: api.UploadSubject{SourcePath: `D:\TV\Example.Show.S04E01.2160p.WEB.h265-GRP.mkv`, FileList: []string{"Example.Show.S04E01.2160p.WEB.h265-GRP.mkv"}}, KeepImages: true})
	if err != nil || result.HasData() || requested {
		t.Fatalf("err=%v data=%t requested=%t", err, result.HasData(), requested)
	}
}

func TestDataLookupOmitsUnverifiedTorrentPages(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		requestedID string
		results     []any
		wantID      string
	}{
		{
			name:    "multiple search results",
			results: []any{map[string]any{"id": "41"}, map[string]any{"id": "42"}},
			wantID:  "41",
		},
		{
			name:        "mismatched ID result",
			requestedID: "42",
			results:     []any{map[string]any{"id": "41"}},
			wantID:      "42",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status_code": 1,
					"success":     true,
					"results":     test.results,
				})
			}))
			defer server.Close()
			token := strings.Repeat("a", minDataTokenLength)
			lookup := &dataLookup{
				cfg:     config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"BHD": {APIKey: token, BhdRSSKey: token}}}},
				http:    server.Client(),
				baseURL: server.URL,
			}
			result, err := lookup.Lookup(t.Context(), trackers.DataLookupRequest{
				TrackerID:  test.requestedID,
				Meta:       api.UploadSubject{FileList: []string{"Example.Release.mkv"}},
				SearchName: "Example.Release.mkv",
				OnlyID:     true,
			})
			if err != nil || result.TorrentURL != "" || result.TrackerID != test.wantID {
				t.Fatalf("unverified torrent result = %+v, error = %v", result, err)
			}
		})
	}
}
