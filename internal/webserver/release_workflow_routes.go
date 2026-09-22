// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

const workflowStagedMediaMaxBytes = 20 << 20

func (s *Server) registerReleaseWorkflowAppRoutes(mux *http.ServeMux) {
	s.registerActiveInputRoutes(mux)
	mux.HandleFunc("/api/app/ContinueReleaseWorkflow", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var request api.ContinueReleaseWorkflowRequest
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := request.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		result, err := s.backend.continueReleaseWorkflow(
			releaseworkflow.WithTrackerDecisionMode(
				r.Context(),
				releaseworkflow.TrackerDecisionModeWebUIControls,
			),
			current.ID,
			request,
		)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}))

	registerReleaseWorkflowCommand[api.SetReleaseWorkflowMediaSelectionRequest](s, mux, "SetReleaseWorkflowMediaSelection", false)
	registerReleaseWorkflowCommand[api.DeleteReleaseWorkflowMediaRequest](s, mux, "DeleteReleaseWorkflowMedia", false)
	registerReleaseWorkflowCommand[api.ReorderReleaseWorkflowMediaRequest](s, mux, "ReorderReleaseWorkflowMedia", false)
	registerReleaseWorkflowCommand[api.AttachReleaseWorkflowMediaRequest](s, mux, "AttachReleaseWorkflowMedia", false)
	registerReleaseWorkflowCommand[api.UploadReleaseWorkflowImagesRequest](s, mux, "UploadReleaseWorkflowImages", true)
	registerReleaseWorkflowCommand[api.RetryReleaseWorkflowImageHostRequest](s, mux, "RetryReleaseWorkflowImageHost", true)
	registerReleaseWorkflowCommand[api.RemoveReleaseWorkflowHostedImagesRequest](s, mux, "RemoveReleaseWorkflowHostedImages", false)
	registerReleaseWorkflowCommand[api.AnalyzeReleaseWorkflowAudioRequest](s, mux, "AnalyzeReleaseWorkflowAudio", true)
	registerReleaseWorkflowCommand[api.SetReleaseWorkflowAudioAnalysisEnabledRequest](s, mux, "SetReleaseWorkflowAudioAnalysisEnabled", false)
	registerReleaseWorkflowCommand[api.SaveReleaseWorkflowDescriptionOverrideRequest](s, mux, "SaveReleaseWorkflowDescriptionOverride", false)
	registerReleaseWorkflowCommand[api.ResetReleaseWorkflowDescriptionOverrideRequest](s, mux, "ResetReleaseWorkflowDescriptionOverride", false)
	registerReleaseWorkflowCommand[api.RetryReleaseWorkflowUploadRequest](s, mux, "RetryReleaseWorkflowUpload", true)
	registerReleaseWorkflowCommand[api.RetryReleaseWorkflowClientInjectionRequest](
		s,
		mux,
		"RetryReleaseWorkflowClientInjection",
		true,
	)
	registerReleaseWorkflowCommand[api.CancelReleaseWorkflowRequest](s, mux, "CancelReleaseWorkflow", false)
	registerReleaseWorkflowCommand[api.InvalidateReleaseWorkflowTrackersRequest](s, mux, "InvalidateReleaseWorkflowTrackers", false)

	mux.HandleFunc("/api/app/GetReleaseWorkflow", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var request api.GetReleaseWorkflowRequest
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := request.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		result, err := s.backend.currentReleaseWorkflow(r.Context(), current.ID, request.WorkflowID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}))

	mux.HandleFunc("/api/app/GetReleaseWorkflowOperation", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var request api.ReleaseWorkflowOperationRequest
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := request.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		result, err := s.backend.releaseWorkflowOperation(r.Context(), current.ID, request.WorkflowID, request.OperationID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}))

	mux.HandleFunc("/api/app/CancelReleaseWorkflowOperation", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var request api.ReleaseWorkflowOperationRequest
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := request.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		result, err := s.backend.cancelReleaseWorkflowOperation(r.Context(), current.ID, request.WorkflowID, request.OperationID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}))

	mux.HandleFunc("/api/app/GetReleaseWorkflowMediaPlan", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var request api.GetReleaseWorkflowMediaPlanRequest
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := request.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		plan, err := s.backend.releaseWorkflowMediaPlan(r.Context(), current.ID, request.WorkflowID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, plan)
	}))

	mux.HandleFunc("/api/app/StageReleaseWorkflowMedia", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		content, err := readStagedMediaUpload(w, r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		revision, err := strconv.ParseUint(strings.TrimSpace(r.FormValue("expectedRevision")), 10, 64)
		if err != nil || revision == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid expected workflow revision is required"})
			return
		}
		resource, err := s.backend.stageReleaseWorkflowMediaResource(
			r.Context(),
			current.ID,
			api.WorkflowID(r.FormValue("workflowId")),
			api.WorkflowRevision(revision),
			content,
		)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, resource)
	}))

	mux.HandleFunc("/api/app/PreviewReleaseWorkflowFrame", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var request api.PreviewReleaseWorkflowFrameRequest
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := request.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		preview, err := s.backend.previewReleaseWorkflowFrame(
			r.Context(),
			current.ID,
			request.WorkflowID,
			request.ExpectedRevision,
			request.DiscID,
			request.TimestampSeconds,
		)
		if err != nil {
			writeAppError(w, err)
			return
		}
		query := url.Values{
			"workflowId": []string{string(preview.WorkflowID)},
			"previewId":  []string{string(preview.ID)},
		}
		preview.ContentURL = "/api/app/release-workflow-preview?" + query.Encode()
		writeJSON(w, http.StatusOK, preview)
	}))

	mux.HandleFunc("/api/app/release-workflow-preview", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		content, err := s.backend.openReleaseWorkflowPreview(
			r.Context(),
			current.ID,
			api.WorkflowID(r.URL.Query().Get("workflowId")),
			api.PublicResourceID(r.URL.Query().Get("previewId")),
		)
		if err != nil {
			writeAppError(w, err)
			return
		}
		s.writeReleaseWorkflowArtifact(w, content, "", "releaseworkflow: preview response interrupted")
	}))

	mux.HandleFunc("/api/app/release-workflow-media", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		revision, err := strconv.ParseUint(r.URL.Query().Get("mediaRevision"), 10, 64)
		if err != nil || revision == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid media revision"})
			return
		}
		content, err := s.backend.openReleaseWorkflowMediaArtifact(
			r.Context(),
			current.ID,
			api.WorkflowID(r.URL.Query().Get("workflowId")),
			api.MediaArtifactSetRef{
				ID:       api.MediaArtifactSetID(r.URL.Query().Get("mediaId")),
				Revision: api.WorkflowRevision(revision),
			},
			api.PublicResourceID(r.URL.Query().Get("artifactId")),
		)
		if err != nil {
			writeAppError(w, err)
			return
		}
		s.writeReleaseWorkflowArtifact(w, content, "", "releaseworkflow: media response interrupted")
	}))

	mux.HandleFunc("/api/app/release-workflow-audio-analysis", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		revision, err := strconv.ParseUint(r.URL.Query().Get("analysisRevision"), 10, 64)
		if err != nil || revision == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid audio analysis revision"})
			return
		}
		content, err := s.backend.openReleaseWorkflowAudioAnalysisArtifact(
			r.Context(),
			current.ID,
			api.WorkflowID(r.URL.Query().Get("workflowId")),
			api.AudioAnalysisRef{
				ID:       api.AudioAnalysisResultID(r.URL.Query().Get("analysisId")),
				Revision: api.WorkflowRevision(revision),
			},
			api.PublicResourceID(r.URL.Query().Get("artifactId")),
		)
		if err != nil {
			writeAppError(w, err)
			return
		}
		s.writeReleaseWorkflowArtifact(
			w,
			content,
			audioAnalysisContentDisposition(content.ContentType),
			"releaseworkflow: audio analysis response interrupted",
		)
	}))
}

func audioAnalysisContentDisposition(contentType string) string {
	if contentType == "text/plain; charset=utf-8" {
		return `inline; filename="audio-analysis-stats.txt"`
	}
	return `inline; filename="audio-analysis.png"`
}

func (s *Server) writeReleaseWorkflowArtifact(
	w http.ResponseWriter,
	content releaseworkflow.MediaArtifactContent,
	contentDisposition string,
	interruptedMessage string,
) {
	defer content.Body.Close()
	w.Header().Set("Cache-Control", "private, no-store")
	if contentDisposition != "" {
		w.Header().Set("Content-Disposition", contentDisposition)
	}
	w.Header().Set("Content-Type", content.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(w, content.Body); err != nil {
		s.backend.logDebug(interruptedMessage)
	}
}

func readStagedMediaUpload(w http.ResponseWriter, r *http.Request) (releaseworkflow.StagedMediaContent, error) {
	r.Body = http.MaxBytesReader(w, r.Body, workflowStagedMediaMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(workflowStagedMediaMaxBytes); err != nil {
		return releaseworkflow.StagedMediaContent{}, errors.New("invalid staged media upload")
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return releaseworkflow.StagedMediaContent{}, errors.New("staged media file is required")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, workflowStagedMediaMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > workflowStagedMediaMaxBytes {
		return releaseworkflow.StagedMediaContent{}, errors.New("staged media file must be between 1 byte and 20 MiB")
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(header.Header.Get("Content-Type"), ";", 2)[0]))
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = http.DetectContentType(data)
	}
	switch contentType {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return releaseworkflow.StagedMediaContent{}, errors.New("staged media must be PNG, JPEG, or WebP")
	}
	return releaseworkflow.StagedMediaContent{
		Name:        header.Filename,
		Bytes:       data,
		ContentType: contentType,
	}, nil
}

func registerReleaseWorkflowCommand[Request any](
	s *Server,
	mux *http.ServeMux,
	method string,
	longRunning bool,
) {
	mux.HandleFunc("/api/app/"+method, s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var request Request
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		command, err := mapReleaseWorkflowRequest(request)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		commandCtx := r.Context()
		var executed releaseworkflow.CommandResult
		if longRunning {
			operation, err := s.backend.startReleaseWorkflow(commandCtx, current.ID, command)
			if err != nil {
				writeAppError(w, err)
				return
			}
			executed.Workflow.ID = operation.WorkflowID
			executed.Operation = &operation
		} else {
			var err error
			executed, err = s.backend.executeReleaseWorkflow(commandCtx, current.ID, command)
			if err != nil {
				writeAppError(w, err)
				return
			}
		}
		result, err := s.backend.currentReleaseWorkflow(commandCtx, current.ID, executed.Workflow.ID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		result.Operation = executed.Operation
		status := http.StatusOK
		if longRunning {
			status = http.StatusAccepted
		}
		writeJSON(w, status, result)
	}))
}
