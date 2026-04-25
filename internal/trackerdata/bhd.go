// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerdata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/pkg/api"
)

func (c *Client) lookupBHD(
	ctx context.Context,
	trackerID string,
	meta api.PreparedMetadata,
	searchFileName string,
	onlyID bool,
	keepImages bool,
) (Result, error) {
	cfg, ok := c.trackerCfg("BHD")
	if !ok {
		return Result{}, nil
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	rssKey := strings.TrimSpace(cfg.BhdRSSKey)
	if len(apiKey) < minTokenLength || len(rssKey) < minTokenLength {
		return Result{}, nil
	}

	endpoint := strings.TrimRight(c.bhdBaseURL, "/") + "/" + apiKey
	payload := map[string]any{}
	if id := strings.TrimSpace(trackerID); id != "" {
		payload["action"] = "details"
		payload["torrent_id"] = id
	} else {
		payload["action"] = "search"
		payload["rsskey"] = rssKey
		hasSearchFilter := false
		if shouldUseFolderSearch(meta) {
			payload["folder_name"] = pathutil.Base(meta.SourcePath)
			hasSearchFilter = true
		} else if trimmed := strings.TrimSpace(searchFileName); trimmed != "" {
			payload["file_name"] = trimmed
			hasSearchFilter = true
		}
		if !hasSearchFilter {
			return Result{}, nil
		}
	}

	first, err := c.bhdRequestFirst(ctx, endpoint, payload)
	if err != nil || len(first) == 0 {
		return Result{}, err
	}

	result := Result{
		TrackerID: trackerID,
		IMDBID:    imdbFromAny(first["imdb_id"]),
	}
	result.Category, result.TMDBID = parseBHDTMDB(first["tmdb_id"])
	descriptionValue := first["description"]
	description := ""
	if stringValue(descriptionValue) == "1" {
		torrentID := stringValue(first["id"])
		if torrentID == "" {
			torrentID = strings.TrimSpace(trackerID)
		}
		if torrentID != "" {
			descMap, derr := c.bhdRequest(ctx, endpoint, map[string]any{
				"action":     "description",
				"torrent_id": torrentID,
			})
			if derr == nil {
				description = stringValue(descMap["result"])
			}
		}
	} else {
		description = stringValue(descriptionValue)
	}

	if onlyID && !keepImages {
		return result, nil
	}
	report := bbcode.CleanBHDDescription(description, bbcode.BHDOptions{})
	if !onlyID {
		result.Description = strings.TrimSpace(report.Description)
	}
	if keepImages {
		result.Images = report.Images
	}
	if strings.TrimSpace(result.TrackerID) == "" {
		result.TrackerID = stringValue(first["id"])
	}
	return result, nil
}

func (c *Client) bhdRequestFirst(ctx context.Context, endpoint string, payload map[string]any) (map[string]any, error) {
	body, err := c.bhdRequest(ctx, endpoint, payload)
	if err != nil || len(body) == 0 {
		return nil, err
	}
	if items, ok := body["results"].([]any); ok && len(items) > 0 {
		return mapValue(items[0]), nil
	}
	if item, ok := body["result"].(map[string]any); ok {
		return item, nil
	}
	return nil, nil
}

func (c *Client) bhdRequest(ctx context.Context, endpoint string, payload map[string]any) (map[string]any, error) {
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("trackerdata: bhd request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trackerdata: bhd request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil
	}
	result := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("trackerdata: bhd decode: %w", err)
	}
	if intValue(result["status_code"]) == 0 {
		return nil, nil
	}
	if success, ok := result["success"].(bool); ok && !success {
		return nil, nil
	}
	return result, nil
}

func parseBHDTMDB(value any) (string, int) {
	raw := strings.ToLower(strings.TrimSpace(stringValue(value)))
	if raw == "" || raw == "0" {
		return "", 0
	}
	if strings.HasPrefix(raw, "tv/") {
		return "TV", intValue(strings.TrimPrefix(raw, "tv/"))
	}
	if strings.HasPrefix(raw, "movie/") {
		return "MOVIE", intValue(strings.TrimPrefix(raw, "movie/"))
	}
	return "", intValue(raw)
}
