// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/autobrr/upbrr/internal/authmaterial"
)

func (s *Server) registerRoutes(mux *http.ServeMux) {
	s.registerAPIV1Routes(mux)

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
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
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

func (s *Server) allowAuthRequest(r *http.Request) bool {
	return s.authLimiter.Allow(s.clientIP(r))
}

func (s *Server) allowGeneralRequest(r *http.Request) bool {
	return s.generalLimiter.Allow(s.clientIP(r))
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
