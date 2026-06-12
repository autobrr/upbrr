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
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Domain string
		URL    string
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := s.cfg
	if s.backend != nil {
		cfg = s.backend.currentConfig()
	}
	domain, resolvedURL := config.ResolveTrackerDomain(&cfg, strings.TrimSpace(req.Domain))
	urlToUse := strings.TrimSpace(req.URL)
	if urlToUse == "" {
		urlToUse = resolvedURL
	}

	sanitized := trackericon.SafeDomainFilename(domain)
	if sanitized == "" {
		http.Error(w, "invalid domain", http.StatusBadRequest)
		return
	}

	dataURL, err := trackericon.GetTrackerIcon(r.Context(), cfg.MainSettings.DBPath, domain, urlToUse)
	if err != nil {
		if strings.Contains(err.Error(), "negative cached") || strings.Contains(err.Error(), "failed to fetch") {
			http.NotFound(w, r)
			return
		}
		if s.backend != nil {
			if logger := s.backend.currentLogger(); logger != nil {
				logger.Errorf("trackericon: get tracker icon %s: %v", sanitized, err)
			}
		}
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

	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/x-icon", "image/bmp", "image/vnd.microsoft.icon":
		// Safe image MIME
	default:
		// Not a safe image MIME, fallback or reject to avoid serving active content
		http.Error(w, "invalid image type", http.StatusUnsupportedMediaType)
		return
	}

	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=604800") // 7 days browser cache
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", mime)
	_, _ = w.Write(data)
}
