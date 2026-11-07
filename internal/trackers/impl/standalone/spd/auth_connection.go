// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

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
	endpoint := "https://speedapp.io/api/torrent?" + url.Values{"search": {"upbrr.connection.test.synthetic"}}.Encode()
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, endpoint, nil, http.Header{"Authorization": {key}})
	if failure.Code != "" {
		return failure
	}
	var response []map[string]json.RawMessage
	if json.Unmarshal(body, &response) != nil || response == nil {
		return connectiontest.UnexpectedResponse()
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "The tracker returned its authenticated read API response using the entered key. Upload permission was not tested.",
	}
}
