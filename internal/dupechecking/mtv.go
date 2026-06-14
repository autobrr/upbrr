// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

const mtvTorznabEndpoint = "https://www.morethantv.me/api/torznab"

type mtvHandler struct {
	cfg  config.Config
	http *http.Client
}

func (h mtvHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	_, apiKey, ok := trackerCfgWithAPIKey(h.cfg, "MTV")
	if !ok {
		return nil, []string{noteSkip("missing api_key for tracker")}, nil
	}

	params := url.Values{}
	params.Set("t", "search")
	params.Set("apikey", apiKey)
	params.Set("limit", "100")

	switch {
	case meta.ExternalIDs.IMDBID != 0:
		params.Set("imdbid", "tt"+strconv.Itoa(meta.ExternalIDs.IMDBID))
	case meta.ExternalIDs.TMDBID != 0:
		params.Set("tmdbid", strconv.Itoa(meta.ExternalIDs.TMDBID))
	case isMTVTVCategory(meta) && meta.ExternalIDs.TVDBID != 0:
		params.Set("tvdbid", strconv.Itoa(meta.ExternalIDs.TVDBID))
	default:
		query := cleanMTVSearchTitle(meta)
		if query == "" {
			return nil, []string{noteSkip("missing imdb/tmdb/tvdb id or title for MTV dupe search")}, nil
		}
		params.Set("q", query)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mtvTorznabEndpoint, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build MTV request: %w", err)
	}
	req.URL.RawQuery = params.Encode()

	resp, err := h.http.Do(req)
	if err != nil {
		return nil, []string{noteSkip("MTV search failed")}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, []string{noteSkip("MTV search failed")}, nil
	}

	var payload mtvRSS
	if err := xml.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, []string{noteSkip("MTV response parse failed")}, nil
	}

	entries := make([]api.DupeEntry, 0, len(payload.Channel.Items))
	for _, item := range payload.Channel.Items {
		title := strings.TrimSpace(item.Title)
		if title == "" {
			continue
		}

		fileCount := parsePositiveInt(item.Files)
		sizeBytes := parsePositiveInt64(item.Size)
		for _, attr := range item.allAttrs() {
			name := strings.ToLower(strings.TrimSpace(attr.Name))
			value := strings.TrimSpace(attr.Value)
			switch {
			case fileCount == 0 && (name == "files" || name == "file_count" || name == "filecount"):
				fileCount = parsePositiveInt(value)
			case sizeBytes == 0 && name == "size":
				sizeBytes = parsePositiveInt64(value)
			}
		}

		guid := strings.TrimSpace(item.GUID)
		download := strings.TrimSpace(item.Link)
		if download == "" {
			download = strings.TrimSpace(item.Enclosure.URL)
		}

		entry := api.DupeEntry{
			Name:      title,
			Files:     []string{title},
			FileCount: fileCount,
			ID:        guid,
			Link:      guid,
			Download:  strings.ReplaceAll(download, "&amp;", "&"),
		}
		if sizeBytes > 0 {
			entry.SizeKnown = true
			entry.SizeBytes = sizeBytes
		}
		entries = append(entries, entry)
	}

	return entries, nil, nil
}

// isMTVTVCategory reports whether MTV torznab searches may use a TVDB ID query.
// Explicit movie categories suppress TVDB even when MediaInfo classifies the release as TV.
func isMTVTVCategory(meta api.PreparedMetadata) bool {
	if isMTVMovieCategory(meta) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(meta.ExternalIDs.Category), "TV") ||
		strings.EqualFold(strings.TrimSpace(meta.MediaInfoCategory), "TV") ||
		strings.EqualFold(strings.TrimSpace(meta.Release.Category), "TV")
}

func isMTVMovieCategory(meta api.PreparedMetadata) bool {
	return strings.EqualFold(strings.TrimSpace(meta.ExternalIDs.Category), "MOVIE") ||
		strings.EqualFold(strings.TrimSpace(meta.MediaInfoCategory), "MOVIE") ||
		strings.EqualFold(strings.TrimSpace(meta.Release.Category), "MOVIE")
}

func cleanMTVSearchTitle(meta api.PreparedMetadata) string {
	query := strings.TrimSpace(meta.Release.Title)
	if query == "" {
		query = strings.TrimSpace(meta.ReleaseName)
	}
	if query == "" {
		return ""
	}
	query = strings.ReplaceAll(query, ": ", " ")
	query = strings.ReplaceAll(query, "’", "")
	query = strings.ReplaceAll(query, "'", "")
	return strings.Join(strings.Fields(query), " ")
}

func parsePositiveInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func parsePositiveInt64(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

type mtvRSS struct {
	Channel mtvChannel `xml:"channel"`
}

type mtvChannel struct {
	Items []mtvItem `xml:"item"`
}

type mtvItem struct {
	Title        string       `xml:"title"`
	Files        string       `xml:"files"`
	Size         string       `xml:"size"`
	GUID         string       `xml:"guid"`
	Link         string       `xml:"link"`
	Enclosure    mtvEnclosure `xml:"enclosure"`
	Attrs        []mtvAttr    `xml:"attr"`
	TorznabAttrs []mtvAttr    `xml:"http://torznab.com/schemas/2015/feed attr"`
}

func (i mtvItem) allAttrs() []mtvAttr {
	if len(i.Attrs) == 0 {
		return i.TorznabAttrs
	}
	if len(i.TorznabAttrs) == 0 {
		return i.Attrs
	}
	combined := make([]mtvAttr, 0, len(i.Attrs)+len(i.TorznabAttrs))
	combined = append(combined, i.Attrs...)
	combined = append(combined, i.TorznabAttrs...)
	return combined
}

type mtvAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type mtvEnclosure struct {
	URL string `xml:"url,attr"`
}
