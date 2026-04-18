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

type flHandler struct {
	cfg  config.Config
	http *http.Client
}

func (h flHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	cookies, err := loadTrackerCookies(ctx, h.cfg, "FL", ".filelist.io")
	if err != nil {
		return nil, []string{noteSkip("missing valid FL cookies")}, nil
	}
	params := url.Values{}
	params.Set("cat", strconv.Itoa(resolveFLCategoryID(meta)))
	if imdb := imdbForLookup(meta); imdb != "" {
		params.Set("search", imdb)
		params.Set("searchin", "3")
	} else {
		query := firstNonEmpty(meta.Release.Title, meta.ReleaseName)
		if query == "" {
			return nil, []string{noteSkip("missing FL search query")}, nil
		}
		params.Set("search", query)
		params.Set("searchin", "0")
	}
	resp, root, err := doHTMLGet(ctx, h.http, trackerBaseURL(h.cfg, "FL", "https://filelist.io")+"/browse.php", params, nil, cookies)
	if err != nil || resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, []string{noteSkip("FL search failed")}, nil
	}
	links := findNodes(root, func(node *xhtml.Node) bool {
		href := attrValueHTML(node, "href")
		return node.Type == xhtml.ElementNode && node.Data == "a" && strings.HasPrefix(href, "details.php?id=") && !strings.Contains(href, "&")
	})
	entries := make([]api.DupeEntry, 0, len(links))
	baseURL := trackerBaseURL(h.cfg, "FL", "https://filelist.io")
	for _, link := range links {
		entry := api.DupeEntry{
			Name: strings.TrimSpace(firstNonEmpty(attrValueHTML(link, "title"), nodeTextHTML(link))),
			Link: absoluteURL(baseURL, attrValueHTML(link, "href")),
		}
		if entry.Name == "" {
			continue
		}
		if parsed, err := url.Parse(entry.Link); err == nil {
			entry.ID = strings.TrimSpace(parsed.Query().Get("id"))
		}
		entries = append(entries, entry)
	}
	return entries, nil, nil
}
