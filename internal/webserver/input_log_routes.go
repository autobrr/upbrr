// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/hex"
	"io"
	"net/http"
	"path/filepath"
	"unicode/utf8"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

type inputHistoryList struct {
	Entries         []api.HistoryEntry
	LogsUnavailable bool `json:",omitempty"`
}

func (s *Server) registerInputLogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/app/ListInputHistory", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		entries, err := s.backend.ListHistory(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		storedPaths := make([]string, 0, len(entries))
		for _, entry := range entries {
			storedPaths = append(storedPaths, entry.SourcePath)
		}
		logOnly, logErr := s.backend.repo.InputLogSources(r.Context(), storedPaths)
		for _, source := range logOnly {
			entries = append(entries, api.HistoryEntry{
				SourcePath:   source,
				ReleaseTitle: filepath.Base(source),
				LogsOnly:     true,
			})
		}
		writeJSON(w, http.StatusOK, inputHistoryList{Entries: nonNilAppList(entries), LogsUnavailable: logErr != nil})
	}))
	mux.HandleFunc("POST /api/app/GetInputLog", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
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
		data, err := logging.ReadInputLogs(r.Context(), s.backend.currentLogger(), s.backend.repo.DBPath(), attempts)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Could not read retained input logs."})
			return
		}
		id := ""
		if data.Text != "" && len(attempts) != 0 {
			id = attempts[0].ID
		}
		const previewLimit = 256 * 1024
		if len(data.Text) > previewLimit {
			end := len(data.Text) - previewLimit
			for end < len(data.Text) && !utf8.RuneStart(data.Text[end]) {
				end++
			}
			data.Text = "Showing the end of the log. Download for all retained entries.\n\n" + data.Text[end:]
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, struct {
			logging.InputLogExport
			DownloadID string
		}{data, id})
	}))
	mux.HandleFunc("GET /api/app/InputLog/{id}", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		id := r.PathValue("id")
		if _, err := hex.DecodeString(id); len(id) != 24 || err != nil {
			http.NotFound(w, r)
			return
		}
		anchor, err := s.backend.repo.InputLogs(r.Context(), "", id)
		if err != nil || len(anchor) != 1 {
			http.NotFound(w, r)
			return
		}
		attempts, err := s.backend.repo.InputLogs(r.Context(), anchor[0].SourcePath, "")
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Input log history is unavailable."})
			return
		}
		data, err := logging.ReadInputLogs(r.Context(), s.backend.currentLogger(), s.backend.repo.DBPath(), attempts)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Could not read retained input logs."})
			return
		}
		if data.Text == "" {
			http.Error(w, "The retained log has expired or was not saved.", http.StatusNotFound)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", `attachment; filename="upbrr-input-`+id+`.log"`)
		_, _ = io.WriteString(w, data.Text)
	}))
}
