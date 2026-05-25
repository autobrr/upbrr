// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type isHandler struct {
	cfg  config.Config
	http *http.Client
}

func (h isHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	baseURL := trackerBaseURL(h.cfg, "IS", "https://immortalseed.me")
	cookies, err := loadTrackerCookies(ctx, h.cfg, "IS", trackerHost(baseURL, "immortalseed.me"))
	if err != nil {
		return nil, []string{noteSkip("missing valid IS cookies")}, nil
	}
	params := url.Values{"do": {"search"}}
	if strings.EqualFold(categoryOfSiteMeta(meta), "MOVIE") {
		imdb := imdbForLookup(meta)
		if imdb == "" {
			return nil, []string{noteSkip("missing IMDb ID for IS movie dupe search")}, nil
		}
		params.Set("search_type", "t_genre")
		params.Set("keywords", imdb)
	} else {
		params.Set("search_type", "t_name")
		params.Set("keywords", strings.TrimSpace(firstNonEmpty(meta.Release.Title, meta.ReleaseName)+" "+resolveSeasonEpisodeQuery(meta)))
	}
	resp, root, err := doHTMLGet(ctx, h.http, baseURL+"/browse.php", params, cookies)
	if err != nil || !resp.ok() {
		return nil, []string{noteSkip("IS search failed")}, nil
	}
	table := firstNode(root, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "table" && attrValueHTML(node, "id") == "sortabletable"
	})
	if table == nil {
		return nil, nil, nil
	}
	rows := findNodes(table, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "tr"
	})
	entries := make([]api.DupeEntry, 0)
	for _, row := range rows {
		link := firstNode(row, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(attrValueHTML(node, "href"), "details.php?id=")
		})
		if link == nil {
			continue
		}
		entry := api.DupeEntry{
			Name: strings.TrimSpace(nodeTextHTML(link)),
			Link: absoluteURL(baseURL, attrValueHTML(link, "href")),
		}
		cells := findNodes(row, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "td"
		})
		if len(cells) >= 5 {
			addSize(&entry, nodeTextHTML(cells[4]))
		}
		if entry.Name != "" {
			entries = append(entries, entry)
		}
	}
	return entries, nil, nil
}
