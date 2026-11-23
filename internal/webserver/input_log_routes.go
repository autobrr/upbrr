// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/hex"
	"io"
	"net/http"

	"github.com/autobrr/upbrr/internal/logging"
)

func (s *Server) registerInputLogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/app/ListInputLogSources", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		paths, err := s.backend.repo.InputLogSources(r.Context())
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Input log history is unavailable."})
			return
		}
		writeJSON(w, http.StatusOK, paths)
	}))
	mux.HandleFunc("POST /api/app/ListInputLogs", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
		var req struct{ SourcePath string }
		if err := decodeJSON(r, &req); err != nil || req.SourcePath == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Select an input to see its logs."})
			return
		}
		attempts, err := s.backend.repo.InputLogs(r.Context(), req.SourcePath, "")
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Input log history is unavailable."})
			return
		}
		writeJSON(w, http.StatusOK, attempts)
	}))
	mux.HandleFunc("GET /api/app/InputLog/{id}", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		id := r.PathValue("id")
		if _, err := hex.DecodeString(id); len(id) != 24 || err != nil {
			http.NotFound(w, r)
			return
		}
		attempts, err := s.backend.repo.InputLogs(r.Context(), "", id)
		if err != nil || len(attempts) != 1 {
			http.NotFound(w, r)
			return
		}
		data, err := logging.ReadInputLog(r.Context(), s.backend.currentLogger(), s.backend.repo.DBPath(), attempts[0])
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Could not read retained input logs."})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Query().Get("download") != "1" {
			writeJSON(w, http.StatusOK, data)
			return
		}
		if data.Text == "" {
			http.Error(w, "The retained log has expired or was not saved.", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", `attachment; filename="upbrr-input-`+id+`.log"`)
		_, _ = io.WriteString(w, data.Text)
	}))
}
