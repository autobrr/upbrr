// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	apiV1RequestMaxBytes = 1 << 20
	apiV1IdempotencyMax  = 128
)

func (s *Server) registerV1Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/docs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		serveReleaseWorkflowSwaggerUI(w)
	})
	mux.HandleFunc("/api/v1/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		s.serveReleaseWorkflowOpenAPI(w)
	})
	mux.HandleFunc("/api/v1/capabilities", s.handleAPIV1Capabilities)
	mux.HandleFunc("/api/v1/uploads", s.handleAPIV1UploadCreate)
	mux.HandleFunc("/api/v1/uploads/", s.handleAPIV1UploadFeedback)
	mux.HandleFunc("/api/v1/continuations", s.handleAPIV1WorkflowContinuation)
	mux.HandleFunc("/api/v1/workflows", http.NotFound)
	mux.HandleFunc("/api/v1/workflows/", s.handleAPIV1WorkflowResource)
}

func (s *Server) handleAPIV1Capabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	principal, ok := s.authenticateAPIRequest(w, r, APITokenScopeWorkflowRead)
	if !ok {
		return
	}
	schemaHash, err := releaseWorkflowUploadOptionSchemaHash()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "capability schema unavailable"})
		return
	}
	scopes := make([]string, len(principal.Scopes))
	for index, scope := range principal.Scopes {
		scopes[index] = string(scope)
	}
	capabilities, err := s.backend.GetReleaseWorkflowCapabilities(principal.OwnerID, scopes, schemaHash)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "capability catalog unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, capabilities)
}

func (s *Server) handleAPIV1UploadCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	principal, ok := s.authenticateAPIRequest(w, r, APITokenScopeWorkflowExecute)
	if !ok {
		return
	}
	idempotencyKey, ok := apiV1IdempotencyKey(w, r)
	if !ok {
		return
	}
	var request api.CreateReleaseWorkflowUploadRequest
	if !decodeAPIV1JSON(w, r, &request) {
		return
	}
	request.IdempotencyKey = idempotencyKey
	if err := request.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	requestContext, resolved, err := resolveAPIV1UploadInputs(r.Context(), request)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := s.backend.startReleaseWorkflowUpload(requestContext, principal.OwnerID, resolved)
	if err != nil {
		writeAPIV1WorkflowError(w, err)
		return
	}
	writeAPIV1UploadResult(s, w, result)
}

func (s *Server) handleAPIV1UploadFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	segments := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/uploads/"), "/"), "/")
	if len(segments) != 2 || strings.TrimSpace(segments[0]) == "" || segments[1] != "feedback" {
		http.NotFound(w, r)
		return
	}
	principal, ok := s.authenticateAPIRequest(w, r, APITokenScopeWorkflowExecute)
	if !ok {
		return
	}
	idempotencyKey, ok := apiV1IdempotencyKey(w, r)
	if !ok {
		return
	}
	revision, ok := apiV1ExpectedRevision(w, r)
	if !ok {
		return
	}
	var feedback api.ReleaseWorkflowUploadFeedback
	if !decodeAPIV1JSON(w, r, &feedback) {
		return
	}
	feedback.IdempotencyKey = idempotencyKey
	if feedback.Action.WorkflowRevision != revision {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "feedback action revision does not match If-Match"})
		return
	}
	if err := feedback.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := s.backend.submitReleaseWorkflowUploadFeedback(
		r.Context(),
		principal.OwnerID,
		api.WorkflowID(segments[0]),
		feedback,
	)
	if err != nil {
		writeAPIV1WorkflowError(w, err)
		return
	}
	writeAPIV1UploadResult(s, w, result)
}

func writeAPIV1UploadResult(s *Server, w http.ResponseWriter, result releaseworkflow.CommandResult) {
	status := http.StatusOK
	if result.Operation != nil && !apiV1WorkflowOperationTerminal(result.Operation.Status) {
		status = http.StatusAccepted
	}
	basePath := s.externalBasePath()
	w.Header().Set("Location", joinBasePath(basePath, "/api/v1/workflows/"+string(result.Workflow.ID)))
	if result.Operation != nil {
		w.Header().Set(
			"Operation-Location",
			joinBasePath(
				basePath,
				"/api/v1/workflows/"+string(result.Workflow.ID)+"/operations/"+string(result.Operation.ID),
			),
		)
	}
	writeAPIV1WorkflowResult(w, status, result)
}

func (s *Server) handleAPIV1WorkflowContinuation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	principal, ok := s.authenticateAPIRequest(w, r, APITokenScopeWorkflowWrite)
	if !ok {
		return
	}
	idempotencyKey, ok := apiV1IdempotencyKey(w, r)
	if !ok {
		return
	}
	var request api.ContinueReleaseWorkflowRequest
	if !decodeAPIV1JSON(w, r, &request) {
		return
	}
	if request.Goal == api.WorkflowGoalUploaded {
		principal, ok = s.authenticateAPIRequest(w, r, APITokenScopeWorkflowExecute)
		if !ok {
			return
		}
	}
	request.IdempotencyKey = idempotencyKey
	if err := request.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := s.backend.continueReleaseWorkflow(
		releaseworkflow.WithTrackerDecisionMode(
			r.Context(),
			releaseworkflow.TrackerDecisionModePostDupeGate,
		),
		principal.OwnerID,
		request,
	)
	if err != nil {
		writeAPIV1WorkflowError(w, err)
		return
	}
	status := http.StatusOK
	if result.Operation != nil && !apiV1WorkflowOperationTerminal(result.Operation.Status) {
		status = http.StatusAccepted
	}
	writeAPIV1WorkflowResult(w, status, result)
}

func apiV1WorkflowOperationTerminal(status api.StageStatus) bool {
	switch status {
	case api.StageStatusBlocked,
		api.StageStatusStale,
		api.StageStatusFailed,
		api.StageStatusPartial,
		api.StageStatusSkipped,
		api.StageStatusCompleted,
		api.StageStatusExecuted,
		api.StageStatusInterrupted,
		api.StageStatusCanceled,
		api.StageStatusUnavailable:
		return true
	case api.StageStatusPending, api.StageStatusQueued, api.StageStatusReady, api.StageStatusRunning:
		return false
	}
	return false
}

func (s *Server) handleAPIV1WorkflowResource(w http.ResponseWriter, r *http.Request) {
	segments := apiV1WorkflowSegments(r.URL.Path)
	if len(segments) == 0 {
		http.NotFound(w, r)
		return
	}
	scope := APITokenScopeWorkflowWrite
	if r.Method == http.MethodGet {
		scope = APITokenScopeWorkflowRead
	} else if len(segments) == 4 && segments[1] == "uploads" && segments[3] == "retry" ||
		len(segments) == 5 && segments[1] == "uploads" && segments[3] == "client-injections" && segments[4] == "retry" {
		scope = APITokenScopeWorkflowExecute
	}
	principal, ok := s.authenticateAPIRequest(w, r, scope)
	if !ok {
		return
	}
	workflowID := api.WorkflowID(segments[0])
	if workflowID == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		s.handleAPIV1WorkflowRead(w, r, principal, workflowID, segments)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if len(segments) == 4 && segments[1] == "operations" && segments[3] == "cancel" {
		operation, err := s.backend.cancelReleaseWorkflowOperation(
			r.Context(),
			principal.OwnerID,
			workflowID,
			api.WorkflowOperationID(segments[2]),
		)
		if err != nil {
			writeAPIV1WorkflowError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, operation)
		return
	}
	revision, ok := apiV1ExpectedRevision(w, r)
	if !ok {
		return
	}
	if len(segments) == 3 && segments[1] == "media" && segments[2] == "resources" {
		content, err := readStagedMediaUpload(w, r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		resource, err := s.backend.stageReleaseWorkflowMediaResource(
			r.Context(),
			principal.OwnerID,
			workflowID,
			revision,
			content,
		)
		if err != nil {
			writeAPIV1WorkflowError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, resource)
		return
	}
	idempotencyKey, ok := apiV1IdempotencyKey(w, r)
	if !ok {
		return
	}
	if len(segments) == 3 && segments[1] == "media" && segments[2] == "previews" {
		var request api.PreviewReleaseWorkflowFrameRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return
		}
		request.ReleaseWorkflowCommandContext = api.ReleaseWorkflowCommandContext{
			WorkflowID:       workflowID,
			ExpectedRevision: revision,
			IdempotencyKey:   idempotencyKey,
		}
		if err := request.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		preview, err := s.backend.previewReleaseWorkflowFrame(
			r.Context(),
			principal.OwnerID,
			workflowID,
			revision,
			request.TimestampSeconds,
		)
		if err != nil {
			writeAPIV1WorkflowError(w, err)
			return
		}
		preview.ContentURL = "/api/v1/workflows/" + string(workflowID) + "/media/previews/" + string(preview.ID)
		writeJSON(w, http.StatusOK, preview)
		return
	}
	request, ok := s.apiV1WorkflowCommand(w, r, workflowID, revision, idempotencyKey, segments)
	if !ok {
		return
	}
	command, commandOK := request.(releaseworkflow.Command)
	if !commandOK {
		var err error
		command, err = mapReleaseWorkflowRequest(request)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	var result releaseworkflow.CommandResult
	longRunning := isLongRunningReleaseWorkflowCommand(command)
	if longRunning {
		operation, err := s.backend.startReleaseWorkflow(r.Context(), principal.OwnerID, command)
		if err != nil {
			writeAPIV1WorkflowError(w, err)
			return
		}
		result.Workflow.ID = operation.WorkflowID
		result.Operation = &operation
	} else {
		var err error
		result, err = s.backend.executeReleaseWorkflow(r.Context(), principal.OwnerID, command)
		if err != nil {
			writeAPIV1WorkflowError(w, err)
			return
		}
	}
	current, err := s.backend.currentReleaseWorkflow(r.Context(), principal.OwnerID, result.Workflow.ID)
	if err != nil {
		writeAPIV1WorkflowError(w, err)
		return
	}
	current.Operation = result.Operation
	status := http.StatusOK
	if longRunning {
		status = http.StatusAccepted
	}
	writeAPIV1WorkflowResult(w, status, current)
}

func (s *Server) handleAPIV1WorkflowRead(
	w http.ResponseWriter,
	r *http.Request,
	principal apiPrincipal,
	workflowID api.WorkflowID,
	segments []string,
) {
	switch {
	case len(segments) == 1:
		current, err := s.backend.currentReleaseWorkflow(r.Context(), principal.OwnerID, workflowID)
		if err != nil {
			writeAPIV1WorkflowError(w, err)
			return
		}
		writeAPIV1WorkflowResult(w, http.StatusOK, current)
	case len(segments) == 3 && segments[1] == "operations":
		operation, err := s.backend.releaseWorkflowOperation(
			r.Context(),
			principal.OwnerID,
			workflowID,
			api.WorkflowOperationID(segments[2]),
		)
		if err != nil {
			writeAPIV1WorkflowError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, operation)
	case len(segments) == 3 && segments[1] == "media" && segments[2] == "plan":
		plan, err := s.backend.releaseWorkflowMediaPlan(r.Context(), principal.OwnerID, workflowID)
		if err != nil {
			writeAPIV1WorkflowError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, plan)
	case len(segments) == 4 && segments[1] == "media" && segments[2] == "previews":
		content, err := s.backend.openReleaseWorkflowPreview(
			r.Context(),
			principal.OwnerID,
			workflowID,
			api.PublicResourceID(segments[3]),
		)
		if err != nil {
			writeAPIV1WorkflowError(w, err)
			return
		}
		defer content.Body.Close()
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", content.ContentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if _, err := io.Copy(w, content.Body); err != nil {
			s.backend.logDebug("releaseworkflow: preview API response interrupted")
		}
	case len(segments) == 5 && segments[1] == "media" && segments[3] == "artifacts":
		revision, err := strconv.ParseUint(strings.TrimSpace(r.URL.Query().Get("revision")), 10, 64)
		if err != nil || revision == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid media revision is required"})
			return
		}
		content, err := s.backend.openReleaseWorkflowMediaArtifact(
			r.Context(),
			principal.OwnerID,
			workflowID,
			api.MediaArtifactSetRef{
				ID:       api.MediaArtifactSetID(segments[2]),
				Revision: api.WorkflowRevision(revision),
			},
			api.PublicResourceID(segments[4]),
		)
		if err != nil {
			writeAPIV1WorkflowError(w, err)
			return
		}
		defer content.Body.Close()
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", content.ContentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if _, err := io.Copy(w, content.Body); err != nil {
			s.backend.logDebug("releaseworkflow: media API response interrupted")
		}
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) apiV1WorkflowCommand(
	w http.ResponseWriter,
	r *http.Request,
	workflowID api.WorkflowID,
	revision api.WorkflowRevision,
	idempotencyKey string,
	segments []string,
) (any, bool) {
	commandContext := api.ReleaseWorkflowCommandContext{
		WorkflowID:       workflowID,
		ExpectedRevision: revision,
		IdempotencyKey:   idempotencyKey,
	}
	switch {
	case len(segments) == 3 && segments[1] == "trackers" && segments[2] == "invalidate":
		var request api.InvalidateReleaseWorkflowTrackersRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return nil, false
		}
		request.ReleaseWorkflowCommandContext = commandContext
		return mapAPIV1WorkflowRequest(w, request)
	case len(segments) == 4 && segments[1] == "media" && segments[3] == "selection" && r.Method == http.MethodPut:
		var request api.SetReleaseWorkflowMediaSelectionRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return nil, false
		}
		request.ReleaseWorkflowCommandContext = commandContext
		request.Media.ID = api.MediaArtifactSetID(segments[2])
		return mapAPIV1WorkflowRequest(w, request)
	case len(segments) == 4 && segments[1] == "media" && segments[3] == "delete":
		var request api.DeleteReleaseWorkflowMediaRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return nil, false
		}
		request.ReleaseWorkflowCommandContext = commandContext
		request.Media.ID = api.MediaArtifactSetID(segments[2])
		return mapAPIV1WorkflowRequest(w, request)
	case len(segments) == 4 && segments[1] == "media" && segments[3] == "reorder" && r.Method == http.MethodPut:
		var request api.ReorderReleaseWorkflowMediaRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return nil, false
		}
		request.ReleaseWorkflowCommandContext = commandContext
		request.Media.ID = api.MediaArtifactSetID(segments[2])
		return mapAPIV1WorkflowRequest(w, request)
	case len(segments) == 3 && segments[1] == "media" && segments[2] == "attach":
		var request api.AttachReleaseWorkflowMediaRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return nil, false
		}
		request.ReleaseWorkflowCommandContext = commandContext
		return mapAPIV1WorkflowRequest(w, request)
	case len(segments) == 5 && segments[1] == "media" && segments[3] == "images" && segments[4] == "upload":
		var request api.UploadReleaseWorkflowImagesRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return nil, false
		}
		request.ReleaseWorkflowCommandContext = commandContext
		request.Media.ID = api.MediaArtifactSetID(segments[2])
		return mapAPIV1WorkflowRequest(w, request)
	case len(segments) == 5 && segments[1] == "media" && segments[3] == "images" && segments[4] == "retry":
		var request api.RetryReleaseWorkflowImageHostRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return nil, false
		}
		request.ReleaseWorkflowCommandContext = commandContext
		request.Media.ID = api.MediaArtifactSetID(segments[2])
		return mapAPIV1WorkflowRequest(w, request)
	case len(segments) == 5 && segments[1] == "media" && segments[3] == "images" && segments[4] == "remove":
		var request api.RemoveReleaseWorkflowHostedImagesRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return nil, false
		}
		request.ReleaseWorkflowCommandContext = commandContext
		request.Media.ID = api.MediaArtifactSetID(segments[2])
		return mapAPIV1WorkflowRequest(w, request)
	case len(segments) == 6 && segments[1] == "descriptions" && segments[3] == "groups" && segments[5] == "save":
		var request api.SaveReleaseWorkflowDescriptionOverrideRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return nil, false
		}
		request.ReleaseWorkflowCommandContext = commandContext
		request.Override.Descriptions.ID = api.DescriptionSetID(segments[2])
		request.Override.GroupKey = segments[4]
		return mapAPIV1WorkflowRequest(w, request)
	case len(segments) == 6 && segments[1] == "descriptions" && segments[3] == "groups" && segments[5] == "reset":
		var request api.ResetReleaseWorkflowDescriptionOverrideRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return nil, false
		}
		request.ReleaseWorkflowCommandContext = commandContext
		request.Descriptions.ID = api.DescriptionSetID(segments[2])
		request.GroupKey = segments[4]
		return mapAPIV1WorkflowRequest(w, request)
	case len(segments) == 4 && segments[1] == "uploads" && segments[3] == "retry":
		var request api.RetryReleaseWorkflowUploadRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return nil, false
		}
		request.ReleaseWorkflowCommandContext = commandContext
		request.Retry.Result.ID = api.UploadResultID(segments[2])
		return mapAPIV1WorkflowRequest(w, request)
	case len(segments) == 5 && segments[1] == "uploads" && segments[3] == "client-injections" && segments[4] == "retry":
		var request api.RetryReleaseWorkflowClientInjectionRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return nil, false
		}
		request.ReleaseWorkflowCommandContext = commandContext
		request.Retry.Result.ID = api.UploadResultID(segments[2])
		return mapAPIV1WorkflowRequest(w, request)
	case len(segments) == 2 && segments[1] == "cancel":
		var request api.CancelReleaseWorkflowRequest
		if !decodeAPIV1JSON(w, r, &request) {
			return nil, false
		}
		request.ReleaseWorkflowCommandContext = commandContext
		return mapAPIV1WorkflowRequest(w, request)
	default:
		http.NotFound(w, r)
		return nil, false
	}
}

func mapAPIV1WorkflowRequest(w http.ResponseWriter, request any) (any, bool) {
	if validator, ok := request.(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return nil, false
		}
	}
	command, err := mapReleaseWorkflowRequest(request)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return nil, false
	}
	return command, true
}

func isLongRunningReleaseWorkflowCommand(command releaseworkflow.Command) bool {
	_, longRunning := releaseworkflow.CommandOperationKind(command)
	return longRunning
}

func apiV1WorkflowSegments(requestPath string) []string {
	remainder := strings.TrimPrefix(requestPath, "/api/v1/workflows/")
	if remainder == requestPath {
		return nil
	}
	parts := strings.Split(strings.Trim(remainder, "/"), "/")
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || part == "." || part == ".." {
			return nil
		}
	}
	return parts
}

func apiV1ExpectedRevision(w http.ResponseWriter, r *http.Request) (api.WorkflowRevision, bool) {
	value := strings.TrimSpace(r.Header.Get("If-Match"))
	value = strings.TrimPrefix(value, "W/")
	value = strings.Trim(value, "\"")
	revision, err := strconv.ParseUint(value, 10, 64)
	if err != nil || revision == 0 {
		writeJSON(w, http.StatusPreconditionRequired, map[string]string{"error": "valid If-Match workflow revision required"})
		return 0, false
	}
	return api.WorkflowRevision(revision), true
}

func apiV1IdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > apiV1IdempotencyMax {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Idempotency-Key header is required and must not exceed 128 characters"})
		return "", false
	}
	return key, true
}

func decodeAPIV1JSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, apiV1RequestMaxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		status := http.StatusBadRequest
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, map[string]string{"error": "invalid API request body"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "API request body must contain one JSON value"})
		return false
	}
	return true
}

func writeAPIV1WorkflowResult(w http.ResponseWriter, status int, result releaseworkflow.CommandResult) {
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", result.Workflow.Revision))
	writeJSON(w, status, result)
}

func writeAPIV1WorkflowError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, releaseworkflow.ErrWorkflowNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "release workflow not found"})
	case errors.Is(err, releaseworkflow.ErrRevisionConflict), errors.Is(err, releaseworkflow.ErrIdempotencyConflict):
		writeAppError(w, err)
	default:
		writeAppError(w, err)
	}
}
