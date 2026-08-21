// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const dcDupeMaxResponseBytes = 4 << 20
const dcDupePageSize = 100
const dcDupeMaxPages = 100

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	endpoint string
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
		endpoint: "https://digitalcore.club/api/v1/torrents/dupe-search",
		maxPages: deps.MaxPages(dcDupeMaxPages),
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	apiKey := dcAPIKey(s.cfg)
	if apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}
	params := url.Values{"limit": {strconv.Itoa(dcDupePageSize)}}
	workScope := dupe.WorkScopeProviderID
	if meta.Identity.IMDBID != 0 {
		params.Set("imdb", providerid.IMDb(meta.Identity.IMDBID).Prefixed())
	} else {
		workScope = dupe.WorkScopeTitle
	}
	if name := dcSearchName(meta); name != "" {
		params.Set("releaseName", name)
	}
	if meta.Identity.IMDBID == 0 && params.Get("releaseName") == "" {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing imdb id or release name for DC dupe search", nil)
	}

	maxPages := s.maxPages
	if maxPages <= 0 {
		maxPages = dcDupeMaxPages
	}
	entries := make([]api.DupeEntry, 0)
	complete := false
	pages := 0
	index := 0
	expectedTotal := -1
	pendingCoverageOK := true
	for pages < maxPages {
		pageParams := cloneDCValues(params)
		pageParams.Set("index", strconv.Itoa(index))
		page, failureCode, err := s.searchPage(ctx, apiKey, pageParams)
		if failureCode != "" {
			return dupe.Failed(failureCode, "DC search failed", err)
		}
		pages++
		if !validDCPage(page, index, dcDupePageSize, expectedTotal) {
			if page.IncludesPending == nil || !*page.IncludesPending {
				pendingCoverageOK = false
			}
			break
		}
		entries = append(entries, dcDupeEntries(page.Results)...)
		if expectedTotal < 0 {
			expectedTotal = *page.Total
		}
		nextIndex := *page.Index + *page.Count
		switch {
		case expectedTotal == 0 && *page.Count == 0:
			complete = true
		case nextIndex >= expectedTotal:
			complete = true
		case *page.Count > 0 && nextIndex > index:
			index = nextIndex
			continue
		}
		break
	}

	warnings := []string(nil)
	if !pendingCoverageOK {
		warnings = append(warnings, "DC search did not confirm pending/modqueue coverage")
	}
	if !complete {
		warnings = append(warnings, "DC search reached a pagination bound or returned incomplete pagination metadata")
	}
	return dupe.ResolvedWithSearch(entries, warnings, dupe.SearchEvidence{
		Complete:  complete,
		WorkScope: workScope,
		Pages:     pages,
		Scope:     "dupe_preflight",
		Warnings:  warnings,
	})
}

func (s *dupeSearcher) searchPage(
	ctx context.Context,
	apiKey string,
	params url.Values,
) (dcDupePage, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		return dcDupePage{}, dupe.FailureRequest, fmt.Errorf("build DC duplicate request: %w", err)
	}
	req.URL.RawQuery = params.Encode()
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return dcDupePage{}, dupe.FailureRequest, fmt.Errorf("request DC duplicate search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return dcDupePage{}, dupe.FailureResponseStatus, fmt.Errorf("unexpected DC duplicate response status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, dcDupeMaxResponseBytes+1))
	if err != nil {
		return dcDupePage{}, dupe.FailureResponseParse, fmt.Errorf("read DC duplicate response: %w", err)
	}
	if len(body) > dcDupeMaxResponseBytes {
		return dcDupePage{}, dupe.FailureResponseParse, fmt.Errorf("duplicate response exceeds %d bytes", dcDupeMaxResponseBytes)
	}

	page, err := dcParseDupePage(body)
	if err != nil {
		return dcDupePage{}, dupe.FailureResponseParse, err
	}
	return page, "", nil
}

func dcAPIKey(cfg config.Config) string {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "DC") {
			return strings.TrimSpace(entry.APIKey)
		}
	}
	return ""
}

func dcSearchName(meta api.DuplicateSubject) string {
	values := []string{
		dupe.ProjectedSearchName(meta),
		meta.ReleaseName,
		meta.SceneName,
		meta.Filename,
	}
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type dcDupePage struct {
	Results         []map[string]any `json:"results"`
	Index           *int             `json:"index"`
	Limit           *int             `json:"limit"`
	Count           *int             `json:"count"`
	Total           *int             `json:"total"`
	IncludesPending *bool            `json:"includesPending"`
}

func dcParseDupePage(body []byte) (dcDupePage, error) {
	var payload dcDupePage
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return dcDupePage{}, fmt.Errorf("decode DC duplicate response: %w", err)
	}
	return payload, nil
}

func dcDupeEntries(results []map[string]any) []api.DupeEntry {
	entries := make([]api.DupeEntry, 0, len(results))
	for _, item := range results {
		entry := dcDupeEntry(item)
		if entry.Name != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func validDCPage(page dcDupePage, requestedIndex int, requestedLimit int, expectedTotal int) bool {
	if page.Index == nil || page.Limit == nil || page.Count == nil || page.Total == nil || page.IncludesPending == nil {
		return false
	}
	if !*page.IncludesPending || *page.Index != requestedIndex || *page.Limit != requestedLimit {
		return false
	}
	if *page.Count < 0 || *page.Total < 0 || *page.Count != len(page.Results) {
		return false
	}
	if *page.Index+*page.Count > *page.Total {
		return false
	}
	if expectedTotal >= 0 && *page.Total != expectedTotal {
		return false
	}
	return true
}

func cloneDCValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, entries := range values {
		cloned[key] = append([]string(nil), entries...)
	}
	return cloned
}

func dcDupeEntry(item map[string]any) api.DupeEntry {
	id := dcScalarString(item["id"])
	category := dcScalarString(item["categoryName"])
	entry := api.DupeEntry{
		ID:            id,
		Name:          dcScalarString(item["name"]),
		Category:      category,
		Type:          dcMediaType(dcScalarString(item["type"])),
		Res:           dcResolutionFromCategory(category),
		Source:        dcSourceFromCategory(category),
		FileCount:     int(dcScalarInt64(item["numfiles"])),
		Pack:          dcScalarBool(item["pack"]) || strings.Contains(strings.ToUpper(category), "PACK"),
		ThreeD:        dcThreeDValue(item),
		ReleaseOrigin: dcReleaseOrigin(item),
		Attributes: map[string]string{
			"approved": strconv.FormatBool(dcScalarBool(item["approved"])),
			"pending":  strconv.FormatBool(dcScalarBool(item["pending"])),
			"status":   dcScalarString(item["status"]),
		},
	}
	if id != "" {
		entry.Link = "https://digitalcore.club/torrent/" + id + "/"
	}
	if size := dcScalarInt64(item["size"]); size > 0 {
		entry.SizeKnown, entry.SizeBytes = true, size
	}
	return entry
}

func dcMediaType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "single", "multi":
		return ""
	default:
		return strings.TrimSpace(value)
	}
}

func dcThreeDValue(item map[string]any) string {
	if dcScalarBool(item["3d"]) || dcScalarBool(item["threeD"]) {
		return "3d"
	}
	return ""
}

func dcReleaseOrigin(item map[string]any) string {
	if dcScalarBool(item["p2p"]) {
		return "p2p"
	}
	if raw, ok := item["p2p"]; ok && raw != nil {
		return "scene"
	}
	return ""
}

func dcResolutionFromCategory(category string) string {
	upper := strings.ToUpper(category)
	switch {
	case strings.Contains(upper, "2160"), strings.Contains(upper, "4K"), strings.Contains(upper, "UHD"):
		return "2160p"
	case strings.Contains(upper, "1080"):
		return "1080p"
	case strings.Contains(upper, "720"):
		return "720p"
	case strings.Contains(upper, "SD"):
		return "sd"
	default:
		return ""
	}
}

func dcSourceFromCategory(category string) string {
	upper := strings.ToUpper(category)
	switch {
	case strings.Contains(upper, "BLURAY"), strings.Contains(upper, "BLU-RAY"):
		return "bluray"
	case strings.Contains(upper, "DVD"):
		return "dvd"
	default:
		return ""
	}
}

func dcScalarString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func dcScalarInt64(value any) int64 {
	switch typed := value.(type) {
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case float64:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func dcScalarBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case json.Number:
		return typed.String() != "0"
	case float64:
		return typed != 0
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed || strings.TrimSpace(typed) == "1"
	default:
		return false
	}
}
