// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

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
	username, passkey := strings.TrimSpace(input.Config.Username), strings.TrimSpace(input.Config.Passkey)
	if !connectiontest.CredentialPresent(username) || !connectiontest.CredentialPresent(passkey) {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Enter the HDB username and API passkey before testing."}
	}
	payload, err := json.Marshal(hdbDupeRequest{
		Username: username,
		Passkey:  passkey,
		Search:   "upbrr.connection.test.synthetic",
		Limit:    1,
	})
	if err != nil {
		return api.ConnectionTestResult{Code: "invalid_configuration", Message: "The API read parameters could not be encoded."}
	}
	body, failure := connectiontest.Request(
		ctx,
		client,
		http.MethodPost,
		"https://hdbits.org/api/torrents",
		bytes.NewReader(payload),
		http.Header{"Content-Type": {"application/json"}},
	)
	if failure.Code != "" {
		return failure
	}
	var response struct {
		Status *json.Number     `json:"status"`
		Data   []map[string]any `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil || response.Status == nil || response.Data == nil {
		return connectiontest.UnexpectedResponse()
	}
	status, err := response.Status.Float64()
	if err != nil || status != 0 {
		return connectiontest.UnexpectedResponse()
	}
	for _, item := range response.Data {
		if item == nil {
			return connectiontest.UnexpectedResponse()
		}
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "HDB accepted the entered username and passkey for its read API. Stored upload cookies and image-host access were not tested.",
	}
}
