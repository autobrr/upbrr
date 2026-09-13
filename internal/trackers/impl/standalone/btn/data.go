// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// NewDataLookup returns a BTN JSON-RPC lookup bound to cfg and httpClient.
func (d *Definition) NewDataLookup(cfg config.Config, httpClient *http.Client, _ api.Logger) trackers.DataLookup {
	return &dataLookup{
		cfg:      cfg,
		http:     httpClient,
		endpoint: "https://api.broadcasthe.net/",
	}
}

// Lookup resolves IMDb and TVDB identifiers for a BTN torrent ID. Missing or
// short API tokens, missing tracker IDs, non-success responses, API errors, and
// empty torrent results produce an empty result without an error. Ambiguous or
// mismatched torrent results return an error before identifiers can be applied.
func (l *dataLookup) Lookup(ctx context.Context, req trackers.DataLookupRequest) (trackers.DataLookupResult, error) {
	token := strings.TrimSpace(config.ResolveBTNAPIToken(l.cfg))
	trackerID := strings.TrimSpace(req.TrackerID)
	if len(token) < 25 || trackerID == "" {
		return trackers.DataLookupResult{}, nil
	}
	payload := map[string]any{
		"id":     "ua-go",
		"method": "getTorrents",
		"params": map[string]any{
			"key":     token,
			"search":  map[string]any{"id": trackerID},
			"results": 1,
			"offset":  0,
		},
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
	if len(decoded.Result.Torrents) == 0 {
		return trackers.DataLookupResult{}, nil
	}
	value, found := decoded.Result.Torrents[trackerID]
	if len(decoded.Result.Torrents) != 1 || !found || decodeBTNTorrent(trackerID, value).id != trackerID {
		return trackers.DataLookupResult{}, errors.New("trackerdata: btn response did not identify the requested torrent")
	}
	return trackers.DataLookupResult{
		TrackerID: trackerID,
		IMDBID:    int(btnInt(value["ImdbID"])),
		TVDBID:    int(btnInt(value["TvdbID"])),
	}, nil
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
