// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	arBrowseEndpoint = "https://alpharatio.cc/ajax.php"
)

type dupeSearcher struct {
	cfg    config.Config
	http   *http.Client
	logger api.Logger
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:    cfg,
		http:   httpClient,
		logger: logger,
	}
}

func (h dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	if h.http == nil {
		return dupe.Failed(dupe.FailureInternal, "AR handler misconfigured: no HTTP client", nil)
	}

	query := arSearchQuery(meta)
	if query == "" {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing title for AR dupe search", nil)
	}

	cookies, cookiePath, err := h.resolveCookies(ctx)
	if err != nil || len(cookies) == 0 {
		if err != nil && h.logger != nil {
			h.logger.Debugf("dupechecking: AR cookie resolution failed: %v", err)
		}
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing valid AR cookies", nil)
	}
	if h.logger != nil && cookiePath != "" {
		h.logger.Debugf("dupechecking: AR using stored cookies from %s", cookiePath)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, arBrowseEndpoint, nil)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "AR request failed", fmt.Errorf("build AR request: %w", err))
	}
	params := req.URL.Query()
	params.Set("action", "browse")
	params.Set("searchstr", query)
	req.URL.RawQuery = params.Encode()
	req.Header.Set("User-Agent", "upbrr")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	resp, err := h.http.Do(req)
	if err != nil {
		if h.logger != nil {
			h.logger.Debugf("dupechecking: AR request failed: %v", err)
		}
		return dupe.Failed(dupe.FailureRequest, "AR request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if h.logger != nil {
			h.logger.Debugf("dupechecking: AR returned status %d", resp.StatusCode)
		}
		return dupe.Failed(dupe.FailureResponseStatus, "AR search returned non-success status", nil)
	}

	var payload arResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		if h.logger != nil {
			h.logger.Debugf("dupechecking: AR response decode failed: %v", err)
		}
		return dupe.Failed(dupe.FailureResponseParse, "AR response decode failed", err)
	}
	if !strings.EqualFold(strings.TrimSpace(payload.Status), "success") {
		return dupe.Failed(dupe.FailureResponseStatus, "AR API returned non-success status", nil)
	}

	entries := make([]api.DupeEntry, 0, len(payload.Response.Results))
	for _, result := range payload.Response.Results {
		name := strings.TrimSpace(result.GroupName)
		if name == "" {
			continue
		}
		entry := api.DupeEntry{
			Name:      name,
			Files:     []string{name},
			FileCount: result.FileCount,
			ID:        strconv.FormatInt(result.TorrentID, 10),
			Link: "https://alpharatio.cc/torrents.php?id=" + strconv.FormatInt(
				result.GroupID,
				10,
			) + "&torrentid=" + strconv.FormatInt(
				result.TorrentID,
				10,
			),
			Download: "https://alpharatio.cc/torrents.php?action=download&id=" + strconv.FormatInt(result.TorrentID, 10),
		}
		if result.Size > 0 {
			entry.SizeKnown = true
			entry.SizeBytes = result.Size
		}
		entries = append(entries, entry)
	}

	return dupe.Resolved(entries, nil)
}

func (h dupeSearcher) resolveCookies(ctx context.Context) ([]*http.Cookie, string, error) {
	arURL, _ := url.Parse("https://alpharatio.cc/")
	merged := map[string]*http.Cookie{}

	if h.http != nil && h.http.Jar != nil && arURL != nil {
		for _, cookie := range h.http.Jar.Cookies(arURL) {
			if cookie == nil || strings.TrimSpace(cookie.Name) == "" {
				continue
			}
			merged[cookie.Name] = cookie
		}
	}
	if len(merged) > 0 {
		if h.logger != nil {
			h.logger.Debugf("dupechecking: AR using %d cookies from HTTP client jar", len(merged))
		}
		return mapCookiesToSlice(merged), "", nil
	}

	loaded, err := cookiepkg.LoadTrackerHTTPCookies(ctx, h.cfg.MainSettings.DBPath, "AR", "alpharatio.cc")
	if err != nil {
		return nil, "", fmt.Errorf("dupechecking: %w", err)
	}
	for _, cookie := range loaded {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" {
			continue
		}
		merged[cookie.Name] = cookie
	}
	if len(merged) == 0 {
		return nil, "", errors.New("no valid cookies found")
	}
	return mapCookiesToSlice(merged), "shared store", nil
}

func mapCookiesToSlice(values map[string]*http.Cookie) []*http.Cookie {
	if len(values) == 0 {
		return nil
	}
	out := make([]*http.Cookie, 0, len(values))
	for _, cookie := range values {
		out = append(out, cookie)
	}
	return out
}

func arSearchQuery(meta api.DuplicateSubject) string {
	title := strings.TrimSpace(meta.Release.Title)
	if title == "" && meta.ProviderMetadata.TMDB != nil {
		title = strings.TrimSpace(meta.ProviderMetadata.TMDB.Title)
	}
	if title == "" && meta.ProviderMetadata.IMDB != nil {
		title = strings.TrimSpace(meta.ProviderMetadata.IMDB.Title)
	}
	if title == "" {
		title = strings.TrimSpace(meta.ReleaseName)
	}
	if title == "" {
		return ""
	}

	year := meta.Release.Year
	if year == 0 && meta.ProviderMetadata.TMDB != nil && meta.ProviderMetadata.TMDB.Year > 0 {
		year = meta.ProviderMetadata.TMDB.Year
	}
	if year == 0 && meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Year > 0 {
		year = meta.ProviderMetadata.IMDB.Year
	}
	if year > 0 {
		return strings.TrimSpace(title + " " + strconv.Itoa(year))
	}
	return title
}

type arResponse struct {
	Status   string `json:"status"`
	Response struct {
		Results []struct {
			GroupName string `json:"groupName"`
			Size      int64  `json:"size"`
			FileCount int    `json:"fileCount"`
			GroupID   int64  `json:"groupId"`
			TorrentID int64  `json:"torrentId"`
		} `json:"results"`
	} `json:"response"`
}
