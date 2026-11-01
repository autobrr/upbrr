// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"context"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// testConnection reads the protected form used by fetchAuth with the selected
// stored session. It does not submit the form or save returned cookies.
func testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	if len(input.Cookies) == 0 {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Import this tracker's browser cookies before testing its session."}
	}
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, uploadURL, nil, connectiontest.CookieHeaders(input.Cookies))
	if failure.Code != "" {
		return failure
	}
	lower := strings.ToLower(string(body))
	match := authPattern.FindSubmatch(body)
	if strings.Contains(lower, `name="password"`) || strings.Contains(lower, "cf-chl-") || strings.Contains(lower, "challenge-platform") || len(match) < 2 ||
		strings.TrimSpace(string(match[1])) == "" {
		return connectiontest.UnexpectedResponse()
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "The tracker returned its protected upload form using the stored browser session. No upload was attempted; announce credentials were not tested.",
	}
}
