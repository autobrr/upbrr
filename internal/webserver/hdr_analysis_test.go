// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

type hdrArtifactCoreFixture struct {
	ReleaseWorkflowCapability
	expectedOwner string
	owner         string
	ref           api.HDRAnalysisRef
	artifact      api.PublicResourceID
	calls         int
	contentType   string
}

func (f *hdrArtifactCoreFixture) OpenReleaseWorkflowHDRAnalysisArtifact(_ context.Context, owner string, workflow api.WorkflowID, ref api.HDRAnalysisRef, artifact api.PublicResourceID) (releaseworkflow.MediaArtifactContent, error) {
	f.owner, f.ref, f.artifact = owner, ref, artifact
	f.calls++
	if owner != f.expectedOwner || workflow != "workflow" || ref.ID != "analysis" || artifact != "plot" {
		return releaseworkflow.MediaArtifactContent{}, releaseworkflow.ErrWorkflowNotFound
	}
	contentType := f.contentType
	if contentType == "" {
		contentType = "image/png"
	}
	return releaseworkflow.MediaArtifactContent{Body: io.NopCloser(strings.NewReader("synthetic-png")), ContentType: contentType}, nil
}

func TestHDRArtifactTransportAuthorityAndPNGHeaders(t *testing.T) {
	t.Parallel()
	store, err := newAPITokenStore([]APITokenCredential{{
		Token:   apiV1TestToken,
		OwnerID: "reader",
		Scopes:  []APITokenScope{APITokenScopeWorkflowRead},
	}})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &hdrArtifactCoreFixture{expectedOwner: "api:reader"}
	server := &Server{
		backend:        &Backend{capabilities: CoreCapabilities{ReleaseWorkflow: fixture}},
		apiTokens:      store,
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
	}
	mux := http.NewServeMux()
	server.registerV1Routes(mux)
	path := "/api/v1/workflows/workflow/hdr-analysis/analysis/artifacts/plot?revision=7"
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+apiV1TestToken)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "synthetic-png" || fixture.ref.Revision != 7 || fixture.owner != "api:reader" || response.Header().Get("Content-Type") != "image/png" || response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Content-Disposition") != `inline; filename="hdr10plus.png"` {
		t.Fatalf("HDR API response=%d headers=%v ref=%#v", response.Code, response.Header(), fixture.ref)
	}
	invalid := httptest.NewRequestWithContext(t.Context(), http.MethodGet, strings.Replace(path, "revision=7", "revision=0", 1), nil)
	invalid.Header.Set("Authorization", "Bearer "+apiV1TestToken)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, invalid)
	if response.Code != http.StatusBadRequest || fixture.calls != 1 {
		t.Fatalf("invalid revision=%d calls=%d", response.Code, fixture.calls)
	}
	fixture.contentType = "text/plain"
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatal("HDR transport streamed an unexpected content type")
	}
	fixture.contentType = ""
	fixture.expectedOwner = "foreign"
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("foreign HDR access=%d", response.Code)
	}

	app := newAuthTestServer(t, filepath.Join(t.TempDir(), "state.db"))
	session, err := app.sessions.Create("admin", false)
	if err != nil {
		t.Fatal(err)
	}
	fixture.expectedOwner = session.ID
	app.backend.replaceRuntime(config.Config{}, CoreCapabilities{ReleaseWorkflow: fixture}, nil)
	appMux := http.NewServeMux()
	app.registerReleaseWorkflowAppRoutes(appMux)
	request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/app/release-workflow-hdr-analysis?workflowId=workflow&analysisId=analysis&analysisRevision=4&artifactId=plot", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.ID})
	response = httptest.NewRecorder()
	appMux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fixture.owner != session.ID || fixture.ref.Revision != 4 || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("HDR app authority=%d owner=%q ref=%#v", response.Code, fixture.owner, fixture.ref)
	}
	request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/app/release-workflow-hdr-analysis", nil)
	response = httptest.NewRecorder()
	appMux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated HDR app access=%d", response.Code)
	}
}
