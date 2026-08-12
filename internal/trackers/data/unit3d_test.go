// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package data

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
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

type testDefinition struct{ name string }

func (d testDefinition) Name() string { return d.name }

func (testDefinition) UploadContentMode() trackers.UploadContentMode {
	return trackers.UploadContentModeDescription
}

func (testDefinition) Prepare(context.Context, trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.TrackerPlan{}, nil
}

func testUnit3DRegistry(t *testing.T, name string, baseURL string) *trackers.Registry {
	t.Helper()
	registry := trackers.NewRegistry()
	if err := registry.RegisterDescriptor(trackers.Descriptor{
		Name:       name,
		Family:     trackers.FamilyUnit3D,
		BaseURL:    baseURL,
		Definition: testDefinition{name: name},
	}); err != nil {
		t.Fatalf("register test tracker: %v", err)
	}
	return registry
}

func TestSetUnit3DAPIHeadersUsesBearerAuthorization(t *testing.T) {
	t.Parallel()

	SetUnit3DAPIHeaders(nil, "ignored")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "https://tracker.example/api/torrents/upload", nil)
	SetUnit3DAPIHeaders(req, " secret ")
	if req.Header.Get("Authorization") != "Bearer secret" {
		t.Fatal("expected Unit3D Bearer authorization")
	}
	if req.Header.Get("User-Agent") != "upbrr" {
		t.Fatal("expected Unit3D upbrr user agent")
	}
	if req.Header.Get("Accept") != "application/json" {
		t.Fatal("expected Unit3D JSON accept header")
	}
	if req.URL.Query().Has("api_token") {
		t.Fatal("Unit3D API token must not be placed in the query")
	}
}

func TestUnit3DMappings(t *testing.T) {
	t.Parallel()

	if got := CategoryID("movie"); got != "1" {
		t.Fatalf("category id mismatch: %q", got)
	}
	if got := TypeID("webdl"); got != "4" {
		t.Fatalf("type id mismatch: %q", got)
	}
	if got := ResolutionID("2160p"); got != "2" {
		t.Fatalf("resolution id mismatch: %q", got)
	}
	if got := TypeID("web-dl"); got != "4" {
		t.Fatalf("type id alias mismatch: %q", got)
	}
	if got := ResolutionID("1080P"); got != "3" {
		t.Fatalf("resolution id alias mismatch: %q", got)
	}
}

func TestUnit3DReverseMappings(t *testing.T) {
	t.Parallel()

	if got := CategoryName("1"); got != "MOVIE" {
		t.Fatalf("category name mismatch: %q", got)
	}
	if got := TypeName("4"); got != "WEBDL" {
		t.Fatalf("type name mismatch: %q", got)
	}
	resolutions := ResolutionNames("3")
	if len(resolutions) != 2 || resolutions[0] != "1080P" || resolutions[1] != "1440P" {
		t.Fatalf("resolution names mismatch: %#v", resolutions)
	}
	if got := ResolutionName("99"); got != "" {
		t.Fatalf("expected unknown resolution id to return empty, got %q", got)
	}
}

func TestExtractAttributesFromDataAndTopLevel(t *testing.T) {
	t.Parallel()

	resp := unit3dResponse{
		Data: json.RawMessage(`[{"attributes":{"tmdb_id":12,"imdb_id":34,"tvdb_id":56,"mal_id":78,"description":"desc"}}]`),
	}
	attrs := resp.extractAttributes(false)
	if attrs == nil || attrs.tmdbID != 12 || attrs.imdbID != 34 || attrs.tvdbID != 56 || attrs.malID != 78 {
		t.Fatalf("unexpected attrs from data: %+v", attrs)
	}

	top := unit3dResponse{
		Attributes: json.RawMessage(`{"tmdb_id":1,"description":"top"}`),
	}
	topAttrs := top.extractAttributes(true)
	if topAttrs == nil || topAttrs.tmdbID != 1 {
		t.Fatalf("unexpected attrs from top-level: %+v", topAttrs)
	}
}

func TestExtractAttributesHandles404AndMissing(t *testing.T) {
	t.Parallel()

	resp := unit3dResponse{Data: json.RawMessage(`"404"`)}
	if attrs := resp.extractAttributes(false); attrs != nil {
		t.Fatalf("expected nil attrs for 404 payload, got %+v", attrs)
	}

	empty := unit3dResponse{}
	if attrs := empty.extractAttributes(true); attrs != nil {
		t.Fatalf("expected nil attrs for empty payload, got %+v", attrs)
	}
}

func TestParseNumberToInt64(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value   json.Number
		want    int64
		wantErr bool
	}{
		{value: json.Number("12"), want: 12},
		{value: json.Number("12.9"), want: 12},
		{value: json.Number(""), wantErr: true},
	}

	for _, tc := range cases {
		got, err := parseNumberToInt64(tc.value)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("expected error for %q", tc.value.String())
			}
			continue
		}
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", tc.value.String(), err)
		}
		if got != tc.want {
			t.Fatalf("value mismatch for %q: got %d want %d", tc.value.String(), got, tc.want)
		}
	}
}

func TestSearchTorrentsCBRIncludesPendingAndFiltersTMDB(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("bearer authorization mismatch")
			return
		}
		if r.Header.Get("User-Agent") != "upbrr" {
			t.Error("user agent mismatch")
			return
		}
		if r.URL.Query().Has("api_token") {
			t.Error("API token must not be placed in the query")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/torrents/filter":
			_, _ = w.Write([]byte(`{"data":[{"id":101,"attributes":{"name":"Existing.Release","size":123,"files":[{"name":"existing.mkv"}],"details_link":"https://example.test/torrents/101","download_link":"https://example.test/download/101","type":"WEBDL","resolution":"1080p","internal":true}}],"meta":{"current_page":1,"last_page":1,"total":1}}`))
		case "/api/torrents/pending":
			_, _ = w.Write([]byte(`{"data":[{"id":202,"tmdb_id":42,"name":"Pending.Release","size":456,"files":[{"name":"pending.mkv"}],"download_link":"https://example.test/download/202","type":"REMUX","resolution":"2160p"},{"id":203,"tmdb_id":99,"name":"Wrong.Movie","size":789}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	baseURL := "https://cbr.example"
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	client := NewClientWithRegistry(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"CBR": {
					APIKey: "secret",
				},
			},
		},
	}, api.NopLogger{}, &http.Client{Transport: rewriteHostTransport{base: base, rt: server.Client().Transport}}, testUnit3DRegistry(t, "CBR", baseURL))

	params := url.Values{}
	params.Set("tmdbId", "42")
	entries, warning, err := client.SearchTorrents(context.Background(), "CBR", params, false)
	if err != nil {
		t.Fatalf("search torrents: %v", err)
	}
	if !strings.Contains(warning, "omitted 1 row with conflicting TMDB IDs") {
		t.Fatalf("wrong-work warning = %q", warning)
	}
	if len(entries) != 2 {
		t.Fatalf("entry count mismatch: got %d entries %#v", len(entries), entries)
	}
	if len(paths) != 2 {
		t.Fatalf("request count mismatch: got %d paths %#v", len(paths), paths)
	}
	if paths[0] != "/api/torrents/filter" || paths[1] != "/api/torrents/pending" {
		t.Fatalf("unexpected request paths: %#v", paths)
	}
	if entries[0].Name != "Existing.Release" || entries[0].Link != "https://example.test/torrents/101" {
		t.Fatalf("unexpected filter entry: %#v", entries[0])
	}
	if entries[1].Name != "Pending.Release" || entries[1].Link != baseURL+"/torrents/pending" {
		t.Fatalf("unexpected pending entry: %#v", entries[1])
	}
	if entries[1].ID != "202" || entries[1].SizeBytes != 456 || entries[1].Files[0] != "pending.mkv" {
		t.Fatalf("unexpected pending fields: %#v", entries[1])
	}
}

func TestSearchTorrentsWithEvidenceConsumesAdvertisedFinalPage(t *testing.T) {
	t.Parallel()

	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "1":
			_, _ = w.Write([]byte(
				`{"data":[{"id":1,"attributes":{"name":"Example.Release.2026.1080p.WEB-DL-GRP"}}],"meta":{"current_page":1,"last_page":2,"total":2}}`,
			))
		case "2":
			_, _ = w.Write([]byte(
				`{"data":[{"id":2,"attributes":{"name":"Example.Release.2026.2160p.WEB-DL-GRP"}}],"meta":{"current_page":2,"last_page":2,"total":2}}`,
			))
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	client := newUnit3DSearchTestClient(t, server)
	params := url.Values{"perPage": []string{"1"}}
	result, err := client.SearchTorrentsWithEvidenceBound(context.Background(), "AITHER", params, false, 10)
	if err != nil {
		t.Fatalf("search torrents: %v", err)
	}
	if !result.Complete || result.Pages != 2 || len(result.Entries) != 2 ||
		result.Entries[1].Name != "Example.Release.2026.2160p.WEB-DL-GRP" {
		t.Fatalf("paginated result = %#v", result)
	}
	if len(pages) != 2 || pages[0] != "1" || pages[1] != "2" {
		t.Fatalf("requested pages = %#v", pages)
	}
}

func TestSearchTorrentsWithEvidenceFailsClosedAtPolicyBound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(
			w,
			`{"data":[{"id":%s,"attributes":{"name":"Example.Release.2026.Page.%s-GRP"}}],"meta":{"current_page":%s,"last_page":3,"total":3}}`,
			page,
			page,
			page,
		)
	}))
	t.Cleanup(server.Close)

	client := newUnit3DSearchTestClient(t, server)
	result, err := client.SearchTorrentsWithEvidenceBound(
		context.Background(),
		"AITHER",
		url.Values{"perPage": []string{"1"}},
		false,
		2,
	)
	if err != nil {
		t.Fatalf("search torrents: %v", err)
	}
	if result.Complete || result.Pages != 2 || len(result.Entries) != 2 ||
		result.Warning != "Unit3D search reached pagination safety bound" {
		t.Fatalf("bounded result = %#v", result)
	}
}

func TestSearchTorrentsWithEvidenceTreatsCapacityWithoutMetaAsIncomplete(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"data":[{"id":1,"attributes":{"name":"Example.Release.2026.1080p.WEB-DL-GRP"}}]}`,
		))
	}))
	t.Cleanup(server.Close)

	client := newUnit3DSearchTestClient(t, server)
	result, err := client.SearchTorrentsWithEvidenceBound(
		context.Background(),
		"AITHER",
		url.Values{"perPage": []string{"1"}},
		false,
		10,
	)
	if err != nil {
		t.Fatalf("search torrents: %v", err)
	}
	if result.Complete || result.Pages != 1 ||
		result.Warning != "Unit3D search response omitted a valid result total" {
		t.Fatalf("metadata-free result = %#v", result)
	}
}

func TestSearchTorrentsWithEvidenceValidatesAdvertisedTotal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		responses []string
		warning   string
	}{
		{
			name: "final count mismatch",
			responses: []string{
				`{"data":[{"id":1,"attributes":{"name":"Example.Release.2026.1080p.WEB-DL-GRP"}}],"meta":{"current_page":1,"last_page":1,"total":2}}`,
			},
			warning: "Unit3D search result count did not match its advertised total",
		},
		{
			name: "total changes",
			responses: []string{
				`{"data":[{"id":1,"attributes":{"name":"Example.Release.2026.1080p.WEB-DL-GRP"}}],"meta":{"current_page":1,"last_page":2,"total":2}}`,
				`{"data":[{"id":2,"attributes":{"name":"Example.Release.2026.2160p.WEB-DL-GRP"}}],"meta":{"current_page":2,"last_page":2,"total":3}}`,
			},
			warning: "Unit3D search returned inconsistent pagination metadata",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if calls >= len(tc.responses) {
					http.Error(w, "unexpected page", http.StatusBadRequest)
					return
				}
				_, _ = w.Write([]byte(tc.responses[calls]))
				calls++
			}))
			t.Cleanup(server.Close)

			client := newUnit3DSearchTestClient(t, server)
			result, err := client.SearchTorrentsWithEvidenceBound(
				context.Background(),
				"AITHER",
				url.Values{"perPage": []string{"1"}},
				false,
				10,
			)
			if err != nil {
				t.Fatalf("search torrents: %v", err)
			}
			if result.Complete || result.Pages != len(tc.responses) || result.Warning != tc.warning {
				t.Fatalf("validated total result = %#v", result)
			}
		})
	}
}

func newUnit3DSearchTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	const tracker = "AITHER"
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	baseURL := "https://" + strings.ToLower(tracker) + ".example"
	return NewClientWithRegistry(
		config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			tracker: {APIKey: "secret"},
		}}},
		api.NopLogger{},
		&http.Client{Transport: rewriteHostTransport{base: base, rt: server.Client().Transport}},
		testUnit3DRegistry(t, tracker, baseURL),
	)
}

func TestDedupeUnit3DEntriesKeepsRicherEvidence(t *testing.T) {
	t.Parallel()

	entries := dedupeUnit3DEntries([]api.DupeEntry{
		{
			ID:          "42",
			Name:        "Example.Release",
			Description: strings.Repeat("long but sparse ", 20),
		},
		{
			ID:     "42",
			Name:   "Example.Release",
			Type:   "WEBDL",
			Res:    "1080p",
			Source: "WEB",
			Files:  []string{"example.mkv"},
		},
		{ID: "42", Name: "Different.Release"},
	})
	if len(entries) != 2 || entries[0].Type != "WEBDL" || entries[1].Name != "Different.Release" {
		t.Fatalf("deduped entries = %#v", entries)
	}
}

func TestTorrentInfoUsesBearerAuthorization(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("bearer authorization mismatch")
			return
		}
		if r.Header.Get("User-Agent") != "upbrr" {
			t.Error("user agent mismatch")
			return
		}
		if r.URL.Query().Has("api_token") {
			t.Error("API token must not be placed in the query")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"attributes":{"tmdb_id":123,"category":"MOVIE"}}]}`))
	}))
	t.Cleanup(server.Close)

	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal("parse test server URL")
	}
	client := NewClientWithRegistry(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"AITHER": {APIKey: "secret", AnnounceURL: "https://aither.cc/announce"},
		}},
	}, api.NopLogger{}, &http.Client{Transport: rewriteHostTransport{base: base, rt: server.Client().Transport}}, testUnit3DRegistry(t, "AITHER", "https://aither.cc"))

	result, err := client.TorrentInfo(context.Background(), "AITHER", "123", "", true, false)
	if err != nil {
		t.Fatalf("torrent info: %v", err)
	}
	if result.TMDBID != 123 || result.Category != "MOVIE" {
		t.Fatalf("unexpected Unit3D lookup result: %#v", result)
	}
}
