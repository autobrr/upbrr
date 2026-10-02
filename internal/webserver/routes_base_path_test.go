// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestRegisterRoutesMountsConfiguredBasePath(t *testing.T) {
	t.Parallel()

	server := &Server{
		cliCfg: CLIConfig{BaseURL: "https://example.test/upbrr/"},
		assets: fstest.MapFS{
			"index.html": {
				Data: []byte(`<!doctype html><html><head><link rel="icon" href="/favicon.ico" /><script type="module" src="/assets/index.js"></script></head><body></body></html>`),
			},
			"assets/index.js": {
				Data: []byte(`console.log("ok");`),
			},
			"site.webmanifest": {
				Data: []byte(`{"icons":[{"src":"/icon-192.png"}],"start_url":"/","scope":"/"}`),
			},
		},
		developmentNoAuth: true,
		developmentSession: session{
			ID:        "dev-no-auth",
			Username:  "dev",
			CSRFToken: "csrf",
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
		authLimiter:    newFixedWindowLimiter(100, time.Minute),
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
	}

	mux := http.NewServeMux()
	server.registerRoutes(mux)

	status := serveBasePathTestRequest(t, mux, "/upbrr/api/auth/status")
	if status.Code != http.StatusOK {
		t.Fatalf("prefixed auth status returned %d: %s", status.Code, status.Body.String())
	}
	if !strings.Contains(status.Body.String(), `"authenticated":true`) {
		t.Fatalf("expected development auth status, got %s", status.Body.String())
	}
	browsePolicyReq := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/upbrr/api/auth/browse-policy",
		nil,
	)
	browsePolicy := httptest.NewRecorder()
	mux.ServeHTTP(browsePolicy, browsePolicyReq)
	if browsePolicy.Code != http.StatusUnauthorized {
		t.Fatalf("prefixed initial browse-policy route returned %d, want 401", browsePolicy.Code)
	}

	docs := serveBasePathTestRequest(t, mux, "/upbrr/api/v1/docs")
	if docs.Code != http.StatusOK {
		t.Fatalf("prefixed API docs returned %d: %s", docs.Code, docs.Body.String())
	}
	if !strings.Contains(docs.Body.String(), `url: "openapi.json"`) ||
		!strings.Contains(docs.Body.String(), `href="../../favicon.ico"`) {
		t.Fatalf("prefixed API docs did not retain request-relative URLs: %s", docs.Body.String())
	}

	openAPI := serveBasePathTestRequest(t, mux, "/upbrr/api/v1/openapi.json")
	if openAPI.Code != http.StatusOK {
		t.Fatalf("prefixed OpenAPI returned %d: %s", openAPI.Code, openAPI.Body.String())
	}
	var openAPIDocument struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(openAPI.Body.Bytes(), &openAPIDocument); err != nil {
		t.Fatalf("prefixed OpenAPI JSON: %v", err)
	}
	if len(openAPIDocument.Servers) != 1 || openAPIDocument.Servers[0].URL != "/upbrr/api/v1" {
		t.Fatalf("prefixed OpenAPI servers = %#v", openAPIDocument.Servers)
	}

	rootIndex := serveBasePathTestRequest(t, mux, "/")
	if rootIndex.Code != http.StatusFound {
		t.Fatalf("root index returned %d, want 302", rootIndex.Code)
	}
	if location := rootIndex.Header().Get("Location"); location != "/upbrr/" {
		t.Fatalf("root redirect location = %q, want /upbrr/", location)
	}

	rootStatus := serveBasePathTestRequest(t, mux, "/api/auth/status")
	if rootStatus.Code != http.StatusNotFound {
		t.Fatalf("root auth status returned %d, want 404", rootStatus.Code)
	}
	rootDocs := serveBasePathTestRequest(t, mux, "/api/v1/docs")
	if rootDocs.Code != http.StatusNotFound {
		t.Fatalf("root API docs returned %d, want 404", rootDocs.Code)
	}

	index := serveBasePathTestRequest(t, mux, "/upbrr/")
	if index.Code != http.StatusOK {
		t.Fatalf("prefixed index returned %d: %s", index.Code, index.Body.String())
	}
	for _, want := range []string{
		`window.__UPBRR_BASE_URL__="/upbrr/"`,
		`href="/upbrr/favicon.ico"`,
		`src="/upbrr/assets/index.js"`,
	} {
		if !strings.Contains(index.Body.String(), want) {
			t.Fatalf("expected rewritten index to contain %q, got %s", want, index.Body.String())
		}
	}
	assertUIFallbackContract(t, mux, "/upbrr")

	manifest := serveBasePathTestRequest(t, mux, "/upbrr/site.webmanifest")
	if manifest.Code != http.StatusOK {
		t.Fatalf("prefixed manifest returned %d: %s", manifest.Code, manifest.Body.String())
	}
	for _, want := range []string{`"src":"/icon-192.png"`, `"start_url":"/"`, `"scope":"/"`} {
		if strings.Contains(manifest.Body.String(), want) {
			t.Fatalf("manifest still contains root absolute %q: %s", want, manifest.Body.String())
		}
	}
	if !strings.Contains(manifest.Body.String(), `"/upbrr/icon-192.png"`) {
		t.Fatalf("expected rewritten manifest icons, got %s", manifest.Body.String())
	}

	missingAPI := serveBasePathTestRequest(t, mux, "/upbrr/api/missing")
	if missingAPI.Code != http.StatusNotFound {
		t.Fatalf("missing prefixed API returned %d, want 404", missingAPI.Code)
	}

	noSlash := serveBasePathTestRequest(t, mux, "/upbrr")
	if noSlash.Code != http.StatusMovedPermanently {
		t.Fatalf("base path without slash returned %d, want 301", noSlash.Code)
	}
	if location := noSlash.Header().Get("Location"); location != "/upbrr/" {
		t.Fatalf("redirect location = %q, want /upbrr/", location)
	}
}

func TestRegisterRoutesPreservesRootMode(t *testing.T) {
	t.Parallel()

	server := &Server{
		assets: fstest.MapFS{
			"index.html": {Data: []byte(`<!doctype html><html><head></head><body></body></html>`)},
		},
		developmentNoAuth: true,
		developmentSession: session{
			ID:        "dev-no-auth",
			Username:  "dev",
			CSRFToken: "csrf",
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
		authLimiter:    newFixedWindowLimiter(100, time.Minute),
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
	}

	mux := http.NewServeMux()
	server.registerRoutes(mux)

	index := serveBasePathTestRequest(t, mux, "/")
	if index.Code != http.StatusOK {
		t.Fatalf("root index returned %d: %s", index.Code, index.Body.String())
	}
	status := serveBasePathTestRequest(t, mux, "/api/auth/status")
	if status.Code != http.StatusOK {
		t.Fatalf("root auth status returned %d: %s", status.Code, status.Body.String())
	}
	docs := serveBasePathTestRequest(t, mux, "/api/v1/docs")
	if docs.Code != http.StatusOK {
		t.Fatalf("root API docs returned %d: %s", docs.Code, docs.Body.String())
	}
	openAPI := serveBasePathTestRequest(t, mux, "/api/v1/openapi.json")
	if openAPI.Code != http.StatusOK {
		t.Fatalf("root OpenAPI returned %d: %s", openAPI.Code, openAPI.Body.String())
	}
	var document struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(openAPI.Body.Bytes(), &document); err != nil {
		t.Fatalf("root OpenAPI JSON: %v", err)
	}
	if len(document.Servers) != 1 || document.Servers[0].URL != "/api/v1" {
		t.Fatalf("root OpenAPI servers = %#v", document.Servers)
	}
	assertUIFallbackContract(t, mux, "")
}

func assertUIFallbackContract(t *testing.T, mux *http.ServeMux, prefix string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/ui-route-paths.json")
	if err != nil {
		t.Fatal(err)
	}
	var routes []string
	if err := json.Unmarshal(raw, &routes); err != nil {
		t.Fatal(err)
	}
	if len(routes) != len(uiRoutePaths) {
		t.Fatalf("UI route ledger has %d paths; server allows %d", len(routes), len(uiRoutePaths))
	}
	seen := make(map[string]bool, len(routes))
	for _, route := range routes {
		if seen[route] {
			t.Fatalf("duplicate UI route in ledger: %s", route)
		}
		seen[route] = true
		if _, ok := uiRoutePaths[route]; !ok {
			t.Fatalf("UI route ledger path %s is absent from server allowlist", route)
		}
		for _, suffix := range []string{"", "/"} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				name := method + " " + prefix + route + suffix
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					request := httptest.NewRequestWithContext(t.Context(), method, prefix+route+suffix+"?filter=x", nil)
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, request)
					if response.Code != http.StatusOK {
						t.Fatalf("UI route returned %d, want 200", response.Code)
					}
					if method == http.MethodGet && !strings.Contains(response.Body.String(), "__UPBRR_BASE_URL__") {
						t.Fatalf("UI route did not serve shell: %s", response.Body.String())
					}
				})
			}
		}
	}
	for _, path := range []string{"/unknown", "/assets/missing.js", "/assets/missing.css", "/images/missing.png", "/fonts/missing.woff2", "/api/missing"} {
		response := serveBasePathTestRequest(t, mux, prefix+path)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s returned %d, want 404", prefix+path, response.Code)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		request := httptest.NewRequestWithContext(t.Context(), method, prefix+"/settings", nil)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s returned %d, want 405", method, prefix+"/settings", response.Code)
		}
	}
}

func TestRewriteIndexHTMLNormalizesRelativeAssets(t *testing.T) {
	t.Parallel()
	raw := []byte(`<!doctype html><html><head>` +
		`<script src="./appearance-bootstrap.js"></script>` +
		`<script type="module" src="./assets/index.js"></script>` +
		`<link rel="stylesheet" href="assets/index.css">` +
		`<link rel="modulepreload" href="./assets/chunk.js">` +
		`<link rel="icon" href="/favicon.ico">` +
		`<script src="https://cdn.example.test/app.js"></script>` +
		`<script src="//cdn.example.test/app.js"></script>` +
		`<script type="application/json">{"src":"./assets/private.js"}</script>` +
		`</head><body></body></html>`)
	for _, base := range []string{"/", "/upbrr/"} {
		result := string(rewriteIndexHTML(raw, base))
		for _, want := range []string{
			`src="` + base + `appearance-bootstrap.js"`,
			`src="` + base + `assets/index.js"`,
			`href="` + base + `assets/index.css"`,
			`href="` + base + `assets/chunk.js"`,
			`href="` + base + `favicon.ico"`,
			`src="https://cdn.example.test/app.js"`,
			`src="//cdn.example.test/app.js"`,
			`{"src":"./assets/private.js"}`,
		} {
			if !strings.Contains(result, want) {
				t.Errorf("base %q: missing %q in %s", base, want, result)
			}
		}
	}
}

func TestRewriteRootAbsoluteManifestPathsHandlesJSONFormatting(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{name: "compact", raw: `{"icons":[{"src":"/icon-192.png"}],"start_url":"/","scope":"/"}`},
		{name: "spaced", raw: `{"icons": [{"src" : "/icon-192.png"}], "start_url" : "/", "scope" : "/"}`},
		{name: "newlines", raw: "{\n  \"icons\": [\n    {\n      \"src\"\n      :\n      \"/icon-192.png\"\n    }\n  ],\n  \"start_url\": \"/\",\n  \"scope\": \"/\"\n}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rewritten := rewriteRootAbsoluteAssetPaths([]byte(tc.raw), "/upbrr/")

			var manifest struct {
				Icons []struct {
					Src string `json:"src"`
				} `json:"icons"`
				StartURL string `json:"start_url"`
				Scope    string `json:"scope"`
			}
			if err := json.Unmarshal(rewritten, &manifest); err != nil {
				t.Fatalf("manifest JSON: %v\n%s", err, rewritten)
			}
			if len(manifest.Icons) != 1 || manifest.Icons[0].Src != "/upbrr/icon-192.png" {
				t.Fatalf("icon src not rewritten: %s", rewritten)
			}
			if manifest.StartURL != "/upbrr/" || manifest.Scope != "/upbrr/" {
				t.Fatalf("manifest paths not rewritten: %s", rewritten)
			}
		})
	}
}

func TestRewriteRootAbsoluteManifestPathsPreservesRootMode(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"icons":[{"src":"/icon-192.png"}],"start_url":"/","scope":"/"}`)
	rewritten := rewriteRootAbsoluteAssetPaths(raw, "/")
	if string(rewritten) != string(raw) {
		t.Fatalf("root mode rewrite changed manifest: %s", rewritten)
	}
}

func TestRewriteIndexHTMLDoesNotRewriteNonAppURLsOrJSONScripts(t *testing.T) {
	t.Parallel()

	raw := []byte(`<!doctype html><html><head>` +
		`<link rel="icon" href="/favicon.ico">` +
		`<link rel="preconnect" href="https://fonts.example.test">` +
		`<script type="application/json">{"src":"/user/content.png","absolute":"https://cdn.example.test/app.js","protocol":"//cdn.example.test/app.js"}</script>` +
		`<script type="module" src="/assets/index.js"></script>` +
		`<script src="//cdn.example.test/lib.js"></script>` +
		`</head><body></body></html>`)
	rewritten := string(rewriteIndexHTML(raw, "/upbrr/"))

	for _, want := range []string{
		`href="/upbrr/favicon.ico"`,
		`src="/upbrr/assets/index.js"`,
		`href="https://fonts.example.test"`,
		`src="//cdn.example.test/lib.js"`,
		`"src":"/user/content.png"`,
		`"absolute":"https://cdn.example.test/app.js"`,
		`"protocol":"//cdn.example.test/app.js"`,
	} {
		if !strings.Contains(rewritten, want) {
			t.Fatalf("rewritten HTML missing %q: %s", want, rewritten)
		}
	}
}

func serveBasePathTestRequest(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	req.Host = "127.0.0.1:7480"
	req.RemoteAddr = "127.0.0.1:5000"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	_, _ = io.Copy(io.Discard, recorder.Result().Body)
	return recorder
}
