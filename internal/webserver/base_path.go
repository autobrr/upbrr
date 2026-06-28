// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"fmt"
	"net/url"
	"path" //nolint:depguard // Normalizes URL route paths, not local filesystem paths.
	"strings"
)

// NormalizeBaseURL validates and canonicalizes a browser-visible Web UI base
// URL. Empty/root values mean root deployment. Path-only values are returned as
// normalized paths without a trailing slash; absolute http(s) URLs keep a
// trailing slash for browser-open behavior and have query/fragment stripped.
func NormalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if strings.HasPrefix(trimmed, "//") {
		return "", fmt.Errorf("base URL %q cannot be protocol-relative", raw)
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("base URL %q is invalid: %w", raw, err)
	}
	if parsed.Scheme != "" {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", fmt.Errorf("base URL %q must use http or https", raw)
		}
		if parsed.Host == "" {
			return "", fmt.Errorf("base URL %q must include a host", raw)
		}
		if parsed.User != nil {
			return "", fmt.Errorf("base URL %q cannot include userinfo", raw)
		}
		basePath, err := normalizeBaseURLPath(parsed.Path, raw)
		if err != nil {
			return "", err
		}
		if basePath == "" {
			parsed.Path = "/"
		} else {
			parsed.Path = basePath + "/"
		}
		parsed.RawPath = ""
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		parsed.Fragment = ""
		return parsed.String(), nil
	}
	if parsed.Host != "" || parsed.User != nil {
		return "", fmt.Errorf("base URL %q must be an http(s) URL or path", raw)
	}
	if parsed.Path == "" && (parsed.RawQuery != "" || parsed.Fragment != "") {
		return "", fmt.Errorf("base URL %q must include a path", raw)
	}

	basePath, err := normalizeBaseURLPath(parsed.Path, raw)
	if err != nil {
		return "", err
	}
	return basePath, nil
}

func normalizeBaseURLPath(rawPath string, raw string) (string, error) {
	pathValue := strings.TrimSpace(rawPath)
	if pathValue == "" || pathValue == "/" {
		return "", nil
	}
	if hasUnsafeBaseURLPathSegment(pathValue) {
		return "", fmt.Errorf("base URL %q cannot contain path traversal", raw)
	}
	decoded, err := url.PathUnescape(pathValue)
	if err != nil {
		return "", fmt.Errorf("base URL %q contains invalid path escaping: %w", raw, err)
	}
	if hasUnsafeBaseURLPathSegment(decoded) {
		return "", fmt.Errorf("base URL %q cannot contain encoded path traversal", raw)
	}

	cleaned := path.Clean("/" + strings.Trim(pathValue, "/"))
	if cleaned == "/" || cleaned == "." {
		return "", nil
	}
	return cleaned, nil
}

func hasUnsafeBaseURLPathSegment(pathValue string) bool {
	for _, segment := range strings.Split(strings.ReplaceAll(pathValue, `\`, "/"), "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

// externalBasePath derives the externally visible route prefix from baseURL.
// Empty, root, and absolute URLs with a root path return "" so root-mode routes
// keep their existing paths. Query strings and fragments are ignored.
func externalBasePath(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return ""
	}
	if normalized, err := NormalizeBaseURL(trimmed); err == nil {
		trimmed = normalized
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
