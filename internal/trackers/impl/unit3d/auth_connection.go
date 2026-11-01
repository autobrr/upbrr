// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
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
	endpoint := strings.TrimRight(d.profile.BaseURL, "/") + "/api/torrents/filter"
	body, failure := connectiontest.Request(ctx, client, http.MethodGet,
		endpoint+"?name=upbrr.connection.test.synthetic&perPage=1", nil, headers.Header)
	if failure.Code != "" {
		return failure
	}
	var response struct {
		Data  []map[string]json.RawMessage `json:"data"`
		Links map[string]*string           `json:"links"`
		Meta  struct {
			CurrentPage *int   `json:"current_page"`
			Total       *int   `json:"total"`
			PerPage     int    `json:"per_page"`
			Path        string `json:"path"`
		} `json:"meta"`
	}
	if json.Unmarshal(body, &response) != nil || response.Data == nil || len(response.Data) > 1 {
		return connectiontest.UnexpectedResponse()
	}
	for _, item := range response.Data {
		if item == nil {
			return connectiontest.UnexpectedResponse()
		}
	}
	// Older length-aware pagination includes total. Current upstream uses
	// simple pagination (self link) or Laravel cursor pagination (meta.path).
	// Neither requires total; cursor responses also omit current_page.
	pageValid := response.Meta.CurrentPage != nil && *response.Meta.CurrentPage >= 1
	paginationValid := pageValid && response.Meta.Total != nil && *response.Meta.Total >= len(response.Data)
	if response.Meta.Total == nil && response.Meta.PerPage == 1 {
		_, hasNext := response.Links["next"]
		_, hasPrev := response.Links["prev"]
		linksValid := hasNext && hasPrev
		for _, link := range response.Links {
			if link != nil && !connectionPageLink(*link, endpoint) {
				linksValid = false
			}
		}
		self := response.Links["self"]
		paginationValid = linksValid && (response.Meta.CurrentPage == nil || pageValid) &&
			((pageValid && self != nil && connectionPageLink(*self, endpoint)) || connectionPageLink(response.Meta.Path, endpoint))
	}
	if !paginationValid {
		return connectiontest.UnexpectedResponse()
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "The tracker accepted the API key for a read-only Unit3D search. Upload permission was not tested.",
	}
}

// connectionPageLink recognizes pagination for the requested filter without
// following returned links or trusting arbitrary data arrays as service proof.
func connectionPageLink(link, endpoint string) bool {
	if link == "" {
		return false
	}
	base, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	parsed, err := url.Parse(link)
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	parsed = base.ResolveReference(parsed)
	// Match the repository's deployment identity normalization while retaining
	// this probe's exact scheme/path boundary and never following the link.
	for _, endpointURL := range []*url.URL{base, parsed} {
		endpointURL.Host = strings.ToLower(endpointURL.Host)
		if endpointURL.Scheme == "https" && endpointURL.Port() == "443" || endpointURL.Scheme == "http" && endpointURL.Port() == "80" {
			endpointURL.Host = strings.TrimSuffix(endpointURL.Host, ":"+endpointURL.Port())
		}
	}
	return parsed.Scheme == base.Scheme && parsed.Host == base.Host && parsed.Path == base.Path
}
