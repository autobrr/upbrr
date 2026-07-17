// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

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
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if resolution != "2160p" && resolution != "1080p" && resolution != "1080i" && resolution != "720p" {
		return dupe.NotRun(dupe.NotRunUnsupportedContent, "resolution below HDT dupe-check minimum", nil)
	}
	baseURL := hdtBaseURL(s.cfg)
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return dupe.Failed(dupe.FailureInternal, "HDT search failed", err)
	}
	trackerCookies, err := cookies.LoadTrackerHTTPCookies(ctx, s.cfg.MainSettings.DBPath, "HDT", parsed.Hostname())
	if err != nil {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing valid HDT cookies", nil)
	}
	params := url.Values{"active": {"0"}, "category[]": {strconv.Itoa(hdtCategoryID(meta))}}
	if meta.Identity.IMDBID != 0 {
		params.Set("search", fmt.Sprintf("tt%07d", meta.Identity.IMDBID))
		params.Set("options", "2")
	} else {
		params.Set("search", metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName))
		params.Set("options", "3")
	}
	status, root, err := commonhttp.GetHTML(ctx, s.http, baseURL+"/torrents.php", params, trackerCookies)
	if err != nil || status < http.StatusOK || status >= http.StatusMultipleChoices || root == nil {
		return dupe.Failed(dupe.FailureRequest, "HDT search failed", err)
	}
	entries := make([]api.DupeEntry, 0)
	for _, row := range commonhttp.FindNodes(root, func(node *xhtml.Node) bool { return node.Type == xhtml.ElementNode && node.Data == "tr" }) {
		nameNode := commonhttp.FirstNode(row, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(commonhttp.Attr(node, "href"), "details.php?id=")
		})
		if nameNode == nil {
			continue
		}
		entry := api.DupeEntry{Name: strings.TrimSpace(commonhttp.NodeText(nameNode)), Link: commonhttp.AbsoluteURL(baseURL, commonhttp.Attr(nameNode, "href"))}
		for _, cell := range commonhttp.FindNodes(row, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "td" && commonhttp.HasClass(node, "mainblockcontent")
		}) {
			if size, ok := commonhttp.ParseSizeBytes(commonhttp.NodeText(cell)); ok {
				entry.SizeKnown, entry.SizeBytes = true, size
				break
			}
		}
		if entry.Name != "" {
			entries = append(entries, entry)
		}
	}
	return dupe.Resolved(entries, nil)
}

func hdtBaseURL(_ config.Config) string {
	return "https://hd-torrents.me"
}
func hdtCategoryID(meta api.DuplicateSubject) int {
	category := strings.ToUpper(strings.TrimSpace(string(meta.Identity.Category)))
	resolution := strings.TrimSpace(meta.Release.Resolution)
	disc := strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(meta.Type), "DISC")
	remux := strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX")
	uhd := strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && resolution == "2160p"
	if category == "TV" {
		if disc {
			if resolution == "2160p" {
				return 72
			}
			return 59
		}
		if remux {
			if uhd {
				return 73
			}
			return 60
		}
		if resolution == "2160p" {
			return 65
		}
		if resolution == "1080p" || resolution == "1080i" {
			return 30
		}
		return 38
	}
	if disc {
		if resolution == "2160p" {
			return 70
		}
		return 1
	}
	if remux {
		if uhd {
			return 71
		}
		return 2
	}
	if resolution == "2160p" {
		return 64
	}
	if resolution == "1080p" || resolution == "1080i" {
		return 5
	}
	return 3
}
