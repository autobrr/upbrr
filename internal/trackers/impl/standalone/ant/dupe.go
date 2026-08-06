// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	endpoint string
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
		endpoint: "https://anthelion.me/api.php",
		maxPages: deps.MaxPages(100),
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	apiKey := antAPIKey(s.cfg)
	if apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}
	params := url.Values{"t": {"search"}, "o": {"json"}}
	switch {
	case meta.Identity.TMDBID != 0:
		params.Set("tmdb", strconv.Itoa(meta.Identity.TMDBID))
	case meta.Identity.IMDBID != 0:
		params.Set("imdb", providerid.IMDb(meta.Identity.IMDBID).Digits())
	default:
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing tmdb/imdb id for ANT dupe search", nil)
	}
	const pageLimit = 100
	params.Set("limit", strconv.Itoa(pageLimit))

	maxPages := s.maxPages
	if maxPages <= 0 {
		maxPages = 100
	}
	entries := make([]api.DupeEntry, 0)
	offset := 0
	complete := false
	pages := 0
	for pages < maxPages {
		pageParams := cloneANTValues(params)
		if offset > 0 {
			pageParams.Set("offset", strconv.Itoa(offset))
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
		if err != nil {
			return dupe.Failed(dupe.FailureRequest, "ANT request failed", err)
		}
		req.URL.RawQuery = pageParams.Encode()
		req.Header.Set("User-Agent", "upbrr")
		req.Header.Set("X-Api-Key", apiKey)
		resp, err := s.http.Do(req)
		if err != nil {
			return dupe.Failed(dupe.FailureRequest, "ANT request failed", err)
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			resp.Body.Close()
			return dupe.Failed(dupe.FailureResponseStatus, "ANT search failed", nil)
		}
		var payload map[string]any
		decoder := json.NewDecoder(resp.Body)
		decoder.UseNumber()
		decodeErr := decoder.Decode(&payload)
		resp.Body.Close()
		if decodeErr != nil || len(payload) == 0 {
			return dupe.Failed(dupe.FailureResponseParse, "ANT search failed", decodeErr)
		}

		pageEntries := antDupeEntries(payload, meta.Release.Resolution)
		entries = append(entries, pageEntries...)
		pages++
		itemCount := antItemCount(payload)
		pagination := parseANTPagination(payload)
		pageOffset := offset
		if pagination.OffsetPresent {
			if pagination.Offset != offset {
				break
			}
			pageOffset = pagination.Offset
		}
		nextOffset := pageOffset + itemCount
		switch {
		case pagination.TotalPresent && pagination.Total == 0 && itemCount == 0:
			complete = true
		case pagination.TotalPresent && pagination.Total > 0 && nextOffset >= pagination.Total:
			complete = true
		case pagination.TotalPresent && pagination.Total > nextOffset && nextOffset > offset:
			offset = nextOffset
			continue
		case pagination.TotalPresent:
			// Metadata is contradictory or the endpoint made no progress.
		case itemCount < pageLimit:
			complete = true
		case nextOffset > offset:
			offset = nextOffset
			continue
		}
		break
	}

	warnings := []string(nil)
	if !complete {
		warnings = []string{"ANT search result is truncated or lacks complete pagination evidence"}
	}
	return dupe.ResolvedWithSearch(entries, warnings, dupe.SearchEvidence{
		Complete: complete,
		Pages:    pages,
		Scope:    "work_identity",
		Warnings: warnings,
	})
}

func antDupeEntries(payload map[string]any, _ string) []api.DupeEntry {
	items, _ := payload["item"].([]any)
	entries := make([]api.DupeEntry, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		files := antFiles(item["files"])
		fileCount := int(antInt(item["fileCount"]))
		if fileCount == 0 {
			fileCount = len(files)
		}
		entry := api.DupeEntry{
			Name:          antCandidateName(item, files),
			Files:         files,
			FileCount:     fileCount,
			Link:          antString(item["guid"]),
			Download:      strings.ReplaceAll(antString(item["link"]), "&amp;", "&"),
			Res:           antString(item["resolution"]),
			Type:          antString(item["type"]),
			CanonicalType: antCandidateType(item),
			Source:        antCandidateSource(item),
			Codec:         antString(item["codec"]),
			Container:     antString(item["container"]),
			Group:         antString(item["releaseGroup"]),
		}
		if size := antInt(item["size"]); size > 0 {
			entry.SizeKnown, entry.SizeBytes = true, size
		}
		_, entry.FlagsPresent = item["flags"]
		if flags, ok := item["flags"].([]any); ok {
			for _, rawFlag := range flags {
				if flag := antString(rawFlag); flag != "" {
					entry.Flags = append(entry.Flags, flag)
				}
			}
		}
		entry.FlagsComplete = false
		entry.HDR = dupe.NormalizeTrackerHDRFlags(entry.Flags, entry.FlagsPresent, false)
		entries = append(entries, entry)
	}
	return entries
}

// antCandidateName prefers the listed content name for single-file torrents and ANT's torrent filename for multi-file torrents.
func antCandidateName(item map[string]any, files []string) string {
	if len(files) == 1 {
		return files[0]
	}
	for _, field := range []string{"fileName", "releaseName", "name", "title"} {
		if value := antString(item[field]); value != "" {
			return value
		}
	}
	return ""
}

// antCandidateSource falls back to ANT's media field when the response omits source.
func antCandidateSource(item map[string]any) string {
	if source := antString(item["source"]); source != "" {
		return source
	}
	return antString(item["media"])
}

// antCandidateType classifies disc containers explicitly and otherwise preserves ANT's type value.
func antCandidateType(item map[string]any) string {
	typeValue := antString(item["type"])
	switch strings.ToLower(antString(item["container"])) {
	case "m2ts", "bdmv", "vob/ifo", "video_ts", "iso":
		return "DISC"
	default:
		return typeValue
	}
}

type antPagination struct {
	Offset        int
	Total         int
	OffsetPresent bool
	TotalPresent  bool
}

func antItemCount(payload map[string]any) int {
	items, _ := payload["item"].([]any)
	return len(items)
}

func parseANTPagination(payload map[string]any) antPagination {
	for _, key := range []string{"response", "newznab:response", "newznab_response"} {
		raw, ok := payload[key]
		if !ok {
			continue
		}
		response, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if attributes, ok := response["@attributes"].(map[string]any); ok {
			response = attributes
		}
		offset, offsetPresent := antNonNegativeInt(response["offset"])
		total, totalPresent := antNonNegativeInt(response["total"])
		return antPagination{
			Offset:        offset,
			Total:         total,
			OffsetPresent: offsetPresent,
			TotalPresent:  totalPresent,
		}
	}
	return antPagination{}
}

func antNonNegativeInt(value any) (int, bool) {
	var raw string
	switch typed := value.(type) {
	case json.Number:
		raw = typed.String()
	case float64:
		raw = strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		if typed < 0 {
			return 0, false
		}
		return typed, true
	case int64:
		raw = strconv.FormatInt(typed, 10)
	case string:
		raw = strings.TrimSpace(typed)
	default:
		return 0, false
	}
	number, err := strconv.Atoi(raw)
	return number, err == nil && number >= 0
}

func cloneANTValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, entries := range values {
		cloned[key] = append([]string(nil), entries...)
	}
	return cloned
}

func antAPIKey(cfg config.Config) string {
	for name, trackerCfg := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "ANT") {
			return strings.TrimSpace(trackerCfg.APIKey)
		}
	}
	return ""
}

func antFiles(value any) []string {
	rawFiles, _ := value.([]any)
	files := make([]string, 0, len(rawFiles))
	for _, raw := range rawFiles {
		if file, ok := raw.(map[string]any); ok {
			if name := antString(file["name"]); name != "" {
				files = append(files, name)
			}
		}
	}
	return files
}

func antString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(fmt.Sprint(value), ".0"), ".00"))
}

func antInt(value any) int64 {
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
