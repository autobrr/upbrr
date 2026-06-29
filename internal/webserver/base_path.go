// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"fmt"
	"net/url"
	"path" //nolint:depguard // Normalizes URL route paths, not local filesystem paths.
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/redaction"
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
		return "", fmt.Errorf("base URL %q cannot be protocol-relative", redactedBaseURL(raw))
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("base URL %q is invalid: %s", redactedBaseURL(raw), redactedBaseURLError(err))
	}
	if parsed.Scheme != "" {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", fmt.Errorf("base URL %q must use http or https", redactedBaseURL(raw))
		}
		if parsed.Host == "" {
			return "", fmt.Errorf("base URL %q must include a host", redactedBaseURL(raw))
		}
		if parsed.User != nil {
			return "", fmt.Errorf("base URL %q cannot include userinfo", redactedBaseURL(raw))
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
		return "", fmt.Errorf("base URL %q must be an http(s) URL or path", redactedBaseURL(raw))
	}
	if parsed.Path == "" && (parsed.RawQuery != "" || parsed.Fragment != "") {
		return "", fmt.Errorf("base URL %q must include a path", redactedBaseURL(raw))
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
		return "", fmt.Errorf("base URL %q cannot contain path traversal", redactedBaseURL(raw))
	}
	decoded, err := url.PathUnescape(pathValue)
	if err != nil {
		return "", fmt.Errorf("base URL %q contains invalid path escaping: %w", redactedBaseURL(raw), err)
	}
	if hasUnsafeBaseURLPathSegment(decoded) {
		return "", fmt.Errorf("base URL %q cannot contain encoded path traversal", redactedBaseURL(raw))
	}

	cleaned := path.Clean("/" + strings.Trim(pathValue, "/"))
	if cleaned == "/" || cleaned == "." {
		return "", nil
	}
	return cleaned, nil
}

// redactedBaseURL returns a display-safe version of a rejected base URL for
// validation errors.
func redactedBaseURL(raw string) string {
	redacted := redaction.RedactValue(strings.TrimSpace(raw), nil)
	return redactURLUserinfo(redacted)
}

// redactedBaseURLError returns a display-safe parse error. url.Parse errors can
// include the raw input, so they must be redacted separately from the value.
func redactedBaseURLError(err error) string {
	if err == nil {
		return ""
	}
	return redactURLUserinfo(redaction.RedactValue(err.Error(), nil))
}

// redactURLUserinfo removes credentials from absolute and protocol-relative
// URL-shaped values before they are surfaced to users.
func redactURLUserinfo(value string) string {
	authorityStart := -1
	if strings.HasPrefix(value, "//") {
		authorityStart = 2
	} else if schemeEnd := strings.Index(value, "://"); schemeEnd >= 0 {
		authorityStart = schemeEnd + len("://")
	}
	if authorityStart < 0 {
		return value
	}

	authorityEnd := len(value)
	for _, separator := range []string{"/", "?", "#"} {
		if idx := strings.Index(value[authorityStart:], separator); idx >= 0 {
			authorityEnd = min(authorityEnd, authorityStart+idx)
		}
	}
	if at := strings.LastIndex(value[authorityStart:authorityEnd], "@"); at >= 0 {
		userinfoEnd := authorityStart + at + 1
		return value[:authorityStart] + "REDACTED@" + value[userinfoEnd:]
	}
	return value
}

func hasUnsafeBaseURLPathSegment(pathValue string) bool {
	return slices.Contains(strings.Split(strings.ReplaceAll(pathValue, `\`, "/"), "/"), "..")
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
