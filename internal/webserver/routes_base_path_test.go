// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
