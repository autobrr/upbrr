// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

var thrNamePattern = regexp.MustCompile(`overlibImage\('(.+?)','/images`)

type dupeSearcher struct {
	cfg  config.Config
	http *http.Client
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func (Definition) NewDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{cfg: cfg, http: httpClient}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	if meta.Identity.IMDBID == 0 {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing IMDb ID for THR dupe search", nil)
	}
	trackerCfg, ok := thrConfig(s.cfg)
	if !ok || strings.TrimSpace(trackerCfg.Username) == "" || strings.TrimSpace(trackerCfg.Password) == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing THR username/password", nil)
	}
	baseURL := thrBaseURL(trackerCfg)
	sessionCookies, err := thrLogin(ctx, s.http, baseURL, trackerCfg.Username, trackerCfg.Password)
	if err != nil {
		return dupe.Failed(dupe.FailureAuthentication, "THR login failed", err)
	}
	entries := make([]api.DupeEntry, 0)
	for page := 0; page <= 10; page++ {
		params := url.Values{
			"search":   {fmt.Sprintf("tt%07d", meta.Identity.IMDBID)},
			"blah":     {"2"},
			"incldead": {"1"},
		}
		if page > 0 {
			params.Set("page", strconv.Itoa(page))
		}
		status, root, err := commonhttp.GetHTML(ctx, s.http, baseURL+"/browse.php", params, sessionCookies)
		if err != nil || status < http.StatusOK || status >= http.StatusMultipleChoices || root == nil {
			return dupe.Failed(dupe.FailureRequest, "THR search failed", err)
		}
		before := len(entries)
		for _, link := range commonhttp.FindNodes(root, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "a" && strings.HasPrefix(commonhttp.Attr(node, "href"), "details.php")
		}) {
			name := thrName(commonhttp.Attr(link, "onmousemove"))
			if name == "" {
				name = strings.TrimSpace(commonhttp.NodeText(link))
			}
			if name != "" {
				entries = append(entries, api.DupeEntry{Name: name, Link: commonhttp.AbsoluteURL(baseURL, commonhttp.Attr(link, "href"))})
			}
		}
		next := commonhttp.FirstNode(root, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(strings.ToLower(commonhttp.NodeText(node)), "next")
		})
		if next == nil || len(entries) == before {
			break
		}
	}
	return dupe.Resolved(entries, nil)
}

func thrLogin(ctx context.Context, client *http.Client, baseURL, username, password string) ([]*http.Cookie, error) {
	status, root, err := commonhttp.GetHTML(ctx, client, baseURL+"/login.php", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch login page: %w", err)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices || root == nil {
		return nil, fmt.Errorf("fetch login page: status=%d", status)
	}
	form := url.Values{}
	for _, input := range commonhttp.FindNodes(root, func(node *xhtml.Node) bool { return node.Type == xhtml.ElementNode && node.Data == "input" }) {
		if name := commonhttp.Attr(input, "name"); name != "" {
			form.Set(name, commonhttp.Attr(input, "value"))
		}
	}
	form.Set("username", strings.TrimSpace(username))
	form.Set("password", strings.TrimSpace(password))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/takelogin.php", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("trackers: create THR login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", baseURL+"/login.php")
	req.Header.Set("User-Agent", "upbrr")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trackers: submit THR login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	if len(resp.Cookies()) == 0 {
		return nil, errors.New("no cookies returned")
	}
	return resp.Cookies(), nil
}

func thrConfig(cfg config.Config) (config.TrackerConfig, bool) {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "THR") {
			return entry, true
		}
	}
	return config.TrackerConfig{}, false
}
func thrBaseURL(_ config.TrackerConfig) string {
	return "https://www.torrenthr.org"
}
func thrName(value string) string {
	match := thrNamePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}
