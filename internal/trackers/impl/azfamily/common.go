// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	azTokenPattern     = regexp.MustCompile(`name="_token"\s+content="([^"]+)"`)
	azTaskIDPattern    = regexp.MustCompile(`/(\d+)$`)
	azTorrentIDPattern = regexp.MustCompile(`/torrent/(\d+)`)
)

type mediaLookupResult struct {
	MediaCode string
	Missing   bool
}

type taskInfo struct {
	TaskID      string
	InfoHash    string
	RedirectURL string
}

type languageBundle struct {
	Audio     []string
	Subtitles []string
}

func imdbForLookup(meta api.UploadSubject) string {
	if meta.Identity.IMDBID != 0 {
		return providerid.IMDb(meta.Identity.IMDBID).Prefixed()
	}
	return ""
}

func tmdbForLookup(meta api.UploadSubject) string {
	if meta.Identity.TMDBID != 0 {
		return strconv.Itoa(meta.Identity.TMDBID)
	}
	return ""
}

// tvdbForLookup returns a TVDB search value only for identified TV content.
func tvdbForLookup(meta api.UploadSubject) string {
	if isTV(meta) && meta.Identity.TVDBID > 0 {
		return strconv.Itoa(meta.Identity.TVDBID)
	}
	return ""
}

func lookupTitle(meta api.UploadSubject) string {
	if title := strings.TrimSpace(meta.Release.Title); title != "" {
		return title
	}
	if meta.ProviderMetadata.TMDB != nil {
		if title := strings.TrimSpace(meta.ProviderMetadata.TMDB.Title); title != "" {
			return title
		}
	}
	return strings.TrimSpace(meta.Filename)
}

func tvCode(meta api.UploadSubject) string {
	if meta.SeasonInt <= 0 || meta.EpisodeInt <= 0 {
		return ""
	}
	return fmt.Sprintf("S%02dE%02d", meta.SeasonInt, meta.EpisodeInt)
}

func anonEnabled(req trackers.PreparationInput) bool {
	return req.TrackerConfig.Anon
}

func absoluteURL(baseURL, location string) string {
	trimmed := strings.TrimSpace(location)
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(trimmed, "/")
}

func extractPatternGroup(pattern *regexp.Regexp, value string) string {
	if pattern == nil {
		return ""
	}
	matches := pattern.FindStringSubmatch(value)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func sortedKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func attrValue(node *xhtml.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func nodeText(node *xhtml.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == xhtml.TextNode {
		return node.Data
	}
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		builder.WriteString(nodeText(child))
	}
	return builder.String()
}

func valuesToMap(values url.Values) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, entries := range values {
		out[key] = strings.Join(entries, ",")
	}
	return out
}
