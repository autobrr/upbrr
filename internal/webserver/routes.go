// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"bytes"
	"encoding/csv"
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
	"sync"
	"time"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/redaction"
)

// eventSessionLogStopGracePeriod lets fast SSE reconnects register a replacement
// subscriber before per-session log streams are stopped as idle.
const eventSessionLogStopGracePeriod = 50 * time.Millisecond

var sessionLogStopGenerations = struct {
	mu     sync.Mutex
	byServ map[*Server]map[string]uint64
}{
	byServ: make(map[*Server]map[string]uint64),
}

// nextSessionLogStopGeneration records a new idle-stop attempt for a session
// and returns the generation that delayed cleanup must still own before it can
// stop shared session log streams.
func nextSessionLogStopGeneration(s *Server, sessionID string) uint64 {
	sessionLogStopGenerations.mu.Lock()
	defer sessionLogStopGenerations.mu.Unlock()

	return nextSessionLogStopGenerationLocked(s, sessionID)
}

func nextSessionLogStopGenerationLocked(s *Server, sessionID string) uint64 {
	if _, ok := sessionLogStopGenerations.byServ[s]; !ok {
		sessionLogStopGenerations.byServ[s] = make(map[string]uint64)
	}
	sessionLogStopGenerations.byServ[s][sessionID]++
	return sessionLogStopGenerations.byServ[s][sessionID]
}

// clearSessionLogStopGeneration drops idle-stop generation state after the
// owning delayed cleanup exits, regardless of whether it stopped the session
// log streams or yielded to a replacement subscriber.
func clearSessionLogStopGeneration(s *Server, sessionID string) {
	sessionLogStopGenerations.mu.Lock()
	defer sessionLogStopGenerations.mu.Unlock()

	clearSessionLogStopGenerationLocked(s, sessionID)
}

func clearSessionLogStopGenerationLocked(s *Server, sessionID string) {
	sessionGenerations, ok := sessionLogStopGenerations.byServ[s]
	if !ok {
		return
	}
	delete(sessionGenerations, sessionID)
	if len(sessionGenerations) == 0 {
		delete(sessionLogStopGenerations.byServ, s)
	}
}

// registerRoutes installs the embedded web UI under the configured external
// base path. In base-path mode, root "/" redirects to the base path and root
// API paths remain unavailable.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	rootMux := http.NewServeMux()
	s.registerRootRoutes(rootMux)

	basePath := s.externalBasePath()
	if basePath != "" {
		mux.HandleFunc(basePath, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, basePath+"/", http.StatusMovedPermanently)
		})
		mux.Handle(basePath+"/", http.StripPrefix(basePath, rootMux))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				http.Redirect(w, r, basePath+"/", http.StatusFound)
				return
			}
			http.NotFound(w, r)
		})
		return
	}
	mux.Handle("/", rootMux)
}

// registerRootRoutes installs the unprefixed API and UI routes. When a base
// path is configured, the same handler is mounted under that prefix with the
// prefix stripped before dispatch.
func (s *Server) registerRootRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/status", func(w http.ResponseWriter, r *http.Request) { s.handleAuthStatus(w, r, session{}) })
	mux.HandleFunc("/api/auth/bootstrap", func(w http.ResponseWriter, r *http.Request) { s.handleBootstrap(w, r, session{}) })
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) { s.handleLogin(w, r, session{}) })
	mux.HandleFunc("/api/auth/oidc/login", s.handleOIDCLogin)
	mux.HandleFunc("/api/auth/oidc/callback", s.handleOIDCCallback)
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
			s.serveIndex(w, r)
			return
		}
		assetName := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if _, err := fsStat(s.assets, assetName); err != nil {
			s.serveIndex(w, r)
			return
		}
		if assetName == "index.html" {
			s.serveIndex(w, r)
			return
		}
		if assetName == "site.webmanifest" {
			s.serveWebManifest(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// serveIndex injects the browser base-path contract into the SPA shell before
// sending it. This keeps one embedded frontend build usable at root or under a
// reverse-proxy path prefix.
func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	raw, err := fs.ReadFile(s.assets, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(rewriteIndexHTML(raw, s.externalBaseURLPath()))
}

// serveWebManifest prefixes root-absolute manifest asset paths with the active
// external base path.
func (s *Server) serveWebManifest(w http.ResponseWriter, r *http.Request) {
	raw, err := fs.ReadFile(s.assets, "site.webmanifest")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	_, _ = w.Write(rewriteRootAbsoluteAssetPaths(raw, s.externalBaseURLPath()))
}

// rewriteIndexHTML prefixes root-absolute asset links and injects
// window.__UPBRR_BASE_URL__ before the closing head tag.
func rewriteIndexHTML(raw []byte, baseURLPath string) []byte {
	rewritten := rewriteRootAbsoluteAssetPaths(raw, baseURLPath)
	baseScriptValue, err := json.Marshal(baseURLPath)
	if err != nil {
		return rewritten
	}
	injected := []byte("<script>window.__UPBRR_BASE_URL__=" + string(baseScriptValue) + ";</script>")
	headClose := []byte("</head>")
	idx := bytes.Index(bytes.ToLower(rewritten), headClose)
	if idx < 0 {
		return rewritten
	}
	next := make([]byte, 0, len(rewritten)+len(injected))
	next = append(next, rewritten[:idx]...)
	next = append(next, injected...)
	next = append(next, rewritten[idx:]...)
	return next
}

// rewriteRootAbsoluteAssetPaths rewrites known root-absolute HTML and manifest
// asset references for reverse-proxy path deployments. JSON manifests are
// rewritten structurally; other assets use known HTML/text patterns. Root mode
// returns the original bytes unchanged.
func rewriteRootAbsoluteAssetPaths(raw []byte, baseURLPath string) []byte {
	baseURLPath = externalBaseURLPath(baseURLPath)
	if baseURLPath == "/" {
		return raw
	}
	if rewritten, ok := rewriteManifestRootAbsoluteAssetPaths(raw, baseURLPath); ok {
		return rewritten
	}
	return rewriteHTMLRootAbsoluteAssetPaths(raw, baseURLPath)
}

// rewriteHTMLRootAbsoluteAssetPaths prefixes root-absolute href/src attributes
// without inspecting script contents or other user-controlled text.
func rewriteHTMLRootAbsoluteAssetPaths(raw []byte, baseURLPath string) []byte {
	root, err := xhtml.Parse(bytes.NewReader(raw))
	if err != nil {
		return raw
	}
	if !rewriteHTMLNodeRootAbsoluteAssetPaths(root, baseURLPath) {
		return raw
	}
	var out bytes.Buffer
	if err := xhtml.Render(&out, root); err != nil {
		return raw
	}
	return out.Bytes()
}

// rewriteHTMLNodeRootAbsoluteAssetPaths walks parsed HTML nodes in place and
// reports whether any asset attribute changed.
func rewriteHTMLNodeRootAbsoluteAssetPaths(node *xhtml.Node, baseURLPath string) bool {
	if node == nil {
		return false
	}
	changed := false
	if node.Type == xhtml.ElementNode {
		for idx := range node.Attr {
			key := strings.ToLower(node.Attr[idx].Key)
			if (key == "href" || key == "src") && isRootAbsoluteAssetPath(node.Attr[idx].Val) {
				node.Attr[idx].Val = baseURLPath + strings.TrimPrefix(node.Attr[idx].Val, "/")
				changed = true
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if rewriteHTMLNodeRootAbsoluteAssetPaths(child, baseURLPath) {
			changed = true
		}
	}
	return changed
}

// rewriteManifestRootAbsoluteAssetPaths parses a web manifest and prefixes
// root-absolute path fields. It returns ok=false when raw is not manifest JSON.
func rewriteManifestRootAbsoluteAssetPaths(raw []byte, baseURLPath string) ([]byte, bool) {
	var manifest any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, false
	}
	if !rewriteManifestValueRootAbsoluteAssetPaths(manifest, baseURLPath) {
		return raw, true
	}
	rewritten, err := json.Marshal(manifest)
	if err != nil {
		return nil, false
	}
	return rewritten, true
}

// rewriteManifestValueRootAbsoluteAssetPaths walks manifest objects and arrays,
// rewriting path fields in place and reporting whether any value changed.
func rewriteManifestValueRootAbsoluteAssetPaths(value any, baseURLPath string) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if text, ok := item.(string); ok && isManifestPathKey(key) && isRootAbsoluteAssetPath(text) {
				typed[key] = baseURLPath + strings.TrimPrefix(text, "/")
				changed = true
				continue
			}
			if rewriteManifestValueRootAbsoluteAssetPaths(item, baseURLPath) {
				changed = true
			}
		}
	case []any:
		for _, item := range typed {
			if rewriteManifestValueRootAbsoluteAssetPaths(item, baseURLPath) {
				changed = true
			}
		}
	}
	return changed
}

// isManifestPathKey reports whether a manifest string field contains a
// browser-visible URL path that must follow the configured base path.
func isManifestPathKey(key string) bool {
	return key == "src" || key == "start_url" || key == "scope"
}

// isRootAbsoluteAssetPath reports whether value is an app-root path rather than
// a protocol-relative URL.
func isRootAbsoluteAssetPath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//")
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request, _ session) {
	if current, ok := s.developmentCurrentSession(r); ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated":           true,
			"needsSetup":              false,
			"username":                current.Username,
			"csrfToken":               current.CSRFToken,
			"nativeBrowseEnabled":     s.nativeBrowseAvailable(r),
			"caseInsensitivePaths":    runtime.GOOS == "windows",
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
	// On an SSO-only deployment the account is provisioned by the first OIDC
	// login, so the setup form must not be offered even before it exists.
	needsSetup := !exists && !s.oidc.DisableBuiltInLogin()
	payload := map[string]any{
		"authenticated":           ok,
		"needsSetup":              needsSetup,
		"username":                "",
		"csrfToken":               "",
		"nativeBrowseEnabled":     browseAvailable,
		"caseInsensitivePaths":    runtime.GOOS == "windows",
		"browseRoot":              "",
		"allowUnrestrictedBrowse": false,
		"needsBrowsePolicy":       false,
		"oidcEnabled":             s.oidc.Enabled(),
		"oidcDisableBuiltInLogin": s.oidc.DisableBuiltInLogin(),
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
	// With the built-in login disabled, first-run setup must be closed too:
	// otherwise the trust-on-first-use window below would hand an unauthenticated
	// caller a password account on a deployment that is meant to be SSO-only.
	// The account is instead provisioned by the first successful OIDC login.
	if s.oidc.DisableBuiltInLogin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "password login is disabled"})
		return
	}
	// First-run setup uses trust-on-first-use: it is reachable from any host so
	// containerized/LAN deployments can complete setup remotely. The
	// "user already exists" guard in auth.Bootstrap closes this window once the
	// admin account is created.
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
		"caseInsensitivePaths":    runtime.GOOS == "windows",
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
	// Enforced on the server, not just hidden in the UI: with the built-in
	// login disabled, the password endpoint must not remain a usable way in.
	if s.oidc.DisableBuiltInLogin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "password login is disabled"})
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
			s.logErrorf(
				"web: auth upgrade failed incident=%s username=%s",
				"auth_upgrade_resume_rewrap_failed",
				redactAuthUsername(record.Username),
			)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to refresh credentials"})
			return
		}
		finalized, err := s.auth.FinalizePendingUpgrade(record.Username)
		if err != nil {
			s.logErrorf(
				"web: auth upgrade failed incident=%s username=%s",
				"auth_upgrade_resume_finalize_failed",
				redactAuthUsername(record.Username),
			)
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
			s.logErrorf(
				"web: auth upgrade failed incident=%s username=%s",
				"auth_upgrade_rewrap_failed",
				redactAuthUsername(record.Username),
			)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to refresh credentials"})
			return
		}
		finalized, err := s.auth.FinalizePendingUpgrade(record.Username)
		if err != nil {
			s.logErrorf(
				"web: auth upgrade failed incident=%s username=%s",
				"auth_upgrade_finalize_failed",
				redactAuthUsername(record.Username),
			)
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
		"caseInsensitivePaths":    runtime.GOOS == "windows",
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
		s.logErrorf("web: failed to delete session during logout: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to clear session"})
		return
	}
	if s.backend != nil {
		s.backend.StopSessionLogStreams(current.ID)
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value == current.ID {
		//nolint:gosec // Session clear cookie sets HttpOnly, SameSite, and Secure for HTTPS requests.
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     s.sessionCookiePath(),
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   s.requestScheme(r) == "https",
		})
	}
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
		"caseInsensitivePaths":    runtime.GOOS == "windows",
		"browseRoot":              joinBrowsePolicyRoots(roots),
		"allowUnrestrictedBrowse": req.AllowUnrestrictedBrowse,
		"needsBrowsePolicy":       false,
	})
}

// splitBrowsePolicyRoots decodes stored or submitted browse roots. A single
// existing directory wins before CSV parsing so unquoted comma-containing paths
// from older records still round-trip as one root.
func splitBrowsePolicyRoots(value string) []string {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return nil
	}
	if info, err := os.Stat(trimmedValue); err == nil && info.IsDir() {
		return []string{trimmedValue}
	}

	reader := csv.NewReader(strings.NewReader(trimmedValue))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	parts, err := reader.Read()
	if err != nil {
		parts = strings.Split(trimmedValue, ",")
	}
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

// joinBrowsePolicyRoots encodes browse roots as one CSV record so commas in
// host filesystem paths are escaped instead of becoming root separators.
func joinBrowsePolicyRoots(roots []string) string {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if err := writer.Write(roots); err != nil {
		return strings.Join(roots, ", ")
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return strings.Join(roots, ", ")
	}
	return strings.TrimRight(buf.String(), "\r\n")
}

func sameFilesystemPath(left string, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// handleEvents streams session-scoped server-sent events until the request
// context ends, then stops shared session log streams only after the session has
// no replacement event subscribers.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, current session) {
	// Query csrfToken is a legacy cookie-bound consistency check. Header tokens
	// from the fetch-based browser stream are validated against the session cookie
	// in currentSession.
	if token := strings.TrimSpace(r.URL.Query().Get("csrfToken")); token != "" && token != current.CSRFToken {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf validation failed"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsubscribe := s.hub.Subscribe(current.ID)
	defer func() {
		trimmedSessionID := strings.TrimSpace(current.ID)
		if trimmedSessionID == "" || s == nil || s.backend == nil {
			unsubscribe()
			return
		}

		// Claim the next idle-stop generation before removing the current
		// subscriber so an older disconnect timer cannot observe a newer gap.
		generation := nextSessionLogStopGeneration(s, trimmedSessionID)
		unsubscribe()
		s.scheduleStopSessionLogStreamsIfIdle(trimmedSessionID, generation)
	}()

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

// stopSessionLogStreamsIfIdle schedules cleanup for the latest session
// disconnect only, so stale SSE disconnect timers cannot stop shared session
// logs during a newer reconnect grace window.
func (s *Server) stopSessionLogStreamsIfIdle(sessionID string) {
	if s == nil || s.backend == nil {
		return
	}
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return
	}

	s.scheduleStopSessionLogStreamsIfIdle(trimmedSessionID, nextSessionLogStopGeneration(s, trimmedSessionID))
}

func (s *Server) scheduleStopSessionLogStreamsIfIdle(sessionID string, generation uint64) {
	if s == nil || s.backend == nil || generation == 0 {
		return
	}

	time.AfterFunc(eventSessionLogStopGracePeriod, func() {
		s.stopSessionLogStreamsIfOwnedAndIdle(sessionID, generation)
	})
}

// stopSessionLogStreamsIfOwnedAndIdle stops shared session log streams only
// while generation still owns the latest disconnect and no replacement
// subscriber is registered under the event hub lock.
func (s *Server) stopSessionLogStreamsIfOwnedAndIdle(sessionID string, generation uint64) {
	if s == nil || s.backend == nil {
		return
	}
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return
	}

	if s.hub != nil {
		s.hub.mu.Lock()
		defer s.hub.mu.Unlock()
	}

	sessionLogStopGenerations.mu.Lock()
	sessionGenerations := sessionLogStopGenerations.byServ[s]
	if sessionGenerations[trimmedSessionID] != generation {
		sessionLogStopGenerations.mu.Unlock()
		return
	}

	if s.hub != nil && len(s.hub.subscribers[trimmedSessionID]) > 0 {
		clearSessionLogStopGenerationLocked(s, trimmedSessionID)
		sessionLogStopGenerations.mu.Unlock()
		return
	}

	clearSessionLogStopGenerationLocked(s, trimmedSessionID)
	sessionLogStopGenerations.mu.Unlock()

	s.backend.StopSessionLogStreams(trimmedSessionID)
}

// requireSession applies request rate limits and resolves the cookie-bound
// session before dispatch. Unsafe HTTP methods must also pass same-origin and
// CSRF checks for that session.
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

// currentSession resolves the cookie session and, when a CSRF header is present,
// requires that token to match the cookie-bound session.
func (s *Server) currentSession(r *http.Request) (session, bool) {
	if token := sessionTokenFromRequest(r); token != "" {
		return s.currentSessionByToken(r, token)
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil && s.sessions != nil {
		if current, ok := s.sessions.Get(cookie.Value); ok {
			return current, true
		}
	}
	return s.developmentCurrentSession(r)
}

// currentSessionByToken validates the request CSRF token against the
// cookie-bound session. The token is not a bearer credential.
func (s *Server) currentSessionByToken(r *http.Request, token string) (session, bool) {
	if token == "" {
		return session{}, false
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil && s.sessions != nil {
		if current, ok := s.sessions.Get(cookie.Value); ok {
			if current.CSRFToken == token {
				return current, true
			}
			return session{}, false
		}
	}
	if current, ok := s.developmentCurrentSession(r); ok && current.CSRFToken == token {
		return current, true
	}
	return session{}, false
}

// sessionTokenFromRequest returns the CSRF token supplied by app calls.
func sessionTokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get("X-Csrf-Token"))
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
	//nolint:gosec // Session cookie sets HttpOnly, SameSite, and Secure for HTTPS requests.
	cookie := &http.Cookie{
		Name:     sessionCookieName,
		Value:    current.ID,
		Path:     s.sessionCookiePath(),
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

// sessionCookiePath scopes browser sessions to the configured external base
// path, falling back to "/" for root and subdomain deployments.
func (s *Server) sessionCookiePath() string {
	if basePath := s.externalBasePath(); basePath != "" {
		return basePath
	}
	return "/"
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
