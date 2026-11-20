// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
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
	ID       int           `json:"id"`
	Name     string        `json:"name"`
	Size     int64         `json:"size"`
	Internal bool          `json:"internal"`
	Badges   []searchBadge `json:"badges"`
}

type searchBadge struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

func loadSessionCookies(ctx context.Context, dbPath string) (map[string]string, error) {
	loaded, err := cookiepkg.LoadTrackerHTTPCookies(ctx, dbPath, sourceFlag, siteHost)
	if err != nil {
		return nil, fmt.Errorf("load ASC session cookies: %w", err)
	}
	cookies := make(map[string]string, len(loaded))
	for _, cookie := range loaded {
		cookies[cookie.Name] = cookie.Value
	}
	return cookies, nil
}

func resolveAuthSession(ctx context.Context, _ config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
	cookies, err := loadSessionCookies(ctx, dbPath)
	if errors.Is(err, cookiepkg.ErrTrackerCookiesNotFound) || (err == nil && len(cookies) == 0) {
		return &trackers.AuthResolutionError{Reason: "ASC cookies missing", AuthRequired: true}
	}
	if err != nil {
		return errors.Join(&trackers.AuthResolutionError{Reason: "could not read ASC cookies", Transient: true}, err)
	}
	result := connectiontest.Run(ctx, func(ctx context.Context, client *http.Client) api.ConnectionTestResult {
		return testConnection(ctx, trackers.ConnectionProbeInput{Cookies: cookies}, client)
	})
	switch result.Code {
	case "success":
		return nil
	case "authentication_required":
		return &trackers.AuthResolutionError{Reason: result.Message, AuthRequired: true}
	default:
		return &trackers.AuthResolutionError{Reason: result.Message, Transient: true}
	}
}

func testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	if len(input.Cookies) == 0 {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Import a current ASC session cookie before testing."}
	}
	page, failure := readPageData(ctx, client, input.Cookies, siteURL+"/dashboard")
	if failure.Code != "" {
		return failure
	}
	var payload struct {
		Props struct {
			Auth struct {
				User struct {
					ID int `json:"id"`
				} `json:"user"`
			} `json:"auth"`
		} `json:"props"`
	}
	if err := json.Unmarshal([]byte(page), &payload); err != nil || payload.Props.Auth.User.ID <= 0 {
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
