// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	cfg  config.Config
	http *http.Client
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func (Definition) NewDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{cfg: cfg, http: httpClient}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	apiKey := spdAPIKey(s.cfg)
	if apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}
	params := url.Values{}
	switch {
	case meta.Identity.IMDBID != 0:
		params.Set("imdbId", strconv.Itoa(meta.Identity.IMDBID))
	case strings.TrimSpace(meta.Release.Title) != "":
		params.Set("search", strings.TrimSpace(meta.Release.Title))
	default:
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing imdb/title for SPD dupe search", nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://speedapp.io/api/torrent", nil)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "SPD search failed", err)
	}
	req.URL.RawQuery = params.Encode()
	req.Header.Set("Authorization", apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "SPD search failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return dupe.Failed(dupe.FailureResponseStatus, "SPD search failed", nil)
	}
	var items []map[string]any
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&items); err != nil {
		return dupe.Failed(dupe.FailureResponseParse, "SPD search failed", err)
	}
	entries := make([]api.DupeEntry, 0, len(items))
	for _, item := range items {
		id := spdString(item["id"])
		entry := api.DupeEntry{
			Name: spdString(item["name"]),
			ID:   id,
			Link: "https://speedapp.io/browse/" + id + "/",
		}
		if size := spdInt64(item["size"]); size > 0 {
			entry.SizeKnown, entry.SizeBytes = true, size
		}
		entries = append(entries, entry)
	}
	return dupe.Resolved(entries, nil)
}

func spdAPIKey(cfg config.Config) string {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "SPD") {
			return strings.TrimSpace(entry.APIKey)
		}
	}
	return ""
}
func spdString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}
func spdInt64(value any) int64 {
	switch typed := value.(type) {
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case float64:
		return int64(typed)
	default:
		return 0
	}
}
