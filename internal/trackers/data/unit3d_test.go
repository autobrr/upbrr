// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package data

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

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type rewriteHostTransport struct {
	base *url.URL
	rt   http.RoundTripper
}

type unit3DSearchRecordingLogger struct {
	api.NopLogger
	trace []string
}

type unit3DImageRecordingLogger struct {
	api.NopLogger
	debug []string
}

func (l *unit3DImageRecordingLogger) Debugf(format string, args ...any) {
	l.debug = append(l.debug, fmt.Sprintf(format, args...))
}

func TestValidateImagesLogsSafeRejectionReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	logger := &unit3DImageRecordingLogger{}
	images := []bbcode.Image{{RawURL: "http://93.184.216.34/private-image.png"}}
	validated := validateImages(t.Context(), &http.Client{Transport: rewriteHostTransport{base: base, rt: server.Client().Transport}}, images, "AITHER", logger)
	if len(validated) != 0 {
		t.Fatalf("expected rejected image, got %#v", validated)
	}
	if len(logger.debug) != 1 || !strings.Contains(logger.debug[0], "AITHER") ||
		!strings.Contains(logger.debug[0], "index=1 reason=http_status_403") {
		t.Fatalf("expected status reason in debug log, got %v", logger.debug)
	}
	if strings.Contains(logger.debug[0], "private-image") {
		t.Fatalf("image URL leaked into debug log: %q", logger.debug[0])
	}
}

func TestFetchImgBBFullImageURLUsesPageMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><meta content="https://i.ibb.co/example/full.png" property="og:image"></head></html>`))
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	client := Unit3DImageHTTPClient(&http.Client{Transport: rewriteHostTransport{base: base, rt: server.Client().Transport}})
	fullURL, reason := fetchImgBBFullImageURL(t.Context(), client, "https://93.184.216.34/shot")
	if reason != "" || fullURL != "https://i.ibb.co/example/full.png" {
		t.Fatalf("full image = %q, reason = %q", fullURL, reason)
	}
}

func TestImgBBFullImageURLRejectsForeignMetadata(t *testing.T) {
	for _, page := range []string{
		`<meta property="og:image" content="https://i.ibb.co.evil.example/full.png">`,
		`<meta property="og:image" content="http://i.ibb.co/full.png">`,
		`<meta property="og:image" content="https://i.ibb.co:8443/full.png">`,
	} {
		if fullURL := imgBBFullImageURL([]byte(page), "https://ibb.co/example"); fullURL != "" {
			t.Fatalf("unexpected full image URL %q", fullURL)
		}
	}
	if !isImgBBPageURL("https://ibb.co/example") || !isImgBBPageURL("https://www.ibb.co/example") {
		t.Fatal("expected ImgBB pages to be recognized")
	}
	if isImgBBPageURL("https://ibb.co.evil.example/example") || isImgBBPageURL("http://ibb.co/example") {
		t.Fatal("foreign or insecure ImgBB pages must not be fetched")
	}
}

func TestImgBBFullImageURLUsesImageSourceFallback(t *testing.T) {
	page := `<meta property="og:image" content="https://i.ibb.co.evil.example/wrong.png"><link rel="image_src" href="//i.ibb.co/example/full.png">`
	if got := imgBBFullImageURL([]byte(page), "https://ibb.co/example"); got != "https://i.ibb.co/example/full.png" {
		t.Fatalf("ImgBB image source = %q", got)
	}
}

func TestResolveImgBBImagesSkipsThumbnailWhenPageLookupFails(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	logger := &unit3DImageRecordingLogger{}
	images := []bbcode.Image{
		{RawURL: "https://93.184.216.34/first.png", WebURL: "https://93.184.216.34/first.png"},
		{RawURL: "https://i.ibb.co/example/thumb.png", WebURL: "https://ibb.co/example"},
		{RawURL: "https://93.184.216.34/third.png", WebURL: "https://93.184.216.34/third.png"},
	}
	resolved := resolveImgBBImages(ctx, nil, "AITHER", logger, images)
	if len(resolved) != 3 || resolved[1] != (bbcode.Image{}) || resolved[2].RawURL != "https://93.184.216.34/third.png" {
		t.Fatalf("failed ImgBB lookup shifted later screenshot or retained thumbnail: %#v", resolved)
	}
	if len(logger.debug) != 1 || !strings.Contains(logger.debug[0], "decision=skip_thumbnail") ||
		strings.Contains(logger.debug[0], "thumb.png") {
		t.Fatalf("expected safe thumbnail rejection reason, got %v", logger.debug)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		if err := png.Encode(w, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
			t.Errorf("encode image: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	validated := validateImages(t.Context(), &http.Client{Transport: rewriteHostTransport{base: base, rt: server.Client().Transport}},
		resolved, "AITHER", logger)
	if len(validated) != 3 || validated[0].RawURL != "https://93.184.216.34/first.png" ||
		validated[1] != (bbcode.Image{}) || validated[2].RawURL != "https://93.184.216.34/third.png" {
		t.Fatalf("validation shifted images after failed ImgBB lookup: %#v", validated)
	}
}

func TestResolveImgBBImagesKeepsDirectImageWhenPageLookupFails(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	logger := &unit3DImageRecordingLogger{}
	image := bbcode.Image{
		RawURL: "https://i.ibb.co/example/full.png",
		WebURL: "https://ibb.co/example",
	}
	resolved := resolveImgBBImages(ctx, nil, "BLU", logger, []bbcode.Image{image})
	if len(resolved) != 1 || resolved[0] != image {
		t.Fatalf("direct ImgBB image was lost when page lookup failed: %#v", resolved)
	}
	if len(logger.debug) != 1 || !strings.Contains(logger.debug[0], "decision=validate_direct_image") ||
		strings.Contains(logger.debug[0], "full.png") {
		t.Fatalf("expected safe direct-image fallback reason, got %v", logger.debug)
	}
}

func TestResolveImgBBImagesFindsViewerInsideWsrvSource(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	logger := &unit3DImageRecordingLogger{}
	image := bbcode.Image{
		ImgURL: "https://wsrv.nl/?url=https%3A%2F%2Fibb.co%2FExample",
		RawURL: "https://wsrv.nl/?url=https%3A%2F%2Fibb.co%2FExample",
		WebURL: "https://wsrv.nl/?url=https%3A%2F%2Fibb.co%2FExample",
	}
	resolved := resolveImgBBImages(ctx, nil, "AITHER", logger, []bbcode.Image{image})
	if len(resolved) != 1 || resolved[0] != (bbcode.Image{}) {
		t.Fatalf("ImgBB viewer in proxy was not resolved: %#v", resolved)
	}
	if len(logger.debug) != 1 || !strings.Contains(logger.debug[0], "decision=skip_thumbnail") {
		t.Fatalf("missing ImgBB lookup decision: %v", logger.debug)
	}
}

func (l *unit3DSearchRecordingLogger) Tracef(format string, args ...any) {
	l.trace = append(l.trace, fmt.Sprintf(format, args...))
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
			_, _ = w.Write([]byte(`{"data":[{"id":101,"attributes":{"name":"Existing.Release","size":123,"files":[{"name":"existing.mkv"}],"details_link":"https://example.test/torrents/101","download_link":"https://example.test/download/101","type":"WEBDL","resolution":"1080p","internal":true}}],"links":{"next":null}}`))
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
	logger := &unit3DSearchRecordingLogger{}
	client := NewClientWithRegistry(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"CBR": {
					APIKey: "secret",
				},
			},
		},
	}, logger, &http.Client{Transport: rewriteHostTransport{base: base, rt: server.Client().Transport}}, testUnit3DRegistry(t, "CBR", baseURL))

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
	logs := strings.Join(logger.trace, "\n")
	for _, endpoint := range []string{"/api/torrents/filter", "/api/torrents/pending"} {
		if !strings.Contains(logs, "dupechecking: request tracker=CBR method=GET endpoint="+endpoint) {
			t.Fatalf("request TRACE missing endpoint %q", endpoint)
		}
	}
	if !strings.Contains(logs, `"tmdbId":["42"]`) || strings.Contains(logs, "secret") || strings.Contains(logs, baseURL) {
		t.Fatal("request TRACE missing the work ID or exposed authentication/URL")
	}
}

func TestSearchTorrentsWithEvidenceFollowsLinksAndIgnoresMeta(t *testing.T) {
	t.Parallel()

	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Query().Encode())
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(
				`{"data":[{"id":1,"attributes":{"name":"Example.Release.2026.1080p.WEB-DL-GRP"}}],"links":{"next":"https://aither.example/api/torrents/filter?cursor=second"},"meta":{"current_page":99,"last_page":1,"total":999}}`,
			))
		case "second":
			_, _ = w.Write([]byte(
				`{"data":[{"id":2,"attributes":{"name":"Example.Release.2026.2160p.WEB-DL-GRP"}}],"links":{"next":null},"meta":{"current_page":0,"last_page":0,"total":-1}}`,
			))
		default:
			http.Error(w, "unexpected cursor", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	logger := &unit3DSearchRecordingLogger{}
	client := newUnit3DSearchTestClient(t, server)
	client.logger = logger
	result, err := client.SearchTorrentsWithEvidenceBound(
		t.Context(),
		"AITHER",
		url.Values{"perPage": []string{"1"}},
		false,
		10,
	)
	if err != nil {
		t.Fatalf("search torrents: %v", err)
	}
	if !result.Complete || result.Pages != 2 || len(result.Entries) != 2 ||
		result.Entries[1].Name != "Example.Release.2026.2160p.WEB-DL-GRP" {
		t.Fatalf("link-paginated result = %#v", result)
	}
	if strings.Join(requests, ",") != "page=1&perPage=1,cursor=second" {
		t.Fatalf("requested queries = %#v", requests)
	}
	logs := strings.Join(logger.trace, "\n")
	for _, decision := range []string{
		`"continuation":false`,
		`"continuation":true`,
		`"request_page":1`,
		`"request_page":2`,
		"state=active decision=link_mode count=1",
		"state=active decision=continue count=1",
		"state=completed decision=terminal count=2",
	} {
		if !strings.Contains(logs, decision) {
			t.Fatalf("pagination logs missing %q: %q", decision, logs)
		}
	}
	if strings.Contains(logs, "cursor") || strings.Contains(logs, "second") {
		t.Fatalf("pagination logs exposed continuation data: %q", logs)
	}
}

func TestSearchTorrentsWithEvidenceRejectsRepeatedLink(t *testing.T) {
	t.Parallel()

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"data":[{"id":1,"attributes":{"name":"Example.Release.2026.1080p.WEB-DL-GRP"}}],"links":{"next":"https://aither.example/api/torrents/filter?perPage=1&page=1"},"meta":{"current_page":1,"per_page":1}}`,
		))
	}))
	t.Cleanup(server.Close)

	logger := &unit3DSearchRecordingLogger{}
	client := newUnit3DSearchTestClient(t, server)
	client.logger = logger
	result, err := client.SearchTorrentsWithEvidenceBound(
		t.Context(),
		"AITHER",
		url.Values{"perPage": []string{"1"}},
		false,
		10,
	)
	if err != nil {
		t.Fatalf("search torrents: %v", err)
	}
	if result.Complete || result.Pages != 1 || calls != 1 ||
		result.Warning != "Unit3D search returned inconsistent pagination metadata" {
		t.Fatalf("repeated-link result = %#v calls=%d", result, calls)
	}
	if logs := strings.Join(logger.trace, "\n"); !strings.Contains(logs, "state=rejected decision=repeated_continuation count=1") {
		t.Fatalf("pagination rejection log = %q", logs)
	}
}

func TestUnit3DNextSearchURLRequiresSameOrigin(t *testing.T) {
	t.Parallel()

	const endpoint = "https://aither.example/api/torrents/filter"
	for _, tc := range []struct {
		name         string
		raw          string
		wantValid    bool
		wantTerminal bool
	}{
		{
			name:         "terminal",
			raw:          `null`,
			wantValid:    true,
			wantTerminal: true,
		},
		{
			name:      "absolute",
			raw:       `"https://aither.example/api/torrents/filter?page=2"`,
			wantValid: true,
		},
		{
			name:      "relative",
			raw:       `"/api/torrents/filter?page=2"`,
			wantValid: true,
		},
		{name: "other host", raw: `"https://other.example/api/torrents/filter?page=2"`},
		{name: "downgrade", raw: `"http://aither.example/api/torrents/filter?page=2"`},
		{name: "other port", raw: `"https://aither.example:444/api/torrents/filter?page=2"`},
		{name: "userinfo", raw: `"https://user@aither.example/api/torrents/filter?page=2"`},
		{name: "fragment", raw: `"https://aither.example/api/torrents/filter?page=2#fragment"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next, terminal, valid := unit3DNextSearchURL(endpoint, json.RawMessage(tc.raw))
			if valid != tc.wantValid || terminal != tc.wantTerminal {
				t.Fatalf("next URL valid=%t terminal=%t", valid, terminal)
			}
			if valid && !terminal && next == nil {
				t.Fatal("valid continuation returned nil URL")
			}
		})
	}
}

func TestSearchTorrentsWithEvidenceFailsClosedAtPolicyBound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		nextPage := "2"
		if page == "2" {
			nextPage = "3"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(
			w,
			`{"data":[{"id":%s,"attributes":{"name":"Example.Release.2026.Page.%s-GRP"}}],"links":{"next":"https://aither.example/api/torrents/filter?page=%s&perPage=1"}}`,
			page,
			page,
			nextPage,
		)
	}))
	t.Cleanup(server.Close)

	client := newUnit3DSearchTestClient(t, server)
	result, err := client.SearchTorrentsWithEvidenceBound(
		t.Context(),
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

func TestSearchTorrentsWithEvidenceRejectsMissingContinuationLink(t *testing.T) {
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
		t.Context(),
		"AITHER",
		url.Values{"perPage": []string{"1"}},
		false,
		10,
	)
	if err != nil {
		t.Fatalf("search torrents: %v", err)
	}
	if result.Complete || result.Pages != 1 ||
		result.Warning != "Unit3D search returned inconsistent pagination metadata" {
		t.Fatalf("missing-link result = %#v", result)
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

func TestTorrentInfoOtherUnit3DTrackerImportsDescriptionAndImages(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/torrents/42":
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Error("bearer authorization mismatch")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"attributes": map[string]any{
				"description": "Release notes\n[img]https://93.184.216.34/full.png[/img]",
			}})
		case "/full.png":
			w.Header().Set("Content-Type", "image/png")
			if err := png.Encode(w, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
				t.Errorf("encode test image: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	client := NewClientWithRegistry(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"BLU": {APIKey: "secret"},
	}}}, api.NopLogger{}, &http.Client{Transport: rewriteHostTransport{base: base, rt: server.Client().Transport}}, testUnit3DRegistry(t, "BLU", "https://blu.example"))
	result, err := client.TorrentInfo(t.Context(), "BLU", "42", "", false, true)
	if err != nil {
		t.Fatalf("torrent info: %v", err)
	}
	if result.TrackerID != "42" || !strings.Contains(result.Description, "Release notes") ||
		len(result.Images) != 1 || result.Images[0].RawURL != "https://93.184.216.34/full.png" {
		t.Fatalf("BLU description/images not imported: %#v", result)
	}
}
