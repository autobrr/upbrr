// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/pkg/api"
)

type apiV1Context struct {
	Token apiTokenStatus
}

type apiV1Handler func(*Server, http.ResponseWriter, *http.Request, apiV1Context, map[string]string)

type apiV1Route struct {
	Method      string
	Path        string
	OperationID string
	Summary     string
	Public      bool
	Request     any
	Response    any
	Handler     apiV1Handler
}

func (s *Server) registerAPIV1Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/openapi.json", s.handleOpenAPI)
	mux.HandleFunc("/api/docs", s.handleSwaggerUI)
	mux.HandleFunc("/api/docs/", s.handleSwaggerUI)
	mux.HandleFunc("/api/v1/", s.handleAPIV1)
}

func apiV1Routes() []apiV1Route {
	return []apiV1Route{
		{Method: http.MethodGet, Path: "/api/v1/auth/status", OperationID: "getAuthStatus", Summary: "Get REST auth status", Public: true, Response: apiAuthStatusResponse{}, Handler: handleAPIAuthStatus},
		{Method: http.MethodPost, Path: "/api/v1/auth/bootstrap", OperationID: "bootstrapAuth", Summary: "Create initial REST auth", Public: true, Request: apiAuthCredentialsRequest{}, Response: apiAuthStatusResponse{}, Handler: handleAPIAuthBootstrap},
		{Method: http.MethodPost, Path: "/api/v1/auth/token", OperationID: "createLoginToken", Summary: "Create bearer token from credentials", Public: true, Request: apiAuthCredentialsRequest{}, Response: apiAuthStatusResponse{}, Handler: handleAPIAuthToken},
		{Method: http.MethodPost, Path: "/api/v1/auth/logout", OperationID: "revokeCurrentToken", Summary: "Revoke current bearer token", Response: apiOKResponse{}, Handler: handleAPIAuthLogout},
		{Method: http.MethodPut, Path: "/api/v1/auth/browse-policy", OperationID: "saveBrowsePolicy", Summary: "Save web browse policy", Request: apiBrowsePolicyRequest{}, Response: apiAuthStatusResponse{}, Handler: handleAPISaveBrowsePolicy},
		{Method: http.MethodGet, Path: "/api/v1/auth/web-status", OperationID: "getWebAuthStatus", Summary: "Get web auth material status", Response: apiWebAuthStatusResponse{}, Handler: handleAPIWebAuthStatus},
		{Method: http.MethodGet, Path: "/api/v1/tokens", OperationID: "listAPITokens", Summary: "List API tokens", Response: []apiTokenStatusResponse{}, Handler: handleAPIListTokens},
		{Method: http.MethodPost, Path: "/api/v1/tokens", OperationID: "createAPIToken", Summary: "Create API token", Request: apiTokenRequest{}, Response: apiCreatedTokenResponse{}, Handler: handleAPICreateToken},
		{Method: http.MethodDelete, Path: "/api/v1/tokens/{tokenID}", OperationID: "revokeAPIToken", Summary: "Revoke API token", Response: apiWebAuthStatusResponse{}, Handler: handleAPIRevokeToken},
		{Method: http.MethodGet, Path: "/api/v1/status", OperationID: "getStatus", Summary: "Get API status", Response: apiStatusResponse{}, Handler: handleAPIStatus},
		{Method: http.MethodGet, Path: "/api/v1/events", OperationID: "streamEvents", Summary: "Stream API events", Handler: handleAPIEvents},
		{Method: http.MethodGet, Path: "/api/v1/ui-state", OperationID: "listUIStates", Summary: "List UI states", Response: api.UIStateList{}, Handler: handleAPIListUIStates},
		{Method: http.MethodGet, Path: "/api/v1/ui-state/{stateID}", OperationID: "getUIState", Summary: "Get UI state", Response: api.UIStateRecord{}, Handler: handleAPIGetUIState},
		{Method: http.MethodPost, Path: "/api/v1/ui-state", OperationID: "saveUIState", Summary: "Save UI state", Request: uiStateSaveRequest{}, Response: apiOKResponse{}, Handler: handleAPISaveUIState},
		{Method: http.MethodGet, Path: "/api/v1/config/current", OperationID: "getConfig", Summary: "Get current config", Response: "", Handler: handleAPIGetConfig},
		{Method: http.MethodGet, Path: "/api/v1/config/default", OperationID: "getDefaultConfig", Summary: "Get default config", Response: "", Handler: handleAPIGetDefaultConfig},
		{Method: http.MethodGet, Path: "/api/v1/config/export", OperationID: "exportConfig", Summary: "Export config", Response: "", Handler: handleAPIExportConfig},
		{Method: http.MethodPut, Path: "/api/v1/config/current", OperationID: "saveConfig", Summary: "Save config", Request: configPayloadRequest{}, Response: apiOKResponse{}, Handler: handleAPISaveConfig},
		{Method: http.MethodPost, Path: "/api/v1/config/import", OperationID: "importConfig", Summary: "Import config", Request: importConfigRequest{}, Response: importConfigResponse{}, Handler: handleAPIImportConfig},
		{Method: http.MethodPost, Path: "/api/v1/files/browse", OperationID: "browseDirectory", Summary: "Browse server-side files", Request: api.BrowseDirectoryRequest{}, Response: api.BrowseDirectoryResponse{}, Handler: handleAPIBrowseDirectory},
		{Method: http.MethodPost, Path: "/api/v1/files/native/file", OperationID: "browseNativeFile", Summary: "Open native file picker", Response: "", Handler: handleAPIBrowseNativeFile},
		{Method: http.MethodPost, Path: "/api/v1/files/native/folder", OperationID: "browseNativeFolder", Summary: "Open native folder picker", Response: "", Handler: handleAPIBrowseNativeFolder},
		{Method: http.MethodPost, Path: "/api/v1/media/disc-type", OperationID: "detectDiscType", Summary: "Detect disc type", Request: pathRequest{}, Response: "", Handler: handleAPIDetectDiscType},
		{Method: http.MethodPost, Path: "/api/v1/metadata/fetch", OperationID: "fetchMetadata", Summary: "Fetch metadata", Request: metadataRequest{}, Response: api.MetadataPreview{}, Handler: handleAPIFetchMetadata},
		{Method: http.MethodPost, Path: "/api/v1/metadata/reset", OperationID: "resetMetadata", Summary: "Reset metadata", Request: metadataRequest{}, Response: api.MetadataPreview{}, Handler: handleAPIResetMetadata},
		{Method: http.MethodPost, Path: "/api/v1/dupes/check", OperationID: "checkDupes", Summary: "Check dupes synchronously", Request: contentRequest{}, Response: api.DupeCheckSummary{}, Handler: handleAPICheckDupes},
		{Method: http.MethodPost, Path: "/api/v1/jobs/dupes", OperationID: "startDupeJob", Summary: "Start dupe-check job", Request: contentRequest{}, Response: jobIDResponse{}, Handler: handleAPIStartDupeJob},
		{Method: http.MethodGet, Path: "/api/v1/jobs/dupes/{jobID}", OperationID: "getDupeJob", Summary: "Get dupe-check job snapshot", Response: DupeCheckSnapshot{}, Handler: handleAPIGetDupeJob},
		{Method: http.MethodDelete, Path: "/api/v1/jobs/dupes/{jobID}", OperationID: "cancelDupeJob", Summary: "Cancel dupe-check job", Response: apiOKResponse{}, Handler: handleAPICancelDupeJob},
		{Method: http.MethodPost, Path: "/api/v1/preparation/fetch", OperationID: "fetchPreparation", Summary: "Fetch preparation preview", Request: preparationRequest{}, Response: api.PreparationPreview{}, Handler: handleAPIFetchPreparation},
		{Method: http.MethodPost, Path: "/api/v1/tracker-dry-run/fetch", OperationID: "fetchTrackerDryRun", Summary: "Fetch tracker dry run", Request: trackerDryRunRequest{}, Response: api.TrackerDryRunPreview{}, Handler: handleAPIFetchTrackerDryRun},
		{Method: http.MethodPost, Path: "/api/v1/description-builder/fetch", OperationID: "fetchDescriptionBuilder", Summary: "Fetch description builder", Request: preparationRequest{}, Response: api.DescriptionBuilderPreview{}, Handler: handleAPIFetchDescriptionBuilder},
		{Method: http.MethodPost, Path: "/api/v1/description/render", OperationID: "renderDescription", Summary: "Render description", Request: renderDescriptionRequest{}, Response: "", Handler: handleAPIRenderDescription},
		{Method: http.MethodPost, Path: "/api/v1/description/override", OperationID: "saveDescriptionOverride", Summary: "Save description override", Request: saveDescriptionOverrideRequest{}, Response: api.DescriptionBuilderGroup{}, Handler: handleAPISaveDescriptionOverride},
		{Method: http.MethodPost, Path: "/api/v1/playlists/discover", OperationID: "discoverPlaylists", Summary: "Discover playlists", Request: pathRequest{}, Response: []api.PlaylistInfo{}, Handler: handleAPIDiscoverPlaylists},
		{Method: http.MethodPut, Path: "/api/v1/playlists/selection", OperationID: "savePlaylistSelection", Summary: "Save playlist selection", Request: playlistSelectionRequest{}, Response: apiOKResponse{}, Handler: handleAPISavePlaylistSelection},
		{Method: http.MethodPost, Path: "/api/v1/playlists/selection/get", OperationID: "loadPlaylistSelection", Summary: "Load playlist selection", Request: pathRequest{}, Response: api.PlaylistSelection{}, Handler: handleAPILoadPlaylistSelection},
		{Method: http.MethodPost, Path: "/api/v1/screenshots/plan", OperationID: "fetchScreenshotPlan", Summary: "Fetch screenshot plan", Request: screenshotPlanRequest{}, Response: api.ScreenshotPlan{}, Handler: handleAPIFetchScreenshotPlan},
		{Method: http.MethodPost, Path: "/api/v1/screenshots/generate", OperationID: "generateScreenshots", Summary: "Generate screenshots", Request: generateScreenshotsRequest{}, Response: api.ScreenshotResult{}, Handler: handleAPIGenerateScreenshots},
		{Method: http.MethodPost, Path: "/api/v1/screenshots/preview-frame", OperationID: "previewScreenshotFrame", Summary: "Preview screenshot frame", Request: previewScreenshotFrameRequest{}, Response: "", Handler: handleAPIPreviewScreenshotFrame},
		{Method: http.MethodPost, Path: "/api/v1/screenshots/read-image", OperationID: "readScreenshotImage", Summary: "Read screenshot image", Request: pathRequest{}, Response: "", Handler: handleAPIReadScreenshotImage},
		{Method: http.MethodPost, Path: "/api/v1/screenshots/delete", OperationID: "deleteScreenshot", Summary: "Delete screenshot", Request: screenshotImageRequest{}, Response: apiOKResponse{}, Handler: handleAPIDeleteScreenshot},
		{Method: http.MethodPost, Path: "/api/v1/screenshots/delete-tracker-url", OperationID: "deleteTrackerImageURL", Summary: "Delete tracker image URL", Request: trackerImageURLRequest{}, Response: apiOKResponse{}, Handler: handleAPIDeleteTrackerImageURL},
		{Method: http.MethodPut, Path: "/api/v1/screenshots/final-selections", OperationID: "saveFinalScreenshotSelections", Summary: "Save final screenshot selections", Request: finalScreenshotSelectionsRequest{}, Response: apiOKResponse{}, Handler: handleAPISaveFinalScreenshotSelections},
		{Method: http.MethodPost, Path: "/api/v1/screenshots/upload-candidates", OperationID: "listUploadCandidates", Summary: "List upload candidates", Request: screenshotPlanRequest{}, Response: []api.ScreenshotImage{}, Handler: handleAPIListUploadCandidates},
		{Method: http.MethodPost, Path: "/api/v1/screenshots/uploaded-images", OperationID: "listUploadedImages", Summary: "List uploaded images", Request: screenshotPlanRequest{}, Response: []api.UploadedImageLink{}, Handler: handleAPIListUploadedImages},
		{Method: http.MethodPost, Path: "/api/v1/screenshots/upload", OperationID: "uploadImages", Summary: "Upload images", Request: uploadImagesRequest{}, Response: []api.UploadedImageLink{}, Handler: handleAPIUploadImages},
		{Method: http.MethodPost, Path: "/api/v1/screenshots/delete-uploaded", OperationID: "deleteUploadedImage", Summary: "Delete uploaded image", Request: deleteUploadedImageRequest{}, Response: apiOKResponse{}, Handler: handleAPIDeleteUploadedImage},
		{Method: http.MethodGet, Path: "/api/v1/trackers/known", OperationID: "listKnownTrackers", Summary: "List known trackers", Response: []string{}, Handler: handleAPIListKnownTrackers},
		{Method: http.MethodGet, Path: "/api/v1/history", OperationID: "listHistory", Summary: "List history", Response: []api.HistoryEntry{}, Handler: handleAPIListHistory},
		{Method: http.MethodPost, Path: "/api/v1/history/overview", OperationID: "getHistoryOverview", Summary: "Get history overview", Request: sourcePathRequest{}, Response: api.HistoryOverview{}, Handler: handleAPIGetHistoryOverview},
		{Method: http.MethodPost, Path: "/api/v1/history/delete", OperationID: "deleteHistoryRelease", Summary: "Delete history release", Request: sourcePathRequest{}, Response: apiOKResponse{}, Handler: handleAPIDeleteHistoryRelease},
		{Method: http.MethodGet, Path: "/api/v1/logs/path", OperationID: "getLogPath", Summary: "Get log path", Response: "", Handler: handleAPIGetLogPath},
		{Method: http.MethodPost, Path: "/api/v1/logs/recent", OperationID: "getRecentLogs", Summary: "Get recent logs", Request: recentLogsRequest{}, Response: []any{}, Handler: handleAPIGetRecentLogs},
		{Method: http.MethodPost, Path: "/api/v1/logs/streams", OperationID: "startLogStream", Summary: "Start log stream", Response: "", Handler: handleAPIStartLogStream},
		{Method: http.MethodDelete, Path: "/api/v1/logs/streams/{streamID}", OperationID: "stopLogStream", Summary: "Stop log stream", Response: apiOKResponse{}, Handler: handleAPIStopLogStream},
		{Method: http.MethodGet, Path: "/api/v1/logs/exclusions", OperationID: "getLogExclusions", Summary: "Get log exclusions", Response: []string{}, Handler: handleAPIGetLogExclusions},
		{Method: http.MethodPut, Path: "/api/v1/logs/exclusions", OperationID: "updateLogExclusions", Summary: "Update log exclusions", Request: logExclusionsRequest{}, Response: apiOKResponse{}, Handler: handleAPIUpdateLogExclusions},
		{Method: http.MethodPost, Path: "/api/v1/jobs/uploads", OperationID: "startTrackerUploadJob", Summary: "Start tracker upload job", Request: trackerUploadRequest{}, Response: jobIDResponse{}, Handler: handleAPIStartTrackerUploadJob},
		{Method: http.MethodGet, Path: "/api/v1/jobs/uploads/{jobID}", OperationID: "getTrackerUploadJob", Summary: "Get tracker upload job snapshot", Response: TrackerUploadSnapshot{}, Handler: handleAPIGetTrackerUploadJob},
		{Method: http.MethodDelete, Path: "/api/v1/jobs/uploads/{jobID}", OperationID: "cancelTrackerUploadJob", Summary: "Cancel tracker upload job", Response: apiOKResponse{}, Handler: handleAPICancelTrackerUploadJob},
		{Method: http.MethodPost, Path: "/api/v1/jobs/uploads/{jobID}/retry", OperationID: "retryTrackerUploadJob", Summary: "Retry failed tracker upload job", Response: jobIDResponse{}, Handler: handleAPIRetryTrackerUploadJob},
	}
}

func (s *Server) handleAPIV1(w http.ResponseWriter, r *http.Request) {
	if !s.allowGeneralRequest(r) {
		writeAPIError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	for _, route := range apiV1Routes() {
		if route.Method != r.Method {
			continue
		}
		params, ok := matchAPIPath(route.Path, r.URL.Path)
		if !ok {
			continue
		}
		ctx := apiV1Context{}
		if !route.Public {
			token, ok := s.authenticateAPIToken(w, r)
			if !ok {
				return
			}
			ctx.Token = token
		}
		route.Handler(s, w, r, ctx, params)
		return
	}
	writeAPIError(w, http.StatusNotFound, "api route not found")
}

func (s *Server) authenticateAPIToken(w http.ResponseWriter, r *http.Request) (apiTokenStatus, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	prefix := "Bearer "
	if !strings.HasPrefix(header, prefix) {
		writeAPIError(w, http.StatusUnauthorized, "bearer token required")
		return apiTokenStatus{}, false
	}
	status, ok, err := s.auth.VerifyAPIToken(strings.TrimSpace(strings.TrimPrefix(header, prefix)))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "api token validation failed")
		return apiTokenStatus{}, false
	}
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "invalid bearer token")
		return apiTokenStatus{}, false
	}
	return status, true
}

func matchAPIPath(pattern string, actual string) (map[string]string, bool) {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	actualParts := strings.Split(strings.Trim(actual, "/"), "/")
	if len(patternParts) != len(actualParts) {
		return nil, false
	}
	params := make(map[string]string)
	for idx, part := range patternParts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			params[strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")] = actualParts[idx]
			continue
		}
		if part != actualParts[idx] {
			return nil, false
		}
	}
	return params, true
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, apiErrorResponse{Error: message})
}

func writeAPIResult(w http.ResponseWriter, values ...any) {
	if len(values) != 2 {
		writeAPIError(w, http.StatusInternalServerError, "invalid api result")
		return
	}
	value := values[0]
	var err error
	if values[1] != nil {
		var ok bool
		err, ok = values[1].(error)
		if !ok {
			writeAPIError(w, http.StatusInternalServerError, "invalid api error")
			return
		}
	}
	if err != nil {
		var rescanErr *api.BDMVRescanRequiredError
		if errors.As(err, &rescanErr) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":              err.Error(),
				"code":               api.ErrCodeBDMVRescanRequired,
				"source_path":        rescanErr.SourcePath,
				"selected_playlists": rescanErr.SelectedPlaylists,
				"cached_playlists":   rescanErr.CachedPlaylists,
				"missing_playlists":  rescanErr.MissingPlaylists,
			})
			return
		}
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func writeAPICall[T any](w http.ResponseWriter, call func() (T, error)) {
	value, err := call()
	writeAPIResult(w, value, err)
}

func decodeOrError(w http.ResponseWriter, r *http.Request, dest any) bool {
	if err := decodeJSON(r, dest); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func handleAPIAuthStatus(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	if strings.HasPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ") {
		token, ok := s.authenticateAPIToken(w, r)
		if !ok {
			return
		}
		record, err := s.auth.Load()
		if err == nil {
			writeJSON(w, http.StatusOK, s.authStatusPayload(r, record.Username, ""))
			return
		}
		writeJSON(w, http.StatusOK, s.authStatusPayload(r, token.Name, ""))
		return
	}
	writeJSON(w, http.StatusOK, s.authStatusPayload(r, "", ""))
}

func handleAPIAuthBootstrap(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	if !s.allowAuthRequest(r) {
		writeAPIError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	var req apiAuthCredentialsRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	exists, err := s.auth.Exists()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if exists {
		record, err := s.auth.Load()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if strings.TrimSpace(record.Username) != authmaterial.DesktopUsername {
			writeAPIError(w, http.StatusBadRequest, "web auth already exists")
			return
		}
		if err := s.auth.BootstrapReplacingDesktop(req.Username, req.Password); err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if err := s.auth.Bootstrap(req.Username, req.Password); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	token, username, err := s.createWebSessionToken(req.Username)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.authStatusPayload(r, username, token))
}

func handleAPIAuthToken(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	if !s.allowAuthRequest(r) {
		writeAPIError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	var req apiAuthCredentialsRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	record, err := s.auth.Load()
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if strings.TrimSpace(record.Username) == authmaterial.DesktopUsername || !strings.EqualFold(strings.TrimSpace(record.Username), strings.TrimSpace(req.Username)) {
		writeAPIError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	ok, upgrade := verifyPasswordWithUpgrade(req.Password, record.PasswordHash)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if upgrade {
		hash, err := hashPassword(req.Password)
		if err == nil {
			_ = s.auth.UpdatePasswordHash(record.Username, hash)
		}
	}
	token, username, err := s.createWebSessionToken(record.Username)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.authStatusPayload(r, username, token))
}

func handleAPIAuthLogout(s *Server, w http.ResponseWriter, _ *http.Request, ctx apiV1Context, _ map[string]string) {
	writeAPIResult(w, apiOKResponse{OK: true}, s.auth.RevokeAPIToken(ctx.Token.ID))
}

func handleAPISaveBrowsePolicy(s *Server, w http.ResponseWriter, r *http.Request, ctx apiV1Context, _ map[string]string) {
	var req apiBrowsePolicyRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	roots, err := normalizeBrowsePolicyRoots(splitBrowsePolicyRoots(req.BrowseRoot))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !req.AllowUnrestrictedBrowse && len(roots) == 0 {
		writeAPIError(w, http.StatusBadRequest, "at least one browse root is required unless unrestricted browsing is explicitly allowed")
		return
	}
	record, err := s.auth.Load()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	record.BrowseRoot = joinBrowsePolicyRoots(roots)
	record.AllowUnrestrictedBrowse = req.AllowUnrestrictedBrowse
	if err := s.auth.UpdateRecord(record); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.authStatusPayload(r, record.Username, ""))
}

func handleAPIWebAuthStatus(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, _ map[string]string) {
	writeJSON(w, http.StatusOK, s.webAuthStatusPayload())
}

func handleAPIListTokens(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, _ map[string]string) {
	tokens, err := s.auth.ListAPITokens()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, formatAPITokenStatuses(tokens))
}

func handleAPICreateToken(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req apiTokenRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	created, err := s.auth.CreateAPIToken(req.Name)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, apiCreatedTokenResponse{
		Token:  created.Token,
		Record: formatAPITokenStatus(apiTokenStatus{ID: created.Record.ID, Name: created.Record.Name, CreatedAt: created.Record.CreatedAt}),
	})
}

func handleAPIRevokeToken(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, params map[string]string) {
	if err := s.auth.RevokeAPIToken(params["tokenID"]); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.webAuthStatusPayload())
}

func handleAPIStatus(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, _ map[string]string) {
	writeJSON(w, http.StatusOK, apiStatusResponse{OK: true, Version: "v1", CoreReady: s.backend != nil && s.backend.core != nil})
}

func handleAPIListUIStates(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, _ map[string]string) {
	writeAPICall(w, s.backend.ListUIStates)
}

func handleAPIGetUIState(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, params map[string]string) {
	writeAPICall(w, func() (api.UIStateRecord, error) { return s.backend.GetUIState(params["stateID"]) })
}

func handleAPISaveUIState(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req uiStateSaveRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPIResult(w, apiOKResponse{OK: true}, s.backend.SaveUIState(req.ID, req.Label, req.State))
}

func (s *Server) createWebSessionToken(username string) (string, string, error) {
	created, err := s.auth.CreateAPIToken(authmaterial.WebSessionAPITokenName)
	if err != nil {
		return "", "", err
	}
	return created.Token, strings.TrimSpace(username), nil
}

func (s *Server) authStatusPayload(r *http.Request, username string, bearerToken string) apiAuthStatusResponse {
	payload := apiAuthStatusResponse{
		Authenticated:       strings.TrimSpace(username) != "",
		NeedsSetup:          true,
		Username:            strings.TrimSpace(username),
		BearerToken:         strings.TrimSpace(bearerToken),
		NativeBrowseEnabled: s.nativeBrowseAvailable(r),
	}
	record, err := s.auth.Load()
	if err != nil {
		return payload
	}
	if strings.TrimSpace(record.Username) != "" && strings.TrimSpace(record.Username) != authmaterial.DesktopUsername {
		payload.NeedsSetup = false
	}
	if payload.Username == "" && payload.Authenticated {
		payload.Username = record.Username
	}
	if payload.Authenticated {
		browseRoots := recordBrowseRoots(record)
		payload.BrowseRoot = joinBrowsePolicyRoots(browseRoots)
		payload.AllowUnrestrictedBrowse = record.AllowUnrestrictedBrowse
		payload.NeedsBrowsePolicy = !record.AllowUnrestrictedBrowse && len(browseRoots) == 0
	}
	return payload
}

func (s *Server) webAuthStatusPayload() apiWebAuthStatusResponse {
	dbPath := ""
	if s != nil {
		dbPath = strings.TrimSpace(s.cfg.MainSettings.DBPath)
	}
	status := apiWebAuthStatusResponse{
		Path:      AuthFilePath(dbPath),
		CanCreate: true,
		Message:   "No web auth file found. Secrets will continue to be stored in plaintext until one is created.",
	}
	record, err := s.auth.Load()
	if err != nil {
		return status
	}
	status.Exists = true
	status.CanCreate = strings.TrimSpace(record.Username) == authmaterial.DesktopUsername
	status.Usable = strings.TrimSpace(record.Username) != "" && strings.TrimSpace(record.Username) != authmaterial.DesktopUsername && strings.TrimSpace(record.PasswordHash) != ""
	status.Username = record.Username
	status.AllowUnencryptedExport = record.AllowUnencryptedExport
	status.BrowseRoot = record.BrowseRoot
	status.AllowUnrestrictedBrowse = record.AllowUnrestrictedBrowse
	status.EncryptionEnabled = status.Usable
	status.APITokens = formatStoredAPITokenStatuses(record.APITokens)
	switch {
	case status.Usable:
		status.Message = "Secret encryption is enabled for this installation."
	case strings.TrimSpace(record.Username) == authmaterial.DesktopUsername:
		status.Message = "Desktop REST access is enabled. Create web auth to use encrypted secret storage."
	default:
		status.Message = "web-auth.json exists but is not usable for secret encryption."
	}
	return status
}

func formatAPITokenStatuses(tokens []apiTokenStatus) []apiTokenStatusResponse {
	if len(tokens) == 0 {
		return nil
	}
	statuses := make([]apiTokenStatusResponse, 0, len(tokens))
	for _, token := range tokens {
		statuses = append(statuses, formatAPITokenStatus(token))
	}
	return statuses
}

func formatStoredAPITokenStatuses(tokens []authRecordAPIToken) []apiTokenStatusResponse {
	if len(tokens) == 0 {
		return nil
	}
	statuses := make([]apiTokenStatusResponse, 0, len(tokens))
	for _, token := range tokens {
		statuses = append(statuses, formatAPITokenStatus(apiTokenStatus{
			ID:         token.ID,
			Name:       token.Name,
			CreatedAt:  token.CreatedAt,
			LastUsedAt: token.LastUsedAt,
			RevokedAt:  token.RevokedAt,
		}))
	}
	return statuses
}

func formatAPITokenStatus(token apiTokenStatus) apiTokenStatusResponse {
	status := apiTokenStatusResponse{
		ID:        token.ID,
		Name:      token.Name,
		CreatedAt: token.CreatedAt.Format(time.RFC3339),
	}
	if token.LastUsedAt != nil {
		status.LastUsedAt = token.LastUsedAt.Format(time.RFC3339)
	}
	if token.RevokedAt != nil {
		status.RevokedAt = token.RevokedAt.Format(time.RFC3339)
	}
	return status
}

func handleAPIGetConfig(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, _ map[string]string) {
	writeAPICall(w, s.backend.GetConfig)
}

func handleAPIGetDefaultConfig(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, _ map[string]string) {
	writeAPICall(w, s.backend.GetDefaultConfig)
}

func handleAPIExportConfig(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, _ map[string]string) {
	writeAPICall(w, s.backend.ExportConfig)
}

func handleAPISaveConfig(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req configPayloadRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPIResult(w, apiOKResponse{OK: true}, s.backend.SaveConfig(req.Payload))
}

func handleAPIImportConfig(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req importConfigRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	result, warnings, err := s.backend.ImportConfig(req.FileName, req.FileContent)
	writeAPIResult(w, importConfigResponse{Result: result, Warnings: warnings}, err)
}

func handleAPIBrowseDirectory(s *Server, w http.ResponseWriter, r *http.Request, ctx apiV1Context, _ map[string]string) {
	var req api.BrowseDirectoryRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	if s.isDesktopAPIRequest(r, ctx.Token) {
		writeAPICall(w, func() (api.BrowseDirectoryResponse, error) {
			return s.backend.BrowseDirectory(req.Path, req.Mode)
		})
		return
	}
	policy, err := s.webBrowsePolicy()
	if err != nil {
		writeAPIResult(w, nil, err)
		return
	}
	if !policy.AllowUnrestricted && len(policy.Roots) == 0 {
		writeAPIError(w, http.StatusForbidden, "web browse root is not configured")
		return
	}
	writeAPICall(w, func() (api.BrowseDirectoryResponse, error) {
		return s.backend.BrowseDirectoryWithinRoots(req.Path, req.Mode, policy.Roots)
	})
}

func handleAPIBrowseNativeFile(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	if !s.nativeBrowseAvailable(r) {
		writeAPIError(w, http.StatusForbidden, "native browse is only available from localhost web sessions")
		return
	}
	writeAPICall(w, s.picker.BrowseFile)
}

func handleAPIBrowseNativeFolder(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	if !s.nativeBrowseAvailable(r) {
		writeAPIError(w, http.StatusForbidden, "native browse is only available from localhost web sessions")
		return
	}
	writeAPICall(w, s.picker.BrowseFolder)
}

func handleAPIDetectDiscType(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req pathRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (string, error) { return s.backend.DetectDiscType(req.Path) })
}

func handleAPIFetchMetadata(s *Server, w http.ResponseWriter, r *http.Request, ctx apiV1Context, _ map[string]string) {
	var req metadataRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (api.MetadataPreview, error) {
		return s.backend.FetchMetadata(ctx.Token.ID, req.Path, req.SourceLookupURL, req.Overrides, req.NameOverrides, req.Trackers, req.ConfirmBDMVRescan)
	})
}

func handleAPIResetMetadata(s *Server, w http.ResponseWriter, r *http.Request, ctx apiV1Context, _ map[string]string) {
	var req metadataRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (api.MetadataPreview, error) {
		return s.backend.ResetMetadata(ctx.Token.ID, req.Path, req.SourceLookupURL, req.Overrides, req.NameOverrides, req.Trackers, req.ConfirmBDMVRescan)
	})
}

func handleAPICheckDupes(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req contentRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (api.DupeCheckSummary, error) {
		return s.backend.CheckDupes(req.Path, req.Overrides, req.NameOverrides, req.Trackers)
	})
}

func handleAPIStartDupeJob(s *Server, w http.ResponseWriter, r *http.Request, ctx apiV1Context, _ map[string]string) {
	var req contentRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	id, err := s.backend.StartDupeCheck(ctx.Token.ID, req.Path, req.Overrides, req.NameOverrides, req.Trackers)
	writeAPIResult(w, jobIDResponse{JobID: id}, err)
}

func handleAPIGetDupeJob(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, params map[string]string) {
	writeAPICall(w, func() (DupeCheckSnapshot, error) {
		return s.backend.GetDupeCheckSnapshot(params["jobID"])
	})
}

func handleAPICancelDupeJob(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, params map[string]string) {
	writeAPIResult(w, apiOKResponse{OK: true}, s.backend.CancelDupeCheck(params["jobID"]))
}

func handleAPIFetchPreparation(s *Server, w http.ResponseWriter, r *http.Request, ctx apiV1Context, _ map[string]string) {
	var req preparationRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (api.PreparationPreview, error) {
		return s.backend.FetchPreparation(ctx.Token.ID, req.Path, req.Overrides, req.NameOverrides, req.Trackers, req.IgnoreDupesFor)
	})
}

func handleAPIFetchTrackerDryRun(s *Server, w http.ResponseWriter, r *http.Request, ctx apiV1Context, _ map[string]string) {
	var req trackerDryRunRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (api.TrackerDryRunPreview, error) {
		return s.backend.FetchTrackerDryRun(ctx.Token.ID, req.Path, req.Overrides, req.NameOverrides, req.Trackers, req.IgnoreRuleFailures, req.IgnoreDupesFor, req.QuestionnaireAnswers, req.DescriptionGroups, req.Debug, req.RunLogLevel)
	})
}

func handleAPIFetchDescriptionBuilder(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req preparationRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (api.DescriptionBuilderPreview, error) {
		return s.backend.FetchDescriptionBuilder(req.Path, req.Overrides, req.NameOverrides, req.Trackers, req.IgnoreDupesFor)
	})
}

func handleAPIRenderDescription(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req renderDescriptionRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (string, error) { return s.backend.RenderDescription(req.Raw) })
}

func handleAPISaveDescriptionOverride(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req saveDescriptionOverrideRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (api.DescriptionBuilderGroup, error) {
		return s.backend.SaveDescriptionOverride(req.Path, req.GroupKey, req.Raw, req.Trackers, req.Overrides, req.NameOverrides)
	})
}

func handleAPIDiscoverPlaylists(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req pathRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() ([]api.PlaylistInfo, error) { return s.backend.DiscoverPlaylists(req.Path) })
}

func handleAPISavePlaylistSelection(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req playlistSelectionRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPIResult(w, apiOKResponse{OK: true}, s.backend.SavePlaylistSelection(req.Path, req.Playlists, req.UseAll))
}

func handleAPILoadPlaylistSelection(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req pathRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (api.PlaylistSelection, error) { return s.backend.LoadPlaylistSelection(req.Path) })
}

func handleAPIFetchScreenshotPlan(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req screenshotPlanRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (api.ScreenshotPlan, error) {
		return s.backend.FetchScreenshotPlan(req.Path, req.Overrides, req.NameOverrides)
	})
}

func handleAPIGenerateScreenshots(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req generateScreenshotsRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (api.ScreenshotResult, error) {
		return s.backend.GenerateScreenshots(req.Path, req.Overrides, req.NameOverrides, req.Selections, req.Purpose)
	})
}

func handleAPIPreviewScreenshotFrame(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req previewScreenshotFrameRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (string, error) {
		return s.backend.PreviewScreenshotFrame(req.Path, req.Overrides, req.NameOverrides, req.TimestampSeconds)
	})
}

func handleAPIReadScreenshotImage(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req pathRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (string, error) { return s.backend.ReadScreenshotImage(req.Path) })
}

func handleAPIDeleteScreenshot(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req screenshotImageRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPIResult(w, apiOKResponse{OK: true}, s.backend.DeleteScreenshot(req.Path, req.Overrides, req.NameOverrides, req.ImagePath))
}

func handleAPIDeleteTrackerImageURL(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req trackerImageURLRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPIResult(w, apiOKResponse{OK: true}, s.backend.DeleteTrackerImageURL(req.Path, req.Overrides, req.NameOverrides, req.URL))
}

func handleAPISaveFinalScreenshotSelections(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req finalScreenshotSelectionsRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPIResult(w, apiOKResponse{OK: true}, s.backend.SaveFinalScreenshotSelections(req.Path, req.Overrides, req.NameOverrides, req.Images))
}

func handleAPIListUploadCandidates(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req screenshotPlanRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() ([]api.ScreenshotImage, error) {
		return s.backend.ListUploadCandidates(req.Path, req.Overrides, req.NameOverrides)
	})
}

func handleAPIListUploadedImages(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req screenshotPlanRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() ([]api.UploadedImageLink, error) {
		return s.backend.ListUploadedImages(req.Path, req.Overrides, req.NameOverrides)
	})
}

func handleAPIUploadImages(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req uploadImagesRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() ([]api.UploadedImageLink, error) {
		return s.backend.UploadImages(req.Path, req.Overrides, req.NameOverrides, req.Host, req.Images)
	})
}

func handleAPIDeleteUploadedImage(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req deleteUploadedImageRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPIResult(w, apiOKResponse{OK: true}, s.backend.DeleteUploadedImage(req.Path, req.ImagePath, req.Host))
}

func handleAPIListKnownTrackers(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, _ map[string]string) {
	writeAPICall(w, s.backend.ListKnownTrackers)
}

func handleAPIListHistory(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, _ map[string]string) {
	writeAPICall(w, s.backend.ListHistory)
}

func handleAPIGetHistoryOverview(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req sourcePathRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPICall(w, func() (api.HistoryOverview, error) {
		return s.backend.GetHistoryOverview(req.SourcePath)
	})
}

func handleAPIDeleteHistoryRelease(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req sourcePathRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPIResult(w, apiOKResponse{OK: true}, s.backend.DeleteHistoryRelease(req.SourcePath))
}

func handleAPIGetLogPath(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, _ map[string]string) {
	writeAPICall(w, s.backend.GetLogPath)
}

func handleAPIGetRecentLogs(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req recentLogsRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	entries, err := s.backend.GetRecentLogs(req.Limit)
	writeAPIResult(w, entries, err)
}

func handleAPIStartLogStream(s *Server, w http.ResponseWriter, _ *http.Request, ctx apiV1Context, _ map[string]string) {
	writeAPICall(w, func() (string, error) { return s.backend.StartLogStream(ctx.Token.ID) })
}

func handleAPIStopLogStream(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, params map[string]string) {
	writeAPIResult(w, apiOKResponse{OK: true}, s.backend.StopLogStream(params["streamID"]))
}

func handleAPIGetLogExclusions(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, _ map[string]string) {
	writeAPICall(w, s.backend.GetLogExclusions)
}

func handleAPIUpdateLogExclusions(s *Server, w http.ResponseWriter, r *http.Request, _ apiV1Context, _ map[string]string) {
	var req logExclusionsRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	writeAPIResult(w, apiOKResponse{OK: true}, s.backend.UpdateLogExclusions(req.Patterns))
}

func handleAPIStartTrackerUploadJob(s *Server, w http.ResponseWriter, r *http.Request, ctx apiV1Context, _ map[string]string) {
	var req trackerUploadRequest
	if !decodeOrError(w, r, &req) {
		return
	}
	id, err := s.backend.StartTrackerUpload(ctx.Token.ID, req.Path, req.Overrides, req.NameOverrides, req.Trackers, req.IgnoreRuleFailures, req.IgnoreDupesFor, req.QuestionnaireAnswers, req.DescriptionGroups, req.Debug, req.RunLogLevel)
	writeAPIResult(w, jobIDResponse{JobID: id}, err)
}

func handleAPIGetTrackerUploadJob(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, params map[string]string) {
	writeAPICall(w, func() (TrackerUploadSnapshot, error) {
		return s.backend.GetTrackerUploadSnapshot(params["jobID"])
	})
}

func handleAPICancelTrackerUploadJob(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, params map[string]string) {
	writeAPIResult(w, apiOKResponse{OK: true}, s.backend.CancelTrackerUpload(params["jobID"]))
}

func handleAPIRetryTrackerUploadJob(s *Server, w http.ResponseWriter, _ *http.Request, _ apiV1Context, params map[string]string) {
	id, err := s.backend.RetryFailedTrackerUpload(params["jobID"])
	writeAPIResult(w, jobIDResponse{JobID: id}, err)
}

func handleAPIEvents(s *Server, w http.ResponseWriter, r *http.Request, ctx apiV1Context, _ map[string]string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsubscribe := s.hub.Subscribe(ctx.Token.ID)
	defer unsubscribe()
	defer s.backend.StopSessionLogStreams(ctx.Token.ID)

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			_, _ = w.Write([]byte("event: " + event.Name + "\n"))
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(event.Data)
			_, _ = w.Write([]byte("\n\n"))
			flusher.Flush()
		}
	}
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	payload, err := buildOpenAPIDocument(apiV1Routes())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) handleSwaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerUIHTML))
}

const swaggerUIHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>upbrr API Docs</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <style>body{margin:0;background:#0f172a}.swagger-ui .topbar{display:none}</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: "/api/openapi.json",
      dom_id: "#swagger-ui",
      persistAuthorization: true,
      deepLinking: true
    });
  </script>
</body>
</html>`
