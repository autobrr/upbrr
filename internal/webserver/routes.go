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
	"os"
	"path" //nolint:depguard // Cleans HTTP route paths, not local filesystem paths.
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/redaction"
)

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/status", func(w http.ResponseWriter, r *http.Request) { s.handleAuthStatus(w, r, session{}) })
	mux.HandleFunc("/api/auth/bootstrap", func(w http.ResponseWriter, r *http.Request) { s.handleBootstrap(w, r, session{}) })
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) { s.handleLogin(w, r, session{}) })
	mux.HandleFunc("/api/auth/logout", s.requireSession(s.handleLogout))
	mux.HandleFunc("/api/auth/browse-policy", s.requireSession(s.handleBrowsePolicy))
	mux.HandleFunc("/api/events", s.requireSession(s.handleEvents))
	mux.HandleFunc("/api/app/TrackerIcon", s.requireSession(s.handleTrackerIcon))

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
	if current, ok := s.developmentCurrentSession(r); ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated":           true,
			"needsSetup":              false,
			"username":                current.Username,
			"csrfToken":               current.CSRFToken,
			"nativeBrowseEnabled":     s.nativeBrowseAvailable(r),
			"browseRoot":              "",
			"allowUnrestrictedBrowse": true,
			"needsBrowsePolicy":       false,
		})
		return
	}

	exists, err := s.auth.Exists()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	current, ok := s.currentSession(r)
	browseAvailable := s.nativeBrowseAvailable(r)
	payload := map[string]any{
		"authenticated":           ok,
		"needsSetup":              !exists,
		"username":                "",
		"csrfToken":               "",
		"nativeBrowseEnabled":     browseAvailable,
		"browseRoot":              "",
		"allowUnrestrictedBrowse": false,
		"needsBrowsePolicy":       false,
	}
	if exists {
		record, err := s.auth.Load()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if ok {
			browseRoots := recordBrowseRoots(record)
			payload["browseRoot"] = joinBrowsePolicyRoots(browseRoots)
			payload["allowUnrestrictedBrowse"] = record.AllowUnrestrictedBrowse
			payload["needsBrowsePolicy"] = !record.AllowUnrestrictedBrowse && len(browseRoots) == 0
		}
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
	if !s.isLocalWebUIRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "bootstrap is only available from localhost web sessions"})
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
		"authenticated":           true,
		"needsSetup":              false,
		"username":                current.Username,
		"csrfToken":               current.CSRFToken,
		"nativeBrowseEnabled":     s.nativeBrowseAvailable(r),
		"browseRoot":              "",
		"allowUnrestrictedBrowse": false,
		"needsBrowsePolicy":       true,
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
	if record.PendingUpgrade != nil {
		target := record.PendingUpgrade.Target
		if err := s.rewrapProtectedDataForAuthChange(r.Context(), record, target); err != nil {
			if s.backend != nil && s.backend.logger != nil {
				s.backend.logger.Errorf(
					"web: auth upgrade failed incident=%s username=%s",
					"auth_upgrade_resume_rewrap_failed",
					redactAuthUsername(record.Username),
				)
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to refresh credentials"})
			return
		}
		finalized, err := s.auth.FinalizePendingUpgrade(record.Username)
		if err != nil {
			if s.backend != nil && s.backend.logger != nil {
				s.backend.logger.Errorf(
					"web: auth upgrade failed incident=%s username=%s",
					"auth_upgrade_resume_finalize_failed",
					redactAuthUsername(record.Username),
				)
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to refresh credentials"})
			return
		}
		record = finalized
	} else if needsUpgrade {
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
		if err := s.rewrapProtectedDataForAuthChange(r.Context(), record, upgradedRecord); err != nil {
			if s.backend != nil && s.backend.logger != nil {
				s.backend.logger.Errorf(
					"web: auth upgrade failed incident=%s username=%s",
					"auth_upgrade_rewrap_failed",
					redactAuthUsername(record.Username),
				)
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to refresh credentials"})
			return
		}
		finalized, err := s.auth.FinalizePendingUpgrade(record.Username)
		if err != nil {
			if s.backend != nil && s.backend.logger != nil {
				s.backend.logger.Errorf(
					"web: auth upgrade failed incident=%s username=%s",
					"auth_upgrade_finalize_failed",
					redactAuthUsername(record.Username),
				)
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to refresh credentials"})
			return
		}
		record = finalized
	}
	current, err := s.sessions.Create(record.Username, req.RetainLogin)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.writeSessionCookie(w, r, current)
	browseRoots := recordBrowseRoots(record)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":           true,
		"needsSetup":              false,
		"username":                current.Username,
		"csrfToken":               current.CSRFToken,
		"nativeBrowseEnabled":     s.nativeBrowseAvailable(r),
		"browseRoot":              joinBrowsePolicyRoots(browseRoots),
		"allowUnrestrictedBrowse": record.AllowUnrestrictedBrowse,
		"needsBrowsePolicy":       !record.AllowUnrestrictedBrowse && len(browseRoots) == 0,
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
	//nolint:gosec // clearing session cookie doesn't require strict security flags
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleBrowsePolicy(w http.ResponseWriter, r *http.Request, current session) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		BrowseRoot              string `json:"browseRoot"`
		AllowUnrestrictedBrowse bool   `json:"allowUnrestrictedBrowse"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	roots, err := normalizeBrowsePolicyRoots(splitBrowsePolicyRoots(req.BrowseRoot))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !req.AllowUnrestrictedBrowse && len(roots) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one browse root is required unless unrestricted browsing is explicitly allowed"})
		return
	}

	record, err := s.auth.Load()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(record.Username) != strings.TrimSpace(current.Username) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "session user does not match auth record"})
		return
	}
	record.BrowseRoot = joinBrowsePolicyRoots(roots)
	record.AllowUnrestrictedBrowse = req.AllowUnrestrictedBrowse
	if err := s.auth.UpdateRecord(record); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":           true,
		"needsSetup":              false,
		"username":                current.Username,
		"csrfToken":               current.CSRFToken,
		"nativeBrowseEnabled":     s.nativeBrowseAvailable(r),
		"browseRoot":              joinBrowsePolicyRoots(roots),
		"allowUnrestrictedBrowse": req.AllowUnrestrictedBrowse,
		"needsBrowsePolicy":       false,
	})
}

func splitBrowsePolicyRoots(value string) []string {
	parts := strings.Split(value, ",")
	roots := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			roots = append(roots, trimmed)
		}
	}
	return roots
}

func normalizeBrowsePolicyRoots(values []string) ([]string, error) {
	roots := make([]string, 0, len(values))
	for _, value := range values {
		root, err := normalizeBrowsePolicyRoot(value)
		if err != nil {
			return nil, err
		}
		if root == "" {
			continue
		}
		duplicate := false
		for _, existing := range roots {
			if sameFilesystemPath(existing, root) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			roots = append(roots, root)
		}
	}
	return roots, nil
}

func normalizeBrowsePolicyRoot(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	root, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return "", fmt.Errorf("browse root: resolve path: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("browse root: stat path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("browse root %q is not a directory", root)
	}
	return root, nil
}

func recordBrowseRoots(record authmaterial.Record) []string {
	roots := splitBrowsePolicyRoots(record.BrowseRoot)
	normalized, err := normalizeBrowsePolicyRoots(roots)
	if err != nil {
		return roots
	}
	return normalized
}

func joinBrowsePolicyRoots(roots []string) string {
	return strings.Join(roots, ", ")
}

func sameFilesystemPath(left string, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
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
			if !s.verifySameOrigin(r, current) || !s.verifyCSRF(r, current) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf validation failed"})
				return
			}
		}
		next(w, r, current)
	}
}

func (s *Server) currentSession(r *http.Request) (session, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil && s.sessions != nil {
		if current, ok := s.sessions.Get(cookie.Value); ok {
			return current, true
		}
	}
	return s.developmentCurrentSession(r)
}

func (s *Server) developmentCurrentSession(r *http.Request) (session, bool) {
	if s == nil || !s.developmentNoAuth || !s.isLocalWebUIRequest(r) {
		return session{}, false
	}
	current := s.developmentSession
	if current.ID == "" || current.CSRFToken == "" {
		return session{}, false
	}
	return current, true
}

func (s *Server) isDevelopmentSession(current session) bool {
	return s != nil &&
		s.developmentNoAuth &&
		s.developmentSession.ID != "" &&
		current.ID == s.developmentSession.ID &&
		current.CSRFToken == s.developmentSession.CSRFToken
}

func (s *Server) writeSessionCookie(w http.ResponseWriter, r *http.Request, current session) {
	//nolint:gosec // secure flag is configured dynamically based on incoming request scheme
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
	token := strings.TrimSpace(r.Header.Get("X-Csrf-Token"))
	if token == "" {
		return false
	}
	return token == current.CSRFToken
}

func (s *Server) verifySameOrigin(r *http.Request, current session) bool {
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
	if strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	return s.isDevelopmentSession(current) && isLoopbackHostPort(parsed.Host) && isLoopbackHostPort(r.Host)
}

func isLoopbackHostPort(host string) bool {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return false
	}
	if parsedHost, _, err := net.SplitHostPort(trimmed); err == nil {
		trimmed = parsedHost
	}
	return isLoopbackHostname(strings.Trim(trimmed, "[]"))
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
	if err := json.NewDecoder(r.Body).Decode(dest); err != nil {
		return fmt.Errorf("web: decode request JSON: %w", err)
	}
	return nil
}

func fsStat(root fs.FS, name string) (fs.FileInfo, error) {
	info, err := fs.Stat(root, name)
	if err != nil {
		return nil, fmt.Errorf("stat asset %q: %w", name, err)
	}
	return info, nil
}

func redactAuthUsername(username string) string {
	return redaction.RedactValue(strings.TrimSpace(username), nil)
}
