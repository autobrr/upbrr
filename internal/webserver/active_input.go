// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

type activeInputCapability interface {
	GetActiveInput(context.Context, string) (api.ActiveInputSnapshot, error)
	OpenActiveInput(context.Context, string, api.OpenActiveInputRequest) (api.ActiveInputSnapshot, error)
	ReleaseActiveInput(context.Context, string, api.ReleaseActiveInputRequest) (api.ActiveInputSnapshot, error)
	RecoverLegacyActiveInput(context.Context, string, api.RecoverLegacyActiveInputRequest) (api.ActiveInputSnapshot, error)
	ReconcileActiveInput(context.Context, string, api.ReconcileActiveInputRequest) (api.ActiveInputSnapshot, error)
}

func (s *Server) registerActiveInputRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/app/GetActiveInput", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		s.withActiveInput(w, r, func(ctx context.Context, capability activeInputCapability) (api.ActiveInputSnapshot, error) {
			return capability.GetActiveInput(ctx, current.ID)
		})
	}))
	mux.HandleFunc("/api/app/OpenActiveInput", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var request api.OpenActiveInputRequest
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.withActiveInput(w, r, func(ctx context.Context, capability activeInputCapability) (api.ActiveInputSnapshot, error) {
			ctx = api.WithPreparationProgressReporter(
				ctx,
				activeInputVerificationReporter(s.backend.hub, current.ID, request.Request.IdempotencyKey),
			)
			result, err := capability.OpenActiveInput(
				releaseworkflow.WithTrackerDecisionMode(ctx, releaseworkflow.TrackerDecisionModeWebUIControls),
				current.ID,
				request,
			)
			if err != nil {
				return result, fmt.Errorf("web open active input: %w", err)
			}
			if s.backend.hub != nil {
				s.backend.hub.Emit(current.ID, "input:changed", map[string]uint64{"revision": result.Revision})
			}
			return result, nil
		})
	}))
	mux.HandleFunc("/api/app/ReleaseActiveInput", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var request api.ReleaseActiveInputRequest
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.withActiveInput(w, r, func(ctx context.Context, capability activeInputCapability) (api.ActiveInputSnapshot, error) {
			result, err := capability.ReleaseActiveInput(ctx, current.ID, request)
			if err != nil {
				return result, fmt.Errorf("web release active input: %w", err)
			}
			if s.backend.hub != nil {
				s.backend.hub.Emit(current.ID, "input:changed", map[string]uint64{"revision": result.Revision})
			}
			return result, nil
		})
	}))
	mux.HandleFunc("/api/app/RecoverLegacyActiveInput", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var request api.RecoverLegacyActiveInputRequest
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.withActiveInput(w, r, func(ctx context.Context, capability activeInputCapability) (api.ActiveInputSnapshot, error) {
			result, err := capability.RecoverLegacyActiveInput(ctx, current.ID, request)
			if err != nil {
				return result, fmt.Errorf("web recover active input: %w", err)
			}
			if s.backend.hub != nil {
				s.backend.hub.Emit(current.ID, "input:changed", map[string]uint64{"revision": result.Revision})
			}
			return result, nil
		})
	}))
	mux.HandleFunc("/api/app/ReconcileActiveInput", s.requireSession(func(w http.ResponseWriter, r *http.Request, current session) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var request api.ReconcileActiveInputRequest
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.withActiveInput(w, r, func(ctx context.Context, capability activeInputCapability) (api.ActiveInputSnapshot, error) {
			result, err := capability.ReconcileActiveInput(ctx, current.ID, request)
			if err != nil {
				return result, fmt.Errorf("web reconcile active input: %w", err)
			}
			if s.backend.hub != nil {
				s.backend.hub.Emit(current.ID, "input:changed", map[string]uint64{"revision": result.Revision})
			}
			return result, nil
		})
	}))
}

// activeInputVerificationReporter sends owner-scoped, frontend-safe source
// verification progress for one explicit active-input open request.
func activeInputVerificationReporter(
	hub *eventHub,
	sessionID string,
	correlationID string,
) api.PreparationProgressReporter {
	return func(update api.PreparationProgressUpdate) {
		if hub == nil || update.Phase != api.PreparationPhaseSourceInspection {
			return
		}
		update.CorrelationID = correlationID
		update.Label = logging.SanitizeMessage(update.Label)
		update.Message = logging.SanitizeMessage(update.Message)
		update.CompletedBytes = max(update.CompletedBytes, 0)
		update.TotalBytes = max(update.TotalBytes, 0)
		if update.TotalBytes > 0 {
			update.CompletedBytes = min(update.CompletedBytes, update.TotalBytes)
		}
		update.Timestamp = time.Now().UTC().Format(time.RFC3339)
		hub.Emit(sessionID, "input:verification", update)
	}
}

func (s *Server) withActiveInput(w http.ResponseWriter, r *http.Request,
	operation func(context.Context, activeInputCapability) (api.ActiveInputSnapshot, error),
) {
	runtime, err := s.backend.borrowRuntime()
	if err != nil {
		writeAppError(w, err)
		return
	}
	defer runtime.release()
	workflow, err := runtime.releaseWorkflowCore()
	if err != nil {
		writeAppError(w, err)
		return
	}
	capability, ok := workflow.(activeInputCapability)
	if !ok {
		writeAppError(w, errors.New("active input capability unavailable"))
		return
	}
	result, err := operation(r.Context(), capability)
	if err != nil {
		s.backend.logErrorf("active input: state=failed cause=%s", releaseWorkflowDiagnosticMessage(err))
		writeAppError(w, classifyReleaseWorkflowError(err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}
