// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type bhdHandler struct {
	cfg    config.Config
	http   *http.Client
	logger api.Logger
}

func (h bhdHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	cfg, apiKey, ok := trackerCfgWithAPIKey(h.cfg, "BHD")
	if !ok {
		return nil, []string{noteSkip("missing api_key for tracker")}, nil
	}
	if meta.ExternalIDs.TMDBID == 0 {
		return nil, []string{noteSkip("missing tmdb id for BHD dupe search")}, nil
	}
	category := "Movies"
	tmdbPrefix := "movie"
	if strings.EqualFold(meta.ExternalIDs.Category, "TV") {
		category = "TV"
		tmdbPrefix = "tv"
	}
	payload := map[string]any{
		"action":     "search",
		"tmdb_id":    tmdbPrefix + "/" + strconv.Itoa(meta.ExternalIDs.TMDBID),
		"categories": category,
	}
	if season := resolveSeasonValue(meta); season != "" && strings.EqualFold(category, "TV") {
		payload["search"] = season
	}
	if rss := strings.TrimSpace(cfg.BhdRSSKey); rss != "" {
		payload["rsskey"] = rss
	}

	endpoint := "https://beyond-hd.me/api/torrents/" + apiKey
	status, body, err := doJSONPost(ctx, h.http, endpoint, payload, nil)
	if err != nil {
		return nil, []string{noteSkip("BHD request failed")}, nil
	}
	if status < 200 || status >= 300 || len(body) == 0 {
		return nil, []string{noteSkip("BHD search failed")}, nil
	}
	if intFromAny(body["status_code"]) == 0 {
		return nil, []string{noteSkip("BHD api rejected search")}, nil
	}

	results, _ := body["results"].([]any)
	entries := make([]api.DupeEntry, 0, len(results))
	for _, raw := range results {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		entry := api.DupeEntry{
			Name: stringFromAny(item["name"]),
			Link: stringFromAny(item["url"]),
		}
		size := intFromAny(item["size"])
		if size > 0 {
			entry.SizeKnown = true
			entry.SizeBytes = size
		}
		if intFromAny(item["dv"]) == 1 {
			entry.Flags = append(entry.Flags, "DV")
		}
		if intFromAny(item["hdr10"]) == 1 || intFromAny(item["hdr10+"]) == 1 {
			entry.Flags = append(entry.Flags, "HDR")
		}
		entries = append(entries, entry)
	}
	return entries, nil, nil
}
