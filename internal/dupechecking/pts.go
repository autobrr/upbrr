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

type ptsHandler struct {
	cfg  config.Config
	http *http.Client
}

func (h ptsHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	imdb := imdbForLookup(meta)
	if imdb == "" {
		return nil, []string{noteSkip("missing IMDb ID for PTS dupe search")}, nil
	}
	baseURL := trackerBaseURL(h.cfg, "PTS", "https://www.ptskit.org")
	cookies, err := loadTrackerCookies(ctx, h.cfg, "PTS", trackerHost(baseURL, "ptskit.org"))
	if err != nil {
		return nil, []string{noteSkip("missing valid PTS cookies")}, nil
	}
	params := url.Values{"incldead": {"1"}, "search": {imdb}, "search_area": {"4"}}
	resp, root, err := doHTMLGet(ctx, h.http, baseURL+"/torrents.php", params, cookies)
	if err != nil || !resp.ok() {
		return nil, []string{noteSkip("PTS search failed")}, nil
	}
	names := findNodes(root, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "b"
	})
	entries := make([]api.DupeEntry, 0, len(names))
	for _, node := range names {
		name := strings.TrimSpace(nodeTextHTML(node))
		if name == "" {
			continue
		}
		entries = append(entries, api.DupeEntry{Name: name})
	}
	return entries, nil, nil
}
