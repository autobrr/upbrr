// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

// ConnectionProbe validates the draft key using the same authenticated filter
// endpoint and Bearer transport as duplicate lookup. A synthetic name limits the
// read; no upload or account changes are requested.
func (d *Definition) ConnectionProbe() trackers.ConnectionProbe {
	return trackers.ConnectionProbe{Test: d.testConnection, DraftConfig: trackers.APIKeyProbeConfig}
}

func (d *Definition) testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	key := strings.TrimSpace(input.Config.APIKey)
	if !connectiontest.CredentialPresent(key) {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Enter this tracker's API key before testing."}
	}
	// Keep authentication and client identification aligned with production.
	headers := &http.Request{Header: make(http.Header)}
	trackerdata.SetUnit3DAPIHeaders(headers, key)
	body, failure := connectiontest.Request(ctx, client, http.MethodGet,
		strings.TrimRight(d.profile.BaseURL, "/")+"/api/torrents/filter?name=upbrr.connection.test.synthetic&perPage=1", nil, headers.Header)
	if failure.Code != "" {
		return failure
	}
	var response struct {
		Data json.RawMessage `json:"data"`
		Meta struct {
			CurrentPage int  `json:"current_page"`
			Total       *int `json:"total"`
		} `json:"meta"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.Data) == 0 || response.Data[0] != '[' ||
		response.Meta.CurrentPage < 1 || response.Meta.Total == nil || *response.Meta.Total < 0 {
		return connectiontest.UnexpectedResponse()
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "The tracker accepted the API key for a read-only Unit3D search. Upload permission was not tested.",
	}
}
