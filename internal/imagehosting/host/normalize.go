// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package host

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	imageThumbSuffix      = regexp.MustCompile(`(?i)\.(?:md|th|thumb)(\.(?:png|jpe?g|gif|webp|avif|bmp))$`)
	onlyImageMediumSuffix = regexp.MustCompile(`(?i)\.md(\.(?:png|jpe?g|gif|webp|avif|bmp))$`)
	imgboxThumbSuffix     = regexp.MustCompile(`(?i)_t(\.(?:png|jpe?g|gif|webp|avif|bmp))$`)
	onlyImagePagePath     = regexp.MustCompile(`(?i)^/image/([a-z0-9]+)/?$`)
)

// WsrvSourceURL returns the URL carried by a known image proxy, if present.
func WsrvSourceURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "wsrv.nl" && !strings.HasSuffix(host, ".wsrv.nl") &&
		host != "wsrv.aither.cc" && !strings.HasSuffix(host, ".wsrv.aither.cc") {
		return ""
	}
	return parsed.Query().Get("url")
}

// IsSourceOnlyURL reports image URLs that may be downloaded but cannot be
// reused as hosted links on another tracker.
func IsSourceOnlyURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "wsrv.nl" || strings.HasSuffix(host, ".wsrv.nl") ||
		host == "wsrv.aither.cc" || strings.HasSuffix(host, ".wsrv.aither.cc") ||
		host == "passthepopcorn.me" || strings.HasSuffix(host, ".passthepopcorn.me")
}

// NormalizeRawURL expands supported thumbnail and viewer URLs to direct full-size images.
func NormalizeRawURL(value string) string {
	trimmed := strings.TrimSpace(value)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Hostname() == "" {
		return trimmed
	}
	host := strings.ToLower(parsed.Hostname())
	if source := WsrvSourceURL(trimmed); source != "" {
		sourceURL, sourceErr := url.Parse(source)
		if sourceErr != nil || sourceURL.Scheme != "http" && sourceURL.Scheme != "https" || sourceURL.Hostname() == "" {
			return trimmed
		}
		fullURL := normalizeDirectImageURL(sourceURL, source)
		if !isImageFilePath(sourceURL.Path) && fullURL == source {
			return trimmed
		}
		if (host == "wsrv.aither.cc" || strings.HasSuffix(host, ".wsrv.aither.cc")) &&
			(strings.EqualFold(sourceURL.Hostname(), "img.blutopia.cc") || strings.HasSuffix(strings.ToLower(sourceURL.Hostname()), ".img.blutopia.cc")) {
			query := parsed.Query()
			query.Set("url", fullURL)
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
		return fullURL
	}
	return normalizeDirectImageURL(parsed, trimmed)
}

func normalizeDirectImageURL(parsed *url.URL, original string) string {
	host := strings.ToLower(parsed.Hostname())
	pathValue := parsed.Path
	if host == "onlyimage.org" || host == "www.onlyimage.org" {
		if match := onlyImagePagePath.FindStringSubmatch(pathValue); len(match) == 2 {
			return "https://img.onlyimage.org/" + match[1] + ".png"
		}
	}
	if host == "img.onlyimage.org" {
		parsed.Path = onlyImageMediumSuffix.ReplaceAllString(pathValue, "$1")
		if strings.EqualFold(parsed.Path, pathValue) {
			return original
		}
		return parsed.String()
	}
	if label, ok := strings.CutSuffix(host, ".imgbox.com"); ok {
		suffix, found := strings.CutPrefix(label, "thumbs")
		if found && strings.IndexFunc(suffix, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
			parsed.Host = "images" + suffix + ".imgbox.com"
			parsed.Path = imgboxThumbSuffix.ReplaceAllString(pathValue, "_o$1")
			return parsed.String()
		}
	}
	if host == "pixhost.cc" || host == "pixhost.to" || strings.HasSuffix(host, ".pixhost.cc") || strings.HasSuffix(host, ".pixhost.to") {
		if strings.HasPrefix(strings.ToLower(pathValue), "/thumbs/") {
			parsed.Path = "/images/" + pathValue[len("/thumbs/"):]
			if label, domain, found := strings.Cut(host, "."); found && strings.HasPrefix(label, "t") && len(label) > 1 &&
				strings.IndexFunc(label[1:], func(r rune) bool { return r < '0' || r > '9' }) < 0 {
				parsed.Host = "img" + label[1:] + "." + domain
			}
			return parsed.String()
		}
	}
	if host == "beyondhd.co" || strings.HasSuffix(host, ".beyondhd.co") ||
		host == "ptscreens.com" || strings.HasSuffix(host, ".ptscreens.com") ||
		host == "img.blutopia.cc" || strings.HasSuffix(host, ".img.blutopia.cc") {
		parsed.Path = imageThumbSuffix.ReplaceAllString(pathValue, "$1")
		if !strings.EqualFold(parsed.Path, pathValue) {
			return parsed.String()
		}
	}
	return original
}

func isImageFilePath(pathValue string) bool {
	lower := strings.ToLower(pathValue)
	return strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") ||
		strings.HasSuffix(lower, ".gif") || strings.HasSuffix(lower, ".webp") || strings.HasSuffix(lower, ".avif") || strings.HasSuffix(lower, ".bmp")
}
