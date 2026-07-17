// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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
	baseURL := flBaseURL(s.cfg)
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return dupe.Failed(dupe.FailureInternal, "FL search failed", err)
	}
	trackerCookies, err := cookies.LoadTrackerHTTPCookies(ctx, s.cfg.MainSettings.DBPath, "FL", parsedBase.Hostname())
	if err != nil {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing valid FL cookies", nil)
	}
	params := url.Values{"cat": {strconv.Itoa(flCategoryID(meta))}}
	if meta.Identity.IMDBID != 0 {
		params.Set("search", fmt.Sprintf("tt%07d", meta.Identity.IMDBID))
		params.Set("searchin", "3")
	} else {
		query := metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName)
		if query == "" {
			return dupe.NotRun(dupe.NotRunMissingMetadata, "missing FL search query", nil)
		}
		params.Set("search", query)
		params.Set("searchin", "0")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/browse.php", nil)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "FL search failed", err)
	}
	req.URL.RawQuery = params.Encode()
	req.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(req, trackerCookies)
	resp, err := s.http.Do(req)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "FL search failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return dupe.Failed(dupe.FailureResponseStatus, "FL search failed", nil)
	}
	root, err := xhtml.Parse(resp.Body)
	if err != nil {
		return dupe.Failed(dupe.FailureResponseParse, "FL search failed", err)
	}
	entries := make([]api.DupeEntry, 0)
	flWalk(root, func(node *xhtml.Node) {
		if node.Type != xhtml.ElementNode || node.Data != "a" {
			return
		}
		href := flAttr(node, "href")
		if !strings.HasPrefix(href, "details.php?id=") || strings.Contains(href, "&") {
			return
		}
		name := metautil.FirstNonEmptyTrimmed(flAttr(node, "title"), flText(node))
		if name == "" {
			return
		}
		link := flAbsoluteURL(baseURL, href)
		id := ""
		if parsed, parseErr := url.Parse(link); parseErr == nil {
			id = strings.TrimSpace(parsed.Query().Get("id"))
		}
		entries = append(entries, api.DupeEntry{
			Name: name,
			ID:   id,
			Link: link,
		})
	})
	return dupe.Resolved(entries, nil)
}

func flBaseURL(_ config.Config) string {
	return "https://filelist.io"
}
func flCategoryID(meta api.DuplicateSubject) int {
	if meta.Anime {
		return 24
	}
	category := strings.ToUpper(strings.TrimSpace(string(meta.Identity.Category)))
	resolution := strings.TrimSpace(meta.Release.Resolution)
	switch {
	case strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD"):
		return 2
	case category == "TV":
		if resolution == "2160p" {
			return 27
		}
		if resolution == "480p" || resolution == "576p" {
			return 23
		}
		return 21
	default:
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
			if resolution == "2160p" {
				return 26
			}
			return 20
		}
		if resolution == "2160p" {
			return 6
		}
		if resolution == "480p" || resolution == "576p" {
			return 1
		}
		return 4
	}
}
func flWalk(node *xhtml.Node, visit func(*xhtml.Node)) {
	if node == nil {
		return
	}
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		flWalk(child, visit)
	}
}
func flAttr(node *xhtml.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}
func flText(node *xhtml.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == xhtml.TextNode {
		return node.Data
	}
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		builder.WriteString(flText(child))
	}
	return strings.TrimSpace(builder.String())
}
func flAbsoluteURL(baseURL, value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(value, "/")
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(value, "/")
	}
	return base.ResolveReference(parsed).String()
}
