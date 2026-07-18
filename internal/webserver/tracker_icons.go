// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/services/trackericon"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
)

// resolveTrackerIconTarget accepts only registered trackers and falls back to
// the registry-owned base URL when no explicit favicon URL is supplied.
func resolveTrackerIconTarget(trackerName string, faviconURL string) (string, string, error) {
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		return "", "", fmt.Errorf("tracker icon: build registry: %w", err)
	}
	descriptor, ok := registry.LookupDescriptor(trackerName)
	if !ok {
		return "", "", fmt.Errorf("tracker icon: unsupported tracker %q", strings.TrimSpace(trackerName))
	}
	urlToUse := strings.TrimSpace(faviconURL)
	if urlToUse == "" {
		urlToUse = descriptor.BaseURL
	}
	return descriptor.Name, urlToUse, nil
}

// handleTrackerIcon serves decoded tracker icon bytes only for an allowlisted
// image MIME type and disables browser content sniffing.
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
	domain, urlToUse, err := resolveTrackerIconTarget(req.Domain, req.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
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
			s.backend.logErrorf("trackericon: get tracker icon %s: %v", sanitized, err)
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
