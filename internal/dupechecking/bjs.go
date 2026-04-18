// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type bjsHandler struct {
	cfg  config.Config
	http *http.Client
}

func (h bjsHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	imdb := imdbForLookup(meta)
	if imdb == "" {
		return nil, []string{noteSkip("missing IMDb ID for BJS dupe search")}, nil
	}
	baseURL := trackerBaseURL(h.cfg, "BJS", "https://bj-share.info")
	cookies, err := loadTrackerCookies(ctx, h.cfg, "BJS", trackerHost(baseURL, "bj-share.info"))
	if err != nil {
		return nil, []string{noteSkip("missing valid BJS cookies")}, nil
	}
	resp, root, err := doHTMLGet(ctx, h.http, baseURL+"/torrents.php", url.Values{"searchstr": {imdb}}, nil, cookies)
	if err != nil || resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, []string{noteSkip("BJS search failed")}, nil
	}
	mainColumn := firstNode(root, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "div" && hasClass(node, "main_column")
	})
	if mainColumn == nil {
		return nil, nil, nil
	}
	rows := findNodes(mainColumn, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "tr" && strings.HasPrefix(attrValueHTML(node, "id"), "torrent")
	})
	loadRe := regexp.MustCompile(`loadIfNeeded\('(\d+)',\s*'(\d+)'`)
	entries := make([]api.DupeEntry, 0)
	for _, row := range rows {
		link := firstNode(row, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(attrValueHTML(node, "onclick"), "loadIfNeeded")
		})
		if link == nil {
			continue
		}
		entry := api.DupeEntry{Name: strings.Join(strings.Fields(nodeTextHTML(link)), " ")}
		if match := loadRe.FindStringSubmatch(attrValueHTML(link, "onclick")); len(match) >= 2 {
			entry.ID = match[1]
			entry.Link = baseURL + "/torrents.php?torrentid=" + match[1]
		}
		if entry.Name != "" {
			entries = append(entries, entry)
		}
	}
	return entries, nil, nil
}
