// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/trackericon"
)

func (s *Server) handleTrackerIcon(w http.ResponseWriter, r *http.Request, _ session) {
	trackerNameOrDomain := strings.TrimSpace(r.URL.Query().Get("domain"))
	customURL := strings.TrimSpace(r.URL.Query().Get("url"))

	domain, resolvedURL := config.ResolveTrackerDomain(&s.cfg, trackerNameOrDomain)
	urlToUse := customURL
	if urlToUse == "" {
		urlToUse = resolvedURL
	}

	sanitized := trackericon.SafeDomainFilename(domain)
	if sanitized == "" {
		http.Error(w, "invalid domain", http.StatusBadRequest)
		return
	}

	dataURL, err := trackericon.GetTrackerIcon(r.Context(), s.cfg.MainSettings.DBPath, domain, urlToUse)
	if err != nil {
		if strings.Contains(err.Error(), "negative cached") || strings.Contains(err.Error(), "failed to fetch") {
			http.NotFound(w, r)
			return
		}
		s.backend.logger.Errorf("trackericon: get tracker icon %s: %v", sanitized, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// dataURL is "data:<mime>;base64,<payload>"
	// Let's decode the payload to serve the raw binary data with proper headers.
	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	mimePart := parts[0]
	base64Data := parts[1]

	mime := "image/x-icon"
	if strings.HasPrefix(mimePart, "data:") && strings.HasSuffix(mimePart, ";base64") {
		mime = mimePart[5 : len(mimePart)-7]
	}

	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=604800") // 7 days browser cache
	w.Header().Set("Content-Type", mime)
	//nolint:gosec // Tainted icon data is safe to serve with explicit image content-type
	_, _ = w.Write(data)
}
