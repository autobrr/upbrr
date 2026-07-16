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
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	tracker string
	cfg     config.Config
	http    *http.Client
	logger  api.Logger
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func (d *Definition) NewDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		tracker: deps.Tracker(),
		cfg:     cfg,
		http:    httpClient,
		logger:  logger,
	}
}

func (h dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	tracker := h.tracker
	if h.http == nil {
		return dupe.Failed(dupe.FailureInternal, "AZ-family handler misconfigured: no HTTP client", nil)
	}
	site := azDupeSite(tracker)
	if cfg, ok := trackerCfg(h.cfg, tracker); ok && strings.TrimSpace(cfg.URL) != "" {
		site.baseURL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	}
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
	pageURL := site.baseURL + "/movies/torrents/" + mediaCode + "?quality=" + url.QueryEscape(azDupeResolution(meta))
	entries, err := h.fetchTorrentList(ctx, site, loadedCookies, pageURL, meta)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, strings.ToUpper(strings.TrimSpace(tracker))+" search failed", err)
	}
	return dupe.Resolved(entries, nil)
}

func (h dupeSearcher) lookupMediaCode(ctx context.Context, site azDupeSiteDef, cookies []*http.Cookie, meta api.DuplicateSubject) (string, error) {
	term := lookupAZDupeTitle(meta)
	imdb := ""
	if meta.Identity.IMDBID != 0 {
		imdb = fmt.Sprintf("tt%07d", meta.Identity.IMDBID)
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
	if len(items) > 0 {
		return stringFromAny(items[0]["id"]), nil
	}
	return "", nil
}

func (h dupeSearcher) fetchTorrentList(
	ctx context.Context,
	site azDupeSiteDef,
	cookies []*http.Cookie,
	pageURL string,
	meta api.DuplicateSubject,
) ([]api.DupeEntry, error) {
	results := make([]api.DupeEntry, 0)
	visited := make(map[string]struct{})
	ripType := azDupeRipType(meta)
	for strings.TrimSpace(pageURL) != "" {
		if _, ok := visited[pageURL]; ok {
			break
		}
		visited[pageURL] = struct{}{}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
		if err != nil {
			return nil, fmt.Errorf("dupechecking: create %s torrent list request: %w", site.baseURL, err)
		}
		req.Header.Set("User-Agent", "upbrr")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		resp, err := h.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("dupechecking: fetch %s torrent list: %w", site.baseURL, err)
		}
		root, err := xhtml.Parse(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("dupechecking: parse %s search response: %w", site.baseURL, err)
		}
		rows := findTorrentRows(root)
		for _, row := range rows {
			entry := parseAZDupeRow(row, site)
			if entry.Name == "" {
				continue
			}
			if ripType != "" && !containsFlag(entry.Flags, ripType) {
				continue
			}
			results = append(results, entry)
		}
		pageURL = nextAZPage(root, site.baseURL)
	}
	return results, nil
}

type azDupeSiteDef struct {
	baseURL string
}

func azDupeSite(tracker string) azDupeSiteDef {
	switch strings.ToUpper(strings.TrimSpace(tracker)) {
	case "CZ":
		return azDupeSiteDef{baseURL: "https://cinemaz.to"}
	case "PHD":
		return azDupeSiteDef{baseURL: "https://privatehd.to"}
	default:
		return azDupeSiteDef{baseURL: "https://avistaz.to"}
	}
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

func azDupeResolution(meta api.DuplicateSubject) string {
	value := strings.TrimSpace(meta.Release.Resolution)
	if value == "" {
		value = detectResolution(meta.ReleaseName)
	}
	switch strings.ToLower(value) {
	case "2160p":
		return "UHD"
	case "720p", "1080p":
		return value
	default:
		return "all"
	}
}

func azDupeRipType(meta api.DuplicateSubject) string {
	switch strings.ToLower(strings.TrimSpace(meta.Type)) {
	case "bdrip":
		return "BDRip"
	case "encode":
		return "BluRay"
	case "brrip":
		return "BRRip"
	case "dvdrip":
		return "DVDRip"
	case "hdrip":
		return "HDRip"
	case "hdtv":
		return "HDTV"
	case "sdtv":
		return "SDTV"
	case "webdl":
		return "WEB-DL"
	case "webrip":
		return "WEBRip"
	case "remux":
		if strings.Contains(strings.ToLower(strings.TrimSpace(meta.Source)), "dvd") {
			return "DVD Remux"
		}
		return "BluRay REMUX"
	default:
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
			return "BluRay Raw"
		}
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
			return "DVD"
		}
		return ""
	}
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

func nextAZPage(root *xhtml.Node, baseURL string) string {
	var next string
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil || next != "" {
			return
		}
		if node.Type == xhtml.ElementNode && node.Data == "a" && strings.EqualFold(attrValueHTML(node, "rel"), "next") {
			next = absoluteAZURL(baseURL, attrValueHTML(node, "href"))
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return next
}

func containsFlag(flags []string, want string) bool {
	for _, flag := range flags {
		if strings.EqualFold(strings.TrimSpace(flag), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
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

func trackerCfg(cfg config.Config, tracker string) (config.TrackerConfig, bool) {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(tracker)) {
			return entry, true
		}
	}
	return config.TrackerConfig{}, false
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
