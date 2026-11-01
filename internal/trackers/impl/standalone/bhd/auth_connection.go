// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"bytes"
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
	payload := map[string]any{
		"action":     "search",
		"categories": "Movies",
		"imdb_id":    bhdIMDB(133093),
		"page":       1,
	}
	if rss := strings.TrimSpace(input.Config.BhdRSSKey); rss != "" {
		payload["rsskey"] = rss
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return api.ConnectionTestResult{Code: "invalid_configuration", Message: "The API search parameters could not be encoded."}
	}
	body, failure := connectiontest.Request(
		ctx,
		client,
		http.MethodPost,
		"https://beyond-hd.me/api/torrents/"+url.PathEscape(key),
		bytes.NewReader(encoded),
		http.Header{"Content-Type": {"application/json"}},
	)
	if failure.Code != "" {
		return failure
	}
	var response map[string]any
	if json.Unmarshal(body, &response) != nil || bhdInt(response["status_code"]) != 1 {
		return connectiontest.UnexpectedResponse()
	}
	if success, present, valid := bhdBoolField(response, "success"); present && (!valid || !success) {
		return connectiontest.UnexpectedResponse()
	}
	items, ok := response["results"].([]any)
	if !ok {
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
