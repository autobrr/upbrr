// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/autobrr/upbrr/internal/apitoken"
	"github.com/autobrr/upbrr/internal/services/trackericon"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	// JSON can encode one raw byte as a six-byte escape. These route caps leave
	// fixed field headroom while downstream importers enforce decoded limits.
	cookieImportRequestEnvelopeMaxBytes = trackerauth.MaxCookieImportContentBytes*6 + 64*1024
	configImportRequestEnvelopeMaxBytes = configImportMaxBytes*6 + 64*1024
)

func nonNilAppList[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// registerAppRoutes installs authenticated browser operations and their
// request-shape adapters on mux.
func (s *Server) registerAppRoutes(mux *http.ServeMux) {
	s.registerReleaseWorkflowAppRoutes(mux)
	mux.HandleFunc("/api/app/ListTrackerAuthCapabilities", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		value, err := s.backend.ListTrackerAuthCapabilities()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, nonNilAppList(value))
	}))

	mux.HandleFunc("/api/app/GetTrackerAuthStatus", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct{ Tracker string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.GetTrackerAuthStatus(req.Tracker)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/ImportTrackerAuthCookieContent", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, cookieImportRequestEnvelopeMaxBytes)
		var req struct {
			Tracker  string
			FileName string
			Content  string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.ImportTrackerAuthCookieContent(r.Context(), req.Tracker, req.FileName, req.Content)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/TestTrackerAuth", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct{ Tracker string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.TestTrackerAuth(r.Context(), req.Tracker)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/LoginTrackerAuth", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct {
			Tracker string
			Login   api.TrackerAuthLoginRequest
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.LoginTrackerAuth(r.Context(), req.Tracker, req.Login)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/SubmitTrackerAuth2FA", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct {
			ChallengeID string
			Code        string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.SubmitTrackerAuth2FA(r.Context(), req.ChallengeID, req.Code)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/DeleteTrackerAuth", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct{ Tracker string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.DeleteTrackerAuth(r.Context(), req.Tracker)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/RenderDescription", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct{ Raw string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.RenderDescription(req.Raw)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/BrowseDirectory", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req api.BrowseDirectoryRequest
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		policy, err := s.webBrowsePolicy(current)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if !policy.AllowUnrestricted && len(policy.Roots) == 0 {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "web browse root is not configured"})
			return
		}
		value, err := s.backend.BrowseDirectoryWithinRoots(req.Path, req.Mode, policy.Roots)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/GetConfig", s.requireSession(func(w http.ResponseWriter, _ *http.Request, _ session) {
		value, err := s.backend.GetConfig()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/ExportConfig", s.requireSession(func(w http.ResponseWriter, _ *http.Request, _ session) {
		value, err := s.backend.ExportConfig()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/GetApplicationInfo", s.requireSession(func(w http.ResponseWriter, _ *http.Request, _ session) {
		value, err := s.backend.GetApplicationInfo()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/ListAPITokens", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		value, err := s.apiTokens.list(r.Context())
		if err != nil {
			if s.backend != nil {
				s.backend.logWarnf("web: list API tokens failed: %v", err)
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list API tokens failed"})
			return
		}
		writeJSON(w, http.StatusOK, nonNilAppList(value))
	}))

	mux.HandleFunc("/api/app/CreateAPIToken", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct {
			Name    string           `json:"name"`
			OwnerID string           `json:"ownerId"`
			Scopes  []apitoken.Scope `json:"scopes"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		created, err := s.apiTokens.create(r.Context(), apitoken.CreateInput{
			Name:    req.Name,
			OwnerID: req.OwnerID,
			Scopes:  req.Scopes,
		})
		if err != nil {
			if errors.Is(err, apitoken.ErrInvalid) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if s.backend != nil {
				s.backend.logWarnf("web: create API token failed: %v", err)
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create API token failed"})
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}))

	mux.HandleFunc("/api/app/RevokeAPIToken", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.apiTokens.revoke(r.Context(), req.ID); err != nil {
			if errors.Is(err, apitoken.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "API token not found or already revoked"})
				return
			}
			if errors.Is(err, apitoken.ErrInvalid) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if s.backend != nil {
				s.backend.logWarnf("web: revoke API token failed: %v", err)
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "revoke API token failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/app/GetDefaultConfig", s.requireSession(func(w http.ResponseWriter, _ *http.Request, _ session) {
		value, err := s.backend.GetDefaultConfig()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/SaveConfig", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct{ Payload string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.backend.SaveConfig(req.Payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/app/ImportConfig", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, configImportRequestEnvelopeMaxBytes)
		var req struct {
			FileName    string
			FileContent string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		result, warnings, err := s.backend.ImportConfig(req.FileName, req.FileContent)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": result, "warnings": warnings})
	}))

	mux.HandleFunc("/api/app/ListTrackerCatalog", s.requireSession(func(w http.ResponseWriter, _ *http.Request, _ session) {
		value, err := s.backend.ListTrackerCatalog()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/GetImageHostPolicyMetadata", s.requireSession(func(w http.ResponseWriter, _ *http.Request, _ session) {
		value, err := s.backend.GetImageHostPolicyMetadata()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/ListHistory", s.requireSession(func(w http.ResponseWriter, _ *http.Request, _ session) {
		value, err := s.backend.ListHistory()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, nonNilAppList(value))
	}))

	mux.HandleFunc("/api/app/GetHistoryOverview", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct{ SourcePath string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.GetHistoryOverview(req.SourcePath)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/DeleteHistoryRelease", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct{ SourcePath string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.backend.DeleteHistoryRelease(req.SourcePath); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/app/GetLogPath", s.requireSession(func(w http.ResponseWriter, _ *http.Request, _ session) {
		value, err := s.backend.GetLogPath()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/GetRecentLogs", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct{ Limit int }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.GetRecentLogs(req.Limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, nonNilAppList(value))
	}))

	mux.HandleFunc("/api/app/StartLogStream", s.requireSession(func(w http.ResponseWriter, _ *http.Request, current session) {
		value, err := s.backend.StartLogStream(current.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/StopLogStream", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct{ StreamID string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.backend.StopLogStream(current.ID, req.StreamID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/app/GetLogExclusions", s.requireSession(func(w http.ResponseWriter, _ *http.Request, _ session) {
		value, err := s.backend.GetLogExclusions()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, nonNilAppList(value))
	}))

	mux.HandleFunc("/api/app/UpdateLogExclusions", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct{ Patterns []string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.backend.UpdateLogExclusions(req.Patterns); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/app/GetTrackerIcon", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct {
			Domain string
			URL    string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		cfg := s.cfg
		if s.backend != nil {
			cfg = s.backend.currentConfig()
		}
		domain, urlToUse, err := resolveTrackerIconTarget(req.Domain, req.URL)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := trackericon.GetTrackerIcon(r.Context(), cfg.MainSettings.DBPath, domain, urlToUse)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))
}

type webBrowsePolicy struct {
	Roots             []string
	AllowUnrestricted bool
}

// webBrowsePolicy returns the filesystem roots that browser-mode file
// operations may read, or unrestricted access for trusted development sessions.
func (s *Server) webBrowsePolicy(current session) (webBrowsePolicy, error) {
	if s != nil && s.isDevelopmentSession(current) {
		return webBrowsePolicy{AllowUnrestricted: true}, nil
	}
	if s == nil || s.auth == nil {
		return webBrowsePolicy{}, nil
	}
	record, err := s.auth.Load()
	if err != nil {
		if os.IsNotExist(err) {
			return webBrowsePolicy{}, nil
		}
		return webBrowsePolicy{}, fmt.Errorf("web: %w", err)
	}
	if record.AllowUnrestrictedBrowse {
		return webBrowsePolicy{AllowUnrestricted: true}, nil
	}
	roots, err := normalizeBrowsePolicyRoots(splitBrowsePolicyRoots(record.BrowseRoot))
	if err != nil {
		return webBrowsePolicy{}, err
	}
	return webBrowsePolicy{Roots: roots}, nil
}

// writeAppError exposes structured operation failures with their safe message
// and hides all unstructured error detail behind a generic internal failure.
func writeAppError(w http.ResponseWriter, err error) {
	if failure, ok := api.AsOperationFailure(err); ok {
		status := http.StatusConflict
		if failure.Code == api.OperationFailureInvalidInput || failure.Code == api.OperationFailureInvalidSource {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{
			"error":   failure.Message,
			"failure": failure,
		})
		return
	}
	failure := api.OperationFailure{
		Code:      api.OperationFailureInternal,
		Operation: api.OperationKindUnknown,
		Message:   "The operation could not be completed.",
		Recovery:  api.OperationRecoveryRetry,
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error":   failure.Message,
		"failure": failure,
	})
}
