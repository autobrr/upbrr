// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/pkg/api"
)

const azCookieUserAgent = "upbrr"

type sessionState struct {
	client *http.Client
	token  string
}

func newSession(ctx context.Context, site siteDefinition, dbPath string, logger api.Logger) (sessionState, error) {
	cookies, err := resolveCookies(ctx, dbPath, site)
	if err != nil {
		return sessionState{}, err
	}
	cookieMap := make(map[string]*http.Cookie, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" {
			continue
		}
		cookieMap[cookie.Name] = cookie
	}
	client := &http.Client{
		Timeout: 40 * time.Second,
		Jar:     simpleCookieJar{baseURL: mustParseURL(site.BaseURL), cookies: cookieMap},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, site.BaseURL+"/torrents", nil)
	if err != nil {
		return sessionState{}, err
	}
	req.Header.Set("User-Agent", azCookieUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return sessionState{}, fmt.Errorf("trackers: %s cookie validation request: %w", site.Name, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if readErr != nil && logger != nil {
		logger.Debugf("trackers: %s cookie validation body read failed status=%d err=%v", site.Name, resp.StatusCode, readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || strings.Contains(strings.ToLower(string(body)), "page not found") {
		return sessionState{}, fmt.Errorf("trackers: %s missing valid cookies", site.Name)
	}
	token := extractPatternGroup(azTokenPattern, string(body))
	if token == "" {
		return sessionState{}, fmt.Errorf("trackers: %s csrf token not found", site.Name)
	}
	return sessionState{client: client, token: token}, nil
}

func lookupMediaCode(ctx context.Context, site siteDefinition, state sessionState, meta api.PreparedMetadata) (mediaLookupResult, error) {
	categoryIDValue := categoryID(meta)
	if categoryIDValue == "" {
		return mediaLookupResult{}, fmt.Errorf("trackers: %s unsupported category", site.Name)
	}

	search := func(term string) ([]map[string]any, error) {
		term = strings.TrimSpace(term)
		if term == "" {
			return nil, nil
		}
		endpoint := fmt.Sprintf("%s/ajax/movies/%s?term=%s", site.BaseURL, categoryIDValue, url.QueryEscape(term))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Referer", site.BaseURL+"/upload/"+categorySlug(meta))
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("User-Agent", azCookieUserAgent)
		resp, err := state.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("status %d", resp.StatusCode)
		}
		var payload struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, err
		}
		return payload.Data, nil
	}

	imdbID := imdbForLookup(meta)
	tmdbID := tmdbForLookup(meta)
	all := make([]map[string]any, 0)
	if imdbID != "" {
		list, err := search(imdbID)
		if err != nil {
			return mediaLookupResult{}, fmt.Errorf("trackers: %s media search by imdb failed: %w", site.Name, err)
		}
		all = append(all, list...)
	}
	if len(all) == 0 {
		list, err := search(lookupTitle(meta))
		if err != nil {
			return mediaLookupResult{}, fmt.Errorf("trackers: %s media search by title failed: %w", site.Name, err)
		}
		all = append(all, list...)
	}
	for _, item := range all {
		if imdbID != "" && strings.EqualFold(stringValue(item["imdb"]), imdbID) {
			return mediaLookupResult{MediaCode: stringValue(item["id"])}, nil
		}
		if tmdbID != "" && strings.EqualFold(stringValue(item["tmdb"]), tmdbID) {
			return mediaLookupResult{MediaCode: stringValue(item["id"])}, nil
		}
	}
	if len(all) > 0 {
		if id := stringValue(all[0]["id"]); id != "" {
			return mediaLookupResult{MediaCode: id}, nil
		}
	}
	return mediaLookupResult{Missing: true}, nil
}

func searchRequests(ctx context.Context, site siteDefinition, state sessionState, meta api.PreparedMetadata) ([]string, error) {
	query := lookupTitle(meta)
	if isTV(meta) {
		query = strings.TrimSpace(query + " " + tvCode(meta))
	}
	if query == "" {
		return nil, nil
	}
	endpoint := fmt.Sprintf("%s?type=%s&search=%s&condition=new", site.RequestsURL, strings.ToLower(categorySlug(meta)), url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", azCookieUserAgent)
	resp, err := state.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil
	}
	root, err := xhtml.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("trackers: AZ parse upload form: %w", err)
	}
	var names []string
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil {
			return
		}
		if node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(attrValue(node, "class"), "torrent-filename") {
			if text := strings.TrimSpace(nodeText(node)); text != "" {
				names = append(names, text)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return names, nil
}
