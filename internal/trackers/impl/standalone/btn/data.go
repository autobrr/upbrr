// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

func (d *Definition) NewDataLookup(cfg config.Config, httpClient *http.Client, _ api.Logger) trackers.DataLookup {
	return &dataLookup{
		cfg:      cfg,
		http:     httpClient,
		endpoint: "https://api.broadcasthe.net/",
	}
}

func (l *dataLookup) Lookup(ctx context.Context, req trackers.DataLookupRequest) (trackers.DataLookupResult, error) {
	token := strings.TrimSpace(config.ResolveBTNAPIToken(l.cfg))
	trackerID := strings.TrimSpace(req.TrackerID)
	if len(token) < 25 || trackerID == "" {
		return trackers.DataLookupResult{}, nil
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "ua-go",
		"method":  "getTorrentsSearch",
		"params":  []any{token, map[string]any{"id": trackerID}, 50},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return trackers.DataLookupResult{}, fmt.Errorf("trackerdata: btn encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, l.endpoint, bytes.NewReader(body))
	if err != nil {
		return trackers.DataLookupResult{}, fmt.Errorf("trackerdata: btn request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := l.http.Do(httpReq)
	if err != nil {
		return trackers.DataLookupResult{}, fmt.Errorf("trackerdata: btn request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return trackers.DataLookupResult{}, nil
	}
	var decoded struct {
		Error  map[string]any `json:"error"`
		Result struct {
			Torrents map[string]map[string]any `json:"torrents"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return trackers.DataLookupResult{}, fmt.Errorf("trackerdata: btn decode: %w", err)
	}
	if len(decoded.Error) > 0 {
		return trackers.DataLookupResult{}, nil
	}
	for _, value := range decoded.Result.Torrents {
		return trackers.DataLookupResult{
			TrackerID: trackerID,
			IMDBID:    int(btnInt(value["ImdbID"])),
			TVDBID:    int(btnInt(value["TvdbID"])),
		}, nil
	}
	return trackers.DataLookupResult{}, nil
}

func btnInt(value any) int64 {
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
		parsed, _ := json.Number(strings.TrimSpace(typed)).Int64()
		return parsed
	default:
		return 0
	}
}
