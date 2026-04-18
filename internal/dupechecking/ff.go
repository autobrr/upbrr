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
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

type ffHandler struct {
	cfg  config.Config
	http *http.Client
}

func (h ffHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	if !meta.Anime && imdbForLookup(meta) == "" {
		return nil, []string{noteSkip("missing IMDb ID for FF dupe search")}, nil
	}
	cookies, err := loadTrackerCookies(ctx, h.cfg, "FF", trackerHost(trackerBaseURL(h.cfg, "FF", "https://www.funfile.org"), "funfile.org"))
	if err != nil {
		return nil, []string{noteSkip("missing valid FF cookies")}, nil
	}
	query := imdbForLookup(meta)
	if meta.Anime {
		query = metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName)
	}
	resp, root, err := doHTMLGet(ctx, h.http, trackerBaseURL(h.cfg, "FF", "https://www.funfile.org")+"/torrents.php", url.Values{"searchstr": {query}}, nil, cookies)
	if err != nil || resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, []string{noteSkip("FF search failed")}, nil
	}
	groupLinks := findNodes(root, func(node *xhtml.Node) bool {
		href := attrValueHTML(node, "href")
		return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(href, "torrents.php?id=") && !strings.Contains(href, "torrentid")
	})
	groupRe := regexp.MustCompile(`torrents\.php\?id=(\d+)`)
	seen := map[string]struct{}{}
	entries := make([]api.DupeEntry, 0)
	baseURL := trackerBaseURL(h.cfg, "FF", "https://www.funfile.org")
	for _, linkNode := range groupLinks {
		groupLink := absoluteURL(baseURL, attrValueHTML(linkNode, "href"))
		if groupLink == "" {
			continue
		}
		if _, ok := seen[groupLink]; ok {
			continue
		}
		seen[groupLink] = struct{}{}
		groupResp, groupRoot, err := doHTMLGet(ctx, h.http, groupLink, nil, nil, cookies)
		if err != nil || groupResp == nil || groupResp.StatusCode < 200 || groupResp.StatusCode >= 300 {
			continue
		}
		torrents := findNodes(groupRoot, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "tr" && strings.HasPrefix(attrValueHTML(node, "id"), "torrent")
		})
		for _, row := range torrents {
			link := firstNode(row, func(node *xhtml.Node) bool {
				return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(attrValueHTML(node, "onclick"), "gtoggle")
			})
			if link == nil {
				continue
			}
			entry := api.DupeEntry{
				Name: strings.TrimSpace(nodeTextHTML(link)),
				Link: groupLink,
			}
			if match := groupRe.FindStringSubmatch(groupLink); len(match) == 2 {
				entry.ID = match[1]
			}
			if entry.Name != "" {
				entries = append(entries, entry)
			}
		}
	}
	return entries, nil, nil
}
