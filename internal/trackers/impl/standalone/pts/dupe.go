// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pts

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
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
	if meta.Identity.IMDBID == 0 {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing IMDb ID for PTS dupe search", nil)
	}
	baseURL := ptsBaseURL(s.cfg)
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return dupe.Failed(dupe.FailureInternal, "PTS search failed", err)
	}
	trackerCookies, err := cookies.LoadTrackerHTTPCookies(ctx, s.cfg.MainSettings.DBPath, "PTS", parsed.Hostname())
	if err != nil {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing valid PTS cookies", nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/torrents.php", nil)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "PTS search failed", err)
	}
	req.URL.RawQuery = url.Values{
		"incldead":    {"1"},
		"search":      {fmt.Sprintf("tt%07d", meta.Identity.IMDBID)},
		"search_area": {"4"},
	}.Encode()
	req.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(req, trackerCookies)
	resp, err := s.http.Do(req)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "PTS search failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return dupe.Failed(dupe.FailureResponseStatus, "PTS search failed", nil)
	}
	root, err := xhtml.Parse(resp.Body)
	if err != nil {
		return dupe.Failed(dupe.FailureResponseParse, "PTS search failed", err)
	}
	entries := make([]api.DupeEntry, 0)
	ptsWalk(root, func(node *xhtml.Node) {
		if node.Type != xhtml.ElementNode || node.Data != "b" {
			return
		}
		if name := strings.TrimSpace(ptsNodeText(node)); name != "" {
			entries = append(entries, api.DupeEntry{Name: name})
		}
	})
	return dupe.Resolved(entries, nil)
}

func ptsBaseURL(_ config.Config) string {
	return "https://www.ptskit.org"
}

func ptsWalk(node *xhtml.Node, visit func(*xhtml.Node)) {
	if node == nil {
		return
	}
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		ptsWalk(child, visit)
	}
}

func ptsNodeText(node *xhtml.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == xhtml.TextNode {
		return node.Data
	}
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		builder.WriteString(ptsNodeText(child))
	}
	return builder.String()
}
