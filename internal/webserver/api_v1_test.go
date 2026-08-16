// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

const apiV1TestToken = "synthetic-api-token-000000000001"

func TestAPIV1DocumentationRoutesArePublicAndPinned(t *testing.T) {
	t.Parallel()

	server := &Server{}
	mux := http.NewServeMux()
	server.registerV1Routes(mux)

	docsRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/docs", nil)
	docsResponse := httptest.NewRecorder()
	mux.ServeHTTP(docsResponse, docsRequest)
	if docsResponse.Code != http.StatusOK {
		t.Fatalf("docs status = %d, want 200", docsResponse.Code)
	}
	if contentType := docsResponse.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Fatalf("docs content type = %q", contentType)
	}
	if cacheControl := docsResponse.Header().Get("Cache-Control"); cacheControl != "no-cache" {
		t.Fatalf("docs cache control = %q", cacheControl)
	}
	if options := docsResponse.Header().Get("X-Content-Type-Options"); options != "nosniff" {
		t.Fatalf("docs content type options = %q", options)
	}
	page := docsResponse.Body.String()
	for _, want := range []string{
		"https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.11.0/swagger-ui.css",
		"https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.11.0/swagger-ui-bundle.js",
		"https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.11.0/swagger-ui-standalone-preset.js",
		`<meta name="color-scheme" content="light dark">`,
		`window.localStorage.getItem("theme")`,
		`window.localStorage.setItem("theme", nextTheme)`,
		`window.matchMedia("(prefers-color-scheme: dark)")`,
		`document.documentElement.dataset.theme = theme`,
		`id="theme-toggle"`,
		`aria-label="Dark mode"`,
		`aria-pressed="false"`,
		`.theme-toggle[aria-pressed="true"]`,
		`html[data-theme="dark"]`,
		`url: "openapi.json"`,
		"deepLinking: true",
		"persistAuthorization: true",
		"tryItOutEnabled: true",
		"filter: true",
		`docExpansion: "list"`,
		"defaultModelsExpandDepth: 1",
		"defaultModelExpandDepth: 1",
		"displayRequestDuration: true",
		"showExtensions: true",
		"showCommonExtensions: true",
		`tagsSorter: "alpha"`,
		`operationsSorter: "alpha"`,
		"validatorUrl: null",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("docs HTML missing %q", want)
		}
	}
	for _, forbidden := range []string{"{{SWAGGER_UI_VERSION}}", "requestInterceptor", "X-API-Key", apiV1TestToken} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("docs HTML contains forbidden value %q", forbidden)
		}
	}

	postRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/docs", nil)
	postResponse := httptest.NewRecorder()
	mux.ServeHTTP(postResponse, postRequest)
	if postResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("docs POST status = %d, want 405", postResponse.Code)
	}

	openAPIRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/openapi.json", nil)
	openAPIResponse := httptest.NewRecorder()
	mux.ServeHTTP(openAPIResponse, openAPIRequest)
	if openAPIResponse.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d, want 200", openAPIResponse.Code)
	}
	if contentType := openAPIResponse.Header().Get("Content-Type"); contentType != "application/vnd.oai.openapi+json;version=3.1" {
		t.Fatalf("OpenAPI content type = %q", contentType)
	}
	var document struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
		Components struct {
			SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPIResponse.Body.Bytes(), &document); err != nil {
		t.Fatalf("parse served OpenAPI: %v", err)
	}
	if len(document.Servers) != 1 || document.Servers[0].URL != "/api/v1" {
		t.Fatalf("root OpenAPI servers = %#v", document.Servers)
	}
	if _, ok := document.Components.SecuritySchemes["bearerAuth"]; !ok {
		t.Fatal("served OpenAPI lacks native bearer authentication")
	}
}

func TestReleaseWorkflowOpenAPIBasePathChangesOnlyServers(t *testing.T) {
	t.Parallel()

	root, err := releaseWorkflowOpenAPIDocument("")
	if err != nil {
		t.Fatalf("root OpenAPI: %v", err)
	}
	prefixed, err := releaseWorkflowOpenAPIDocument("/upbrr")
	if err != nil {
		t.Fatalf("prefixed OpenAPI: %v", err)
	}
	var rootDocument map[string]any
	if err := json.Unmarshal(root, &rootDocument); err != nil {
		t.Fatalf("parse root OpenAPI: %v", err)
	}
	var prefixedDocument map[string]any
	if err := json.Unmarshal(prefixed, &prefixedDocument); err != nil {
		t.Fatalf("parse prefixed OpenAPI: %v", err)
	}
	rootDocument["servers"] = prefixedDocument["servers"]
	if !reflect.DeepEqual(rootDocument, prefixedDocument) {
		t.Fatal("base-path OpenAPI changed fields other than servers")
	}
}

func TestAPITokenStoreScopesAndOwnerIsolation(t *testing.T) {
	t.Parallel()

	store, err := newAPITokenStore([]APITokenCredential{
		{
			Token:   apiV1TestToken,
			OwnerID: "automation",
			Scopes:  []APITokenScope{APITokenScopeWorkflowRead},
		},
	})
	if err != nil {
		t.Fatalf("new token store: %v", err)
	}
	principal, ok := store.authenticate(apiV1TestToken)
	if !ok || principal.OwnerID != "api:automation" || len(principal.Scopes) != 1 || principal.Scopes[0] != APITokenScopeWorkflowRead {
		t.Fatalf("principal = %#v, ok=%t", principal, ok)
	}
	if _, ok := store.authenticate("synthetic-api-token-000000000002"); ok {
		t.Fatal("unexpected authentication for different token")
	}
	if _, err := newAPITokenStore([]APITokenCredential{{Token: "short", OwnerID: "owner"}}); err == nil {
		t.Fatal("expected short token rejection")
	}
}

func TestAPIV1AuthIsSeparateFromBrowserCSRFAndEnforcesScope(t *testing.T) {
	t.Parallel()

	store, err := newAPITokenStore([]APITokenCredential{{
		Token:   apiV1TestToken,
		OwnerID: "reader",
		Scopes:  []APITokenScope{APITokenScopeWorkflowRead},
	}})
	if err != nil {
		t.Fatalf("new token store: %v", err)
	}
	server := &Server{
		apiTokens:      store,
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
	}
	mux := http.NewServeMux()
	server.registerV1Routes(mux)

	csrfOnly := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/api/v1/continuations",
		strings.NewReader(`{"goal":"prepared","intent":{}}`),
	)
	csrfOnly.Header.Set("X-Csrf-Token", apiV1TestToken)
	csrfOnly.Header.Set("Idempotency-Key", "csrf-is-not-bearer")
	csrfResponse := httptest.NewRecorder()
	mux.ServeHTTP(csrfResponse, csrfOnly)
	if csrfResponse.Code != http.StatusUnauthorized {
		t.Fatalf("CSRF-only status = %d, want 401", csrfResponse.Code)
	}

	readToken := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/api/v1/continuations",
		strings.NewReader(`{"goal":"prepared","intent":{}}`),
	)
	readToken.Header.Set("Authorization", "Bearer "+apiV1TestToken)
	readToken.Header.Set("Idempotency-Key", "read-scope-write")
	readResponse := httptest.NewRecorder()
	mux.ServeHTTP(readResponse, readToken)
	if readResponse.Code != http.StatusForbidden {
		t.Fatalf("read-scope write status = %d, want 403", readResponse.Code)
	}
}

func TestAPIV1RejectsOversizedAndUnknownRequestBodiesBeforeExecution(t *testing.T) {
	t.Parallel()

	store, err := newAPITokenStore([]APITokenCredential{{Token: apiV1TestToken, OwnerID: "writer"}})
	if err != nil {
		t.Fatalf("new token store: %v", err)
	}
	server := &Server{
		apiTokens:      store,
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
	}
	mux := http.NewServeMux()
	server.registerV1Routes(mux)

	oversized := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/api/v1/continuations",
		bytes.NewReader(append([]byte(`{"goal":"prepared","intent":{},"padding":"`), bytes.Repeat([]byte("x"), apiV1RequestMaxBytes)...)),
	)
	oversized.Header.Set("Authorization", "Bearer "+apiV1TestToken)
	oversized.Header.Set("Idempotency-Key", "oversized")
	oversizedResponse := httptest.NewRecorder()
	mux.ServeHTTP(oversizedResponse, oversized)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, want 413", oversizedResponse.Code)
	}

	unknown := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/api/v1/continuations",
		strings.NewReader(`{"goal":"prepared","intent":{},"unknown":true}`),
	)
	unknown.Header.Set("Authorization", "Bearer "+apiV1TestToken)
	unknown.Header.Set("Idempotency-Key", "unknown-field")
	unknownResponse := httptest.NewRecorder()
	mux.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d, want 400", unknownResponse.Code)
	}
}

type apiV1CompositeUploadCoreFake struct {
	ReleaseWorkflowCapability
	startOwner   string
	startRequest api.CreateReleaseWorkflowUploadRequest
	feedback     api.ReleaseWorkflowUploadFeedback
	feedbackID   api.WorkflowID
}

func (f *apiV1CompositeUploadCoreFake) StartReleaseWorkflowUpload(
	_ context.Context,
	ownerID string,
	request api.CreateReleaseWorkflowUploadRequest,
) (releaseworkflow.CommandResult, error) {
	f.startOwner = ownerID
	f.startRequest = request
	return releaseworkflow.CommandResult{
		Workflow: api.ReleaseWorkflow{
			ID:       "workflow-upload",
			Revision: 1,
			Status:   api.WorkflowStatusActive,
		},
		Operation: &api.WorkflowOperationStatus{
			ID:         "operation-upload",
			WorkflowID: "workflow-upload",
			Revision:   1,
			Status:     api.StageStatusQueued,
		},
	}, nil
}

func (f *apiV1CompositeUploadCoreFake) SubmitReleaseWorkflowUploadFeedback(
	_ context.Context,
	_ string,
	workflowID api.WorkflowID,
	feedback api.ReleaseWorkflowUploadFeedback,
) (releaseworkflow.CommandResult, error) {
	f.feedbackID = workflowID
	f.feedback = feedback
	return releaseworkflow.CommandResult{
		Workflow: api.ReleaseWorkflow{
			ID:       workflowID,
			Revision: feedback.Action.WorkflowRevision + 1,
			Status:   api.WorkflowStatusCompleted,
		},
		Operation: &api.WorkflowOperationStatus{
			ID:         "operation-feedback",
			WorkflowID: workflowID,
			Revision:   feedback.Action.WorkflowRevision + 1,
			Status:     api.StageStatusCompleted,
		},
	}, nil
}

func TestAPIV1CompositeUploadCreateAndFeedbackContracts(t *testing.T) {
	t.Parallel()

	store, err := newAPITokenStore([]APITokenCredential{{
		Token:   apiV1TestToken,
		OwnerID: "uploader",
		Scopes:  []APITokenScope{APITokenScopeWorkflowExecute},
	}})
	if err != nil {
		t.Fatalf("new upload token store: %v", err)
	}
	coreFake := &apiV1CompositeUploadCoreFake{}
	server := &Server{
		backend: &Backend{
			capabilities: CoreCapabilities{ReleaseWorkflow: coreFake},
		},
		apiTokens:      store,
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
		cliCfg:         CLIConfig{BaseURL: "/upbrr"},
	}
	mux := http.NewServeMux()
	server.registerV1Routes(mux)

	create := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/api/v1/uploads",
		strings.NewReader(
			`{"source":{"path":"D:\\Example Release 2026"},"unattended":{"confirm":false},"execution":{"mode":"debug"},"trackers":{"include":["EXAMPLE"]}}`,
		),
	)
	create.Header.Set("Authorization", "Bearer "+apiV1TestToken)
	create.Header.Set("Idempotency-Key", "upload-create-1")
	createResponse := httptest.NewRecorder()
	mux.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusAccepted {
		t.Fatalf("composite create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	if coreFake.startOwner != "api:uploader" || coreFake.startRequest.IdempotencyKey != "upload-create-1" ||
		coreFake.startRequest.Execution.Mode != api.ReleaseWorkflowUploadModeDebug {
		t.Fatalf("composite create mapping = owner=%q request=%#v", coreFake.startOwner, coreFake.startRequest)
	}
	if got := createResponse.Header().Get("Location"); got != "/upbrr/api/v1/workflows/workflow-upload" {
		t.Fatalf("composite create Location = %q", got)
	}
	if got := createResponse.Header().Get("Operation-Location"); got !=
		"/upbrr/api/v1/workflows/workflow-upload/operations/operation-upload" {
		t.Fatalf("composite create Operation-Location = %q", got)
	}
	if got := createResponse.Header().Get("ETag"); got != `"1"` {
		t.Fatalf("composite create ETag = %q", got)
	}

	feedback := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/api/v1/uploads/workflow-upload/feedback",
		strings.NewReader(
			`{"action":{"id":"action-example","workflowRevision":7},"response":{"kind":"trackerApproval","trackerApproval":{"confirmed":true,"trackerIds":["EXAMPLE"]}}}`,
		),
	)
	feedback.Header.Set("Authorization", "Bearer "+apiV1TestToken)
	feedback.Header.Set("Idempotency-Key", "upload-feedback-1")
	feedback.Header.Set("If-Match", `"7"`)
	feedbackResponse := httptest.NewRecorder()
	mux.ServeHTTP(feedbackResponse, feedback)
	if feedbackResponse.Code != http.StatusOK {
		t.Fatalf("composite feedback status=%d body=%s", feedbackResponse.Code, feedbackResponse.Body.String())
	}
	if coreFake.feedbackID != "workflow-upload" || coreFake.feedback.IdempotencyKey != "upload-feedback-1" ||
		coreFake.feedback.Response.Kind != api.ReleaseWorkflowUploadFeedbackTrackerApproval {
		t.Fatalf("composite feedback mapping = id=%q feedback=%#v", coreFake.feedbackID, coreFake.feedback)
	}
	if got := feedbackResponse.Header().Get("ETag"); got != `"8"` {
		t.Fatalf("composite feedback ETag = %q", got)
	}
}

func TestAPIV1CapabilitiesRequiresReadScopeAndReturnsSafeCatalog(t *testing.T) {
	t.Parallel()

	readStore, err := newAPITokenStore([]APITokenCredential{{
		Token:   apiV1TestToken,
		OwnerID: "qui",
		Scopes:  []APITokenScope{APITokenScopeWorkflowRead, APITokenScopeWorkflowExecute},
	}})
	if err != nil {
		t.Fatalf("new capability token store: %v", err)
	}
	backend := &Backend{cfg: config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"qui": {
				Type:     "qui",
				Password: "capability-secret-value",
			},
		},
		ImageHosting: config.ImageHostingConfig{
			Host1:             "imgbb",
			ImgBBAPI:          "capability-image-secret",
			SamaritanoEnabled: true,
			SamaritanoAPI:     "capability-samaritano-secret",
		},
	}}
	server := &Server{
		backend:        backend,
		apiTokens:      readStore,
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
	}
	mux := http.NewServeMux()
	server.registerV1Routes(mux)

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/capabilities", nil)
	request.Header.Set("Authorization", "Bearer "+apiV1TestToken)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("capability status = %d", response.Code)
	}
	var capabilities api.ReleaseWorkflowCapabilities
	if err := json.Unmarshal(response.Body.Bytes(), &capabilities); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if capabilities.OwnerID != "api:qui" || capabilities.APIVersion != api.ReleaseWorkflowAPIVersion ||
		!capabilities.Features.CompositeUpload || !capabilities.Features.TypedFeedback ||
		!capabilities.Features.StrictEligibleTrackerContinuation ||
		len(capabilities.UploadOptionSchemaHash) != 64 || len(capabilities.Trackers) == 0 ||
		len(capabilities.TorrentClients) != 1 || len(capabilities.ImageHosts) == 0 {
		t.Fatalf(
			"capability projection is incomplete: owner=%q api=%q trackers=%d clients=%d hosts=%d",
			capabilities.OwnerID,
			capabilities.APIVersion,
			len(capabilities.Trackers),
			len(capabilities.TorrentClients),
			len(capabilities.ImageHosts),
		)
	}
	if strings.Contains(response.Body.String(), "capability-secret-value") ||
		strings.Contains(response.Body.String(), "capability-image-secret") ||
		strings.Contains(response.Body.String(), "capability-samaritano-secret") {
		t.Fatal("capability projection exposed a configured secret")
	}
	if !slices.ContainsFunc(capabilities.ImageHosts, func(host api.ReleaseWorkflowCapabilityResource) bool {
		return host.ID == "samaritano" && host.Configured
	}) {
		t.Fatalf("Samaritano image host was not exposed as configured: %#v", capabilities.ImageHosts)
	}

	executeOnlyStore, err := newAPITokenStore([]APITokenCredential{{
		Token:   apiV1TestToken,
		OwnerID: "qui",
		Scopes:  []APITokenScope{APITokenScopeWorkflowExecute},
	}})
	if err != nil {
		t.Fatalf("new execute-only token store: %v", err)
	}
	server.apiTokens = executeOnlyStore
	denied := httptest.NewRecorder()
	mux.ServeHTTP(denied, request)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("execute-only capability status = %d", denied.Code)
	}

	missing := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/capabilities", nil)
	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, missing)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("missing-token capability status = %d", unauthorized.Code)
	}
}

func TestAPIV1CompositeUploadRejectsMissingAuthorityAndUnknownFields(t *testing.T) {
	t.Parallel()

	store, err := newAPITokenStore([]APITokenCredential{{
		Token:   apiV1TestToken,
		OwnerID: "uploader",
		Scopes:  []APITokenScope{APITokenScopeWorkflowExecute},
	}})
	if err != nil {
		t.Fatalf("new upload token store: %v", err)
	}
	server := &Server{
		apiTokens:      store,
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
	}
	mux := http.NewServeMux()
	server.registerV1Routes(mux)

	missingKey := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/api/v1/uploads",
		strings.NewReader(`{"source":{"path":"D:\\Example Release 2026"},"unattended":{}}`),
	)
	missingKey.Header.Set("Authorization", "Bearer "+apiV1TestToken)
	missingKeyResponse := httptest.NewRecorder()
	mux.ServeHTTP(missingKeyResponse, missingKey)
	if missingKeyResponse.Code != http.StatusBadRequest {
		t.Fatalf("missing upload idempotency status = %d", missingKeyResponse.Code)
	}

	unknown := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/api/v1/uploads",
		strings.NewReader(`{"source":{"path":"D:\\Example Release 2026"},"unattended":{},"unsafe":true}`),
	)
	unknown.Header.Set("Authorization", "Bearer "+apiV1TestToken)
	unknown.Header.Set("Idempotency-Key", "upload-unknown")
	unknownResponse := httptest.NewRecorder()
	mux.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown upload field status = %d", unknownResponse.Code)
	}

	stale := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/api/v1/uploads/workflow-upload/feedback",
		strings.NewReader(
			`{"action":{"id":"action-example","workflowRevision":7},"response":{"kind":"trackerApproval","trackerApproval":{"confirmed":true,"trackerIds":["EXAMPLE"]}}}`,
		),
	)
	stale.Header.Set("Authorization", "Bearer "+apiV1TestToken)
	stale.Header.Set("Idempotency-Key", "upload-stale")
	stale.Header.Set("If-Match", `"6"`)
	staleResponse := httptest.NewRecorder()
	mux.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("stale upload feedback status = %d", staleResponse.Code)
	}
}

func TestAPIV1AppliesGeneralRateLimitBeforeAuthentication(t *testing.T) {
	t.Parallel()

	store, err := newAPITokenStore([]APITokenCredential{{Token: apiV1TestToken, OwnerID: "reader"}})
	if err != nil {
		t.Fatalf("new token store: %v", err)
	}
	server := &Server{
		apiTokens:      store,
		generalLimiter: newFixedWindowLimiter(0, time.Minute),
	}
	mux := http.NewServeMux()
	server.registerV1Routes(mux)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/workflows/workflow-1", nil)
	request.Header.Set("Authorization", "Bearer "+apiV1TestToken)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited status = %d, want 429", response.Code)
	}
}

func TestAPIV1CommandRoutesDecodeSharedWorkflowRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		method   string
		segments []string
		body     string
		want     any
	}{
		{"invalidate", http.MethodPost, []string{"workflow-1", "trackers", "invalidate"}, `{"trackerIds":["EXAMPLE"]}`, releaseworkflow.InvalidateTrackersCommand{}},
		{"select media", http.MethodPut, []string{"workflow-1", "media", "media-1", "selection"}, `{"media":{"revision":1},"artifactIds":["artifact-1"],"selected":true}`, releaseworkflow.SetMediaSelectionCommand{}},
		{"delete media", http.MethodPost, []string{"workflow-1", "media", "media-1", "delete"}, `{"media":{"revision":1},"artifactIds":["artifact-1"]}`, releaseworkflow.DeleteMediaArtifactsCommand{}},
		{"reorder media", http.MethodPut, []string{"workflow-1", "media", "media-1", "reorder"}, `{"media":{"revision":1},"artifactIds":["artifact-1"]}`, releaseworkflow.ReorderMediaArtifactsCommand{}},
		{"attach media", http.MethodPost, []string{"workflow-1", "media", "attach"}, `{"attachments":[{"resource":{"id":"resource-1"},"kind":"screenshot","purpose":"final"}]}`, releaseworkflow.AttachMediaArtifactsCommand{}},
		{"upload images", http.MethodPost, []string{"workflow-1", "media", "media-1", "images", "upload"}, `{"media":{"revision":1},"artifactIds":["artifact-1"],"host":"imgbox"}`, releaseworkflow.UploadMediaImagesCommand{}},
		{"retry image host", http.MethodPost, []string{"workflow-1", "media", "media-1", "images", "retry"}, `{"media":{"revision":1},"artifactIds":["artifact-1"],"host":"imgbox"}`, releaseworkflow.UploadMediaImagesCommand{}},
		{"remove hosted images", http.MethodPost, []string{"workflow-1", "media", "media-1", "images", "remove"}, `{"media":{"revision":1},"artifactIds":["artifact-1"]}`, releaseworkflow.RemoveHostedImagesCommand{}},
		{"save description", http.MethodPost, []string{"workflow-1", "descriptions", "description-1", "groups", "group-1", "save"}, `{"override":{"descriptions":{"revision":1},"source":"synthetic"}}`, releaseworkflow.SaveDescriptionOverrideCommand{}},
		{"reset description", http.MethodPost, []string{"workflow-1", "descriptions", "description-1", "groups", "group-1", "reset"}, `{"descriptions":{"revision":1}}`, releaseworkflow.ResetDescriptionOverrideCommand{}},
		{"retry upload", http.MethodPost, []string{"workflow-1", "uploads", "result-1", "retry"}, `{"retry":{"result":{"revision":1},"trackerIds":["EXAMPLE"]}}`, releaseworkflow.RetryFailedUploadsCommand{}},
		{"retry client injection", http.MethodPost, []string{"workflow-1", "uploads", "result-1", "client-injections", "retry"}, `{"retry":{"result":{"revision":1},"trackerIds":["EXAMPLE"]}}`, releaseworkflow.RetryClientInjectionsCommand{}},
		{"cancel", http.MethodPost, []string{"workflow-1", "cancel"}, `{}`, releaseworkflow.CancelWorkflowCommand{}},
	}

	server := &Server{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequestWithContext(context.Background(), test.method, "/", strings.NewReader(test.body))
			response := httptest.NewRecorder()
			command, ok := server.apiV1WorkflowCommand(
				response,
				request,
				api.WorkflowID("workflow-1"),
				3,
				"idempotency-1",
				test.segments,
			)
			if !ok {
				t.Fatalf("route rejected shared request: status=%d body=%s", response.Code, response.Body.String())
			}
			if reflect.TypeOf(command) != reflect.TypeOf(test.want) {
				t.Fatalf("command type = %T, want %T", command, test.want)
			}
		})
	}
}

func TestAPIV1RetiredStageRoutesAreNotCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		method   string
		segments []string
	}{
		{"replace facts", http.MethodPut, []string{"workflow-1", "facts"}},
		{"prepare", http.MethodPost, []string{"workflow-1", "prepare"}},
		{"reset", http.MethodPost, []string{"workflow-1", "reset"}},
		{"candidate", http.MethodPost, []string{"workflow-1", "candidates", "candidate-1", "select"}},
		{"project", http.MethodPost, []string{"workflow-1", "trackers", "project"}},
		{"preflight", http.MethodPost, []string{"workflow-1", "trackers", "preflight"}},
		{"check duplicates", http.MethodPost, []string{"workflow-1", "duplicates", "check"}},
		{"decide duplicates", http.MethodPost, []string{"workflow-1", "duplicates", "decide"}},
		{"capture media", http.MethodPost, []string{"workflow-1", "media", "capture"}},
		{"generate descriptions", http.MethodPost, []string{"workflow-1", "descriptions", "generate"}},
		{"dry run", http.MethodPost, []string{"workflow-1", "uploads", "dry-run"}},
		{"upload", http.MethodPost, []string{"workflow-1", "uploads", "execute"}},
		{"resolve action", http.MethodPost, []string{"workflow-1", "actions", "action-1"}},
	}

	server := &Server{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequestWithContext(context.Background(), test.method, "/", strings.NewReader(`{}`))
			response := httptest.NewRecorder()
			if command, ok := server.apiV1WorkflowCommand(
				response,
				request,
				api.WorkflowID("workflow-1"),
				3,
				"idempotency-1",
				test.segments,
			); ok || command != nil || response.Code != http.StatusNotFound {
				t.Fatalf("retired route command=%T ok=%v status=%d", command, ok, response.Code)
			}
		})
	}
}

func TestReleaseWorkflowOpenAPICoversRuntimeRoutes(t *testing.T) {
	t.Parallel()

	var document struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]map[string]struct {
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Required []string `json:"required"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(releaseWorkflowOpenAPI, &document); err != nil {
		t.Fatalf("parse embedded OpenAPI: %v", err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("openapi = %q", document.OpenAPI)
	}
	want := map[string]string{
		"/capabilities":                                                                "get",
		"/continuations":                                                               "post",
		"/workflows/{workflowId}":                                                      "get",
		"/workflows/{workflowId}/trackers/invalidate":                                  "post",
		"/workflows/{workflowId}/media/plan":                                           "get",
		"/workflows/{workflowId}/media/previews":                                       "post",
		"/workflows/{workflowId}/media/previews/{previewId}":                           "get",
		"/workflows/{workflowId}/media/resources":                                      "post",
		"/workflows/{workflowId}/media/attach":                                         "post",
		"/workflows/{workflowId}/media/{mediaId}/artifacts/{artifactId}":               "get",
		"/workflows/{workflowId}/media/{mediaId}/selection":                            "put",
		"/workflows/{workflowId}/media/{mediaId}/reorder":                              "put",
		"/workflows/{workflowId}/media/{mediaId}/delete":                               "post",
		"/workflows/{workflowId}/media/{mediaId}/images/upload":                        "post",
		"/workflows/{workflowId}/media/{mediaId}/images/retry":                         "post",
		"/workflows/{workflowId}/media/{mediaId}/images/remove":                        "post",
		"/workflows/{workflowId}/descriptions/{descriptionId}/groups/{groupKey}/save":  "post",
		"/workflows/{workflowId}/descriptions/{descriptionId}/groups/{groupKey}/reset": "post",
		"/workflows/{workflowId}/uploads/{resultId}/retry":                             "post",
		"/workflows/{workflowId}/cancel":                                               "post",
		"/workflows/{workflowId}/operations/{operationId}":                             "get",
		"/workflows/{workflowId}/operations/{operationId}/cancel":                      "post",
	}
	for requestPath, method := range want {
		methods, ok := document.Paths[requestPath]
		if !ok {
			t.Fatalf("OpenAPI missing path %s", requestPath)
		}
		if _, ok := methods[method]; !ok {
			t.Fatalf("OpenAPI path %s missing method %s", requestPath, method)
		}
	}
	for _, requestPath := range []string{
		"/workflows/{workflowId}/media/{mediaId}/images/upload",
		"/workflows/{workflowId}/media/{mediaId}/images/retry",
		"/workflows/{workflowId}/uploads/{resultId}/retry",
	} {
		if _, ok := document.Paths[requestPath]["post"].Responses["202"]; !ok {
			t.Fatalf("OpenAPI long-running path %s does not return 202", requestPath)
		}
	}
	for _, requestPath := range []string{
		"/workflows",
		"/workflows/{workflowId}/facts",
		"/workflows/{workflowId}/prepare",
		"/workflows/{workflowId}/reset",
		"/workflows/{workflowId}/candidates/{candidateId}/select",
		"/workflows/{workflowId}/trackers/project",
		"/workflows/{workflowId}/trackers/preflight",
		"/workflows/{workflowId}/duplicates/check",
		"/workflows/{workflowId}/duplicates/decide",
		"/workflows/{workflowId}/media/capture",
		"/workflows/{workflowId}/descriptions/generate",
		"/workflows/{workflowId}/uploads/dry-run",
		"/workflows/{workflowId}/uploads/execute",
		"/workflows/{workflowId}/actions/{actionId}",
	} {
		if _, ok := document.Paths[requestPath]; ok {
			t.Fatalf("OpenAPI retained retired stage path %s", requestPath)
		}
	}
	required := make(map[string]struct{})
	for _, field := range document.Components.Schemas["Operation"].Required {
		required[field] = struct{}{}
	}
	for _, field := range []string{"sequence", "command", "completed", "total", "updatedAt"} {
		if _, ok := required[field]; !ok {
			t.Fatalf("OpenAPI operation schema does not require %s", field)
		}
	}
}
