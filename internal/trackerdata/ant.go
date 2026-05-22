// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerdata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func (c *Client) lookupANT(ctx context.Context, meta api.PreparedMetadata, searchFileName string) (Result, error) {
	if strings.TrimSpace(meta.DiscType) != "" {
		return Result{}, nil
	}
	cfg, ok := c.trackerCfg("ANT")
	if !ok || strings.TrimSpace(cfg.APIKey) == "" {
		return Result{}, nil
	}
	fileName := strings.TrimSpace(searchFileName)
	if fileName == "" {
		return Result{}, nil
	}

	params := url.Values{}
	params.Set("t", "search")
	params.Set("filename", fileName)
	params.Set("o", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.antURL, nil)
	if err != nil {
		return Result{}, fmt.Errorf("trackerdata: ant request: %w", err)
	}
	req.URL.RawQuery = params.Encode()
	req.Header.Set("User-Agent", "upbrr")
	req.Header.Set("X-API-Key", strings.TrimSpace(cfg.APIKey))

	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("trackerdata: ant request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, nil
	}

	var decoded struct {
		Items []map[string]any `json:"item"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Result{}, fmt.Errorf("trackerdata: ant decode: %w", err)
	}
	item := matchANTItem(decoded.Items, fileName)
	if len(item) == 0 {
		return Result{}, nil
	}
	return Result{TrackerID: "1", IMDBID: imdbFromAny(item["imdb"]), TMDBID: intValue(item["tmdb"])}, nil
}

func matchANTItem(items []map[string]any, fileName string) map[string]any {
	if len(items) == 1 {
		return items[0]
	}
	baseName := strings.ToLower(strings.TrimSpace(trimExt(fileName)))
	for _, item := range items {
		files, _ := item["files"].([]any)
		for _, raw := range files {
			entry := mapValue(raw)
			name := strings.ToLower(strings.TrimSpace(stringValue(entry["name"])))
			if name == "" {
				continue
			}
			if strings.EqualFold(name, fileName) {
				return item
			}
			if trimExt(name) == baseName {
				return item
			}
		}
	}
	return nil
}
