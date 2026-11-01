// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// testConnection checks only the production authenticated read contract from
// dupe.go. It never logs in, refreshes credentials or persists session state.
func testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	key := strings.TrimSpace(input.Config.APIKey)
	if !connectiontest.CredentialPresent(key) {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Enter the BTN API key before testing."}
	}
	payload, err := json.Marshal(map[string]any{
		"id":     "upbrr-connection-test",
		"method": "getTorrents",
		"params": map[string]any{
			"key":     key,
			"search":  map[string]string{"search": "upbrr.connection.test.synthetic"},
			"results": 1,
			"offset":  0,
		},
	})
	if err != nil {
		return api.ConnectionTestResult{Code: "invalid_configuration", Message: "The API read parameters could not be encoded."}
	}
	body, failure := connectiontest.Request(
		ctx,
		client,
		http.MethodPost,
		"https://api.broadcasthe.net/",
		bytes.NewReader(payload),
		http.Header{"Content-Type": {"application/json"}},
	)
	if failure.Code != "" {
		return failure
	}
	var response btnTorrentsResponse
	if json.Unmarshal(body, &response) != nil || btnAPIErrorPresent(response.Error) || response.Result == nil || len(response.Result.Torrents) == 0 {
		return connectiontest.UnexpectedResponse()
	}
	_, count, malformed, err := decodeBTNDupeTorrents(response.Result.Torrents)
	total, known := btnResultCount(response.Result.Results)
	if err != nil || malformed || !known || total < count {
		return connectiontest.UnexpectedResponse()
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "BTN accepted the entered API key for torrent lookup. Stored cookies, login credentials, two-factor authentication and upload access were not tested.",
	}
}
