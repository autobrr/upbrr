// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"context"
	"net/http"

	"encoding/json"

	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// testConnection uses the production search JSON contract in dupe.go with a
// read-only cookie snapshot. The configured upload passkey is not sent or tested.
func testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	if len(input.Cookies) == 0 {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Import current browser cookies before testing this tracker session."}
	}
	headers := connectiontest.CookieHeaders(input.Cookies)
	headers.Set("Accept", "application/json")
	body, failure := connectiontest.Request(
		ctx,
		client,
		http.MethodGet,
		"https://www.torrentleech.org/torrents/browse/list/query/upbrr.connection.test.synthetic",
		nil,
		headers,
	)
	if failure.Code != "" {
		return failure
	}
	var response struct {
		Torrents []map[string]any `json:"torrentList"`
	}
	if json.Unmarshal(body, &response) != nil || response.Torrents == nil {
		return connectiontest.UnexpectedResponse()
	}
	for _, item := range response.Torrents {
		if item == nil {
			return connectiontest.UnexpectedResponse()
		}
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "The tracker returned its authenticated search response using imported cookies. The upload passkey and upload permission were not tested.",
	}
}
