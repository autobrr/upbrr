// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

// testConnection reads a protected page with the selected cookie snapshot.
// The logout link is also the upstream integration's session check:
// https://github.com/Jackett/Jackett/blob/077e038ee3f4e822b6699b682a19b502625f46c9/src/Jackett.Common/Indexers/Definitions/ImmortalSeed.cs
// It does not submit forms, follow redirects or persist response cookies.
func testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	if len(input.Cookies) == 0 {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Import current browser cookies before testing this tracker session."}
	}
	headers := connectiontest.CookieHeaders(input.Cookies)
	headers.Set("Accept", "text/html")
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, "https://immortalseed.me/browse.php", nil, headers)
	if failure.Code != "" {
		return failure
	}
	root, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return connectiontest.UnexpectedResponse()
	}
	authenticated := commonhttp.FirstNode(root, func(node *html.Node) bool {
		if node.Type != html.ElementNode || node.Data != "a" {
			return false
		}
		target, err := url.Parse(commonhttp.Attr(node, "href"))
		return err == nil && (target.Path == "logout.php" || target.Path == "/logout.php")
	})
	if authenticated == nil || strings.Contains(strings.ToLower(string(body)), `name="password"`) {
		return connectiontest.UnexpectedResponse()
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "The tracker returned its protected session page using imported cookies. Upload permission was not tested and no session was saved.",
	}
}
