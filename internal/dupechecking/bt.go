// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type btHandler struct {
	cfg    config.Config
	http   *http.Client
	logger api.Logger
}

func (h btHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	cookies, err := loadTrackerTextCookies(h.cfg, "BT", trackerHost(trackerBaseURL(h.cfg, "BT", "https://brasiltracker.org"), "brasiltracker.org"))
	if err != nil {
		return nil, []string{noteSkip("missing valid BT cookies")}, nil
	}

	imdbID := resolveBTIMDbIDText(meta)

	if imdbID == "" && !meta.Anime {
		return nil, []string{noteSkip("missing IMDb ID for BT dupe search")}, nil
	}

	isTVPack := meta.TVPack

	searchStr := imdbID
	if meta.Anime {
		switch {
		// English
		case strings.TrimSpace(meta.ExternalMetadata.TVDB.NameEnglish) != "":
			searchStr = strings.TrimSpace(meta.ExternalMetadata.TVDB.NameEnglish)
		case strings.TrimSpace(meta.ExternalMetadata.TMDB.Title) != "":
			searchStr = strings.TrimSpace(meta.ExternalMetadata.TMDB.Title)
		case strings.TrimSpace(meta.ExternalMetadata.IMDB.Title) != "":
			searchStr = strings.TrimSpace(meta.ExternalMetadata.IMDB.Title)

		// Original
		case strings.TrimSpace(meta.ExternalMetadata.TVDB.Name) != "":
			searchStr = strings.TrimSpace(meta.ExternalMetadata.TVDB.Name)
		case strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalTitle) != "":
			searchStr = strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalTitle)

		// Release Name
		case strings.TrimSpace(meta.ReleaseName) != "":
			searchStr = strings.TrimSpace(meta.ReleaseName)
		}
	}

	if searchStr == "" {
		return nil, []string{noteSkip("missing search term for BT dupe search")}, nil
	}

	baseURL := trackerBaseURL(h.cfg, "BT", "https://brasiltracker.org")

	resp, stringBody, err := doTextGet(ctx, h.http, baseURL+"/torrents.php", url.Values{"searchstr": {searchStr}}, nil, cookies)
	if err != nil || resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, []string{noteSkip("BT search request failed")}, nil
	}
	if resp.Request != nil && resp.Request.URL != nil && strings.Contains(strings.ToLower(resp.Request.URL.String()), "login") {
		return nil, []string{noteSkip("BT authentication required")}, nil
	}

	node, err := html.Parse(strings.NewReader(stringBody))
	if err != nil {
		return nil, []string{noteSkip("BT search html parse failed")}, nil
	}

	torrentTable := findBTNodeByID(node, "torrent_table")
	if torrentTable == nil {
		return nil, nil, nil
	}

	groupLinks := make(map[string]struct{})
	findBTGroupLinks(torrentTable, groupLinks)

	if len(groupLinks) == 0 {
		return nil, nil, nil
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
				if groupResp == nil || groupResp.StatusCode < 200 || groupResp.StatusCode >= 300 {
					status := 0
					if groupResp != nil {
						status = groupResp.StatusCode
					}
					if h.logger != nil {
						h.logger.Debugf("BT group link request returned non-success status %d for %s", status, link)
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

	return entries, nil, nil
}

func resolveBTIMDbIDText(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbIDText) != "" {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbIDText)
	}
	if meta.ExternalIDs.IMDBID > 0 {
		return fmt.Sprintf("tt%07d", meta.ExternalIDs.IMDBID)
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
			for _, c := range classes {
				if c == class {
					return true
				}
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
