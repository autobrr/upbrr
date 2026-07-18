// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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

// newDuplicateAdapterAt returns a duplicate-search adapter bound to one
// immutable dependency set and base URL.
func newDuplicateAdapterAt(deps dupe.Dependencies, baseURL string) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		endpoint: strings.TrimRight(baseURL, "/") + ptpTorrentPath,
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	apiUser, apiKey := ptpAPIKeys(s.cfg)
	if apiUser == "" || apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing ApiUser/ApiKey for tracker", nil)
	}
	if meta.Identity.IMDBID == 0 {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing imdb id for PTP dupe search", nil)
	}
	headers := map[string]string{"ApiUser": apiUser, "ApiKey": apiKey}
	groupPayload, err := s.get(ctx, url.Values{"imdb": {"tt" + strconv.Itoa(meta.Identity.IMDBID)}}, headers)
	if err != nil || len(groupPayload) == 0 {
		return dupe.Failed(dupe.FailureRequest, "PTP group search failed", err)
	}
	groupID := ptpGroupID(groupPayload)
	if groupID == "" {
		return dupe.Resolved(nil, nil)
	}
	payload, err := s.get(ctx, url.Values{"id": {groupID}}, headers)
	if err != nil || len(payload) == 0 {
		return dupe.Failed(dupe.FailureRequest, "PTP torrent search failed", err)
	}
	return dupe.Resolved(ptpDupeEntries(payload, meta.Release.Resolution, strings.TrimSuffix(s.endpoint, ptpTorrentPath)), nil)
}

func (s *dupeSearcher) get(ctx context.Context, params url.Values, headers map[string]string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("ptp dupe request: %w", err)
	}
	req.URL.RawQuery = params.Encode()
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ptp dupe request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, strconv.ErrSyntax
	}
	var payload map[string]any
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("ptp dupe decode: %w", err)
	}
	return payload, nil
}

func ptpAPIKeys(cfg config.Config) (string, string) {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "PTP") {
			return strings.TrimSpace(entry.PTPAPIUser), strings.TrimSpace(entry.PTPAPIKey)
		}
	}
	return "", ""
}

func ptpGroupID(payload map[string]any) string {
	if movies, ok := payload["Movies"].([]any); ok && len(movies) > 0 {
		if movie, ok := movies[0].(map[string]any); ok {
			return ptpString(movie["GroupId"])
		}
	}
	return ptpString(payload["GroupId"])
}

func ptpDupeEntries(payload map[string]any, resolution string, baseURL string) []api.DupeEntry {
	quality := "High Definition"
	res := strings.ToLower(strings.TrimSpace(resolution))
	if res == "480p" || res == "480i" || res == "576p" || res == "576i" || res == "sd" {
		quality = "Standard Definition"
	} else if strings.Contains(res, "2160") || strings.Contains(res, "4320") || strings.Contains(res, "8640") {
		quality = "Ultra High Definition"
	}
	torrents, _ := payload["Torrents"].([]any)
	entries := make([]api.DupeEntry, 0, len(torrents))
	for _, raw := range torrents {
		item, ok := raw.(map[string]any)
		if !ok || ptpString(item["Quality"]) != "" && !strings.EqualFold(ptpString(item["Quality"]), quality) {
			continue
		}
		id := ptpString(item["Id"])
		entries = append(entries, api.DupeEntry{
			Name: strings.TrimSpace("[" + ptpString(item["Resolution"]) + "] " + ptpString(item["ReleaseName"])),
			ID:   id,
			Link: strings.TrimRight(baseURL, "/") + ptpTorrentPath + "?torrentid=" + id,
		})
	}
	return entries
}

func ptpString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}
