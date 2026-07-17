// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

var seasonPattern = regexp.MustCompile(`(?i)S(\d{1,2})`)

type dupeSearcher struct {
	cfg     config.Config
	http    *http.Client
	baseURL string
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:     cfg,
		http:    httpClient,
		baseURL: "https://beyond-hd.me/api/torrents/",
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	cfg, apiKey := bhdConfig(s.cfg)
	if apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}
	tmdbID, imdbID := meta.Identity.TMDBID, bhdIMDB(meta.Identity.IMDBID)
	if tmdbID == 0 && imdbID == "" {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing tmdb/imdb id for BHD dupe search", nil)
	}
	category, tmdbPrefix := "Movies", "movie"
	if strings.EqualFold(string(meta.Identity.Category), "TV") {
		category, tmdbPrefix = "TV", "tv"
	}
	payload := map[string]any{"action": "search", "categories": category}
	if searchType, ok := dupeSearchType(meta); ok {
		payload["types"] = searchType
	} else {
		payload["types"] = nil
	}
	if dupeIsSD(meta) {
		payload["categories"], payload["types"] = nil, nil
	}
	if tmdbID != 0 {
		payload["tmdb_id"] = tmdbPrefix + "/" + strconv.Itoa(tmdbID)
	} else {
		payload["imdb_id"] = imdbID
	}
	if season := bhdSeason(meta); season != "" && category == "TV" {
		payload["search"] = season
	}
	if rss := strings.TrimSpace(cfg.BhdRSSKey); rss != "" {
		payload["rsskey"] = rss
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return dupe.Failed(dupe.FailureInternal, "BHD request failed", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+apiKey, bytes.NewReader(body))
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "BHD request failed", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "BHD request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return dupe.Failed(dupe.FailureResponseStatus, "BHD search failed", nil)
	}
	var decoded map[string]any
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil || len(decoded) == 0 {
		return dupe.Failed(dupe.FailureResponseParse, "BHD search failed", err)
	}
	if bhdInt(decoded["status_code"]) == 0 {
		return dupe.Failed(dupe.FailureResponseStatus, "BHD api rejected search", nil)
	}
	return dupe.Resolved(bhdEntries(decoded), nil)
}

func bhdEntries(payload map[string]any) []api.DupeEntry {
	results, _ := payload["results"].([]any)
	entries := make([]api.DupeEntry, 0, len(results))
	for _, raw := range results {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		entry := api.DupeEntry{Name: bhdString(item["name"]), Link: bhdString(item["url"])}
		if size := bhdInt(item["size"]); size > 0 {
			entry.SizeKnown, entry.SizeBytes = true, size
		}
		if bhdInt(item["dv"]) == 1 {
			entry.Flags = append(entry.Flags, "DV")
		}
		if bhdInt(item["hdr10"]) == 1 || bhdInt(item["hdr10+"]) == 1 {
			entry.Flags = append(entry.Flags, "HDR")
		}
		entries = append(entries, entry)
	}
	return entries
}

func bhdConfig(cfg config.Config) (config.TrackerConfig, string) {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "BHD") {
			return entry, strings.TrimSpace(entry.APIKey)
		}
	}
	return config.TrackerConfig{}, ""
}

func bhdSeason(meta api.DuplicateSubject) string {
	if meta.ReleaseNameOverrides.Season != nil {
		return normalizeBHDSeason(*meta.ReleaseNameOverrides.Season)
	}
	match := seasonPattern.FindStringSubmatch(meta.ReleaseName)
	if len(match) == 2 {
		return normalizeBHDSeason(match[1])
	}
	return ""
}

func dupeSearchType(meta api.DuplicateSubject) (string, bool) {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return "", false
	}
	return dupeType(meta), true
}

func dupeType(meta api.DuplicateSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		size := 100
		for _, candidate := range []int{25, 50, 66, 100} {
			if meta.SourceSize > 0 && meta.SourceSize < int64(candidate)<<30 {
				size = candidate
				break
			}
		}
		if strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && size != 25 {
			if size == 50 || size == 66 || size == 100 {
				return fmt.Sprintf("UHD %d", size)
			}
			return "Other"
		}
		if size == 25 || size == 50 {
			return fmt.Sprintf("BD %d", size)
		}
		return "Other"
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		upper := strings.ToUpper(strings.TrimSpace(meta.Release.Size))
		switch {
		case strings.Contains(upper, "DVD5"):
			return "DVD 5"
		case strings.Contains(upper, "DVD9"):
			return "DVD 9"
		default:
			return "Other"
		}
	}
	if strings.EqualFold(strings.TrimSpace(firstNonEmpty(meta.Type, meta.Release.Type)), "REMUX") {
		source := firstNonEmpty(meta.Source, meta.Release.Source)
		switch {
		case isHDDVDSource(source):
			return "Other"
		case strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD"):
			return "UHD Remux"
		case isDVDSource(source):
			return "DVD Remux"
		case strings.EqualFold(strings.TrimSpace(source), "BluRay"), strings.EqualFold(strings.TrimSpace(source), "Blu-ray"):
			return "BD Remux"
		default:
			return "Other"
		}
	}
	resolution := normalizeResolution(meta.Release.Resolution)
	switch resolution {
	case "2160p", "1080p", "1080i", "720p", "576p", "540p", "480p":
		return resolution
	default:
		return "Other"
	}
}

func dupeIsSD(meta api.DuplicateSubject) bool {
	resolution := normalizeResolution(meta.Release.Resolution)
	return strings.Contains(resolution, "480") || strings.Contains(resolution, "540") || strings.Contains(resolution, "576")
}

func normalizeBHDSeason(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToUpper(trimmed), "S") {
		return strings.ToUpper(trimmed)
	}
	if number, err := strconv.Atoi(trimmed); err == nil {
		return "S" + strconv.Itoa(number)
	}
	return strings.ToUpper(trimmed)
}

func bhdIMDB(id int) string {
	if id == 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", id)
}

func bhdString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func bhdInt(value any) int64 {
	switch typed := value.(type) {
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case float64:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}
