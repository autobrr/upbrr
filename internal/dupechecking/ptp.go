// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type ptpHandler struct {
	cfg    config.Config
	http   *http.Client
	logger api.Logger
}

func (h ptpHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	cfg, ok := trackerCfg(h.cfg, "PTP")
	apiUser := strings.TrimSpace(cfg.PTPAPIUser)
	apiKey := strings.TrimSpace(cfg.PTPAPIKey)
	if !ok || apiUser == "" || apiKey == "" {
		return nil, []string{noteSkip("missing ApiUser/ApiKey for tracker")}, nil
	}
	if meta.ExternalIDs.IMDBID == 0 {
		return nil, []string{noteSkip("missing imdb id for PTP dupe search")}, nil
	}
	headers := map[string]string{"ApiUser": apiUser, "ApiKey": apiKey}
	groupParams := url.Values{}
	groupParams.Set("imdb", "tt"+strconv.Itoa(meta.ExternalIDs.IMDBID))
	_, groupPayload, err := doJSONGet(ctx, h.http, "https://passthepopcorn.me/torrents.php", groupParams, headers)
	if err != nil || len(groupPayload) == 0 {
		return nil, []string{noteSkip("PTP group search failed")}, nil
	}
	groupID := ""
	if movies, ok := groupPayload["Movies"].([]any); ok && len(movies) > 0 {
		if movie, ok := movies[0].(map[string]any); ok {
			groupID = stringFromAny(movie["GroupId"])
		}
	}
	if groupID == "" {
		if id := stringFromAny(groupPayload["GroupId"]); id != "" {
			groupID = id
		}
	}
	if groupID == "" {
		return nil, nil, nil
	}

	params := url.Values{}
	params.Set("id", groupID)
	_, payload, err := doJSONGet(ctx, h.http, "https://passthepopcorn.me/torrents.php", params, headers)
	if err != nil || len(payload) == 0 {
		return nil, []string{noteSkip("PTP torrent search failed")}, nil
	}

	quality := "High Definition"
	res := strings.ToLower(strings.TrimSpace(meta.Release.Resolution))
	if isSDResolution(res) {
		quality = "Standard Definition"
	}
	if strings.Contains(res, "2160") || strings.Contains(res, "4320") || strings.Contains(res, "8640") {
		quality = "Ultra High Definition"
	}

	torrents, _ := payload["Torrents"].([]any)
	entries := make([]api.DupeEntry, 0, len(torrents))
	for _, raw := range torrents {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if q := stringFromAny(item["Quality"]); q != "" && !strings.EqualFold(q, quality) {
			continue
		}
		id := stringFromAny(item["Id"])
		name := "[" + stringFromAny(item["Resolution"]) + "] " + stringFromAny(item["ReleaseName"])
		entries = append(entries, api.DupeEntry{
			Name: strings.TrimSpace(name),
			ID:   id,
			Link: "https://passthepopcorn.me/torrents.php?torrentid=" + id,
		})
	}
	return entries, nil, nil
}
