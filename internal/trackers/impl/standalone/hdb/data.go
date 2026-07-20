// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

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

type dataLookup struct {
	cfg      config.Config
	http     *http.Client
	endpoint string
}

// NewDataLookup returns an HDB lookup bound to the definition's base URL, cfg,
// and httpClient.
func (d *Definition) NewDataLookup(cfg config.Config, httpClient *http.Client, _ api.Logger) trackers.DataLookup {
	return &dataLookup{
		cfg:      cfg,
		http:     httpClient,
		endpoint: strings.TrimRight(d.baseURL, "/") + "/api/torrents",
	}
}

// Lookup resolves the first HDB torrent by tracker ID or folder/file search.
// Missing credentials or filters, non-success responses, API rejection, and
// empty data produce an empty result without an error. OnlyID and KeepImages
// independently control description and image projection.
func (l *dataLookup) Lookup(ctx context.Context, req trackers.DataLookupRequest) (trackers.DataLookupResult, error) {
	username, passkey := hdbCredentials(l.cfg)
	if username == "" || passkey == "" {
		return trackers.DataLookupResult{}, nil
	}
	payload := map[string]any{"username": username, "passkey": passkey}
	if id := strings.TrimSpace(req.TrackerID); id != "" {
		payload["id"] = id
	} else {
		payload["limit"] = 100
		hasFilter := false
		if strings.TrimSpace(req.Meta.DiscType) != "" || len(req.Meta.FileList) != 1 {
			payload["search"], hasFilter = pathutil.Base(req.Meta.SourcePath), true
		} else if name := strings.TrimSpace(req.SearchName); name != "" {
			payload["file_in_torrent"], hasFilter = name, true
		}
		if !hasFilter {
			return trackers.DataLookupResult{}, nil
		}
	}
	first, err := l.requestFirst(ctx, payload)
	if err != nil || len(first) == 0 {
		return trackers.DataLookupResult{}, err
	}
	result := trackers.DataLookupResult{
		TrackerID: hdbString(first["id"]),
		IMDBID:    hdbNestedInt(first, "imdb", "id"),
		TVDBID:    hdbNestedInt(first, "tvdb", "id"),
		InfoHash:  hdbString(first["hash"]),
	}
	if result.TrackerID == "" {
		result.TrackerID = strings.TrimSpace(req.TrackerID)
	}
	if req.OnlyID && !req.KeepImages {
		return result, nil
	}
	report := CleanDescription(hdbString(first["descr"]))
	if !req.OnlyID {
		result.Description = strings.TrimSpace(report.Description)
	}
	if req.KeepImages {
		result.Images = report.Images
	}
	return result, nil
}

func (l *dataLookup) requestFirst(ctx context.Context, payload map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("trackerdata: hdb encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("trackerdata: hdb request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trackerdata: hdb request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, nil
	}
	result := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("trackerdata: hdb decode: %w", err)
	}
	if hdbInt(result["status"]) != 0 {
		return nil, nil
	}
	items, _ := result["data"].([]any)
	if len(items) == 0 {
		return nil, nil
	}
	item, _ := items[0].(map[string]any)
	return item, nil
}

func hdbCredentials(cfg config.Config) (string, string) {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "HDB") {
			return strings.TrimSpace(entry.Username), strings.TrimSpace(entry.Passkey)
		}
	}
	return "", ""
}

func hdbString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func hdbInt(value any) int {
	var result int
	_, _ = fmt.Sscan(hdbString(value), &result)
	return result
}

func hdbNestedInt(value map[string]any, root, key string) int {
	node, _ := value[root].(map[string]any)
	return hdbInt(node[key])
}
