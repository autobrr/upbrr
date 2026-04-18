// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

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
	"github.com/autobrr/upbrr/pkg/api"
)

type hdsHandler struct {
	cfg  config.Config
	http *http.Client
}

func (h hdsHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	if h.http == nil {
		return nil, []string{noteSkip("missing HTTP client for HDS dupe-check")}, nil
	}
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if resolution != "2160p" && resolution != "1080p" && resolution != "1080i" && resolution != "720p" {
		return nil, []string{noteSkip("resolution below HDS dupe-check minimum")}, nil
	}
	imdb := ""
	if meta.ExternalMetadata.IMDB != nil && meta.ExternalMetadata.IMDB.IMDbIDText != "" {
		// leading zeros are required
		imdb = strings.TrimPrefix(meta.ExternalMetadata.IMDB.IMDbIDText, "tt")
	}
	if imdb == "" && meta.ExternalIDs.IMDBID > 0 {
		imdb = fmt.Sprintf("%07d", meta.ExternalIDs.IMDBID)
	}
	if imdb == "" {
		return nil, []string{noteSkip("missing IMDb ID for HDS dupe search")}, nil
	}
	baseURL := trackerBaseURL(h.cfg, "HDS", "https://hd-space.org")
	cookies, err := loadTrackerCookies(ctx, h.cfg, "HDS", trackerHost(baseURL, "hd-space.org"))
	if err != nil {
		return nil, []string{noteSkip("missing valid HDS cookies")}, nil
	}
	entries := make([]api.DupeEntry, 0)
	pageRe := regexp.MustCompile(`pages=(\d+)`)
	for page := 0; page <= 10; page++ {
		params := url.Values{
			"page":    {"torrents"},
			"search":  {imdb},
			"active":  {"0"},
			"options": {"2"},
			"pages":   {strconv.Itoa(page)},
		}
		resp, body, err := doTextGet(ctx, h.http, baseURL+"/index.php", params, nil, cookies)
		if err != nil || resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, []string{noteSkip("HDS search failed")}, nil
		}
		parts := strings.SplitN(body, "Show/Hide Categories", 2)
		if len(parts) < 2 {
			break
		}
		root, err := xhtml.Parse(strings.NewReader(parts[1]))
		if err != nil {
			return nil, []string{noteSkip("HDS response parse failed")}, nil
		}
		rows := findNodes(root, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "tr"
		})
		beforeCount := len(entries)
		for _, row := range rows {
			nameNode := firstNode(row, func(node *xhtml.Node) bool {
				return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(attrValueHTML(node, "href"), "page=torrent-details")
			})
			if nameNode == nil {
				continue
			}
			entry := api.DupeEntry{
				Name: strings.TrimSpace(firstNonEmpty(nodeTextHTML(nameNode), attrValueHTML(nameNode, "title"))),
				Link: absoluteURL(baseURL, attrValueHTML(nameNode, "href")),
			}
			cells := findNodes(row, func(node *xhtml.Node) bool {
				return node.Type == xhtml.ElementNode && node.Data == "td" && hasClass(node, "lista")
			})
			for _, cell := range cells {
				if sizeText := strings.TrimSpace(nodeTextHTML(cell)); sizePattern.MatchString(sizeText) {
					addSize(&entry, sizeText)
					break
				}
			}
			if entry.Name != "" {
				entries = append(entries, entry)
			}
		}
		next := firstNode(root, func(node *xhtml.Node) bool {
			if node.Type != xhtml.ElementNode || node.Data != "a" {
				return false
			}
			href := attrValueHTML(node, "href")
			text := strings.TrimSpace(nodeTextHTML(node))
			return strings.Contains(href, "pages=") && (strings.EqualFold(text, "Next") || text == ">>" || pageRe.MatchString(href))
		})
		if next == nil || len(entries) == beforeCount {
			break
		}
	}
	return entries, nil, nil
}
