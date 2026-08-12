// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	endpoint string
	maxPages int
}

// newDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		endpoint: "https://nebulance.io/api.php",
		maxPages: deps.MaxPages(100),
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	apiKey := nblAPIKey(s.cfg)
	if apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}
	params := url.Values{
		"action":   {"search"},
		"age":      {">0"},
		"api_key":  {apiKey},
		"per_page": {"100"},
	}
	workScope := dupe.WorkScopeProviderID
	switch {
	case meta.Identity.TVmazeID != 0:
		params.Set("tvmaze", strconv.Itoa(meta.Identity.TVmazeID))
	case meta.Identity.IMDBID != 0:
		params.Set("imdb", providerid.IMDb(meta.Identity.IMDBID).Prefixed())
	default:
		workScope = dupe.WorkScopeTitle
		searchName := strings.TrimSpace(meta.Release.Title)
		if searchName == "" {
			searchName = dupe.ProjectedSearchName(meta)
		}
		if searchName == "" {
			return dupe.NotRun(dupe.NotRunMissingMetadata, "missing tvmaze/imdb/title for NBL dupe search", nil)
		}
		params.Set("release", searchName)
	}

	maxPages := s.maxPages
	if maxPages <= 0 {
		maxPages = 100
	}
	entries := make([]api.DupeEntry, 0)
	pages := 0
	complete := false
	expectedTotalPages := -1
	expectedTotalResults := -1
	for requestedPage := 0; pages < maxPages; requestedPage++ {
		page, failureCode, err := s.searchPage(ctx, params, requestedPage)
		if failureCode != "" {
			return dupe.Failed(failureCode, "NBL search failed", err)
		}
		pages++
		if page.Items == nil {
			return dupe.Failed(dupe.FailureResponseParse, "NBL search failed", nil)
		}
		items := *page.Items
		if !validNBLPage(page, requestedPage, len(items), expectedTotalPages, expectedTotalResults) {
			break
		}
		if expectedTotalPages < 0 {
			expectedTotalPages = *page.TotalPages
			expectedTotalResults = *page.TotalResults
		}
		entries = append(entries, nblDupeEntries(items)...)
		switch {
		case expectedTotalPages == 0 && expectedTotalResults == 0:
			complete = true
		case requestedPage+1 >= expectedTotalPages && len(entries) == expectedTotalResults:
			complete = true
		case requestedPage+1 < expectedTotalPages && len(items) > 0:
			continue
		}
		break
	}

	warnings := []string(nil)
	if !complete {
		warnings = []string{"NBL search reached a pagination bound or returned incomplete pagination metadata"}
	}
	return dupe.ResolvedWithSearch(entries, warnings, dupe.SearchEvidence{
		Complete:  complete,
		WorkScope: workScope,
		Pages:     pages,
		Scope:     "work_identity",
		Warnings:  warnings,
	})
}

type nblSearchPage struct {
	CurrentPage  *int             `json:"current_page"`
	TotalPages   *int             `json:"total_pages"`
	Count        *int             `json:"count"`
	TotalResults *int             `json:"total_results"`
	Items        *[]nblSearchItem `json:"items"`
	Error        json.RawMessage  `json:"error"`
}

type nblSearchItem struct {
	Name     string    `json:"rls_name"`
	Category string    `json:"cat"`
	Download string    `json:"download"`
	Files    []string  `json:"file_list"`
	GroupID  int64     `json:"group_id"`
	Season   int       `json:"season"`
	Episode  int       `json:"episode"`
	Size     int64     `json:"size"`
	Tags     *[]string `json:"tags"`
}

func (s *dupeSearcher) searchPage(
	ctx context.Context,
	params url.Values,
	page int,
) (nblSearchPage, string, error) {
	pageParams := cloneNBLValues(params)
	if page > 0 {
		pageParams.Set("page", strconv.Itoa(page))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint+"?"+pageParams.Encode(), nil)
	if err != nil {
		return nblSearchPage{}, dupe.FailureRequest, fmt.Errorf("build NBL search request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nblSearchPage{}, dupe.FailureRequest, fmt.Errorf("execute NBL search request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nblSearchPage{}, dupe.FailureResponseStatus, nil
	}
	var payload nblSearchPage
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&payload); err != nil {
		return nblSearchPage{}, dupe.FailureResponseParse, fmt.Errorf("decode NBL search response: %w", err)
	}
	if len(payload.Error) > 0 && string(payload.Error) != "null" {
		return nblSearchPage{}, dupe.FailureResponseParse, nil
	}
	return payload, "", nil
}

func validNBLPage(
	page nblSearchPage,
	requestedPage int,
	itemCount int,
	expectedTotalPages int,
	expectedTotalResults int,
) bool {
	if page.CurrentPage == nil || page.TotalPages == nil || page.Count == nil || page.TotalResults == nil ||
		*page.CurrentPage != requestedPage || *page.Count != itemCount || *page.TotalPages < 0 || *page.TotalResults < 0 {
		return false
	}
	if expectedTotalPages >= 0 && (*page.TotalPages != expectedTotalPages || *page.TotalResults != expectedTotalResults) {
		return false
	}
	return (*page.TotalPages == 0 && *page.TotalResults == 0 && itemCount == 0) ||
		(*page.TotalPages > 0 && requestedPage < *page.TotalPages)
}

func nblDupeEntries(items []nblSearchItem) []api.DupeEntry {
	entries := make([]api.DupeEntry, 0, len(items))
	for _, item := range items {
		hdr, flags, tagsPresent := nblHDRFacts(item.Tags, item.Name)
		entry := api.DupeEntry{
			ID:            strconv.FormatInt(item.GroupID, 10),
			Name:          strings.TrimSpace(item.Name),
			Files:         append([]string(nil), item.Files...),
			FileCount:     len(item.Files),
			Link:          "https://nebulance.io/torrents.php?id=" + strconv.FormatInt(item.GroupID, 10),
			Download:      strings.TrimSpace(item.Download),
			Category:      strings.TrimSpace(item.Category),
			Season:        item.Season,
			Episode:       item.Episode,
			Pack:          strings.EqualFold(strings.TrimSpace(item.Category), "season"),
			Flags:         flags,
			FlagsPresent:  tagsPresent,
			FlagsComplete: tagsPresent,
			HDR:           hdr,
		}
		if item.Size > 0 {
			entry.SizeKnown, entry.SizeBytes = true, item.Size
		}
		entries = append(entries, entry)
	}
	return entries
}

func nblHDRFacts(tags *[]string, name string) (api.HDRFacts, []string, bool) {
	if tags != nil {
		flags := nblHDRFlags(*tags, false)
		facts := dupe.NormalizeTrackerHDRFlags(flags, true, true)
		facts.SourceFields = []string{"tags"}
		return facts, flags, true
	}
	flags := nblHDRFlags([]string{name}, true)
	facts := dupe.NormalizeTrackerHDRFlags(flags, false, false)
	if facts.Status != api.HDREvidenceMissing {
		facts.Origin = api.HDREvidenceTrackerTitle
		facts.SourceFields = []string{"title"}
	}
	return facts, flags, false
}

func nblHDRFlags(values []string, title bool) []string {
	joined := strings.Join(values, ".")
	upper := strings.ToUpper(joined)
	dv8 := nblTitleToken(upper, "DV8") || nblTitleToken(upper, "DOVI8")
	flags := make([]string, 0, 2)
	if nblTitleToken(upper, "DOVI") || title && (dv8 || strings.Contains(upper, "DOLBY.VISION") || nblTitleToken(upper, "DV")) {
		flags = append(flags, "DV")
	}
	if nblTitleToken(upper, "HDR") || nblTitleToken(upper, "HDR10") || nblTitleToken(upper, "HDR10+") ||
		nblTitleToken(upper, "HDR10PLUS") || title && (dv8 || nblTitleToken(upper, "HLG") || nblTitleToken(upper, "PQ10")) {
		flags = append(flags, "HDR")
	}
	return flags
}

func nblTitleToken(value string, token string) bool {
	return slices.Contains(strings.FieldsFunc(value, func(r rune) bool {
		return r == '.' || r == ' ' || r == '_' || r == '-' || r == '[' || r == ']'
	}), token)
}

func cloneNBLValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, items := range values {
		cloned[key] = append([]string(nil), items...)
	}
	return cloned
}

func nblAPIKey(cfg config.Config) string {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "NBL") {
			return strings.TrimSpace(entry.APIKey)
		}
	}
	return ""
}
