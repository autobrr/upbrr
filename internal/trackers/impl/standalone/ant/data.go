// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type dataLookup struct {
	cfg      config.Config
	http     *http.Client
	endpoint string
}

// NewDataLookup returns an ANT lookup bound to cfg and httpClient.
func (d *Definition) NewDataLookup(cfg config.Config, httpClient *http.Client, _ api.Logger) trackers.DataLookup {
	return &dataLookup{
		cfg:      cfg,
		http:     httpClient,
		endpoint: "https://anthelion.me/api.php",
	}
}

// Lookup searches ANT by filename and returns IMDb and TMDB identifiers from
// the sole result or the result containing the requested file. Disc uploads,
// missing credentials or names, non-success responses, and no match produce an
// empty result without an error.
func (l *dataLookup) Lookup(ctx context.Context, req trackers.DataLookupRequest) (trackers.DataLookupResult, error) {
	if strings.TrimSpace(req.Meta.DiscType) != "" {
		return trackers.DataLookupResult{}, nil
	}
	apiKey := antAPIKey(l.cfg)
	fileName := strings.TrimSpace(req.SearchName)
	if apiKey == "" || fileName == "" {
		return trackers.DataLookupResult{}, nil
	}
	params := url.Values{
		"t":        {"search"},
		"filename": {fileName},
		"o":        {"json"},
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, l.endpoint, nil)
	if err != nil {
		return trackers.DataLookupResult{}, fmt.Errorf("trackerdata: ant request: %w", err)
	}
	httpReq.URL.RawQuery = params.Encode()
	httpReq.Header.Set("User-Agent", "upbrr")
	httpReq.Header.Set("X-Api-Key", apiKey)
	resp, err := l.http.Do(httpReq)
	if err != nil {
		return trackers.DataLookupResult{}, fmt.Errorf("trackerdata: ant request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return trackers.DataLookupResult{}, nil
	}
	var decoded struct {
		Items []map[string]any `json:"item"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return trackers.DataLookupResult{}, fmt.Errorf("trackerdata: ant decode: %w", err)
	}
	item := matchDataItem(decoded.Items, fileName)
	if len(item) == 0 {
		return trackers.DataLookupResult{}, nil
	}
	return trackers.DataLookupResult{
		TrackerID: "1",
		IMDBID:    antIMDB(item["imdb"]),
		TMDBID:    int(antInt(item["tmdb"])),
	}, nil
}

func matchDataItem(items []map[string]any, fileName string) map[string]any {
	if len(items) == 1 {
		return items[0]
	}
	baseName := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(fileName, filepath.Ext(fileName))))
	for _, item := range items {
		files, _ := item["files"].([]any)
		for _, raw := range files {
			entry, _ := raw.(map[string]any)
			name := strings.ToLower(antString(entry["name"]))
			if strings.EqualFold(name, fileName) || strings.TrimSuffix(name, filepath.Ext(name)) == baseName {
				return item
			}
		}
	}
	return nil
}

func antIMDB(value any) int {
	text := strings.TrimPrefix(strings.ToLower(antString(value)), "tt")
	parsed, _ := strconv.Atoi(text)
	return parsed
}
