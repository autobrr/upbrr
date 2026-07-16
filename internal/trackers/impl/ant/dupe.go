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
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	endpoint string
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func (d *Definition) NewDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		endpoint: "https://anthelion.me/api.php",
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
		params.Set("imdb", strconv.Itoa(meta.Identity.IMDBID))
	default:
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing tmdb/imdb id for ANT dupe search", nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "ANT request failed", err)
	}
	req.URL.RawQuery = params.Encode()
	req.Header.Set("User-Agent", "upbrr")
	req.Header.Set("X-Api-Key", apiKey)
	resp, err := s.http.Do(req)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "ANT request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return dupe.Failed(dupe.FailureResponseStatus, "ANT search failed", nil)
	}
	var payload map[string]any
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil || len(payload) == 0 {
		return dupe.Failed(dupe.FailureResponseParse, "ANT search failed", err)
	}
	return dupe.Resolved(antDupeEntries(payload, meta.Release.Resolution), nil)
}

func antDupeEntries(payload map[string]any, resolution string) []api.DupeEntry {
	items, _ := payload["item"].([]any)
	entries := make([]api.DupeEntry, 0, len(items))
	targetResolution := strings.ToLower(strings.TrimSpace(resolution))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok || targetResolution != "" && strings.ToLower(antString(item["resolution"])) != targetResolution {
			continue
		}
		files := antFiles(item["files"])
		fileCount := int(antInt(item["fileCount"]))
		if fileCount == 0 {
			fileCount = len(files)
		}
		entry := api.DupeEntry{
			Name:      antString(item["fileName"]),
			Files:     files,
			FileCount: fileCount,
			Link:      antString(item["guid"]),
			Download:  strings.ReplaceAll(antString(item["link"]), "&amp;", "&"),
		}
		if entry.Name == "" && len(files) > 0 {
			entry.Name = files[0]
		}
		if size := antInt(item["size"]); size > 0 {
			entry.SizeKnown, entry.SizeBytes = true, size
		}
		if flags, ok := item["flags"].([]any); ok {
			for _, rawFlag := range flags {
				if flag := antString(rawFlag); flag != "" {
					entry.Flags = append(entry.Flags, flag)
				}
			}
		}
		entries = append(entries, entry)
	}
	return entries
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
