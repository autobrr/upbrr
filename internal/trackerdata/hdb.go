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

func (c *Client) lookupHDB(
	ctx context.Context,
	trackerID string,
	meta api.PreparedMetadata,
	searchFileName string,
	onlyID bool,
	keepImages bool,
) (Result, error) {
	cfg, ok := c.trackerCfg("HDB")
	if !ok {
		return Result{}, nil
	}
	username := strings.TrimSpace(cfg.Username)
	passkey := strings.TrimSpace(cfg.Passkey)
	if username == "" || passkey == "" {
		return Result{}, nil
	}

	payload := map[string]any{"username": username, "passkey": passkey}
	if id := strings.TrimSpace(trackerID); id != "" {
		payload["id"] = id
	} else {
		payload["limit"] = 100
		hasSearchFilter := false
		if shouldUseFolderSearch(meta) {
			payload["search"] = pathutil.Base(meta.SourcePath)
			hasSearchFilter = true
		} else if strings.TrimSpace(searchFileName) != "" {
			payload["file_in_torrent"] = searchFileName
			hasSearchFilter = true
		}
		if !hasSearchFilter {
			return Result{}, nil
		}
	}

	first, err := c.hdbRequestFirst(ctx, payload)
	if err != nil || len(first) == 0 {
		return Result{}, err
	}

	result := Result{
		TrackerID: stringValue(first["id"]),
		IMDBID:    nestedInt(first, "imdb", "id"),
		TVDBID:    nestedInt(first, "tvdb", "id"),
		InfoHash:  stringValue(first["hash"]),
	}
	if result.TrackerID == "" {
		result.TrackerID = strings.TrimSpace(trackerID)
	}
	if onlyID && !keepImages {
		return result, nil
	}

	report := bbcode.CleanHDBDescription(stringValue(first["descr"]))
	if !onlyID {
		result.Description = strings.TrimSpace(report.Description)
	}
	if keepImages {
		result.Images = report.Images
	}
	return result, nil
}

func (c *Client) hdbRequestFirst(ctx context.Context, payload map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("trackerdata: hdb encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.hdbURL, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("trackerdata: hdb request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trackerdata: hdb request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil
	}

	result := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("trackerdata: hdb decode: %w", err)
	}
	if intValue(result["status"]) != 0 {
		return nil, nil
	}
	items, ok := result["data"].([]any)
	if !ok || len(items) == 0 {
		return nil, nil
	}
	return mapValue(items[0]), nil
}

func nestedInt(value map[string]any, root string, key string) int {
	node := mapValue(value[root])
	return intValue(node[key])
}
