// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const minDataTokenLength = 25

type dataLookup struct {
	cfg     config.Config
	http    *http.Client
	baseURL string
}

func (d *Definition) NewDataLookup(cfg config.Config, httpClient *http.Client, _ api.Logger) trackers.DataLookup {
	return &dataLookup{
		cfg:     cfg,
		http:    httpClient,
		baseURL: "https://beyond-hd.me/api/torrents",
	}
}

func (l *dataLookup) Lookup(ctx context.Context, req trackers.DataLookupRequest) (trackers.DataLookupResult, error) {
	cfg, apiKey := bhdConfig(l.cfg)
	rssKey := strings.TrimSpace(cfg.BhdRSSKey)
	if len(apiKey) < minDataTokenLength || len(rssKey) < minDataTokenLength {
		return trackers.DataLookupResult{}, nil
	}
	endpoint := strings.TrimRight(l.baseURL, "/") + "/" + apiKey
	payload := map[string]any{}
	if id := strings.TrimSpace(req.TrackerID); id != "" {
		payload["action"], payload["torrent_id"] = "details", id
	} else {
		payload["action"], payload["rsskey"] = "search", rssKey
		hasFilter := false
		if strings.TrimSpace(req.Meta.DiscType) != "" || len(req.Meta.FileList) != 1 {
			payload["folder_name"], hasFilter = pathutil.Base(req.Meta.SourcePath), true
		} else if name := strings.TrimSpace(req.SearchName); name != "" {
			payload["file_name"], hasFilter = name, true
		}
		if !hasFilter {
			return trackers.DataLookupResult{}, nil
		}
	}
	first, err := l.requestFirst(ctx, endpoint, payload)
	if err != nil || len(first) == 0 {
		return trackers.DataLookupResult{}, err
	}
	result := trackers.DataLookupResult{TrackerID: req.TrackerID, IMDBID: bhdIMDBInt(first["imdb_id"])}
	result.Category, result.TMDBID = parseTMDB(first["tmdb_id"])
	description := ""
	if bhdString(first["description"]) == "1" {
		torrentID := bhdString(first["id"])
		if torrentID == "" {
			torrentID = strings.TrimSpace(req.TrackerID)
		}
		if torrentID != "" {
			if body, requestErr := l.request(ctx, endpoint, map[string]any{"action": "description", "torrent_id": torrentID}); requestErr == nil {
				description = bhdString(body["result"])
			}
		}
	} else {
		description = bhdString(first["description"])
	}
	if req.OnlyID && !req.KeepImages {
		return result, nil
	}
	report := CleanDescription(description, BBCodeOptions{})
	if !req.OnlyID {
		result.Description = strings.TrimSpace(report.Description)
	}
	if req.KeepImages {
		result.Images = report.Images
	}
	if strings.TrimSpace(result.TrackerID) == "" {
		result.TrackerID = bhdString(first["id"])
	}
	return result, nil
}

func (l *dataLookup) requestFirst(ctx context.Context, endpoint string, payload map[string]any) (map[string]any, error) {
	body, err := l.request(ctx, endpoint, payload)
	if err != nil || len(body) == 0 {
		return nil, err
	}
	if items, ok := body["results"].([]any); ok && len(items) > 0 {
		item, _ := items[0].(map[string]any)
		return item, nil
	}
	item, _ := body["result"].(map[string]any)
	return item, nil
}

func (l *dataLookup) request(ctx context.Context, endpoint string, payload map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("trackerdata: bhd encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("trackerdata: bhd request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trackerdata: bhd request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, nil
	}
	result := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("trackerdata: bhd decode: %w", err)
	}
	if bhdInt(result["status_code"]) == 0 {
		return nil, nil
	}
	if success, ok := result["success"].(bool); ok && !success {
		return nil, nil
	}
	return result, nil
}

func parseTMDB(value any) (string, int) {
	raw := strings.ToLower(bhdString(value))
	if raw == "" || raw == "0" {
		return "", 0
	}
	if after, ok := strings.CutPrefix(raw, "tv/"); ok {
		return "TV", int(bhdInt(after))
	}
	if after, ok := strings.CutPrefix(raw, "movie/"); ok {
		return "MOVIE", int(bhdInt(after))
	}
	return "", int(bhdInt(raw))
}

func bhdIMDBInt(value any) int {
	return int(bhdInt(strings.TrimPrefix(strings.ToLower(bhdString(value)), "tt")))
}
