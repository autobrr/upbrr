// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

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

// testConnection checks only the production authenticated read contract from
// dupe.go. It never logs in, refreshes credentials or persists session state.
func testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	user, key := strings.TrimSpace(input.Config.PTPAPIUser), strings.TrimSpace(input.Config.PTPAPIKey)
	if !connectiontest.CredentialPresent(user) || !connectiontest.CredentialPresent(key) {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Enter PTP ApiUser and ApiKey before testing."}
	}
	endpoint := "https://passthepopcorn.me/torrents.php?" + url.Values{"imdb": {"0133093"}, "json": {"noredirect"}}.Encode()
	headers := make(http.Header)
	headers.Set("Apiuser", user)
	headers.Set("Apikey", key)
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, endpoint, nil, headers)
	if failure.Code != "" {
		return failure
	}
	var response map[string]any
	if json.Unmarshal(body, &response) != nil || response["error"] != nil {
		return connectiontest.UnexpectedResponse()
	}
	if _, _, valid := ptpResolvedGroupID(response); !valid {
		return connectiontest.UnexpectedResponse()
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "PTP accepted the entered ApiUser and ApiKey for its read API. Stored cookies, login credentials, two-factor authentication and upload access were not tested.",
	}
}
