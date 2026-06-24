// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"net/url"
	"path" //nolint:depguard // Normalizes URL route paths, not local filesystem paths.
	"strings"
)

// externalBasePath derives the externally visible route prefix from baseURL.
// Empty, root, and absolute URLs with a root path return "" so root-mode routes
// keep their existing paths. Query strings and fragments are ignored.
func externalBasePath(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return ""
	}

	basePath := trimmed
	if parsed, err := url.Parse(trimmed); err == nil {
		basePath = parsed.Path
		if !parsed.IsAbs() && parsed.Host == "" && basePath == "" {
			basePath = trimmed
		}
	}

	basePath = strings.TrimSpace(basePath)
	if basePath == "" || basePath == "/" {
		return ""
	}
	basePath = path.Clean("/" + strings.Trim(basePath, "/"))
	if basePath == "/" || basePath == "." {
		return ""
	}
	return basePath
}

// externalBaseURLPath returns the browser-visible base path with a trailing
// slash. It is safe to inject into frontend code and static asset URLs.
func externalBaseURLPath(baseURL string) string {
	basePath := externalBasePath(baseURL)
	if basePath == "" {
		return "/"
	}
	return basePath + "/"
}

// joinBasePath combines a normalized external base path with an absolute or
// relative URL-path suffix.
func joinBasePath(basePath string, suffix string) string {
	cleanBase := externalBasePath(basePath)
	cleanSuffix := "/" + strings.TrimLeft(strings.TrimSpace(suffix), "/")
	if cleanBase == "" {
		return cleanSuffix
	}
	if cleanSuffix == "/" {
		return cleanBase
	}
	return cleanBase + cleanSuffix
}

// externalBasePath returns this server's configured external route prefix.
func (s *Server) externalBasePath() string {
	if s == nil {
		return ""
	}
	return externalBasePath(s.cliCfg.BaseURL)
}

// externalBaseURLPath returns this server's external route prefix for browser
// consumers, including a trailing slash.
func (s *Server) externalBaseURLPath() string {
	if s == nil {
		return "/"
	}
	return externalBaseURLPath(s.cliCfg.BaseURL)
}
