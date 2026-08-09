// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

var seasonPattern = regexp.MustCompile(`(?i)S(\d{1,2})`)

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	baseURL  string
	maxPages int
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		baseURL:  "https://beyond-hd.me/api/torrents/",
		maxPages: deps.MaxPages(100),
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
	payload["types"] = nil
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
	const pageSize = 100
	maxPages := s.maxPages
	if maxPages <= 0 {
		maxPages = 100
	}
	entries := make([]api.DupeEntry, 0)
	complete := false
	pages := 0
	expectedTotalPages := -1
	expectedTotalResults := -1
	firstPageHasPagination := false
	for pageNumber := 1; pageNumber <= maxPages; pageNumber++ {
		pagePayload := maps.Clone(payload)
		pagePayload["page"] = pageNumber
		body, err := json.Marshal(pagePayload)
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
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			resp.Body.Close()
			return dupe.Failed(dupe.FailureResponseStatus, "BHD search failed", nil)
		}
		var decoded map[string]any
		decoder := json.NewDecoder(resp.Body)
		decoder.UseNumber()
		decodeErr := decoder.Decode(&decoded)
		resp.Body.Close()
		if decodeErr != nil || len(decoded) == 0 {
			return dupe.Failed(dupe.FailureResponseParse, "BHD search failed", decodeErr)
		}
		if bhdInt(decoded["status_code"]) == 0 {
			return dupe.Failed(dupe.FailureResponseStatus, "BHD api rejected search", nil)
		}
		pageEntries := bhdEntries(decoded)
		entries = append(entries, pageEntries...)
		pages++
		rawPage, pageKnown := decoded["page"]
		rawTotalPages, totalPagesKnown := decoded["total_pages"]
		rawTotalResults, totalResultsKnown := decoded["total_results"]
		hasPagination := pageKnown || totalPagesKnown || totalResultsKnown
		if pageNumber == 1 {
			firstPageHasPagination = hasPagination
		} else if hasPagination != firstPageHasPagination {
			break
		}
		if expectedTotalPages >= 0 || hasPagination {
			responsePage, validPage := bhdNonNegativeInt(rawPage)
			totalPages, validTotalPages := bhdNonNegativeInt(rawTotalPages)
			totalResults, validTotalResults := bhdNonNegativeInt(rawTotalResults)
			if !pageKnown || !totalPagesKnown || !totalResultsKnown || !validPage || !validTotalPages || !validTotalResults || responsePage != pageNumber {
				break
			}
			if expectedTotalPages < 0 {
				expectedTotalPages, expectedTotalResults = totalPages, totalResults
			} else if totalPages != expectedTotalPages || totalResults != expectedTotalResults {
				break
			}
			switch {
			case totalPages == 0 && totalResults == 0 && len(entries) == 0:
				complete = true
			case totalPages > 0 && pageNumber == totalPages && len(entries) == totalResults:
				complete = true
			case totalPages > 0 && pageNumber < totalPages && len(pageEntries) > 0:
				continue
			}
			break
		}
		if len(pageEntries) < pageSize {
			complete = true
			break
		}
	}
	warnings := []string(nil)
	if !complete {
		warnings = []string{"BHD search reached a pagination bound or omitted completion metadata"}
	}
	return dupe.ResolvedWithSearch(entries, warnings, dupe.SearchEvidence{
		Complete:  complete,
		WorkScope: dupe.WorkScopeProviderID,
		Pages:     pages,
		Scope:     "work_category",
		Warnings:  warnings,
	})
}

func bhdEntries(payload map[string]any) []api.DupeEntry {
	results, _ := payload["results"].([]any)
	entries := make([]api.DupeEntry, 0, len(results))
	for _, raw := range results {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		entry := api.DupeEntry{
			Name:     bhdString(item["name"]),
			Link:     bhdString(item["url"]),
			ID:       bhdString(item["id"]),
			Category: bhdString(item["category"]),
			Type:     bhdString(item["type"]),
			Source:   bhdString(item["source"]),
			Internal: bhdInt(item["internal"]) == 1,
		}
		if size := bhdInt(item["size"]); size > 0 {
			entry.SizeKnown, entry.SizeBytes = true, size
		}
		entry.HDR, entry.Flags = bhdHDRFacts(item)
		entry.FlagsPresent = len(entry.HDR.SourceFields) > 0
		entry.FlagsComplete = entry.HDR.Status == api.HDREvidenceComplete
		entries = append(entries, entry)
	}
	return entries
}

func bhdHDRFacts(item map[string]any) (api.HDRFacts, []string) {
	facts := api.HDRFacts{
		Origin: api.HDREvidenceTrackerAPI,
		Status: api.HDREvidenceMissing,
	}
	var flags []string
	allPresent := true
	allValid := true
	anyValid := false
	for _, field := range []struct {
		key    string
		flag   string
		format api.HDRFormat
	}{
		{
			key:    "dv",
			flag:   "DV",
			format: api.HDRFormatDolbyVision,
		},
		{
			key:    "hdr10",
			flag:   "HDR10",
			format: api.HDRFormatHDR10,
		},
		{
			key:    "hdr10+",
			flag:   "HDR10+",
			format: api.HDRFormatHDR10Plus,
		},
		{
			key:    "hlg",
			flag:   "HLG",
			format: api.HDRFormatHLG,
		},
	} {
		value, present, valid := bhdBoolField(item, field.key)
		if !present {
			allPresent = false
			continue
		}
		facts.SourceFields = append(facts.SourceFields, field.key)
		if !valid {
			allValid = false
			continue
		}
		anyValid = true
		if value {
			facts.Formats = append(facts.Formats, field.format)
			flags = append(flags, field.flag)
		}
	}
	switch {
	case allPresent && allValid:
		facts.Status = api.HDREvidenceComplete
		if len(facts.Formats) == 0 {
			facts.Formats = []api.HDRFormat{api.HDRFormatSDR}
		}
	case anyValid:
		facts.Status = api.HDREvidencePartial
	case len(facts.SourceFields) > 0:
		facts.Status = api.HDREvidencePartial
	default:
		facts.Origin = api.HDREvidenceUnknown
		facts.Status = api.HDREvidenceMissing
	}
	if slices.Contains(facts.Formats, api.HDRFormatDolbyVision) {
		if slices.Contains(facts.Formats, api.HDRFormatHDR10Plus) {
			facts.FallbackFormats = []api.HDRFormat{api.HDRFormatHDR10Plus, api.HDRFormatHDR10}
		} else if slices.Contains(facts.Formats, api.HDRFormatHDR10) {
			facts.FallbackFormats = []api.HDRFormat{api.HDRFormatHDR10}
		}
	}
	return facts, flags
}

func bhdBoolField(item map[string]any, key string) (bool, bool, bool) {
	raw, present := item[key]
	if !present {
		return false, false, false
	}
	switch value := raw.(type) {
	case bool:
		return value, true, true
	case json.Number:
		parsed, err := value.Int64()
		return parsed == 1, true, err == nil && (parsed == 0 || parsed == 1)
	case float64:
		return value == 1, true, value == 0 || value == 1
	case int:
		return value == 1, true, value == 0 || value == 1
	case int64:
		return value == 1, true, value == 0 || value == 1
	case string:
		normalized := strings.ToLower(strings.TrimSpace(value))
		switch normalized {
		case "1", "true":
			return true, true, true
		case "0", "false":
			return false, true, true
		default:
			return false, true, false
		}
	default:
		return false, true, false
	}
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
	return providerid.IMDb(id).Prefixed()
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

func bhdNonNegativeInt(value any) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
	return parsed, err == nil && parsed >= 0
}
