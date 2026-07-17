// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ff

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

var ffGroupPattern = regexp.MustCompile(`torrents\.php\?id=(\d+)`)

type dupeSearcher struct {
	cfg  config.Config
	http *http.Client
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{cfg: cfg, http: httpClient}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	if !meta.Anime && meta.Identity.IMDBID == 0 {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing IMDb ID for FF dupe search", nil)
	}
	baseURL := ffBaseURL(s.cfg)
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return dupe.Failed(dupe.FailureInternal, "FF search failed", err)
	}
	trackerCookies, err := cookies.LoadTrackerHTTPCookies(ctx, s.cfg.MainSettings.DBPath, "FF", parsed.Hostname())
	if err != nil {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing valid FF cookies", nil)
	}
	query := fmt.Sprintf("tt%07d", meta.Identity.IMDBID)
	if meta.Anime {
		query = metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName)
	}
	status, root, err := commonhttp.GetHTML(ctx, s.http, baseURL+"/torrents.php", url.Values{"searchstr": {query}}, trackerCookies)
	if err != nil || status < http.StatusOK || status >= http.StatusMultipleChoices || root == nil {
		return dupe.Failed(dupe.FailureRequest, "FF search failed", err)
	}
	groupLinks := commonhttp.FindNodes(root, func(node *xhtml.Node) bool {
		href := commonhttp.Attr(node, "href")
		return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(href, "torrents.php?id=") && !strings.Contains(href, "torrentid")
	})
	seen := make(map[string]struct{}, len(groupLinks))
	entries := make([]api.DupeEntry, 0)
	for _, linkNode := range groupLinks {
		groupLink := commonhttp.AbsoluteURL(baseURL, commonhttp.Attr(linkNode, "href"))
		if groupLink == "" {
			continue
		}
		if _, ok := seen[groupLink]; ok {
			continue
		}
		seen[groupLink] = struct{}{}
		groupStatus, groupRoot, getErr := commonhttp.GetHTML(ctx, s.http, groupLink, nil, trackerCookies)
		if getErr != nil || groupStatus < http.StatusOK || groupStatus >= http.StatusMultipleChoices || groupRoot == nil {
			continue
		}
		for _, row := range commonhttp.FindNodes(groupRoot, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "tr" && strings.HasPrefix(commonhttp.Attr(node, "id"), "torrent")
		}) {
			link := commonhttp.FirstNode(row, func(node *xhtml.Node) bool {
				return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(commonhttp.Attr(node, "onclick"), "gtoggle")
			})
			if link == nil {
				continue
			}
			entry := api.DupeEntry{Name: strings.TrimSpace(commonhttp.NodeText(link)), Link: groupLink}
			if match := ffGroupPattern.FindStringSubmatch(groupLink); len(match) == 2 {
				entry.ID = match[1]
			}
			if entry.Name != "" {
				entries = append(entries, entry)
			}
		}
	}
	return dupe.Resolved(entries, nil)
}

func ffBaseURL(_ config.Config) string {
	return "https://www.funfile.org"
}
