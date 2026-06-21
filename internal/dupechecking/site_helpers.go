// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

var sizePattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*([kmgt]?i?b)`)
var thrNamePattern = regexp.MustCompile(`overlibImage\('(.+?)','/images`)

type getResponseInfo struct {
	StatusCode int
	FinalURL   string
}

func newGetResponseInfo(resp *http.Response) getResponseInfo {
	info := getResponseInfo{StatusCode: resp.StatusCode}
	if resp.Request != nil && resp.Request.URL != nil {
		info.FinalURL = resp.Request.URL.String()
	}
	return info
}

func (info getResponseInfo) ok() bool {
	return info.StatusCode >= 200 && info.StatusCode < 300
}

func trackerBaseURL(cfg config.Config, tracker string, fallback string) string {
	if trackerCfg, ok := trackerCfg(cfg, tracker); ok && strings.TrimSpace(trackerCfg.URL) != "" {
		trimmed := strings.TrimSpace(trackerCfg.URL)
		if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			return strings.TrimRight(parsed.String(), "/")
		}
		return strings.TrimRight(trimmed, "/")
	}
	return strings.TrimRight(strings.TrimSpace(fallback), "/")
}

func trackerHost(baseURL string, fallback string) string {
	if parsed, err := url.Parse(strings.TrimSpace(baseURL)); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return strings.TrimSpace(fallback)
}

func loadTrackerCookies(ctx context.Context, cfg config.Config, tracker string, domain string) ([]*http.Cookie, error) {
	loaded, err := cookies.LoadTrackerHTTPCookies(ctx, cfg.MainSettings.DBPath, tracker, domain)
	if err != nil {
		return nil, fmt.Errorf("dupechecking: load tracker cookies: %w", err)
	}
	return loaded, nil
}

func doHTMLGet(ctx context.Context, client *http.Client, endpoint string, params url.Values, cookies []*http.Cookie) (getResponseInfo, *xhtml.Node, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return getResponseInfo{}, nil, fmt.Errorf("dupechecking: create HTML GET request: %w", err)
	}
	if len(params) > 0 {
		req.URL.RawQuery = params.Encode()
	}
	req.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(req, cookies)
	resp, err := client.Do(req)
	if err != nil {
		return getResponseInfo{}, nil, fmt.Errorf("dupechecking: HTML GET request: %w", err)
	}
	info := newGetResponseInfo(resp)
	root, err := xhtml.Parse(resp.Body)
	if closeErr := resp.Body.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return info, nil, err
	}
	return info, root, nil
}

func doTextGet(ctx context.Context, client *http.Client, endpoint string, params url.Values, headers map[string]string, cookies []*http.Cookie) (getResponseInfo, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return getResponseInfo{}, "", fmt.Errorf("dupechecking: create text GET request: %w", err)
	}
	if len(params) > 0 {
		req.URL.RawQuery = params.Encode()
	}
	req.Header.Set("User-Agent", "upbrr")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	commonhttp.ApplyCookies(req, cookies)
	resp, err := client.Do(req)
	if err != nil {
		return getResponseInfo{}, "", fmt.Errorf("dupechecking: text GET request: %w", err)
	}
	info := newGetResponseInfo(resp)
	body, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return info, "", err
	}
	return info, string(body), nil
}

func absoluteURL(baseURL string, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(trimmed, "/")
	}
	ref, err := url.Parse(trimmed)
	if err != nil {
		return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(trimmed, "/")
	}
	return base.ResolveReference(ref).String()
}

func findNodes(root *xhtml.Node, match func(*xhtml.Node) bool) []*xhtml.Node {
	nodes := make([]*xhtml.Node, 0)
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil {
			return
		}
		if match(node) {
			nodes = append(nodes, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return nodes
}

func firstNode(root *xhtml.Node, match func(*xhtml.Node) bool) *xhtml.Node {
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

func hasClass(node *xhtml.Node, want string) bool {
	for className := range strings.FieldsSeq(attrValueHTML(node, "class")) {
		if strings.EqualFold(strings.TrimSpace(className), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func parseSizeBytes(value string) (int64, bool) {
	match := sizePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 3 {
		return 0, false
	}
	amount, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, false
	}
	multiplier := float64(1)
	switch strings.ToLower(strings.TrimSpace(match[2])) {
	case "kb":
		multiplier = 1000
	case "mb":
		multiplier = 1000 * 1000
	case "gb":
		multiplier = 1000 * 1000 * 1000
	case "tb":
		multiplier = 1000 * 1000 * 1000 * 1000
	case "kib":
		multiplier = 1024
	case "mib":
		multiplier = 1024 * 1024
	case "gib":
		multiplier = 1024 * 1024 * 1024
	case "tib":
		multiplier = 1024 * 1024 * 1024 * 1024
	case "b":
		multiplier = 1
	}
	return int64(math.Round(amount * multiplier)), true
}

func addSize(entry *api.DupeEntry, value string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	entry.SizeText = trimmed
	if sizeBytes, ok := parseSizeBytes(trimmed); ok {
		entry.SizeKnown = true
		entry.SizeBytes = sizeBytes
	}
}

func categoryOfSiteMeta(meta api.PreparedMetadata) string {
	return strings.ToUpper(strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.ExternalIDs.Category, meta.MediaInfoCategory)))
}

func resolveSeasonEpisodeQuery(meta api.PreparedMetadata) string {
	if meta.SeasonInt > 0 && meta.EpisodeInt > 0 {
		return fmt.Sprintf("S%02dE%02d", meta.SeasonInt, meta.EpisodeInt)
	}
	return strings.TrimSpace(meta.SeasonStr + meta.EpisodeStr)
}

func resolveFLCategoryID(meta api.PreparedMetadata) int {
	if meta.Anime {
		return 24
	}
	category := categoryOfSiteMeta(meta)
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

func resolveHDTCategoryID(meta api.PreparedMetadata) int {
	category := categoryOfSiteMeta(meta)
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if category == "TV" {
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") {
			if resolution == "2160p" {
				return 72
			}
			return 59
		}
		if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
			if strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && resolution == "2160p" {
				return 73
			}
			return 60
		}
		switch resolution {
		case "2160p":
			return 65
		case "1080p", "1080i":
			return 30
		default:
			return 38
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") {
		if resolution == "2160p" {
			return 70
		}
		return 1
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		if strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && resolution == "2160p" {
			return 71
		}
		return 2
	}
	switch resolution {
	case "2160p":
		return 64
	case "1080p", "1080i":
		return 5
	default:
		return 3
	}
}

func loginTHR(ctx context.Context, client *http.Client, baseURL string, username string, password string) ([]*http.Cookie, error) {
	resp, root, err := doHTMLGet(ctx, client, baseURL+"/login.php", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch login page: %w", err)
	}
	if !resp.ok() {
		return nil, fmt.Errorf("fetch login page: status=%d", resp.StatusCode)
	}
	form := url.Values{}
	inputs := findNodes(root, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "input"
	})
	for _, input := range inputs {
		name := strings.TrimSpace(attrValueHTML(input, "name"))
		if name == "" {
			continue
		}
		form.Set(name, strings.TrimSpace(attrValueHTML(input, "value")))
	}
	form.Set("username", strings.TrimSpace(username))
	form.Set("password", strings.TrimSpace(password))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/takelogin.php", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("dupechecking: create THR login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", baseURL+"/login.php")
	req.Header.Set("User-Agent", "upbrr")
	loginResp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dupechecking: submit THR login: %w", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode < 200 || loginResp.StatusCode >= 400 {
		return nil, fmt.Errorf("status %d", loginResp.StatusCode)
	}
	cookies := loginResp.Cookies()
	if len(cookies) == 0 {
		return nil, errors.New("no cookies returned")
	}
	return cookies, nil
}

func parseTHRName(onMouseMove string) string {
	match := thrNamePattern.FindStringSubmatch(strings.TrimSpace(onMouseMove))
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}
