// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// testConnection verifies a protected page with either a fresh draft login or
// the selected read-only cookie snapshot. Login cookies and auth keys are never
// persisted, and a failed draft login never falls back to stored credentials.
func testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	username, password := strings.TrimSpace(input.Config.Username), strings.TrimSpace(input.Config.Password)
	headers := connectiontest.CookieHeaders(input.Cookies)
	headers.Set("Accept", "text/html")
	if username != "" || password != "" {
		if !connectiontest.CredentialPresent(username) || !connectiontest.CredentialPresent(password) {
			return api.ConnectionTestResult{Code: "missing_configuration", Message: "Enter both username and password to test a fresh login."}
		}
		jar, err := cookiejar.New(nil)
		if err != nil {
			return api.ConnectionTestResult{Code: "unavailable", Message: "A temporary login session could not be created."}
		}
		session := *client
		session.Jar = jar
		client = &session
		headers = http.Header{"Accept": {"text/html"}}
		loginPage, failure := connectiontest.Request(ctx, client, http.MethodGet, baseURL+"/login.php", nil, headers)
		if failure.Code != "" {
			return failure
		}
		match := flAuthValidatorPattern.FindSubmatch(loginPage)
		if len(match) != 2 || strings.TrimSpace(string(match[1])) == "" {
			return connectiontest.UnexpectedResponse()
		}
		failure = connectiontest.SubmitLogin(ctx, client, baseURL+"/takelogin.php", url.Values{
			"validator": {string(match[1])},
			"username":  {username},
			"password":  {password},
			"unlock":    {"1"},
		})
		if failure.Code != "" {
			return failure
		}
	} else if len(input.Cookies) == 0 {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Enter login credentials or import current browser cookies before testing."}
	}
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, baseURL+"/index.php", nil, headers)
	if failure.Code != "" {
		return failure
	}
	page := strings.ToLower(string(body))
	if !strings.Contains(page, "logout") || strings.Contains(page, "login.php") || strings.Contains(page, `name="username"`) ||
		strings.Contains(page, `name="password"`) {
		return connectiontest.UnexpectedResponse()
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "The tracker returned its protected session page. Entered login credentials were used when present; otherwise the imported session was checked. No session was saved and no upload was attempted.",
	}
}
