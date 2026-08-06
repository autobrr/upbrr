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
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	arBrowseEndpoint = "https://alpharatio.cc/ajax.php"
)

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	logger   api.Logger
	maxPages int
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		logger:   logger,
		maxPages: deps.MaxPages(100),
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

	if h.logger != nil {
		h.logger.Debugf(
			"dupechecking: AR search request method=GET action=browse searchstr=%q",
			query,
		)
	}

	maxPages := h.maxPages
	if maxPages <= 0 {
		maxPages = 100
	}
	entries := make([]api.DupeEntry, 0)
	seenIDs := make(map[int64]struct{})
	seenPages := make(map[string]struct{})
	expectedPages := -1
	pages := 0
	complete := false
	warning := ""
	for requestedPage := 1; pages < maxPages; requestedPage++ {
		payload, failureCode, fetchErr := h.fetchARPage(ctx, query, cookies, requestedPage)
		if failureCode != "" {
			if pages == 0 {
				return dupe.Failed(failureCode, "AR search failed", fetchErr)
			}
			warning = "AR search stopped after a partial request failure"
			break
		}
		pages++
		currentPage := payload.Response.CurrentPage
		totalPages := payload.Response.Pages
		results := payload.Response.Results
		if currentPage == 0 && totalPages == 0 && len(results) == 0 && requestedPage == 1 {
			complete = true
			break
		}
		if currentPage != requestedPage || totalPages < currentPage || totalPages <= 0 ||
			expectedPages >= 0 && totalPages != expectedPages {
			warning = "AR search pagination evidence is inconsistent"
			break
		}
		if expectedPages < 0 {
			expectedPages = totalPages
		}
		signature := arPageSignature(results)
		if signature != "" {
			if _, ok := seenPages[signature]; ok {
				warning = "AR search repeated a result page"
				break
			}
			seenPages[signature] = struct{}{}
		}
		for _, result := range results {
			if result.TorrentID <= 0 || strings.TrimSpace(result.GroupName) == "" {
				warning = "AR search result evidence is malformed"
				continue
			}
			if _, ok := seenIDs[result.TorrentID]; ok {
				continue
			}
			seenIDs[result.TorrentID] = struct{}{}
			if entry, ok := arDupeEntry(result); ok {
				entries = append(entries, entry)
			}
		}
		if warning != "" {
			break
		}
		if requestedPage == expectedPages {
			complete = true
			break
		}
	}
	if !complete && warning == "" {
		warning = "AR search reached page bound before consuming advertised pages"
	}
	warnings := []string(nil)
	if warning != "" {
		warnings = []string{warning}
	}
	search := dupe.SearchEvidence{
		Complete:  complete,
		WorkScope: dupe.WorkScopeTitle,
		Pages:     pages,
		Scope:     "title_year",
		Warnings:  warnings,
	}
	if h.logger != nil {
		h.logger.Debugf(
			"dupechecking: AR search response pages=%d advertised_pages=%d accepted_results=%d complete=%t decision=completed",
			pages,
			expectedPages,
			len(entries),
			complete,
		)
	}
	return dupe.ResolvedWithSearch(entries, warnings, search)
}

func (h dupeSearcher) fetchARPage(
	ctx context.Context,
	query string,
	cookies []*http.Cookie,
	page int,
) (arResponse, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, arBrowseEndpoint, nil)
	if err != nil {
		return arResponse{}, dupe.FailureRequest, fmt.Errorf("build AR request: %w", err)
	}
	params := req.URL.Query()
	params.Set("action", "browse")
	params.Set("searchstr", query)
	if page > 1 {
		params.Set("page", strconv.Itoa(page))
	}
	req.URL.RawQuery = params.Encode()
	req.Header.Set("User-Agent", "upbrr")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return arResponse{}, dupe.FailureRequest, fmt.Errorf("request AR search page %d: %w", page, err)
	}
	contentType := arResponseContentType(resp)
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if h.logger != nil {
			h.logger.Debugf(
				"dupechecking: AR search response page=%d status_code=%d content_type=%q decision=reject_non_success",
				page,
				resp.StatusCode,
				contentType,
			)
		}
		return arResponse{}, dupe.FailureResponseStatus, nil
	}
	var payload arResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		if h.logger != nil {
			h.logger.Debugf(
				"dupechecking: AR search response page=%d status_code=%d content_type=%q decision=decode_failed error=%v",
				page,
				resp.StatusCode,
				contentType,
				redaction.RedactValue(err.Error(), nil),
			)
		}
		return arResponse{}, dupe.FailureResponseParse, fmt.Errorf("decode AR search page %d: %w", page, err)
	}
	if !strings.EqualFold(strings.TrimSpace(payload.Status), "success") {
		return arResponse{}, dupe.FailureResponseStatus, nil
	}
	return payload, "", nil
}

type arResult struct {
	GroupName string `json:"groupName"`
	Size      int64  `json:"size"`
	FileCount int    `json:"fileCount"`
	GroupID   int64  `json:"groupId"`
	TorrentID int64  `json:"torrentId"`
}

func arDupeEntry(result arResult) (api.DupeEntry, bool) {
	name := strings.TrimSpace(result.GroupName)
	if name == "" || result.TorrentID <= 0 {
		return api.DupeEntry{}, false
	}
	entry := api.DupeEntry{
		Name: name,
		ID:   strconv.FormatInt(result.TorrentID, 10),
		Link: "https://alpharatio.cc/torrents.php?id=" + strconv.FormatInt(result.GroupID, 10) + "&torrentid=" +
			strconv.FormatInt(result.TorrentID, 10),
		Download: "https://alpharatio.cc/torrents.php?action=download&id=" + strconv.FormatInt(result.TorrentID, 10),
	}
	if result.FileCount > 0 {
		entry.FileCount = result.FileCount
	}
	if result.Size > 0 {
		entry.SizeKnown = true
		entry.SizeBytes = result.Size
	}
	return entry, true
}

func arPageSignature(results []arResult) string {
	values := make([]string, 0, len(results))
	for _, result := range results {
		if result.TorrentID > 0 {
			values = append(values, strconv.FormatInt(result.TorrentID, 10))
		}
	}
	return strings.Join(values, ",")
}

func arResponseContentType(resp *http.Response) string {
	if resp == nil {
		return "unknown"
	}
	contentType := strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0])
	if contentType == "" {
		return "unspecified"
	}
	if len(contentType) > 64 {
		return "other"
	}
	return strings.ToLower(contentType)
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
	query := resolveARSearchNameFields(meta.Release, meta.ReleaseName, meta.ProviderMetadata)
	if query == "" && meta.Projection != nil {
		query = dupe.ProjectedSearchName(meta)
	}
	return query
}

type arResponse struct {
	Status   string `json:"status"`
	Response struct {
		CurrentPage int        `json:"currentPage"`
		Pages       int        `json:"pages"`
		Results     []arResult `json:"results"`
	} `json:"response"`
}
