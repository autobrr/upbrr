// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
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
	cookies, err := loadTrackerCookies(ctx, h.cfg, "BT", trackerHost(trackerBaseURL(h.cfg, "BT", "https://brasiltracker.org"), "brasiltracker.org"))
	if err != nil {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing valid BT cookies", nil)
	}

	imdbID := resolveBTIMDbIDText(meta)

	if imdbID == "" && !meta.Anime {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing IMDb ID for BT dupe search", nil)
	}

	isTVPack := meta.TVPack

	searchStr := imdbID
	if meta.Anime {
		tvdbNameEnglish := ""
		tvdbName := ""
		if meta.ProviderMetadata.TVDB != nil {
			tvdbNameEnglish = strings.TrimSpace(meta.ProviderMetadata.TVDB.NameEnglish)
			tvdbName = strings.TrimSpace(meta.ProviderMetadata.TVDB.Name)
		}

		tmdbTitle := ""
		tmdbOriginalTitle := ""
		if meta.ProviderMetadata.TMDB != nil {
			tmdbTitle = strings.TrimSpace(meta.ProviderMetadata.TMDB.Title)
			tmdbOriginalTitle = strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalTitle)
		}

		imdbTitle := ""
		if meta.ProviderMetadata.IMDB != nil {
			imdbTitle = strings.TrimSpace(meta.ProviderMetadata.IMDB.Title)
		}

		releaseName := strings.TrimSpace(meta.ReleaseName)

		switch {
		// English
		case tvdbNameEnglish != "":
			searchStr = tvdbNameEnglish
		case tmdbTitle != "":
			searchStr = tmdbTitle
		case imdbTitle != "":
			searchStr = imdbTitle

		// Original
		case tvdbName != "":
			searchStr = tvdbName
		case tmdbOriginalTitle != "":
			searchStr = tmdbOriginalTitle

		// Release Name
		case releaseName != "":
			searchStr = releaseName
		}
	}

	if searchStr == "" {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing search term for BT dupe search", nil)
	}

	baseURL := trackerBaseURL(h.cfg, "BT", "https://brasiltracker.org")

	resp, stringBody, err := doTextGet(ctx, h.http, baseURL+"/torrents.php", url.Values{"searchstr": {searchStr}}, nil, cookies)
	if err != nil || !resp.ok() {
		return dupe.Failed(dupe.FailureRequest, "BT search request failed", err)
	}
	if strings.Contains(strings.ToLower(resp.FinalURL), "login") {
		return dupe.NotRun(dupe.NotRunAuthNotReady, "BT authentication required", nil)
	}

	node, err := html.Parse(strings.NewReader(stringBody))
	if err != nil {
		return dupe.Failed(dupe.FailureResponseParse, "BT search html parse failed", err)
	}

	torrentTable := findBTNodeByID(node, "torrent_table")
	if torrentTable == nil {
		return dupe.Resolved(nil, nil)
	}

	groupLinks := make(map[string]struct{})
	findBTGroupLinks(torrentTable, groupLinks)

	if len(groupLinks) == 0 {
		return dupe.Resolved(nil, nil)
	}

	var foundItems []string
	if len(groupLinks) > 0 {
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 5)
	linkLoop:
		for groupLink := range groupLinks {
			select {
			case <-ctx.Done():
				break linkLoop
			case sem <- struct{}{}:
			}

			wg.Add(1)
			go func(link string) {
				defer wg.Done()
				defer func() { <-sem }()

				groupResp, groupBody, groupErr := doTextGet(ctx, h.http, baseURL+"/"+link, nil, nil, cookies)
				if groupErr != nil {
					if h.logger != nil {
						h.logger.Debugf("BT group link request failed for %s: %v", link, groupErr)
					}
					return
				}
				if !groupResp.ok() {
					if h.logger != nil {
						h.logger.Debugf("BT group link request returned non-success status %d for %s", groupResp.StatusCode, link)
					}
					return
				}

				groupNode, nodeErr := html.Parse(strings.NewReader(groupBody))
				if nodeErr != nil {
					if h.logger != nil {
						h.logger.Debugf("BT group link html parse failed for %s: %v", link, nodeErr)
					}
					return
				}

				var localFound []string
				processBTGroupPage(groupNode, isTVPack, &localFound)

				if len(localFound) > 0 {
					mu.Lock()
					foundItems = append(foundItems, localFound...)
					mu.Unlock()
				}
			}(groupLink)
		}
		wg.Wait()
	}

	var entries []api.DupeEntry
	for _, item := range foundItems {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			entries = append(entries, api.DupeEntry{Name: trimmed})
		}
	}

	if err := ctx.Err(); err != nil {
		return dupe.Failed(dupe.FailureRequest, "BT search canceled", err)
	}
	return dupe.Resolved(entries, nil)
}

func resolveBTIMDbIDText(meta api.DuplicateSubject) string {
	if meta.Identity.IMDBID > 0 {
		return fmt.Sprintf("tt%07d", meta.Identity.IMDBID)
	}
	return ""
}

func findBTNodeByID(n *html.Node, id string) *html.Node {
	if n.Type == html.ElementNode {
		for _, a := range n.Attr {
			if a.Key == "id" && a.Val == id {
				return n
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if res := findBTNodeByID(c, id); res != nil {
			return res
		}
	}
	return nil
}

func findBTGroupLinks(n *html.Node, links map[string]struct{}) {
	if n.Type == html.ElementNode && n.Data == "a" {
		href := ""
		for _, a := range n.Attr {
			if a.Key == "href" {
				href = a.Val
			}
		}
		if strings.Contains(href, "torrents.php?id=") && !strings.Contains(href, "torrentid") {
			links[href] = struct{}{}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		findBTGroupLinks(c, links)
	}
}

func processBTGroupPage(n *html.Node, isTVPack bool, foundItems *[]string) {
	trs := findBTAllNodes(n, func(node *html.Node) bool {
		if node.Type == html.ElementNode && node.Data == "tr" {
			for _, a := range node.Attr {
				if a.Key == "id" && strings.HasPrefix(a.Val, "torrent") {
					suffix := strings.TrimPrefix(a.Val, "torrent")
					if _, err := strconv.Atoi(suffix); err == nil {
						return true
					}
				}
			}
		}
		return false
	})

	for _, tr := range trs {
		descLink := findBTNode(tr, func(node *html.Node) bool {
			if node.Type == html.ElementNode && node.Data == "a" {
				for _, a := range node.Attr {
					if a.Key == "onclick" && strings.Contains(a.Val, "gtoggle") {
						return true
					}
				}
			}
			return false
		})

		if descLink == nil {
			continue
		}

		descText := strings.ToLower(extractBTText(descLink))
		isDisc := false
		for _, kw := range []string{"bd25", "bd50", "bd66", "bd100", "dvd5", "dvd9", "m2ts"} {
			if strings.Contains(descText, kw) {
				isDisc = true
				break
			}
		}

		idVal := ""
		for _, a := range tr.Attr {
			if a.Key == "id" {
				idVal = a.Val
			}
		}
		torrentID := strings.TrimPrefix(idVal, "torrent")

		fileDiv := findBTNodeByID(n, "files_"+torrentID)
		if fileDiv == nil {
			continue
		}

		if isDisc || isTVPack {
			pathDiv := findBTNode(fileDiv, func(node *html.Node) bool {
				return node.Type == html.ElementNode && node.Data == "div" && hasBTClass(node, "filelist_path")
			})
			if pathDiv != nil {
				folderName := strings.Trim(strings.TrimSpace(extractBTText(pathDiv)), "/")
				if folderName != "" {
					*foundItems = append(*foundItems, folderName)
				}
			}
		} else {
			fileTable := findBTNode(fileDiv, func(node *html.Node) bool {
				return node.Type == html.ElementNode && node.Data == "table" && hasBTClass(node, "filelist_table")
			})
			if fileTable != nil {
				rows := findBTAllNodes(fileTable, func(node *html.Node) bool {
					return node.Type == html.ElementNode && node.Data == "tr"
				})
				for _, row := range rows {
					if hasBTClass(row, "colhead_dark") {
						continue
					}
					cell := findBTNode(row, func(node *html.Node) bool {
						return node.Type == html.ElementNode && node.Data == "td"
					})
					if cell != nil {
						filename := strings.TrimSpace(extractBTText(cell))
						if filename != "" {
							*foundItems = append(*foundItems, filename)
							break
						}
					}
				}
			}
		}
	}
}

func findBTNode(n *html.Node, match func(*html.Node) bool) *html.Node {
	if match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if res := findBTNode(c, match); res != nil {
			return res
		}
	}
	return nil
}

func findBTAllNodes(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var nodes []*html.Node
	if match(n) {
		nodes = append(nodes, n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		nodes = append(nodes, findBTAllNodes(c, match)...)
	}
	return nodes
}

func hasBTClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			classes := strings.Fields(a.Val)
			if slices.Contains(classes, class) {
				return true
			}
		}
	}
	return false
}

func extractBTText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(extractBTText(c))
	}
	return b.String()
}

type responseInfo struct {
	StatusCode int
	FinalURL   string
}

func (r responseInfo) ok() bool {
	return r.StatusCode >= http.StatusOK && r.StatusCode < http.StatusMultipleChoices
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
		return nil, fmt.Errorf("bt: load tracker cookies: %w", err)
	}
	return loaded, nil
}

func doTextGet(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	params url.Values,
	headers map[string]string,
	trackerCookies []*http.Cookie,
) (responseInfo, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return responseInfo{}, "", fmt.Errorf("bt: create GET request: %w", err)
	}
	if len(params) > 0 {
		req.URL.RawQuery = params.Encode()
	}
	req.Header.Set("User-Agent", "upbrr")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	commonhttp.ApplyCookies(req, trackerCookies)
	resp, err := client.Do(req)
	if err != nil {
		return responseInfo{}, "", fmt.Errorf("bt: GET request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return responseInfo{StatusCode: resp.StatusCode}, "", fmt.Errorf("bt: read response: %w", err)
	}
	info := responseInfo{StatusCode: resp.StatusCode}
	if resp.Request != nil && resp.Request.URL != nil {
		info.FinalURL = resp.Request.URL.String()
	}
	return info, string(body), nil
}
