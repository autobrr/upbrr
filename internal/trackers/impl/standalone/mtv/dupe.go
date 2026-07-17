// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const mtvTorznabEndpoint = "https://www.morethantv.me/api/torznab"

type dupeSearcher struct {
	cfg  config.Config
	http *http.Client
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{cfg: cfg, http: httpClient}
}

func (h dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	apiKey := mtvAPIKey(h.cfg)
	if apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}

	params := url.Values{}
	params.Set("t", "search")
	params.Set("apikey", apiKey)
	params.Set("limit", "100")

	switch {
	case meta.Identity.IMDBID != 0:
		params.Set("imdbid", "tt"+strconv.Itoa(meta.Identity.IMDBID))
	case meta.Identity.TMDBID != 0:
		params.Set("tmdbid", strconv.Itoa(meta.Identity.TMDBID))
	case isMTVTVCategory(meta) && meta.Identity.TVDBID != 0:
		params.Set("tvdbid", strconv.Itoa(meta.Identity.TVDBID))
	default:
		query := cleanMTVSearchTitle(meta)
		if query == "" {
			return dupe.NotRun(dupe.NotRunMissingMetadata, "missing imdb/tmdb/tvdb id or title for MTV dupe search", nil)
		}
		params.Set("q", query)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mtvTorznabEndpoint, nil)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "MTV search failed", fmt.Errorf("build MTV request: %w", err))
	}
	req.URL.RawQuery = params.Encode()

	resp, err := h.http.Do(req)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "MTV search failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return dupe.Failed(dupe.FailureResponseStatus, "MTV search failed", nil)
	}

	var payload mtvRSS
	if err := xml.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return dupe.Failed(dupe.FailureResponseParse, "MTV response parse failed", err)
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

	return dupe.Resolved(entries, nil)
}

// isMTVTVCategory reports whether MTV torznab searches may use a TVDB ID query.
// Canonical movie identity suppresses TVDB queries.
func isMTVTVCategory(meta api.DuplicateSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}

func cleanMTVSearchTitle(meta api.DuplicateSubject) string {
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

func mtvAPIKey(cfg config.Config) string {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "MTV") {
			return strings.TrimSpace(entry.APIKey)
		}
	}
	return ""
}
