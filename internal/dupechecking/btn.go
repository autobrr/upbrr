// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

const btnRPCURL = "https://api.broadcasthe.net/"

type btnHandler struct {
	cfg  config.Config
	http *http.Client
}

func (h btnHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	apiToken := strings.TrimSpace(config.ResolveBTNAPIToken(h.cfg))
	if apiToken == "" {
		return nil, []string{noteSkip("missing api_key for tracker")}, nil
	}
	if !isBTNTVMeta(meta) {
		return nil, []string{noteSkip("BTN only supports TV dupe search")}, nil
	}

	filter := make(map[string]any)
	switch {
	case btnTrackerID(meta) != "":
		filter["id"] = btnTrackerID(meta)
	case meta.ExternalIDs.IMDBID != 0:
		filter["imdb"] = formatBTNIMDbID(meta.ExternalIDs.IMDBID)
	case meta.ExternalIDs.TVDBID != 0:
		filter["tvdb"] = meta.ExternalIDs.TVDBID
	case btnSearchTitle(meta) != "":
		filter["searchstr"] = btnSearchTitle(meta)
	default:
		return nil, []string{noteSkip("missing btn/imdb/tvdb id and title for BTN dupe search")}, nil
	}

	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "upbrr-btn-search",
		"method":  "getTorrentsSearch",
		"params":  []any{apiToken, filter, 50},
	}
	status, body, err := doJSONPostAny(ctx, h.http, btnRPCURL, payload, nil)
	if err != nil {
		return nil, []string{noteSkip("BTN request failed")}, nil
	}
	if status < 200 || status >= 300 || body == nil {
		return nil, []string{noteSkip("BTN search failed")}, nil
	}

	response, ok := body.(map[string]any)
	if !ok {
		return nil, []string{noteSkip("BTN search failed")}, nil
	}
	if errPayload, ok := response["error"].(map[string]any); ok && len(errPayload) > 0 {
		return nil, nil, nil
	}

	result, _ := response["result"].(map[string]any)
	if len(result) == 0 {
		return nil, nil, nil
	}
	torrents, _ := result["torrents"].(map[string]any)
	entries := make([]api.DupeEntry, 0, len(torrents))
	for torrentID, raw := range torrents {
		torrentData, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		entry := api.DupeEntry{
			Name: btnReleaseName(torrentID, torrentData),
			ID:   strings.TrimSpace(torrentID),
			Link: btnTorrentLink(torrentID, torrentData),
			Res:  btnResolution(torrentData),
			Type: btnType(torrentData),
		}
		size := intFromAny(firstValue(torrentData, "Size", "size"))
		if size > 0 {
			entry.SizeKnown = true
			entry.SizeBytes = size
		}
		entry.Flags = btnFlags(torrentData)
		entries = append(entries, entry)
	}
	return entries, nil, nil
}

func isBTNTVMeta(meta api.PreparedMetadata) bool {
	category := strings.ToUpper(strings.TrimSpace(meta.ExternalIDs.Category))
	if category == "" {
		category = strings.ToUpper(strings.TrimSpace(meta.MediaInfoCategory))
	}
	return category == "TV"
}

func btnTrackerID(meta api.PreparedMetadata) string {
	if len(meta.TrackerIDs) == 0 {
		return ""
	}
	for key, value := range meta.TrackerIDs {
		if strings.EqualFold(strings.TrimSpace(key), "BTN") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func formatBTNIMDbID(imdbID int) string {
	if imdbID <= 0 {
		return ""
	}
	return "tt" + leftPad(imdbID, 7)
}

func btnSearchTitle(meta api.PreparedMetadata) string {
	candidates := []string{strings.TrimSpace(meta.Release.Title)}
	if meta.ExternalMetadata.TVDB != nil {
		candidates = append(candidates,
			strings.TrimSpace(meta.ExternalMetadata.TVDB.Name),
			strings.TrimSpace(meta.ExternalMetadata.TVDB.NameEnglish),
		)
	}
	if meta.ExternalMetadata.TVmaze != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ExternalMetadata.TVmaze.Name))
	}
	candidates = append(candidates,
		strings.TrimSpace(meta.Filename),
		strings.TrimSpace(meta.ReleaseName),
	)
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func btnReleaseName(torrentID string, torrentData map[string]any) string {
	candidates := []string{
		stringFromAny(firstValue(torrentData, "ReleaseName", "releaseName")),
		stringFromAny(firstValue(torrentData, "SceneName", "Name", "name")),
		stringFromAny(firstValue(torrentData, "Series", "series")),
		strings.TrimSpace(torrentID),
	}
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func btnTorrentLink(torrentID string, torrentData map[string]any) string {
	groupID := stringFromAny(firstValue(torrentData, "GroupID", "groupId"))
	if groupID == "" || strings.TrimSpace(torrentID) == "" {
		return ""
	}
	return "https://broadcasthe.net/torrents.php?id=" + groupID + "&torrentid=" + strings.TrimSpace(torrentID)
}

func btnResolution(torrentData map[string]any) string {
	return stringFromAny(firstValue(torrentData, "Resolution", "resolution"))
}

func btnType(torrentData map[string]any) string {
	return stringFromAny(firstValue(torrentData, "Source", "source", "Type", "type"))
}

func btnFlags(torrentData map[string]any) []string {
	flags := make([]string, 0, 2)
	for _, value := range []string{
		stringFromAny(firstValue(torrentData, "HDR", "hdr")),
		stringFromAny(firstValue(torrentData, "DolbyVision", "dolbyVision", "DV", "dv")),
	} {
		upper := strings.ToUpper(strings.TrimSpace(value))
		switch upper {
		case "", "0", "FALSE", "NO":
			continue
		case "1", "TRUE", "YES":
			continue
		}
		flags = append(flags, upper)
	}
	return flags
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}
