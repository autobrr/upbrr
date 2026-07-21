// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
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

// newDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{cfg: cfg, http: httpClient}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	apiKey := nblAPIKey(s.cfg)
	if apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}
	searchTerm := map[string]any{}
	switch {
	case meta.Identity.TVmazeID != 0:
		searchTerm["tvmaze"] = meta.Identity.TVmazeID
	case meta.Identity.IMDBID != 0:
		searchTerm["imdb"] = strconv.Itoa(meta.Identity.IMDBID)
	default:
		searchTerm["series"] = strings.TrimSpace(meta.Release.Title)
	}
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "getTorrents",
		"params":  []any{apiKey, searchTerm},
	})
	if err != nil {
		return dupe.Failed(dupe.FailureInternal, "NBL search failed", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://nebulance.io/api.php", bytes.NewReader(raw))
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "NBL search failed", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "NBL search failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return dupe.Failed(dupe.FailureResponseStatus, "NBL search failed", nil)
	}
	var payload struct {
		Result struct {
			Items []map[string]any `json:"items"`
		} `json:"result"`
	}
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return dupe.Failed(dupe.FailureResponseParse, "NBL search failed", err)
	}
	entries := make([]api.DupeEntry, 0, len(payload.Result.Items))
	for _, item := range payload.Result.Items {
		entry := api.DupeEntry{
			Name:     nblString(item["rls_name"]),
			Link:     "https://nebulance.io/torrents.php?id=" + nblString(item["group_id"]),
			Download: nblString(item["download"]),
		}
		if size := nblInt64(item["size"]); size > 0 {
			entry.SizeKnown, entry.SizeBytes = true, size
		}
		if files, ok := item["file_list"].([]any); ok {
			entry.FileCount = len(files)
			for _, file := range files {
				if name := nblString(file); name != "" {
					entry.Files = append(entry.Files, name)
				}
			}
		}
		entries = append(entries, entry)
	}
	return dupe.Resolved(entries, nil)
}

func nblAPIKey(cfg config.Config) string {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "NBL") {
			return strings.TrimSpace(entry.APIKey)
		}
	}
	return ""
}
func nblInt64(value any) int64 {
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
