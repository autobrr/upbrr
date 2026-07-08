// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/bhdmeta"
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
	tmdbID := resolveSearchTMDBID(meta)
	imdbID := imdbForLookup(meta)
	if tmdbID == 0 && imdbID == "" {
		return nil, []string{noteSkip("missing tmdb/imdb id for BHD dupe search")}, nil
	}
	category := "Movies"
	tmdbPrefix := "movie"
	if strings.EqualFold(resolveSearchCategory(meta), "TV") {
		category = "TV"
		tmdbPrefix = "tv"
	}
	payload := map[string]any{
		"action":     "search",
		"categories": category,
	}
	if searchType, ok := bhdmeta.SearchType(meta); ok {
		payload["types"] = searchType
	} else {
		payload["types"] = nil
	}
	if bhdmeta.IsSD(meta) {
		payload["categories"] = nil
		payload["types"] = nil
	}
	if tmdbID != 0 {
		payload["tmdb_id"] = tmdbPrefix + "/" + strconv.Itoa(tmdbID)
	} else {
		payload["imdb_id"] = imdbID
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
