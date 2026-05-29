// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerdata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/autobrr/upbrr/internal/config"
)

func (c *Client) lookupBTN(ctx context.Context, trackerID string) (Result, error) {
	apiToken := config.ResolveBTNAPIToken(c.cfg)
	if len(apiToken) < minTokenLength || trackerID == "" {
		return Result{}, nil
	}

	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "ua-go",
		"method":  "getTorrentsSearch",
		"params":  []any{apiToken, map[string]any{"id": trackerID}, 50},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, fmt.Errorf("trackerdata: btn encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.btnURL, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("trackerdata: btn request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("trackerdata: btn request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, nil
	}

	var decoded struct {
		Error  map[string]any `json:"error"`
		Result struct {
			Torrents map[string]map[string]any `json:"torrents"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Result{}, fmt.Errorf("trackerdata: btn decode: %w", err)
	}
	if len(decoded.Error) > 0 {
		return Result{}, nil
	}
	for _, value := range decoded.Result.Torrents {
		return Result{
			TrackerID: trackerID,
			IMDBID:    intValue(value["ImdbID"]),
			TVDBID:    intValue(value["TvdbID"]),
		}, nil
	}
	return Result{}, nil
}
