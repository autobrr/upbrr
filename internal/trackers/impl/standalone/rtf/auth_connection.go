// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// testConnection checks only the production authenticated read contract from
// dupe.go. It never logs in, refreshes credentials or persists session state.
func testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	key := rtfAPIKey(input.Config)
	if !connectiontest.CredentialPresent(key) {
		return api.ConnectionTestResult{
			Code:    "unsupported",
			Message: "This safe RTF test requires an existing API key. Username/password login issues or refreshes a token, so it is not run by this read-only check.",
		}
	}
	endpoint := rtfTorrentEndpoint + "?" + url.Values{"search": {"upbrr.connection.test.synthetic"}, "includingDead": {"1"}}.Encode()
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, endpoint, nil, http.Header{"Authorization": {key}})
	if failure.Code != "" {
		return failure
	}
	var response []map[string]any
	if json.Unmarshal(body, &response) != nil || response == nil {
		return connectiontest.UnexpectedResponse()
	}
	for _, item := range response {
		if item == nil {
			return connectiontest.UnexpectedResponse()
		}
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "RTF accepted the entered API key for its read API. Username/password login and upload permission were not tested; no token was refreshed.",
	}
}
