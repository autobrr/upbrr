// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path" //nolint:depguard // Torrent file paths are slash-separated tracker data, not local filesystem paths.
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	dupeSearchPerPage   = 96
	dupeDetailWorkers   = 5
	animeParentCategory = "11"
)

var (
	inertiaPagePattern = regexp.MustCompile(`(?s)<script[^>]*data-page="app"[^>]*>(.*?)</script>`)
	// ASC display names end with " - S01" for packs or " - S01E02" for episodes.
	episodeSuffixPattern = regexp.MustCompile(`\s-\sS(\d{1,3})(?:E(\d{1,4}))?\s*$`)
)

type dupeSearcher struct {
	cfg    config.Config
	http   *http.Client
	logger api.Logger
}

// newDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	return &dupeSearcher{
		cfg:    deps.BoundConfig(),
		http:   deps.HTTPClient(),
		logger: deps.Logger(),
	}
}

type searchPage struct {
	Torrents struct {
		CurrentPage int            `json:"current_page"`
		LastPage    int            `json:"last_page"`
		Data        []searchResult `json:"data"`
	} `json:"torrents"`
}

type searchResult struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	CategoryID int    `json:"categoryId"`
	Internal   bool   `json:"internal"`
}

type detailFile struct {
	Path string `json:"path"`
}

type detailPage struct {
	Files      detailFiles `json:"files"`
	FilesTotal int         `json:"filesTotal"`
}

// detailFiles accepts the paginated `{data: [...]}` file list as well as the
// earlier plain array.
type detailFiles []detailFile

func (f *detailFiles) UnmarshalJSON(raw []byte) error {
	var list []detailFile
	if err := json.Unmarshal(raw, &list); err == nil {
		*f = list
		return nil
	}
	var page struct {
		Data []detailFile `json:"data"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return fmt.Errorf("decode ASC file list: %s", redaction.RedactValue(err.Error(), nil))
	}
	*f = page.Data
	return nil
}

func (h dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	h.logger = logging.FromContext(ctx, h.logger)

	if h.http == nil {
		return dupe.Failed(dupe.FailureInternal, "ASC handler misconfigured: no HTTP client", nil)
	}
	if !meta.Anime && resolveASCIMDb(meta) == "" {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing IMDb ID for ASC dupe search", nil)
	}
	if meta.Anime && resolveASCTitle(meta) == "" {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing title for ASC anime dupe search", nil)
	}
	cookies, _, err := LoadCookies(ctx, h.cfg.MainSettings.DBPath)
	if err != nil || len(cookies) == 0 {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing valid ASC cookies", nil)
	}

	var results []searchResult
	pages := 0
	complete := false
	for page := 1; page <= maxDupeSearchPages; page++ {
		var parsed searchPage
		if err := h.fetchPageProps(ctx, buildASCSearchURL(meta, page), cookies, &parsed); err != nil {
			return dupeFailure(err)
		}
		pages++
		results = append(results, parsed.Torrents.Data...)
		if parsed.Torrents.CurrentPage >= parsed.Torrents.LastPage {
			complete = true
			break
		}
	}
	h.logger.Tracef("trackers: ASC dupe search tracker=ASC pages=%d count=%d complete=%t", pages, len(results), complete)

	entries := h.buildEntries(ctx, results, cookies)
	if err := ctx.Err(); err != nil {
		return dupe.Failed(dupe.FailureRequest, "ASC search canceled", err)
	}
	workScope := dupe.WorkScopeProviderID
	if meta.Anime {
		workScope = dupe.WorkScopeTitle
	}
	var warnings []string
	if !complete {
		warnings = append(warnings, fmt.Sprintf("ASC search stopped after %d pages", pages))
	}
	return dupe.ResolvedWithSearch(entries, nil, dupe.SearchEvidence{
		Complete:  complete,
		WorkScope: workScope,
		Pages:     pages,
		Scope:     "work_query",
		Warnings:  warnings,
	})
}

// buildEntries enriches search results with torrent file names from detail
// pages. A failed detail lookup keeps the entry under its display name.
func (h dupeSearcher) buildEntries(ctx context.Context, results []searchResult, cookies []*http.Cookie) []api.DupeEntry {
	entries := make([]api.DupeEntry, len(results))
	var wg sync.WaitGroup
	sem := make(chan struct{}, dupeDetailWorkers)
	for idx, result := range results {
		entries[idx] = baseDupeEntry(result)
		select {
		case <-ctx.Done():
			continue
		case sem <- struct{}{}:
		}
		wg.Go(func() {
			defer func() { <-sem }()
			var detail detailPage
			if err := h.fetchPageProps(ctx, baseURL+torrentPath+strconv.Itoa(result.ID), cookies, &detail); err != nil {
				h.logger.Debugf("trackers: ASC dupe detail failed tracker=ASC torrent_id=%d", result.ID)
				return
			}
			applyDetail(&entries[idx], detail)
		})
	}
	wg.Wait()
	return entries
}

func baseDupeEntry(result searchResult) api.DupeEntry {
	id := strconv.Itoa(result.ID)
	entry := api.DupeEntry{
		Name:      strings.TrimSpace(result.Name),
		ID:        id,
		Link:      baseURL + torrentPath + id,
		Download:  baseURL + torrentPath + id + "/download",
		SizeBytes: result.Size,
		SizeKnown: result.Size > 0,
		Internal:  result.Internal,
	}
	if match := episodeSuffixPattern.FindStringSubmatch(result.Name); len(match) == 3 {
		entry.Season, _ = strconv.Atoi(match[1])
		if match[2] != "" {
			entry.Episode, _ = strconv.Atoi(match[2])
		} else {
			entry.Pack = true
		}
	}
	return entry
}

// applyDetail names the entry after its first file, which carries the release
// naming that ASC display titles omit.
func applyDetail(entry *api.DupeEntry, detail detailPage) {
	files := make([]string, 0, len(detail.Files))
	for _, file := range detail.Files {
		if trimmed := strings.TrimSpace(file.Path); trimmed != "" {
			files = append(files, trimmed)
		}
	}
	if len(files) == 0 {
		return
	}
	entry.Files = files
	entry.FileCount = max(detail.FilesTotal, len(files))
	//pathpolicy:allow ASC torrent file paths are slash-separated tracker data, never local filesystem paths.
	entry.Name = path.Base(files[0])
}

func (h dupeSearcher) fetchPageProps(ctx context.Context, target string, cookies []*http.Cookie, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("build ASC request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return fmt.Errorf("ASC request: %w", err)
	}
	defer resp.Body.Close()
	if isLoginRedirect(resp) {
		return errSessionExpired
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return statusError{status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read ASC response: %w", err)
	}
	return decodePageProps(body, out)
}

// decodePageProps extracts the Inertia page `props` embedded in an HTML page.
func decodePageProps(body []byte, out any) error {
	match := inertiaPagePattern.FindSubmatch(body)
	if len(match) != 2 {
		return parseError{cause: errors.New("page data not found")}
	}
	var page struct {
		Props json.RawMessage `json:"props"`
	}
	if err := json.Unmarshal(match[1], &page); err != nil {
		return parseError{cause: err}
	}
	if err := json.Unmarshal(page.Props, out); err != nil {
		return parseError{cause: err}
	}
	return nil
}

type statusError struct{ status int }

func (e statusError) Error() string { return fmt.Sprintf("ASC returned status %d", e.status) }

type parseError struct{ cause error }

func (e parseError) Error() string { return "ASC page parse failed: " + e.cause.Error() }

func (e parseError) Unwrap() error { return e.cause }

func dupeFailure(err error) dupe.AdapterResult {
	var status statusError
	var parse parseError
	switch {
	case errors.Is(err, errSessionExpired):
		return dupe.Failed(dupe.FailureResponseStatus, "ASC session expired or cookies invalid", err)
	case errors.As(err, &status):
		return dupe.Failed(dupe.FailureResponseStatus, "ASC search returned non-success status", err)
	case errors.As(err, &parse):
		return dupe.Failed(dupe.FailureResponseParse, "ASC response parse failed", err)
	default:
		return dupe.Failed(dupe.FailureRequest, "ASC request failed", err)
	}
}

func buildASCSearchURL(meta api.DuplicateSubject, page int) string {
	params := url.Values{"per_page": {strconv.Itoa(dupeSearchPerPage)}}
	switch {
	case meta.Anime:
		params.Set("q", resolveASCTitle(meta))
		params.Set("category", animeParentCategory)
	case strings.EqualFold(resolveASCCategory(meta), "TV"):
		params.Set("q", resolveASCIMDb(meta))
		params.Set("category", categorySeries)
	default:
		params.Set("q", resolveASCIMDb(meta))
		params.Set("category", categoryMovie)
	}
	if page > 1 {
		params.Set("page", strconv.Itoa(page))
	}
	return baseURL + "/torrents?" + params.Encode()
}

func resolveASCIMDb(meta api.DuplicateSubject) string {
	if meta.Identity.IMDBID > 0 {
		return providerid.IMDb(meta.Identity.IMDBID).Prefixed()
	}
	return ""
}

func resolveASCCategory(meta api.DuplicateSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return strings.ToUpper(string(category))
}

func resolveASCTitle(meta api.DuplicateSubject) string {
	title := strings.TrimSpace(meta.Release.Title)
	if title == "" && meta.Projection != nil {
		title = dupe.ProjectedSearchName(meta)
	}
	if title == "" {
		title = strings.TrimSpace(meta.ReleaseName)
	}
	return meta.EffectiveMetadata.PreferredTitle(title)
}
