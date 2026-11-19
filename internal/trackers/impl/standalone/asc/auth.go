// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/connectiontest"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	siteHost = "amigos-share.club"
	siteURL  = "https://" + siteHost
)

type searchPage struct {
	CurrentPage int         `json:"current_page"`
	LastPage    int         `json:"last_page"`
	Rows        []searchRow `json:"data"`
}

type searchRow struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Internal bool   `json:"internal"`
}

func loadSessionCookies(ctx context.Context, dbPath string) (map[string]string, error) {
	loaded, err := cookiepkg.LoadTrackerHTTPCookies(ctx, dbPath, sourceFlag, siteHost)
	cookies := make(map[string]string, len(loaded))
	for _, cookie := range loaded {
		cookies[cookie.Name] = cookie.Value
	}
	return cookies, err
}

func testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	if len(input.Cookies) == 0 {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Import a current ASC session cookie before testing."}
	}
	page, failure := readPageData(ctx, client, input.Cookies, siteURL+"/dashboard")
	if failure.Code != "" {
		return failure
	}
	// Jackett's ASC definition checks the signed-in user marker on this page:
	// https://github.com/Jackett/Jackett/pull/17100
	if !strings.Contains(page, `"user":{"id":`) {
		return connectiontest.UnexpectedResponse()
	}
	return api.ConnectionTestResult{Code: "success", Message: "ASC returned its authenticated dashboard."}
}

func readSearchPage(ctx context.Context, client *http.Client, cookies map[string]string, query string, page int) (searchPage, api.ConnectionTestResult) {
	data, failure := readPageData(ctx, client, cookies, buildASCSearchURL(query, page))
	if failure.Code != "" {
		return searchPage{}, failure
	}
	var payload struct {
		Props struct {
			Torrents searchPage `json:"torrents"`
		} `json:"props"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return searchPage{}, connectiontest.UnexpectedResponse()
	}
	result := payload.Props.Torrents
	if result.CurrentPage != page || result.LastPage < page || result.Rows == nil ||
		(len(result.Rows) == 0 && page < result.LastPage) {
		return searchPage{}, connectiontest.UnexpectedResponse()
	}
	for _, row := range result.Rows {
		if row.ID <= 0 || strings.TrimSpace(row.Name) == "" || row.Size < 0 {
			return searchPage{}, connectiontest.UnexpectedResponse()
		}
	}
	return result, api.ConnectionTestResult{}
}

func readPageData(ctx context.Context, client *http.Client, cookies map[string]string, endpoint string) (string, api.ConnectionTestResult) {
	readClient := *client
	readClient.Jar = nil
	readClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	headers := connectiontest.CookieHeaders(cookies)
	headers.Set("Accept", "text/html")
	body, failure := connectiontest.Request(ctx, &readClient, http.MethodGet, endpoint, nil, headers)
	if failure.Code != "" {
		return "", failure
	}
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", connectiontest.UnexpectedResponse()
	}
	script := commonhttp.FirstNode(doc, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "script" &&
			commonhttp.Attr(node, "data-page") == "app" && commonhttp.Attr(node, "type") == "application/json"
	})
	if script == nil || script.FirstChild == nil {
		return "", connectiontest.UnexpectedResponse()
	}
	return script.FirstChild.Data, api.ConnectionTestResult{}
}
