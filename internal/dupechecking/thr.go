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

type thrHandler struct {
	cfg  config.Config
	http *http.Client
}

func (h thrHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	imdb := imdbForLookup(meta)
	if imdb == "" {
		return nil, []string{noteSkip("missing IMDb ID for THR dupe search")}, nil
	}
	cfg, ok := trackerCfg(h.cfg, "THR")
	if !ok || strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, []string{noteSkip("missing THR username/password")}, nil
	}
	baseURL := trackerBaseURL(h.cfg, "THR", "https://www.torrenthr.org")
	cookies, err := loginTHR(ctx, h.http, baseURL, cfg.Username, cfg.Password)
	if err != nil {
		return nil, []string{noteSkip("THR login failed")}, nil
	}
	entries := make([]api.DupeEntry, 0)
	for page := 0; page <= 10; page++ {
		params := url.Values{"search": {imdb}, "blah": {"2"}, "incldead": {"1"}}
		if page > 0 {
			params.Set("page", strconv.Itoa(page))
		}
		resp, root, err := doHTMLGet(ctx, h.http, baseURL+"/browse.php", params, cookies)
		if err != nil || !resp.ok() {
			return nil, []string{noteSkip("THR search failed")}, nil
		}
		links := findNodes(root, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "a" && strings.HasPrefix(attrValueHTML(node, "href"), "details.php")
		})
		before := len(entries)
		for _, link := range links {
			name := parseTHRName(attrValueHTML(link, "onmousemove"))
			if name == "" {
				name = strings.TrimSpace(nodeTextHTML(link))
			}
			if name == "" {
				continue
			}
			entries = append(entries, api.DupeEntry{Name: name, Link: absoluteURL(baseURL, attrValueHTML(link, "href"))})
		}
		next := firstNode(root, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(strings.ToLower(nodeTextHTML(node)), "next")
		})
		if next == nil || len(entries) == before {
			break
		}
	}
	return entries, nil, nil
}
