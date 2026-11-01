// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// ConnectionProbe checks the same protected browse page as session validation,
// using only a read-only cookie snapshot. It never logs in or saves cookies.
func (d *Definition) ConnectionProbe() trackers.ConnectionProbe {
	return trackers.ConnectionProbe{Test: d.testConnection, StoredCookies: true}
}

func (d *Definition) testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	if len(input.Cookies) == 0 {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Import this tracker's browser cookies before testing its session."}
	}
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, d.site.BaseURL+"/torrents", nil, connectiontest.CookieHeaders(input.Cookies))
	if failure.Code != "" {
		return failure
	}
	lower := strings.ToLower(string(body))
	if hasAZCloudflareChallenge(lower) {
		return connectiontest.UnexpectedResponse()
	}
	if hasAZLoginEvidence(&http.Response{StatusCode: http.StatusOK}, lower) {
		return api.ConnectionTestResult{
			Code:    "credentials_rejected",
			Message: "The tracker did not accept the stored session. Import current browser cookies and test again.",
		}
	}
	if strings.TrimSpace(extractPatternGroup(azTokenPattern, string(body))) == "" {
		return connectiontest.UnexpectedResponse()
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "The tracker accepted the stored browser session. No login or upload was attempted; announce credentials were not tested.",
	}
}
