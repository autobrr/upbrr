// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

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
	endpoint := antAPIURL + "?" + url.Values{
		"t":      {"search"},
		"o":      {"json"},
		"imdbid": {"0133093"},
		"limit":  {"1"},
	}.Encode()
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, endpoint, nil, http.Header{"X-Api-Key": {key}, "User-Agent": {"upbrr"}})
	if failure.Code != "" {
		return failure
	}
	var response map[string]any
	if json.Unmarshal(body, &response) != nil || response["error"] != nil {
		return connectiontest.UnexpectedResponse()
	}
	page := parseANTPagination(response)
	items, hasItems := response["item"].([]any)
	if !page.OffsetPresent || page.Offset != 0 || !page.TotalPresent || page.Total < len(items) || (!hasItems && page.Total != 0) {
		return connectiontest.UnexpectedResponse()
	}
	for _, item := range items {
		if _, ok := item.(map[string]any); !ok {
			return connectiontest.UnexpectedResponse()
		}
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "The tracker returned its authenticated read API response using the entered key. Upload permission was not tested.",
	}
}
