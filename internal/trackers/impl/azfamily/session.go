// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	azCookieUserAgent     = "upbrr"
	azMediaLookupAttempts = 6
	azMediaLookupDelay    = time.Second
)

type sessionState struct {
	client *http.Client
	token  string
}

func newSession(ctx context.Context, site siteDefinition, dbPath string) (sessionState, error) {
	cookies, err := resolveCookies(ctx, dbPath, site)
	if err != nil {
		return sessionState{}, err
	}
	jar, err := newSessionCookieJar(site.BaseURL, cookies)
	if err != nil {
		return sessionState{}, fmt.Errorf("trackers: %s cookie jar: %w", site.Name, err)
	}
	client := &http.Client{
		Timeout: 40 * time.Second,
		Jar:     jar,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, site.BaseURL+"/torrents", nil)
	if err != nil {
		return sessionState{}, fmt.Errorf("trackers: %s cookie validation request build: %w", site.Name, err)
	}
	req.Header.Set("User-Agent", azCookieUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return sessionState{}, fmt.Errorf("trackers: %s cookie validation request: %w", site.Name, err)
	}
	defer resp.Body.Close()
	body, err := readAZAuthResponse(site, resp)
	if err != nil {
		return sessionState{}, err
	}
	token, err := validateAZAuthResponse(site, resp, body)
	if err != nil {
		return sessionState{}, err
	}
	return sessionState{client: client, token: token}, nil
}

func lookupMediaCode(ctx context.Context, site siteDefinition, state sessionState, meta api.UploadSubject) (mediaLookupResult, error) {
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
			return nil, fmt.Errorf("trackers: %s media search request build: %w", site.Name, err)
		}
		req.Header.Set("Referer", site.BaseURL+"/upload/"+categorySlug(meta))
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("User-Agent", azCookieUserAgent)
		resp, err := state.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("trackers: %s media search request: %w", site.Name, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("status %d", resp.StatusCode)
		}
		var payload struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, fmt.Errorf("trackers: %s decode media search response: %w", site.Name, err)
		}
		return payload.Data, nil
	}

	imdbID := imdbForLookup(meta)
	tmdbID := tmdbForLookup(meta)
	tvdbID := tvdbForLookup(meta)
	for _, lookup := range []struct {
		provider string
		value    string
	}{
		{provider: "imdb", value: imdbID},
		{provider: "tmdb", value: tmdbID},
		{provider: "tvdb", value: tvdbID},
	} {
		if lookup.value == "" {
			continue
		}
		list, err := search(lookup.value)
		if err != nil {
			return mediaLookupResult{}, fmt.Errorf("trackers: %s media search by %s failed: %w", site.Name, lookup.provider, err)
		}
		for _, item := range list {
			if mediaItemMatchesIDs(item, imdbID, tmdbID, tvdbID) {
				if id := stringValue(item["id"]); id != "" {
					return mediaLookupResult{MediaCode: id}, nil
				}
			}
		}
	}

	titleResults, err := search(lookupTitle(meta))
	if err != nil {
		return mediaLookupResult{}, fmt.Errorf("trackers: %s media search by title failed: %w", site.Name, err)
	}
	for _, item := range titleResults {
		if mediaItemMatchesIDs(item, imdbID, tmdbID, tvdbID) {
			if id := stringValue(item["id"]); id != "" {
				return mediaLookupResult{MediaCode: id}, nil
			}
		}
	}
	return mediaLookupResult{Missing: true}, nil
}

func addMissingMedia(ctx context.Context, site siteDefinition, state sessionState, meta api.UploadSubject, logger api.Logger) (string, error) {
	if existing, err := lookupMediaCode(ctx, site, state, meta); err != nil {
		return "", err
	} else if !existing.Missing && strings.TrimSpace(existing.MediaCode) != "" {
		return existing.MediaCode, nil
	}

	title := lookupTitle(meta)
	if title == "" {
		return "", fmt.Errorf("trackers: %s media title is required", site.Name)
	}
	values := url.Values{
		"_token":  {state.token},
		"type_id": {categoryID(meta)},
		"title":   {title},
		"imdb_id": {imdbForLookup(meta)},
		"tmdb_id": {tmdbForLookup(meta)},
	}
	if tvdbID := tvdbForLookup(meta); tvdbID != "" {
		values.Set("tvdb_id", tvdbID)
	}
	if logger != nil {
		logger.Infof("trackers: %s media database add start category=%s", site.Name, categorySlug(meta))
	}
	resp, err := postForm(
		ctx,
		noRedirectClient(state.client),
		site.BaseURL+"/add/"+categorySlug(meta),
		values,
		map[string]string{
			"Referer":    site.BaseURL + "/upload",
			"User-Agent": azCookieUserAgent,
		},
	)
	if err != nil {
		return "", fmt.Errorf("trackers: %s add media: %w", site.Name, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		if existing, lookupErr := lookupMediaCode(ctx, site, state, meta); lookupErr == nil &&
			!existing.Missing && strings.TrimSpace(existing.MediaCode) != "" {
			return existing.MediaCode, nil
		}
		//logpolicy:allow HTTP status is safe response metadata; no response body is included.
		return "", fmt.Errorf("trackers: %s add media failed status=%d", site.Name, resp.StatusCode)
	}

	for attempt := range azMediaLookupAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("trackers: %s wait for added media: %w", site.Name, ctx.Err())
			case <-time.After(azMediaLookupDelay):
			}
		}
		media, lookupErr := lookupMediaCode(ctx, site, state, meta)
		if lookupErr != nil {
			return "", lookupErr
		}
		if !media.Missing && strings.TrimSpace(media.MediaCode) != "" {
			if logger != nil {
				logger.Infof("trackers: %s media database add completed category=%s", site.Name, categorySlug(meta))
			}
			return media.MediaCode, nil
		}
	}
	return "", fmt.Errorf("trackers: %s added media did not become searchable", site.Name)
}

// mediaItemMatchesIDs reports whether an item matches any supplied provider ID.
func mediaItemMatchesIDs(item map[string]any, imdbID, tmdbID, tvdbID string) bool {
	return (imdbID != "" && strings.EqualFold(stringValue(item["imdb"]), imdbID)) ||
		(tmdbID != "" && strings.EqualFold(stringValue(item["tmdb"]), tmdbID)) ||
		(tvdbID != "" && strings.EqualFold(stringValue(item["tvdb"]), tvdbID))
}

func searchRequests(ctx context.Context, site siteDefinition, state sessionState, meta api.UploadSubject) ([]string, error) {
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
		return nil, fmt.Errorf("trackers: %s request search request build: %w", site.Name, err)
	}
	req.Header.Set("User-Agent", azCookieUserAgent)
	resp, err := state.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trackers: %s request search request: %w", site.Name, err)
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
