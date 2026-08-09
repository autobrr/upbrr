// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	tracker  string
	baseURL  string
	cfg      config.Config
	http     *http.Client
	logger   api.Logger
	maxPages int
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func (d *Definition) NewDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	return &dupeSearcher{
		tracker:  deps.Tracker(),
		baseURL:  d.site.BaseURL,
		cfg:      cfg,
		http:     httpClient,
		logger:   logger,
		maxPages: deps.MaxPages(100),
	}
}

func (h dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	tracker := h.tracker
	if h.http == nil {
		return dupe.Failed(dupe.FailureInternal, "AZ-family handler misconfigured: no HTTP client", nil)
	}
	site := azDupeSiteDef{baseURL: h.baseURL}
	loadedCookies, err := loadAZFamilyCookies(ctx, h.cfg, tracker, site.baseURL)
	if err != nil {
		return dupe.NotRun(dupe.NotRunMissingCredentials, fmt.Sprintf("missing valid %s cookies", strings.ToUpper(strings.TrimSpace(tracker))), nil)
	}
	mediaCode, err := h.lookupMediaCode(ctx, site, loadedCookies, meta)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, strings.ToUpper(strings.TrimSpace(tracker))+" request failed", err)
	}
	if mediaCode == "" {
		return dupe.NotRun(dupe.NotRunMissingMetadata, strings.ToUpper(strings.TrimSpace(tracker))+" media missing from tracker database", nil)
	}
	pageURL := site.baseURL + "/movies/torrents/" + mediaCode
	entries, pages, complete, warning, err := h.fetchTorrentList(ctx, site, loadedCookies, pageURL)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, strings.ToUpper(strings.TrimSpace(tracker))+" search failed", err)
	}
	warnings := []string(nil)
	if warning != "" {
		warnings = []string{warning}
	}
	return dupe.ResolvedWithSearch(entries, warnings, dupe.SearchEvidence{
		Complete:  complete,
		WorkScope: dupe.WorkScopeTrackerGroup,
		Pages:     pages,
		Scope:     "tracker_group",
		Warnings:  warnings,
	})
}

func (h dupeSearcher) lookupMediaCode(ctx context.Context, site azDupeSiteDef, cookies []*http.Cookie, meta api.DuplicateSubject) (string, error) {
	term := lookupAZDupeTitle(meta)
	imdb := ""
	if meta.Identity.IMDBID != 0 {
		imdb = providerid.IMDb(meta.Identity.IMDBID).Prefixed()
	}
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return "", fmt.Errorf("AZ dupe search: require canonical category: %w", err)
	}
	categoryID := "1"
	if category == api.CanonicalCategoryTV {
		categoryID = "2"
	}
	search := func(term string) ([]map[string]any, error) {
		if strings.TrimSpace(term) == "" {
			return nil, nil
		}
		endpoint := fmt.Sprintf("%s/ajax/movies/%s?term=%s", site.baseURL, categoryID, url.QueryEscape(term))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("dupechecking: create %s media lookup request: %w", site.baseURL, err)
		}
		req.Header.Set("User-Agent", "upbrr")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		resp, err := h.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("dupechecking: fetch %s media lookup: %w", site.baseURL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("status %d", resp.StatusCode)
		}
		var payload struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, fmt.Errorf("dupechecking: decode %s media lookup: %w", site.baseURL, err)
		}
		return payload.Data, nil
	}

	items, err := search(imdb)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		items, err = search(term)
		if err != nil {
			return "", err
		}
	}
	for _, item := range items {
		if imdb != "" && strings.EqualFold(stringFromAny(item["imdb"]), imdb) {
			return stringFromAny(item["id"]), nil
		}
	}
	return "", nil
}

func (h dupeSearcher) fetchTorrentList(
	ctx context.Context,
	site azDupeSiteDef,
	cookies []*http.Cookie,
	pageURL string,
) ([]api.DupeEntry, int, bool, string, error) {
	results := make([]api.DupeEntry, 0)
	visited := make(map[string]struct{})
	pages := 0
	maxPages := h.maxPages
	if maxPages <= 0 {
		maxPages = 100
	}
	complete := false
	warning := ""
	for strings.TrimSpace(pageURL) != "" {
		if pages >= maxPages {
			warning = "AZ-family search reached pagination safety bound"
			if h.logger != nil {
				h.logger.Warnf(
					"dupechecking: search tracker=%s pages=%d complete=false decision=pagination_safety_bound",
					h.tracker,
					pages,
				)
			}
			break
		}
		if _, ok := visited[pageURL]; ok {
			warning = "AZ-family search repeated a result page"
			if h.logger != nil {
				h.logger.Warnf(
					"dupechecking: search tracker=%s pages=%d complete=false decision=repeated_page",
					h.tracker,
					pages,
				)
			}
			break
		}
		visited[pageURL] = struct{}{}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
		if err != nil {
			return nil, pages, false, warning, fmt.Errorf("dupechecking: create %s torrent list request: %w", site.baseURL, err)
		}
		req.Header.Set("User-Agent", "upbrr")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		resp, err := h.http.Do(req)
		if err != nil {
			return nil, pages, false, warning, fmt.Errorf("dupechecking: fetch %s torrent list: %w", site.baseURL, err)
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			resp.Body.Close()
			return nil, pages, false, warning, fmt.Errorf("dupechecking: fetch %s torrent list: status %d", site.baseURL, resp.StatusCode)
		}
		root, err := xhtml.Parse(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, pages, false, warning, fmt.Errorf("dupechecking: parse %s search response: %w", site.baseURL, err)
		}
		pages++
		rows := findTorrentRows(root)
		for _, row := range rows {
			entry := parseAZDupeRow(row, site)
			if entry.Name == "" {
				continue
			}
			entry.Type = azCandidateRipType(entry.Flags)
			entry.CanonicalType = azCanonicalCandidateType(entry.Type)
			results = append(results, entry)
		}
		nextPage, nextPresent := nextAZPage(root, site.baseURL)
		switch {
		case !nextPresent:
			complete = true
			if h.logger != nil {
				h.logger.Debugf("dupechecking: search tracker=%s pages=%d complete=true decision=enumeration_complete", h.tracker, pages)
			}
		case nextPage == "":
			warning = "AZ-family search rejected an invalid next-page link"
			if h.logger != nil {
				h.logger.Warnf("dupechecking: search tracker=%s pages=%d complete=false decision=invalid_next_page", h.tracker, pages)
			}
		}
		pageURL = nextPage
	}
	return dedupeAZEntries(results), pages, complete, warning, nil
}

func azCandidateRipType(flags []string) string {
	for _, flag := range flags {
		if azCanonicalCandidateType(flag) != "" {
			return strings.TrimSpace(flag)
		}
	}
	return ""
}

func dedupeAZEntries(entries []api.DupeEntry) []api.DupeEntry {
	result := make([]api.DupeEntry, 0, len(entries))
	indexes := make(map[string]int)
	for _, entry := range entries {
		key := strings.TrimSpace(entry.ID)
		if key == "" {
			key = strings.TrimSpace(entry.Link)
		}
		if key == "" {
			result = append(result, entry)
			continue
		}
		if index, ok := indexes[key]; ok {
			if len(entry.Flags) > len(result[index].Flags) {
				result[index] = entry
			}
			continue
		}
		indexes[key] = len(result)
		result = append(result, entry)
	}
	return result
}

// azCanonicalCandidateType maps an AZ-family rip filter value to the shared duplicate type vocabulary.
func azCanonicalCandidateType(ripType string) string {
	switch strings.ToLower(strings.TrimSpace(ripType)) {
	case "bluray raw", "dvd":
		return "DISC"
	case "bluray remux", "dvd remux":
		return "REMUX"
	case "web-dl":
		return "WEBDL"
	case "webrip":
		return "WEBRIP"
	case "hdtv", "sdtv":
		return "HDTV"
	case "bdrip", "bluray", "brrip", "dvdrip", "hdrip":
		return "ENCODE"
	default:
		return ""
	}
}

type azDupeSiteDef struct {
	baseURL string
}

func loadAZFamilyCookies(ctx context.Context, cfg config.Config, tracker string, baseURL string) ([]*http.Cookie, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse baseURL %q: %w", baseURL, err)
	}
	host := parsed.Hostname()
	loaded, err := cookies.LoadTrackerHTTPCookies(ctx, cfg.MainSettings.DBPath, strings.ToUpper(strings.TrimSpace(tracker)), host)
	if err != nil {
		return nil, fmt.Errorf("dupechecking: load AZ-family cookies: %w", err)
	}
	return loaded, nil
}

func lookupAZDupeTitle(meta api.DuplicateSubject) string {
	if meta.Projection != nil {
		return dupe.ProjectedSearchName(meta)
	}
	if title := strings.TrimSpace(meta.Release.Title); title != "" {
		return title
	}
	if meta.ProviderMetadata.TMDB != nil {
		if title := strings.TrimSpace(meta.ProviderMetadata.TMDB.Title); title != "" {
			return title
		}
	}
	return strings.TrimSpace(meta.Filename)
}

func findTorrentRows(root *xhtml.Node) []*xhtml.Node {
	rows := make([]*xhtml.Node, 0)
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil {
			return
		}
		if node.Type == xhtml.ElementNode && node.Data == "tr" {
			rows = append(rows, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return rows
}

func parseAZDupeRow(row *xhtml.Node, site azDupeSiteDef) api.DupeEntry {
	entry := api.DupeEntry{}
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil {
			return
		}
		if node.Type == xhtml.ElementNode && node.Data == "span" && strings.Contains(attrValueHTML(node, "class"), "badge-extra") {
			if value := strings.TrimSpace(nodeTextHTML(node)); value != "" {
				entry.Flags = append(entry.Flags, value)
			}
		}
		if node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(attrValueHTML(node, "class"), "torrent-filename") {
			entry.Name = strings.TrimSpace(nodeTextHTML(node))
			href := strings.TrimSpace(attrValueHTML(node, "href"))
			if href != "" {
				entry.Link = absoluteAZURL(site.baseURL, href)
				if id := extractAZTorrentID(entry.Link); id != "" {
					entry.ID = id
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(row)
	return entry
}

func nextAZPage(root *xhtml.Node, baseURL string) (string, bool) {
	var next string
	present := false
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil || present {
			return
		}
		if node.Type == xhtml.ElementNode && node.Data == "a" && strings.EqualFold(attrValueHTML(node, "rel"), "next") {
			present = true
			next = sameOriginAZURL(baseURL, attrValueHTML(node, "href"))
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return next, present
}

func sameOriginAZURL(baseURL, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	base, baseErr := url.Parse(strings.TrimSpace(baseURL))
	reference, referenceErr := url.Parse(strings.TrimSpace(value))
	if baseErr != nil || referenceErr != nil {
		return ""
	}
	resolved := base.ResolveReference(reference)
	if (base.Scheme != "http" && base.Scheme != "https") ||
		!strings.EqualFold(base.Scheme, resolved.Scheme) ||
		!strings.EqualFold(base.Host, resolved.Host) ||
		strings.TrimSpace(base.Host) == "" {
		return ""
	}
	return resolved.String()
}

func attrValueHTML(node *xhtml.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func nodeTextHTML(node *xhtml.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == xhtml.TextNode {
		return node.Data
	}
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		builder.WriteString(nodeTextHTML(child))
	}
	return builder.String()
}

func absoluteAZURL(baseURL, value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(trimmed, "/")
}

func extractAZTorrentID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	last := parts[len(parts)-1]
	if _, err := strconv.Atoi(last); err == nil {
		return last
	}
	return ""
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}
