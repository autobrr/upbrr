// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const fldDupeMaxResponseBytes = 4 << 20

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	logger   api.Logger
	endpoint string
}

func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		logger:   logger,
		endpoint: "https://flood.st/api/torrents",
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	view := *s
	view.logger = logging.FromContext(ctx, s.logger)
	s = &view

	apiKey := fldAPIKey(s.cfg)
	if apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker FLD", nil)
	}

	tmdb := meta.Identity.TMDBID
	if tmdb <= 0 {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing tmdb id for FLD dupe search", nil)
	}

	category := strings.ToUpper(strings.TrimSpace(string(meta.Identity.Category)))
	params := url.Values{}
	if category == "TV" {
		params.Set("tmdb_id", fmt.Sprintf("tv/%d", tmdb))
		if meta.SeasonInt > 0 {
			params.Set("show_season_number", strconv.Itoa(meta.SeasonInt))
		}
		if meta.EpisodeInt > 0 {
			params.Set("show_episode_number", strconv.Itoa(meta.EpisodeInt))
		}
	} else {
		params.Set("tmdb_id", fmt.Sprintf("movie/%d", tmdb))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "build FLD duplicate request", err)
	}
	req.URL.RawQuery = params.Encode()
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "upbrr")
	req.Header.Set("Accept", "application/json")

	dupe.TraceSearchRequest(s.logger, "FLD", req.Method, "/api/torrents", map[string]any{
		"tmdb_id": params.Get("tmdb_id"),
		"season":  params.Get("show_season_number"),
		"episode": params.Get("show_episode_number"),
	})

	resp, err := s.http.Do(req)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "request FLD duplicate search", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return dupe.Failed(dupe.FailureResponseStatus, fmt.Sprintf("unexpected FLD duplicate response status %d", resp.StatusCode), nil)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, fldDupeMaxResponseBytes+1))
	if err != nil {
		return dupe.Failed(dupe.FailureResponseParse, "read FLD duplicate response", err)
	}
	if len(body) > fldDupeMaxResponseBytes {
		return dupe.Failed(dupe.FailureResponseParse, fmt.Sprintf("duplicate response exceeds %d bytes", fldDupeMaxResponseBytes), nil)
	}

	var result struct {
		Items []struct {
			ID          any    `json:"id"`
			Name        string `json:"name"`
			MediaType   string `json:"media_type"`
			MainURL     string `json:"main_url"`
			DownloadURL string `json:"download_url"`
			Size        int64  `json:"size"`
			Files       []struct {
				Name string `json:"name"`
			} `json:"files"`
		} `json:"items"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return dupe.Failed(dupe.FailureResponseParse, "decode FLD duplicate response", err)
	}

	entries := make([]api.DupeEntry, 0, len(result.Items))
	for _, item := range result.Items {
		files := make([]string, 0, len(item.Files))
		for _, f := range item.Files {
			files = append(files, f.Name)
		}
		id := strings.TrimSpace(fmt.Sprint(item.ID))
		entry := api.DupeEntry{
			ID:        id,
			Name:      item.Name,
			Type:      item.MediaType,
			Link:      item.MainURL,
			SizeKnown: item.Size > 0,
			SizeBytes: item.Size,
			FileCount: len(item.Files),
			Files:     files,
		}
		if item.DownloadURL != "" {
			entry.Attributes = map[string]string{
				"download_url": item.DownloadURL,
			}
		}
		entries = append(entries, entry)
	}

	return dupe.ResolvedWithSearch(entries, nil, dupe.SearchEvidence{
		Complete:  true,
		WorkScope: dupe.WorkScopeProviderID,
		Pages:     1,
		Scope:     "tmdb_id",
	})
}

func fldAPIKey(cfg config.Config) string {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "FLD") {
			return strings.TrimSpace(entry.APIKey)
		}
	}
	return ""
}
