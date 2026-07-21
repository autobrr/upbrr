// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

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
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

var hdsPagePattern = regexp.MustCompile(`pages=(\d+)`)

type dupeSearcher struct {
	cfg  config.Config
	http *http.Client
}

func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{cfg: cfg, http: httpClient}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if resolution != "2160p" && resolution != "1080p" && resolution != "1080i" && resolution != "720p" {
		return dupe.NotRun(dupe.NotRunUnsupportedContent, "resolution below HDS dupe-check minimum", nil)
	}
	if meta.Identity.IMDBID == 0 {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing IMDb ID for HDS dupe search", nil)
	}
	baseURL := hdsBaseURL(s.cfg)
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return dupe.Failed(dupe.FailureInternal, "HDS search failed", err)
	}
	trackerCookies, err := cookies.LoadTrackerHTTPCookies(ctx, s.cfg.MainSettings.DBPath, "HDS", parsed.Hostname())
	if err != nil {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing valid HDS cookies", nil)
	}
	entries := make([]api.DupeEntry, 0)
	for page := 0; page <= 10; page++ {
		params := url.Values{
			"page":    {"torrents"},
			"search":  {fmt.Sprintf("tt%07d", meta.Identity.IMDBID)},
			"active":  {"0"},
			"options": {"2"},
			"pages":   {strconv.Itoa(page)},
		}
		status, body, err := commonhttp.GetText(ctx, s.http, baseURL+"/index.php", params, trackerCookies)
		if err != nil || status < http.StatusOK || status >= http.StatusMultipleChoices {
			return dupe.Failed(dupe.FailureRequest, "HDS search failed", err)
		}
		parts := strings.SplitN(body, "Show/Hide Categories", 2)
		if len(parts) < 2 {
			break
		}
		root, err := xhtml.Parse(strings.NewReader(parts[1]))
		if err != nil {
			return dupe.Failed(dupe.FailureResponseParse, "HDS response parse failed", err)
		}
		before := len(entries)
		for _, row := range commonhttp.FindNodes(root, func(node *xhtml.Node) bool { return node.Type == xhtml.ElementNode && node.Data == "tr" }) {
			nameNode := commonhttp.FirstNode(row, func(node *xhtml.Node) bool {
				return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(commonhttp.Attr(node, "href"), "page=torrent-details")
			})
			if nameNode == nil {
				continue
			}
			entry := api.DupeEntry{
				Name: metautil.FirstNonEmptyTrimmed(commonhttp.NodeText(nameNode), commonhttp.Attr(nameNode, "title")),
				Link: commonhttp.AbsoluteURL(baseURL, commonhttp.Attr(nameNode, "href")),
			}
			for _, cell := range commonhttp.FindNodes(row, func(node *xhtml.Node) bool {
				return node.Type == xhtml.ElementNode && node.Data == "td" && commonhttp.HasClass(node, "lista")
			}) {
				if size, ok := commonhttp.ParseSizeBytes(commonhttp.NodeText(cell)); ok {
					entry.SizeText = strings.TrimSpace(commonhttp.NodeText(cell))
					entry.SizeKnown, entry.SizeBytes = true, size
					break
				}
			}
			if entry.Name != "" {
				entries = append(entries, entry)
			}
		}
		next := commonhttp.FirstNode(root, func(node *xhtml.Node) bool {
			if node.Type != xhtml.ElementNode || node.Data != "a" {
				return false
			}
			href, text := commonhttp.Attr(node, "href"), strings.TrimSpace(commonhttp.NodeText(node))
			return strings.Contains(href, "pages=") && (strings.EqualFold(text, "Next") || text == ">>" || hdsPagePattern.MatchString(href))
		})
		if next == nil || len(entries) == before {
			break
		}
	}
	return dupe.Resolved(entries, nil)
}

func hdsBaseURL(_ config.Config) string {
	return "https://hd-space.org"
}
