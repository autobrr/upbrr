// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	cfg  config.Config
	http *http.Client
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func (Definition) NewDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{cfg: cfg, http: httpClient}
}

func (h dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	imdb := imdbForLookup(meta)
	if imdb == "" {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing IMDb ID for BJS dupe search", nil)
	}
	baseURL := trackerBaseURL(h.cfg, "BJS", "https://bj-share.info")
	cookies, err := loadTrackerCookies(ctx, h.cfg, "BJS", trackerHost(baseURL, "bj-share.info"))
	if err != nil {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing valid BJS cookies", nil)
	}
	resp, root, err := doHTMLGet(ctx, h.http, baseURL+"/torrents.php", url.Values{"searchstr": {imdb}}, cookies)
	if err != nil || !resp.ok() {
		return dupe.Failed(dupe.FailureRequest, "BJS search failed", err)
	}
	mainColumn := firstNode(root, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "div" && hasClass(node, "main_column")
	})
	if mainColumn == nil {
		return dupe.Resolved(nil, nil)
	}
	return dupe.Resolved(extractBJSResults(baseURL, mainColumn, meta), nil)
}

func extractBJSResults(baseURL string, root *xhtml.Node, meta api.DuplicateSubject) []api.DupeEntry {
	rows := findNodes(root, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "tr"
	})
	entries := make([]api.DupeEntry, 0)
	currentSeason := ""
	currentResolution := ""
	currentEpisode := ""
	currentPack := false
	for _, row := range rows {
		if updateBJSContext(row, &currentSeason, &currentResolution, &currentEpisode, &currentPack) {
			continue
		}

		rowID := attrValueHTML(row, "id")
		if !strings.HasPrefix(rowID, "torrent") || strings.HasPrefix(rowID, "torrent_") {
			continue
		}
		if !shouldProcessBJSRow(currentSeason, currentResolution, currentEpisode, currentPack, meta) {
			continue
		}

		entry := bjsEntryFromRow(baseURL, row)
		if entry.ID != "" || entry.Name != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func updateBJSContext(row *xhtml.Node, currentSeason *string, currentResolution *string, currentEpisode *string, currentPack *bool) bool {
	for className := range strings.FieldsSeq(attrValueHTML(row, "class")) {
		switch className {
		case "resolution_header":
			if match := regexp.MustCompile(`(?i)(\d{3,4}p|\d{3,4}i)`).FindStringSubmatch(nodeTextHTML(row)); len(match) == 2 {
				*currentResolution = strings.ToLower(match[1])
			}
			return true
		case "season_header":
			if match := regexp.MustCompile(`(?i)temporada\s+(\d+)`).FindStringSubmatch(nodeTextHTML(row)); len(match) == 2 {
				*currentSeason = match[1]
			}
			return true
		}
	}

	rowspanCell := firstNode(row, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "td" && attrValueHTML(node, "rowspan") != ""
	})
	if rowspanCell == nil {
		return false
	}
	link := firstNode(rowspanCell, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(attrValueHTML(node, "href"), "torrents.php?id=")
	})
	if link == nil {
		return false
	}
	text := strings.TrimSpace(nodeTextHTML(link))
	if strings.Contains(strings.ToLower(text), "temporada") {
		*currentPack = true
		*currentEpisode = ""
		return false
	}
	if match := regexp.MustCompile(`(?i)S(\d+)E(\d+)`).FindStringSubmatch(text); len(match) == 3 {
		*currentPack = false
		*currentEpisode = match[2]
	}
	return false
}

func shouldProcessBJSRow(currentSeason string, currentResolution string, currentEpisode string, currentPack bool, meta api.DuplicateSubject) bool {
	category := strings.ToUpper(strings.TrimSpace(string(meta.Identity.Category)))
	switch category {
	case "TV":
		if meta.SeasonInt <= 0 || strings.TrimSpace(currentSeason) == "" {
			return false
		}
		season, err := strconv.Atoi(strings.TrimSpace(currentSeason))
		if err != nil || season != meta.SeasonInt {
			return false
		}
		if meta.TVPack {
			return currentPack
		}
		if currentPack {
			return true
		}
		episode, err := strconv.Atoi(strings.TrimSpace(currentEpisode))
		return err == nil && episode == meta.EpisodeInt
	case "MOVIE":
		wantResolution := strings.ToLower(strings.TrimSpace(meta.Release.Resolution))
		if wantResolution == "" || strings.TrimSpace(currentResolution) == "" {
			return true
		}
		return strings.EqualFold(currentResolution, wantResolution)
	default:
		return true
	}
}

func bjsEntryFromRow(baseURL string, row *xhtml.Node) api.DupeEntry {
	link := firstNode(row, func(node *xhtml.Node) bool {
		if node.Type != xhtml.ElementNode || node.Data != "a" {
			return false
		}
		href := attrValueHTML(node, "href")
		return strings.Contains(href, "torrentid=") || strings.Contains(attrValueHTML(node, "onclick"), "loadIfNeeded")
	})
	entry := api.DupeEntry{}
	if link != nil {
		entry.Name = strings.Join(strings.Fields(nodeTextHTML(link)), " ")
		entry.ID = bjsTorrentIDFromLink(link)
		if entry.ID != "" {
			entry.Link = strings.TrimRight(baseURL, "/") + "/torrents.php?torrentid=" + entry.ID
		}
	}
	if entry.ID == "" {
		entry.ID = strings.TrimPrefix(attrValueHTML(row, "id"), "torrent")
		if entry.ID != "" {
			entry.Link = strings.TrimRight(baseURL, "/") + "/torrents.php?torrentid=" + entry.ID
		}
	}
	sizeCell := firstNode(row, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "td" && hasClass(node, "number_column") && hasClass(node, "nobr")
	})
	if sizeCell != nil {
		addSize(&entry, nodeTextHTML(sizeCell))
	}
	return entry
}

func bjsTorrentIDFromLink(link *xhtml.Node) string {
	if link == nil {
		return ""
	}
	href := attrValueHTML(link, "href")
	if match := regexp.MustCompile(`(?i)torrentid=(\d+)`).FindStringSubmatch(href); len(match) == 2 {
		return match[1]
	}
	if match := regexp.MustCompile(`loadIfNeeded\('(\d+)',\s*'(\d+)'`).FindStringSubmatch(attrValueHTML(link, "onclick")); len(match) >= 2 {
		return match[1]
	}
	return ""
}

type responseInfo struct{ StatusCode int }

func (r responseInfo) ok() bool {
	return r.StatusCode >= http.StatusOK && r.StatusCode < http.StatusMultipleChoices
}
func imdbForLookup(meta api.DuplicateSubject) string {
	if meta.Identity.IMDBID == 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", meta.Identity.IMDBID)
}
func trackerBaseURL(_ config.Config, _ string, fallback string) string {
	return strings.TrimRight(fallback, "/")
}
func trackerHost(baseURL, fallback string) string {
	parsed, err := url.Parse(baseURL)
	if err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return fallback
}
func loadTrackerCookies(ctx context.Context, cfg config.Config, tracker, domain string) ([]*http.Cookie, error) {
	loaded, err := cookies.LoadTrackerHTTPCookies(ctx, cfg.MainSettings.DBPath, tracker, domain)
	if err != nil {
		return nil, fmt.Errorf("bjs: load tracker cookies: %w", err)
	}
	return loaded, nil
}
func doHTMLGet(ctx context.Context, client *http.Client, endpoint string, params url.Values, trackerCookies []*http.Cookie) (responseInfo, *xhtml.Node, error) {
	status, root, err := commonhttp.GetHTML(ctx, client, endpoint, params, trackerCookies)
	if err != nil {
		return responseInfo{StatusCode: status}, nil, fmt.Errorf("bjs: HTML request: %w", err)
	}
	return responseInfo{StatusCode: status}, root, nil
}
func firstNode(root *xhtml.Node, match func(*xhtml.Node) bool) *xhtml.Node {
	return commonhttp.FirstNode(root, match)
}
func findNodes(root *xhtml.Node, match func(*xhtml.Node) bool) []*xhtml.Node {
	return commonhttp.FindNodes(root, match)
}
func hasClass(node *xhtml.Node, name string) bool       { return commonhttp.HasClass(node, name) }
func attrValueHTML(node *xhtml.Node, key string) string { return commonhttp.Attr(node, key) }
func nodeTextHTML(node *xhtml.Node) string              { return commonhttp.NodeText(node) }
func addSize(entry *api.DupeEntry, value string) {
	trimmed := strings.TrimSpace(value)
	entry.SizeText = trimmed
	if size, ok := commonhttp.ParseSizeBytes(trimmed); ok {
		entry.SizeKnown, entry.SizeBytes = true, size
	}
}
