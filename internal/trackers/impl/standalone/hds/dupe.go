// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
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
	pages := 0
	complete := false
	warning := ""
	for page := 0; page <= 10; page++ {
		params := url.Values{
			"page":    {"torrents"},
			"search":  {providerid.IMDb(meta.Identity.IMDBID).Prefixed()},
			"active":  {"0"},
			"options": {"2"},
			"pages":   {strconv.Itoa(page)},
		}
		status, body, err := commonhttp.GetText(ctx, s.http, baseURL+"/index.php", params, trackerCookies)
		if err != nil || status < http.StatusOK || status >= http.StatusMultipleChoices {
			return dupe.Failed(dupe.FailureRequest, "HDS search failed", err)
		}
		pages++
		parts := strings.SplitN(body, "Show/Hide Categories", 2)
		if len(parts) < 2 {
			return dupe.Failed(dupe.FailureResponseParse, "HDS response parse failed", nil)
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
		if !hdsHasNextPage(root, page) {
			complete = true
			break
		}
		if len(entries) == before {
			warning = "HDS search made no pagination progress"
			break
		}
	}
	warnings := []string(nil)
	if !complete {
		if warning == "" {
			warning = "HDS search reached pagination safety bound"
		}
		warnings = []string{warning}
	}
	return dupe.ResolvedWithSearch(entries, nil, dupe.SearchEvidence{
		Complete:  complete,
		WorkScope: dupe.WorkScopeProviderID,
		Pages:     pages,
		Scope:     "provider_all_categories",
		Warnings:  warnings,
	})
}

func hdsHasNextPage(root *xhtml.Node, currentPage int) bool {
	return commonhttp.FirstNode(root, func(node *xhtml.Node) bool {
		if node.Type != xhtml.ElementNode || node.Data != "a" {
			return false
		}
		href, text := commonhttp.Attr(node, "href"), strings.TrimSpace(commonhttp.NodeText(node))
		if strings.EqualFold(text, "Next") || text == ">>" {
			return strings.Contains(href, "pages=")
		}
		match := hdsPagePattern.FindStringSubmatch(href)
		if len(match) != 2 {
			return false
		}
		targetPage, err := strconv.Atoi(match[1])
		return err == nil && targetPage > currentPage
	}) != nil
}

func hdsBaseURL(_ config.Config) string {
	return "https://hd-space.org"
}
