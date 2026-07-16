// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"context"
	"encoding/json"
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

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func (Definition) NewDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		endpoint: "https://greatposterwall.com/api.php",
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	apiKey := gpwAPIKey(s.cfg)
	if apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}
	if meta.Identity.IMDBID == 0 {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing imdb id for GPW dupe search", nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "GPW search failed", err)
	}
	req.URL.RawQuery = url.Values{
		"api_key": {apiKey},
		"action":  {"torrent"},
		"imdbID":  {"tt" + strconv.Itoa(meta.Identity.IMDBID)},
	}.Encode()
	resp, err := s.http.Do(req)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "GPW search failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return dupe.Failed(dupe.FailureResponseStatus, "GPW search failed", nil)
	}
	var payload struct {
		Status   int              `json:"status"`
		Response []map[string]any `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return dupe.Failed(dupe.FailureResponseParse, "GPW search failed", err)
	}
	if payload.Status != http.StatusOK {
		return dupe.Failed(dupe.FailureResponseStatus, "GPW api rejected search", nil)
	}
	entries := make([]api.DupeEntry, 0, len(payload.Response))
	for _, item := range payload.Response {
		parts := []string{
			gpwString(item["Name"]),
			gpwString(item["Year"]),
			gpwString(item["Resolution"]),
			gpwString(item["Source"]),
			gpwString(item["Processing"]),
			gpwString(item["RemasterTitle"]),
			gpwString(item["Codec"]),
		}
		entries = append(entries, api.DupeEntry{Name: strings.Join(strings.Fields(strings.Join(parts, " ")), " ")})
	}
	return dupe.Resolved(entries, nil)
}

func gpwAPIKey(cfg config.Config) string {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "GPW") {
			return strings.TrimSpace(entry.APIKey)
		}
	}
	return ""
}

func gpwString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}
