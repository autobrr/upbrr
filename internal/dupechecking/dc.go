// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"net/http"
	"net/url"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type dcHandler struct {
	cfg  config.Config
	http *http.Client
}

func (h dcHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	_, apiKey, ok := trackerCfgWithAPIKey(h.cfg, "DC")
	if !ok {
		return nil, []string{noteSkip("missing api_key for tracker")}, nil
	}

	imdbID := trackers.ResolveIMDbIDText(meta)
	if imdbID == "" {
		return nil, []string{noteSkip("missing IMDb ID for DC dupe search")}, nil
	}

	params := url.Values{}
	params.Set("searchText", imdbID)
	headers := map[string]string{"X-API-KEY": apiKey}
	status, payload, err := doJSONGetAny(ctx, h.http, "https://digitalcore.club/api/v1/torrents", params, headers)
	if err != nil || status < 200 || status >= 300 {
		return nil, []string{noteSkip("DC search failed")}, nil
	}
	rawList, ok := anyToSlice(payload)
	if !ok {
		return nil, nil, nil
	}
	entries := make([]api.DupeEntry, 0, len(rawList))
	for _, raw := range rawList {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := stringFromAny(item["id"])
		entry := api.DupeEntry{Name: stringFromAny(item["name"]), ID: id, Link: "https://digitalcore.club/torrent/" + id + "/"}
		size := intFromAny(item["size"])
		if size > 0 {
			entry.SizeKnown = true
			entry.SizeBytes = size
		}
		entries = append(entries, entry)
	}
	return entries, nil, nil
}
