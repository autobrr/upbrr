// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type hdtHandler struct {
	cfg  config.Config
	http *http.Client
}

func (h hdtHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if resolution != "2160p" && resolution != "1080p" && resolution != "1080i" && resolution != "720p" {
		return nil, []string{noteSkip("resolution below HDT dupe-check minimum")}, nil
	}
	baseURL := trackerBaseURL(h.cfg, "HDT", "https://hd-torrents.me")
	cookies, err := loadTrackerCookies(ctx, h.cfg, "HDT", trackerHost(baseURL, "hd-torrents.me"))
	if err != nil {
		return nil, []string{noteSkip("missing valid HDT cookies")}, nil
	}
	query := imdbForLookup(meta)
	params := url.Values{
		"active":     {"0"},
		"category[]": {strconv.Itoa(resolveHDTCategoryID(meta))},
	}
	if query != "" {
		params.Set("search", query)
		params.Set("options", "2")
	} else {
		params.Set("search", firstNonEmpty(meta.Release.Title, meta.ReleaseName))
		params.Set("options", "3")
	}
	resp, root, err := doHTMLGet(ctx, h.http, baseURL+"/torrents.php", params, nil, cookies)
	if err != nil || resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, []string{noteSkip("HDT search failed")}, nil
	}
	rows := findNodes(root, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "tr"
	})
	entries := make([]api.DupeEntry, 0)
	for _, row := range rows {
		nameNode := firstNode(row, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(attrValueHTML(node, "href"), "details.php?id=")
		})
		if nameNode == nil {
			continue
		}
		entry := api.DupeEntry{
			Name: strings.TrimSpace(nodeTextHTML(nameNode)),
			Link: absoluteURL(baseURL, attrValueHTML(nameNode, "href")),
		}
		cells := findNodes(row, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "td" && hasClass(node, "mainblockcontent")
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
	return entries, nil, nil
}
