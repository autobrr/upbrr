// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import (
	"context"
	"fmt"
	"math"
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

var isSizePattern = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*(KB|MB|GB|TB|KiB|MiB|GiB|TiB|B)\b`)

type dupeSearcher struct {
	cfg  config.Config
	http *http.Client
}

// newDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{cfg: cfg, http: httpClient}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	baseURL := isBaseURL(s.cfg)
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return dupe.Failed(dupe.FailureInternal, "IS search failed", err)
	}
	trackerCookies, err := cookies.LoadTrackerHTTPCookies(ctx, s.cfg.MainSettings.DBPath, "IS", parsedBase.Hostname())
	if err != nil {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing valid IS cookies", nil)
	}
	params := url.Values{"do": {"search"}}
	category := strings.ToUpper(strings.TrimSpace(string(meta.Identity.Category)))
	if category == "MOVIE" {
		if meta.Identity.IMDBID == 0 {
			return dupe.NotRun(dupe.NotRunMissingMetadata, "missing IMDb ID for IS movie dupe search", nil)
		}
		params.Set("search_type", "t_genre")
		params.Set("keywords", fmt.Sprintf("tt%07d", meta.Identity.IMDBID))
	} else {
		query := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName) + " " + isSeasonEpisode(meta))
		params.Set("search_type", "t_name")
		params.Set("keywords", query)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/browse.php", nil)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "IS search failed", err)
	}
	req.URL.RawQuery = params.Encode()
	req.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(req, trackerCookies)
	resp, err := s.http.Do(req)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "IS search failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return dupe.Failed(dupe.FailureResponseStatus, "IS search failed", nil)
	}
	root, err := xhtml.Parse(resp.Body)
	if err != nil {
		return dupe.Failed(dupe.FailureResponseParse, "IS search failed", err)
	}
	table := isFirst(root, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "table" && isAttr(node, "id") == "sortabletable"
	})
	if table == nil {
		return dupe.Resolved(nil, nil)
	}
	entries := make([]api.DupeEntry, 0)
	for _, row := range isFind(table, func(node *xhtml.Node) bool { return node.Type == xhtml.ElementNode && node.Data == "tr" }) {
		link := isFirst(row, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(isAttr(node, "href"), "details.php?id=")
		})
		if link == nil {
			continue
		}
		entry := api.DupeEntry{Name: strings.TrimSpace(isText(link)), Link: isAbsoluteURL(baseURL, isAttr(link, "href"))}
		cells := isFind(row, func(node *xhtml.Node) bool { return node.Type == xhtml.ElementNode && node.Data == "td" })
		if len(cells) >= 5 {
			if size, ok := isParseSize(isText(cells[4])); ok {
				entry.SizeKnown, entry.SizeBytes = true, size
			}
		}
		if entry.Name != "" {
			entries = append(entries, entry)
		}
	}
	return dupe.Resolved(entries, nil)
}

func isBaseURL(_ config.Config) string {
	return "https://immortalseed.me"
}
func isSeasonEpisode(meta api.DuplicateSubject) string {
	if meta.SeasonInt > 0 && meta.EpisodeInt > 0 {
		return fmt.Sprintf("S%02dE%02d", meta.SeasonInt, meta.EpisodeInt)
	}
	return strings.TrimSpace(meta.SeasonStr + meta.EpisodeStr)
}
func isFind(root *xhtml.Node, match func(*xhtml.Node) bool) []*xhtml.Node {
	result := make([]*xhtml.Node, 0)
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil {
			return
		}
		if match(node) {
			result = append(result, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return result
}
func isFirst(root *xhtml.Node, match func(*xhtml.Node) bool) *xhtml.Node {
	var found *xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil || found != nil {
			return
		}
		if match(node) {
			found = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return found
}
func isAttr(node *xhtml.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}
func isText(node *xhtml.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == xhtml.TextNode {
		return node.Data
	}
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		builder.WriteString(isText(child))
	}
	return builder.String()
}
func isAbsoluteURL(baseURL, value string) string {
	base, baseErr := url.Parse(baseURL)
	ref, refErr := url.Parse(value)
	if baseErr == nil && refErr == nil {
		return base.ResolveReference(ref).String()
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(value, "/")
}
func isParseSize(value string) (int64, bool) {
	match := isSizePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 3 {
		return 0, false
	}
	amount, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, false
	}
	multipliers := map[string]float64{
		"b":   1,
		"kb":  1e3,
		"mb":  1e6,
		"gb":  1e9,
		"tb":  1e12,
		"kib": 1024,
		"mib": 1024 * 1024,
		"gib": 1024 * 1024 * 1024,
		"tib": 1024 * 1024 * 1024 * 1024,
	}
	return int64(math.Round(amount * multipliers[strings.ToLower(match[2])])), true
}
