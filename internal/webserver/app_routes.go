// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/services/trackericon"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/pkg/api"
)

// cookieImportRequestEnvelopeMaxBytes leaves JSON envelope headroom while the
// tracker auth importer enforces the shared raw cookie content limit.
const cookieImportRequestEnvelopeMaxBytes = trackerauth.MaxCookieImportContentBytes*6 + 64*1024

func nonNilAppList[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// registerAppRoutes installs authenticated browser operations and their
// request-shape adapters on mux.
func (s *Server) registerAppRoutes(mux *http.ServeMux) {
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

	mux.HandleFunc("/api/app/DetectDiscType", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct{ Path string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.DetectDiscType(r.Context(), req.Path)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/FetchMetadata", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct {
			CorrelationID     string
			Path              string
			SourceLookupURL   string
			Overrides         api.ExternalIDOverrides
			NameOverrides     api.ReleaseNameOverrides
			Playlist          api.PlaylistInstruction
			ConfirmBDMVRescan bool
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.FetchMetadata(
			r.Context(),
			current.ID,
			metadataPreparationRequest{
				CorrelationID:     req.CorrelationID,
				Path:              req.Path,
				SourceLookupURL:   req.SourceLookupURL,
				Overrides:         req.Overrides,
				NameOverrides:     req.NameOverrides,
				Playlist:          req.Playlist,
				ConfirmBDMVRescan: req.ConfirmBDMVRescan,
			},
		)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/ResetMetadata", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct {
			CorrelationID     string
			Path              string
			SourceLookupURL   string
			Overrides         api.ExternalIDOverrides
			NameOverrides     api.ReleaseNameOverrides
			Playlist          api.PlaylistInstruction
			ConfirmBDMVRescan bool
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.ResetMetadata(
			r.Context(),
			current.ID,
			metadataPreparationRequest{
				CorrelationID:     req.CorrelationID,
				Path:              req.Path,
				SourceLookupURL:   req.SourceLookupURL,
				Overrides:         req.Overrides,
				NameOverrides:     req.NameOverrides,
				Playlist:          req.Playlist,
				ConfirmBDMVRescan: req.ConfirmBDMVRescan,
			},
		)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/SelectBlurayCandidate", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct {
			CorrelationID string
			Path          string
			ReleaseID     string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.SelectBlurayCandidate(r.Context(), current.ID, blurayCandidateSelectionRequest{
			CorrelationID: req.CorrelationID,
			Path:          req.Path,
			ReleaseID:     req.ReleaseID,
		})
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/FetchPreparation", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct {
			Path           string
			Overrides      api.ExternalIDOverrides
			NameOverrides  api.ReleaseNameOverrides
			Trackers       []string
			IgnoreDupesFor []string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.FetchPreparation(current.ID, req.Path, req.Overrides, req.NameOverrides, req.Trackers, req.IgnoreDupesFor)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/FetchTrackerDryRun", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct {
			DupeJobID            string
			Release              api.ReleaseRef
			Trackers             []string
			IgnoreDupesFor       []string
			QuestionnaireAnswers map[string]map[string]string
			DescriptionGroups    []api.DescriptionBuilderGroup
			NoSeed               bool
			RunLogLevel          string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.FetchTrackerDryRun(
			r.Context(),
			current.ID,
			req.DupeJobID,
			req.Release,
			req.Trackers,
			req.IgnoreDupesFor,
			req.QuestionnaireAnswers,
			req.DescriptionGroups,
			req.NoSeed,
			req.RunLogLevel,
		)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/FetchDescriptionBuilder", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct {
			Release  api.ReleaseRef
			Trackers []string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.FetchDescriptionBuilder(req.Release, req.Trackers)
		if err != nil {
			writeAppError(w, err)
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

	mux.HandleFunc("/api/app/SaveDescriptionOverride", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct {
			Release  api.ReleaseRef
			GroupKey string
			Raw      string
			Trackers []string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.SaveDescriptionOverride(req.Release, req.GroupKey, req.Raw, req.Trackers)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/DiscoverPlaylists", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct{ Path string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.DiscoverPlaylists(r.Context(), req.Path)
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

	mux.HandleFunc("/api/app/FetchScreenshotPlan", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct {
			Release api.ReleaseRef
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.FetchScreenshotPlan(req.Release)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/GenerateScreenshots", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct {
			Release    api.ReleaseRef
			Selections []api.ScreenshotSelection
			Purpose    api.ScreenshotPurpose
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.GenerateScreenshots(req.Release, req.Selections, req.Purpose)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/PreviewScreenshotFrame", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct {
			Release          api.ReleaseRef
			TimestampSeconds float64
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.PreviewScreenshotFrame(req.Release, req.TimestampSeconds)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/DeleteScreenshot", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct {
			Release   api.ReleaseRef
			ImagePath string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.backend.DeleteScreenshot(req.Release, req.ImagePath); err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/app/DeleteTrackerImageURL", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct {
			Release api.ReleaseRef
			URL     string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.backend.DeleteTrackerImageURL(req.Release, req.URL); err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/app/SaveFinalScreenshotSelections", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct {
			Release api.ReleaseRef
			Images  []api.ScreenshotImage
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.backend.SaveFinalScreenshotSelections(req.Release, req.Images); err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/app/ImportMenuImages", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct {
			Release api.ReleaseRef
			Paths   []string
		}
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
		importPaths, err := menuImportPathsWithinBrowsePolicy(req.Paths, policy)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.backend.ImportMenuImages(req.Release, importPaths); err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/app/CaptureDVDMenus", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct {
			Release api.ReleaseRef
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.CaptureDVDMenus(r.Context(), req.Release)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/ListDVDMenuScreenshots", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct {
			Release api.ReleaseRef
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.ListDVDMenuScreenshots(req.Release)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, nonNilAppList(value))
	}))

	mux.HandleFunc("/api/app/DeleteDVDMenuScreenshot", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct {
			Release   api.ReleaseRef
			ImagePath string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.backend.DeleteDVDMenuScreenshot(req.Release, req.ImagePath); err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/app/ReadScreenshotImage", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct{ Path string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.ReadScreenshotImage(req.Path)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/ListUploadCandidates", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct {
			Release api.ReleaseRef
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.ListUploadCandidates(req.Release)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, nonNilAppList(value))
	}))

	mux.HandleFunc("/api/app/ListUploadedImages", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct {
			Release api.ReleaseRef
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.ListUploadedImages(req.Release)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, nonNilAppList(value))
	}))

	mux.HandleFunc("/api/app/UploadImages", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct {
			CorrelationID string
			Release       api.ReleaseRef
			Trackers      []string
			Host          string
			Images        []api.ScreenshotImage
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.UploadImages(r.Context(), current.ID, req.CorrelationID, req.Release, req.Trackers, req.Host, req.Images)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/DeleteUploadedImage", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		var req struct {
			Release   api.ReleaseRef
			ImagePath string
			Host      string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.backend.DeleteUploadedImage(req.Release, req.ImagePath, req.Host); err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
		// Allow extra headroom for JSON wrapping (FileName, escaping)
		// beyond the raw file-content limit enforced by the importer.
		r.Body = http.MaxBytesReader(w, r.Body, configImportMaxBytes+1024*1024)
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

	mux.HandleFunc("/api/app/StartDupeCheck", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct {
			Release       api.ReleaseRef
			Trackers      []string
			CorrelationID string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.StartDupeCheck(
			r.Context(), current.ID, req.Release, req.Trackers, req.CorrelationID,
		)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/CancelDupeCheck", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct{ JobID string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.backend.CancelDupeCheck(current.ID, req.JobID); err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/app/GetDupeCheckSnapshot", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct{ JobID string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.GetDupeCheckSnapshot(current.ID, req.JobID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/ReviewTrackerUpload", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct {
			Release              api.ReleaseRef
			Trackers             []string
			IgnoreDupesFor       []string
			RuleAuthorizations   []api.RuleAuthorization
			QuestionnaireAnswers map[string]map[string]string
			DescriptionGroups    []api.DescriptionBuilderGroup
			NoSeed               bool
			RunLogLevel          string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.ReviewTrackerUpload(
			r.Context(),
			current.ID,
			req.Release,
			req.Trackers,
			req.IgnoreDupesFor,
			req.RuleAuthorizations,
			req.QuestionnaireAnswers,
			req.DescriptionGroups,
			req.NoSeed,
			req.RunLogLevel,
		)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/StartReviewedTrackerUpload", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct {
			Token         string
			CorrelationID string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.StartReviewedTrackerUpload(current.ID, req.Token, req.CorrelationID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/CancelTrackerUpload", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct{ JobID string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.backend.CancelTrackerUpload(current.ID, req.JobID); err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/app/RetryFailedTrackerUpload", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct {
			JobID         string
			CorrelationID string
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.RetryFailedTrackerUpload(current.ID, req.JobID, req.CorrelationID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/GetTrackerUploadSnapshot", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		var req struct{ JobID string }
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := s.backend.GetTrackerUploadSnapshot(current.ID, req.JobID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}))

	mux.HandleFunc("/api/app/ListJobs", s.requireSession(func(w http.ResponseWriter, _ *http.Request, current session) {
		value, err := s.backend.ListJobs(current.ID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, nonNilAppList(value))
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

// menuImportPathsWithinBrowsePolicy resolves menu image paths under the active
// browse policy. Directory inputs expand to their immediate non-directory
// entries after each resolved path is checked against configured roots.
func menuImportPathsWithinBrowsePolicy(paths []string, policy webBrowsePolicy) ([]string, error) {
	if policy.AllowUnrestricted {
		return paths, nil
	}
	filtered := make([]string, 0, len(paths))
	for _, rawPath := range paths {
		resolvedPath, info, err := resolveMenuImportPath(rawPath)
		if err != nil {
			return nil, err
		}
		if !pathWithinBrowseRoots(resolvedPath, policy.Roots) {
			return nil, fmt.Errorf("menu image path %q is outside configured web browse roots", rawPath)
		}
		if !info.IsDir() {
			filtered = append(filtered, resolvedPath)
			continue
		}
		entries, err := os.ReadDir(resolvedPath)
		if err != nil {
			return nil, fmt.Errorf("read menu dir %s: %w", resolvedPath, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			entryPath := filepath.Join(resolvedPath, entry.Name())
			entryResolved, entryInfo, err := resolveMenuImportPath(entryPath)
			if err != nil {
				return nil, err
			}
			if entryInfo.IsDir() {
				continue
			}
			if !pathWithinBrowseRoots(entryResolved, policy.Roots) {
				return nil, fmt.Errorf("menu image path %q is outside configured web browse roots", entryPath)
			}
			filtered = append(filtered, entryResolved)
		}
	}
	return filtered, nil
}

// resolveMenuImportPath normalizes a menu image path to its absolute symlink
// target and returns metadata for the resolved filesystem entry.
func resolveMenuImportPath(rawPath string) (string, os.FileInfo, error) {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return "", nil, errors.New("menu image path is required")
	}
	candidate, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return "", nil, fmt.Errorf("resolve menu path %s: %w", trimmed, err)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", nil, fmt.Errorf("resolve menu path symlinks %s: %w", trimmed, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, fmt.Errorf("stat menu path %s: %w", resolved, err)
	}
	return resolved, info, nil
}

// pathWithinBrowseRoots reports whether candidate is contained by any
// normalized browse root.
func pathWithinBrowseRoots(candidate string, roots []string) bool {
	for _, root := range roots {
		if pathutil.IsWithinRoot(root, candidate) {
			return true
		}
	}
	return false
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
