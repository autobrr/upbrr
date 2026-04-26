// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func newAuthTestServer(t *testing.T, dbPath string) *Server {
	t.Helper()
	store, err := newAuthStore(dbPath)
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}
	hub := newEventHub()
	return &Server{
		cfg:            testConfig(dbPath),
		auth:           store,
		hub:            hub,
		authLimiter:    newFixedWindowLimiter(100, 0),
		generalLimiter: newFixedWindowLimiter(1000, 0),
		assets: fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("<!doctype html><div id=\"root\"></div>")},
		},
	}
}

func testConfig(dbPath string) config.Config {
	return config.Config{
		MainSettings: config.MainSettingsConfig{
			DBPath: dbPath,
		},
	}
}

func TestAPIV1RequiresBearerToken(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state.db"))
	if err := server.auth.Bootstrap("tester", "very-secure-password"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	mux := http.NewServeMux()
	server.registerAPIV1Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestAPIV1StatusAcceptsBearerToken(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state.db"))
	if err := server.auth.Bootstrap("tester", "very-secure-password"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	created, err := server.auth.CreateAPIToken("automation")
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	mux := http.NewServeMux()
	server.registerAPIV1Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+created.Token)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload apiStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if !payload.OK || payload.Version != "v1" {
		t.Fatalf("unexpected status payload %#v", payload)
	}
}

func TestAPIV1AuthBootstrapLoginAndLogout(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state.db"))
	mux := http.NewServeMux()
	server.registerAPIV1Routes(mux)

	body := `{"username":"tester","password":"very-secure-password","retainLogin":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/bootstrap", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected bootstrap status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var bootstrap apiAuthStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &bootstrap); err != nil {
		t.Fatalf("unmarshal bootstrap: %v", err)
	}
	if bootstrap.BearerToken == "" || !bootstrap.Authenticated || bootstrap.NeedsSetup {
		t.Fatalf("unexpected bootstrap payload %#v", bootstrap)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+bootstrap.BearerToken)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected logout status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+bootstrap.BearerToken)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected revoked token status 401, got %d: %s", recorder.Code, recorder.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", strings.NewReader(body))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected login status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestLegacyRoutesAreNotRegistered(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state.db"))
	mux := http.NewServeMux()
	server.registerRoutes(mux)

	for _, path := range []string{"/api/app/GetConfig", "/api/auth/status", "/api/events"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("expected %s to be unregistered with status 404, got %d", path, recorder.Code)
		}
	}
}

func TestAPIV1BrowseDirectoryRespectsBrowseRootForWebTokens(t *testing.T) {
	tempDir := t.TempDir()
	allowedRoot := filepath.Join(tempDir, "allowed")
	outsideRoot := filepath.Join(tempDir, "outside")
	if err := os.MkdirAll(allowedRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll allowed root: %v", err)
	}
	if err := os.MkdirAll(outsideRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll outside root: %v", err)
	}

	server := newAuthTestServer(t, filepath.Join(tempDir, "state.db"))
	server.backend = &Backend{cfg: server.cfg}
	if err := server.auth.Bootstrap("tester", "very-secure-password"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	record, err := server.auth.Load()
	if err != nil {
		t.Fatalf("Load auth record: %v", err)
	}
	record.BrowseRoot = allowedRoot
	if err := server.auth.UpdateRecord(record); err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	created, err := server.auth.CreateAPIToken(authmaterial.WebSessionAPITokenName)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	mux := http.NewServeMux()
	server.registerAPIV1Routes(mux)

	body, err := json.Marshal(api.BrowseDirectoryRequest{Path: outsideRoot, Mode: "folder"})
	if err != nil {
		t.Fatalf("Marshal browse request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files/browse", bytes.NewReader(body))
	req.Host = "127.0.0.1"
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer "+created.Token)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected outside browse status 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "outside configured web browse roots") {
		t.Fatalf("expected browse root error, got %s", recorder.Body.String())
	}

	body, err = json.Marshal(api.BrowseDirectoryRequest{Mode: "folder"})
	if err != nil {
		t.Fatalf("Marshal browse request: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/files/browse", bytes.NewReader(body))
	req.Host = "127.0.0.1"
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer "+created.Token)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected allowed browse status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response api.BrowseDirectoryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal browse response: %v", err)
	}
	if !sameFilesystemPath(response.CurrentPath, allowedRoot) {
		t.Fatalf("expected current path %q, got %q", allowedRoot, response.CurrentPath)
	}
}

func TestAPIV1BrowseDirectoryAllowsDesktopTokenOnLoopback(t *testing.T) {
	tempDir := t.TempDir()
	outsideRoot := filepath.Join(tempDir, "outside")
	if err := os.MkdirAll(outsideRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll outside root: %v", err)
	}

	server := newAuthTestServer(t, filepath.Join(tempDir, "state.db"))
	server.backend = &Backend{cfg: server.cfg}
	if err := server.auth.Bootstrap(authmaterial.DesktopUsername, "very-secure-password"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	created, err := server.auth.CreateDesktopAPIToken()
	if err != nil {
		t.Fatalf("CreateDesktopAPIToken: %v", err)
	}

	mux := http.NewServeMux()
	server.registerAPIV1Routes(mux)

	body, err := json.Marshal(api.BrowseDirectoryRequest{Path: outsideRoot, Mode: "folder"})
	if err != nil {
		t.Fatalf("Marshal browse request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files/browse", bytes.NewReader(body))
	req.Host = "127.0.0.1"
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer "+created.Token)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected desktop browse status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response api.BrowseDirectoryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal browse response: %v", err)
	}
	if !sameFilesystemPath(response.CurrentPath, outsideRoot) {
		t.Fatalf("expected current path %q, got %q", outsideRoot, response.CurrentPath)
	}
}

func TestOpenAPIDocumentCoversAPIV1Routes(t *testing.T) {
	routes := apiV1Routes()
	doc, err := openAPIDocumentSpec()
	if err != nil {
		t.Fatalf("openAPIDocumentSpec: %v", err)
	}
	paths := openAPIMap(t, doc["paths"], "openapi.paths")
	seenOperationIDs := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		item := openAPIMap(t, paths[route.Path], route.Path)
		operation := openAPIMap(t, item[lowerHTTPMethod(route.Method)], route.Path+"."+lowerHTTPMethod(route.Method))
		id, _ := operation["operationId"].(string)
		if id == "" {
			t.Fatalf("missing operation id for %s %s", route.Method, route.Path)
		}
		if _, exists := seenOperationIDs[id]; exists {
			t.Fatalf("duplicate operation id %q", id)
		}
		seenOperationIDs[id] = struct{}{}
		if route.Request != nil {
			if _, exists := operation["requestBody"]; !exists {
				t.Fatalf("missing request body for %s %s", route.Method, route.Path)
			}
		}
		_, hasSecurity := operation["security"]
		if route.Public && hasSecurity {
			t.Fatalf("public route should not require security: %s %s", route.Method, route.Path)
		}
		if !route.Public && !hasSecurity {
			t.Fatalf("authenticated route should document security: %s %s", route.Method, route.Path)
		}
	}
}

func TestOpenAPIYAMLHandler(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state.db"))
	mux := http.NewServeMux()
	server.registerAPIV1Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/openapi.yaml", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/yaml") {
		t.Fatalf("expected application/yaml content type, got %q", contentType)
	}
	var payload map[string]any
	if err := yaml.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal openapi yaml: %v", err)
	}
	if got, _ := payload["openapi"].(string); got != "3.1.1" {
		t.Fatalf("unexpected openapi version %q", got)
	}
}

func TestOpenAPIDocumentUsesRouteSpecificErrorResponses(t *testing.T) {
	doc, err := openAPIDocumentSpec()
	if err != nil {
		t.Fatalf("openAPIDocumentSpec: %v", err)
	}
	responses := openAPIOperationResponses(t, doc, "/api/v1/status", "get")
	for _, status := range []string{"400", "403", "404", "409", "429", "500"} {
		if _, exists := responses[status]; exists {
			t.Fatalf("status route should not document %s response", status)
		}
	}
	if _, exists := responses["default"]; !exists {
		t.Fatalf("status route should document default error response")
	}
	if _, exists := openAPIOperationResponses(t, doc, "/api/v1/files/browse", "post")["403"]; !exists {
		t.Fatalf("browse route should document 403 response")
	}
	if _, exists := openAPIOperationResponses(t, doc, "/api/v1/metadata/fetch", "post")["409"]; !exists {
		t.Fatalf("metadata fetch route should document 409 response")
	}
}

func TestOpenAPIRouteErrorStatusesMatchSpec(t *testing.T) {
	doc, err := openAPIDocumentSpec()
	if err != nil {
		t.Fatalf("openAPIDocumentSpec: %v", err)
	}
	for _, route := range apiV1Routes() {
		responses := openAPIOperationResponses(t, doc, route.Path, lowerHTTPMethod(route.Method))
		if _, exists := responses["default"]; !exists {
			t.Fatalf("missing default error response for %s %s", route.Method, route.Path)
		}
		if !route.Public {
			if _, exists := responses["401"]; !exists {
				t.Fatalf("missing 401 response for authenticated route %s %s", route.Method, route.Path)
			}
		}
		for _, status := range route.ErrorStatuses {
			if _, exists := responses[http.StatusText(status)]; exists {
				t.Fatalf("response status should use numeric key, got text for %s %s", route.Method, route.Path)
			}
			key := strings.TrimSpace(strconv.Itoa(status))
			if _, exists := responses[key]; !exists {
				t.Fatalf("missing %s response for %s %s", key, route.Method, route.Path)
			}
		}
	}
}

func openAPIOperationResponses(t *testing.T, doc openAPIDocument, path string, method string) map[string]any {
	t.Helper()
	paths := openAPIMap(t, doc["paths"], "openapi.paths")
	pathItem := openAPIMap(t, paths[path], path)
	operation := openAPIMap(t, pathItem[method], path+"."+method)
	return openAPIMap(t, operation["responses"], path+"."+method+".responses")
}

func openAPIMap(t *testing.T, value any, name string) map[string]any {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case openAPIDocument:
		return map[string]any(typed)
	default:
		t.Fatalf("%s has unexpected type %T", name, value)
		return nil
	}
}

func lowerHTTPMethod(method string) string {
	switch method {
	case http.MethodGet:
		return "get"
	case http.MethodPost:
		return "post"
	case http.MethodPut:
		return "put"
	case http.MethodDelete:
		return "delete"
	default:
		return method
	}
}
