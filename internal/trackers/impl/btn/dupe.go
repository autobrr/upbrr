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
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	endpoint string
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func (d *Definition) NewDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		endpoint: "https://api.broadcasthe.net/",
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	token := strings.TrimSpace(config.ResolveBTNAPIToken(s.cfg))
	if token == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}
	if !isTV(meta) {
		return dupe.NotRun(dupe.NotRunUnsupportedContent, "BTN only supports TV dupe search", nil)
	}
	filter := make(map[string]any)
	switch {
	case trackerID(meta) != "":
		filter["id"] = trackerID(meta)
	case meta.Identity.IMDBID != 0:
		filter["imdb"] = fmt.Sprintf("tt%07d", meta.Identity.IMDBID)
	case meta.Identity.TVDBID != 0:
		filter["tvdb"] = meta.Identity.TVDBID
	case searchTitle(meta) != "":
		filter["searchstr"] = searchTitle(meta)
	default:
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing btn/imdb/tvdb id and title for BTN dupe search", nil)
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "upbrr-btn-search",
		"method":  "getTorrentsSearch",
		"params":  []any{token, filter, 50},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return dupe.Failed(dupe.FailureInternal, "BTN request failed", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(raw))
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "BTN request failed", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "BTN request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return dupe.Failed(dupe.FailureResponseStatus, "BTN search failed", nil)
	}
	var response map[string]any
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&response); err != nil {
		return dupe.Failed(dupe.FailureResponseParse, "BTN search failed", err)
	}
	if errorPayload, ok := response["error"].(map[string]any); ok && len(errorPayload) > 0 {
		return dupe.Failed(dupe.FailureResponseStatus, "BTN api rejected search", nil)
	}
	result, _ := response["result"].(map[string]any)
	torrents, _ := result["torrents"].(map[string]any)
	entries := make([]api.DupeEntry, 0, len(torrents))
	for id, rawTorrent := range torrents {
		torrent, ok := rawTorrent.(map[string]any)
		if !ok {
			continue
		}
		entry := api.DupeEntry{
			Name: releaseName(id, torrent),
			ID:   strings.TrimSpace(id),
			Link: torrentLink(id, torrent),
			Res:  btnString(first(torrent, "Resolution", "resolution")),
			Type: btnString(first(torrent, "Source", "source", "Type", "type")),
		}
		if size := btnInt(first(torrent, "Size", "size")); size > 0 {
			entry.SizeKnown, entry.SizeBytes = true, size
		}
		entry.Flags = flags(torrent)
		entries = append(entries, entry)
	}
	return dupe.Resolved(entries, nil)
}

func isTV(meta api.DuplicateSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}

func trackerID(meta api.DuplicateSubject) string {
	for key, value := range meta.TrackerIDs {
		if strings.EqualFold(strings.TrimSpace(key), "BTN") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func searchTitle(meta api.DuplicateSubject) string {
	candidates := []string{strings.TrimSpace(meta.Release.Title)}
	if meta.ProviderMetadata.TVDB != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TVDB.Name), strings.TrimSpace(meta.ProviderMetadata.TVDB.NameEnglish))
	}
	if meta.ProviderMetadata.TVmaze != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TVmaze.Name))
	}
	candidates = append(candidates, strings.TrimSpace(meta.Filename), strings.TrimSpace(meta.ReleaseName))
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func releaseName(id string, torrent map[string]any) string {
	for _, candidate := range []string{btnString(first(torrent, "ReleaseName", "releaseName")), btnString(first(torrent, "SceneName", "Name", "name")), btnString(first(torrent, "Series", "series")), strings.TrimSpace(id)} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func torrentLink(id string, torrent map[string]any) string {
	groupID := btnString(first(torrent, "GroupID", "groupId"))
	if groupID == "" || strings.TrimSpace(id) == "" {
		return ""
	}
	return "https://broadcasthe.net/torrents.php?id=" + groupID + "&torrentid=" + strings.TrimSpace(id)
}

func flags(torrent map[string]any) []string {
	out := make([]string, 0, 2)
	for _, value := range []string{btnString(first(torrent, "HDR", "hdr")), btnString(first(torrent, "DolbyVision", "dolbyVision", "DV", "dv"))} {
		upper := strings.ToUpper(strings.TrimSpace(value))
		switch upper {
		case "", "0", "FALSE", "NO", "1", "TRUE", "YES":
			continue
		}
		out = append(out, upper)
	}
	return out
}

func first(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func btnString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
