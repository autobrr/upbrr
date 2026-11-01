// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// testConnection performs one authenticated read using the production search
// contract in dupe.go. It does not prepare uploads, download content or persist
// configuration or authentication state.
func testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	key := strings.TrimSpace(input.Config.APIKey)
	if !connectiontest.CredentialPresent(key) {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Enter this tracker's API key before testing."}
	}
	endpoint := "https://nebulance.io/api.php?" + url.Values{
		"action":   {"search"},
		"api_key":  {key},
		"age":      {">0"},
		"release":  {"upbrr.connection.test.synthetic"},
		"per_page": {"1"},
	}.Encode()
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, endpoint, nil, nil)
	if failure.Code != "" {
		return failure
	}
	var page nblSearchPage
	if json.Unmarshal(body, &page) != nil || (len(page.Error) > 0 && string(page.Error) != "null") || page.Items == nil ||
		!validNBLPage(page, 0, len(*page.Items), -1, -1) {
		return connectiontest.UnexpectedResponse()
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "The tracker returned its authenticated read API response using the entered key. Upload permission was not tested.",
	}
}
