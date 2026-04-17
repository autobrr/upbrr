// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/status", func(w http.ResponseWriter, r *http.Request) { s.handleAuthStatus(w, r, session{}) })
	mux.HandleFunc("/api/auth/bootstrap", func(w http.ResponseWriter, r *http.Request) { s.handleBootstrap(w, r, session{}) })
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) { s.handleLogin(w, r, session{}) })
	mux.HandleFunc("/api/auth/logout", s.requireSession(s.handleLogout))
	mux.HandleFunc("/api/events", s.requireSession(s.handleEvents))

	s.registerAppRoutes(mux)

	fileServer := http.FileServer(http.FS(s.assets))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/" {
			http.ServeFileFS(w, r, s.assets, "index.html")
			return
		}
		if _, err := fsStat(s.assets, strings.TrimPrefix(path.Clean(r.URL.Path), "/")); err != nil {
			http.ServeFileFS(w, r, s.assets, "index.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request, _ session) {
	exists, err := s.auth.Exists()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	current, ok := s.currentSession(r)
	browseAvailable := s.nativeBrowseAvailable(r)
	payload := map[string]any{
		"authenticated":       ok,
		"needsSetup":          !exists,
		"username":            "",
		"csrfToken":           "",
		"nativeBrowseEnabled": browseAvailable,
	}
	if ok {
		payload["username"] = current.Username
		payload["csrfToken"] = current.CSRFToken
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request, _ session) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.allowAuthRequest(r) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return
	}
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		RetainLogin bool   `json:"retainLogin"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.auth.Bootstrap(req.Username, req.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	current, err := s.sessions.Create(req.Username, req.RetainLogin)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.writeSessionCookie(w, r, current)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":       true,
		"needsSetup":          false,
		"username":            current.Username,
		"csrfToken":           current.CSRFToken,
		"nativeBrowseEnabled": s.nativeBrowseAvailable(r),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request, _ session) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.allowAuthRequest(r) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return
	}
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		RetainLogin bool   `json:"retainLogin"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	record, err := s.auth.Load()
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	if strings.TrimSpace(record.Username) != strings.TrimSpace(req.Username) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	valid, needsUpgrade := verifyPasswordWithUpgrade(req.Password, record.PasswordHash)
	if !valid {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	if needsUpgrade {
		upgradedHash, err := hashPassword(req.Password)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to refresh credentials"})
			return
		}
		upgradedRecord := record
		upgradedRecord.PasswordHash = upgradedHash
		if strings.TrimSpace(upgradedRecord.EncryptionKeySeed) == "" {
			seed, err := generateStableEncryptionSeed()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to refresh credentials"})
				return
			}
			upgradedRecord.EncryptionKeySeed = seed
		}
		if err := s.rewrapProtectedDataForAuthChange(r, record, upgradedRecord); err != nil {
			if s.backend != nil && s.backend.logger != nil {
				s.backend.logger.Errorf("web: failed to rewrap protected data for %s during auth upgrade: %v", record.Username, err)
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to refresh credentials"})
			return
		}
		if err := s.auth.UpdateRecord(upgradedRecord); err != nil {
			if rollbackErr := s.rollbackProtectedDataForAuthChange(r, upgradedRecord, record); rollbackErr != nil {
				if s.backend != nil && s.backend.logger != nil {
					s.backend.logger.Errorf("web: failed to rollback protected data for %s after auth upgrade failure: %v", record.Username, rollbackErr)
				}
			}
			if s.backend != nil && s.backend.logger != nil {
				s.backend.logger.Errorf("web: failed to upgrade legacy auth hash for %s: %v", record.Username, err)
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to refresh credentials"})
			return
		}
	}
	current, err := s.sessions.Create(record.Username, req.RetainLogin)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.writeSessionCookie(w, r, current)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":       true,
		"needsSetup":          false,
		"username":            current.Username,
		"csrfToken":           current.CSRFToken,
		"nativeBrowseEnabled": s.nativeBrowseAvailable(r),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, current session) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if err := s.sessions.Delete(current.ID); err != nil {
		if s != nil && s.backend != nil && s.backend.logger != nil {
			s.backend.logger.Errorf("web: failed to delete session during logout: %v", err)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to clear session"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, current session) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsubscribe := s.hub.Subscribe(current.ID)
	defer unsubscribe()
	defer s.backend.StopSessionLogStreams(current.ID)

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "event: %s\n", event.Name)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event.Data)
			flusher.Flush()
		case <-ticker.C:
			_, _ = io.WriteString(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) requireSession(next func(http.ResponseWriter, *http.Request, session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.allowGeneralRequest(r) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
			return
		}
		current, ok := s.currentSession(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if !s.verifySameOrigin(r) || !s.verifyCSRF(r, current) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf validation failed"})
				return
			}
		}
		next(w, r, current)
	}
}

func (s *Server) currentSession(r *http.Request) (session, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return session{}, false
	}
	return s.sessions.Get(cookie.Value)
}

func (s *Server) writeSessionCookie(w http.ResponseWriter, r *http.Request, current session) {
	cookie := &http.Cookie{
		Name:     sessionCookieName,
		Value:    current.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.requestScheme(r) == "https",
	}
	if current.Retain {
		cookie.Expires = current.ExpiresAt
		cookie.MaxAge = max(1, int(time.Until(current.ExpiresAt).Seconds()))
	}
	http.SetCookie(w, cookie)
}

func (s *Server) allowAuthRequest(r *http.Request) bool {
	return s.authLimiter.Allow(s.clientIP(r))
}

func (s *Server) allowGeneralRequest(r *http.Request) bool {
	return s.generalLimiter.Allow(s.clientIP(r))
}

func (s *Server) verifyCSRF(r *http.Request, current session) bool {
	token := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
	if token == "" {
		return false
	}
	return token == current.CSRFToken
}

func (s *Server) verifySameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		origin = strings.TrimSpace(r.Header.Get("Referer"))
	}
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func (s *Server) clientIP(r *http.Request) string {
	ip := ipFromAddr(r.RemoteAddr)
	if !s.isTrustedProxy(net.ParseIP(ip)) {
		return ip
	}
	forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	if forwarded == "" {
		return ip
	}
	return forwarded
}

func (s *Server) nativeBrowseAvailable(r *http.Request) bool {
	if s == nil || s.picker == nil || r == nil {
		return false
	}
	return s.isLocalWebUIRequest(r)
}

func (s *Server) isLocalWebUIRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return false
	}
	hostname := host
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		hostname = parsedHost
	}
	hostname = strings.Trim(hostname, "[]")
	if !isLoopbackHostname(hostname) {
		return false
	}
	clientIP := net.ParseIP(strings.TrimSpace(s.clientIP(r)))
	return clientIP != nil && clientIP.IsLoopback()
}

func isLoopbackHostname(host string) bool {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return false
	}
	if strings.EqualFold(trimmed, "localhost") || strings.HasSuffix(strings.ToLower(trimmed), ".localhost") {
		return true
	}
	ip := net.ParseIP(trimmed)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) requestScheme(r *http.Request) string {
	if strings.EqualFold(r.URL.Scheme, "https") {
		return "https"
	}
	ip := net.ParseIP(ipFromAddr(r.RemoteAddr))
	if s.isTrustedProxy(ip) {
		if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
			return strings.ToLower(forwarded)
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func (s *Server) isTrustedProxy(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, network := range s.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func decodeJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dest)
}

func fsStat(root fs.FS, name string) (fs.FileInfo, error) {
	return fs.Stat(root, name)
}
