// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package czt

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// testConnection uses the documented passkey-authenticated Torznab search,
// rather than unauthenticated capabilities or the content-creating upload API.
// Source: https://czteam.me/faq.php (API / direct Sonarr-Radarr setup).
func testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	key := strings.TrimSpace(input.Config.Passkey)
	if !connectiontest.CredentialPresent(key) {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Enter this tracker's passkey before testing."}
	}
	endpoint := defaultBaseURL + "/torznab?" + url.Values{
		"t":      {"search"},
		"q":      {"upbrr.connection.test.synthetic"},
		"limit":  {"1"},
		"apikey": {key},
	}.Encode()
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, endpoint, nil, http.Header{"Accept": {"application/rss+xml, application/xml"}})
	if failure.Code != "" {
		return failure
	}
	var response struct {
		XMLName xml.Name
		Code    int `xml:"code,attr"`
		Channel *struct {
			Responses []struct {
				XMLName xml.Name
				Offset  *int `xml:"offset,attr"`
				Total   *int `xml:"total,attr"`
			} `xml:"response"`
			Items []struct{} `xml:"item"`
		} `xml:"channel"`
	}
	if xml.Unmarshal(body, &response) != nil {
		return connectiontest.UnexpectedResponse()
	}
	if response.XMLName.Local == "error" {
		switch response.Code {
		case 100:
			return api.ConnectionTestResult{Code: "credentials_rejected", Message: "The tracker rejected the passkey for its read API."}
		case 101, 102:
			return api.ConnectionTestResult{Code: "access_denied", Message: "The tracker denied API access. Check the account status and read permissions."}
		default:
			return connectiontest.UnexpectedResponse()
		}
	}
	if response.XMLName.Local != "rss" || response.Channel == nil {
		return connectiontest.UnexpectedResponse()
	}
	// Newznab's empty-result response may omit channel title/link. Require its
	// namespaced pagination contract instead of accepting a generic RSS page.
	// https://newznab.readthedocs.io/en/latest/misc/api.html#search
	for _, page := range response.Channel.Responses {
		if (page.XMLName.Space == "http://www.newznab.com/DTD/2010/feeds/attributes/" || page.XMLName.Space == "http://torznab.com/schemas/2015/feed") &&
			page.Offset != nil && *page.Offset == 0 && page.Total != nil && *page.Total >= len(response.Channel.Items) {
			return api.ConnectionTestResult{
				Code:    "success",
				Message: "The tracker accepted the passkey for a read-only Torznab search. No torrent was uploaded or downloaded; upload permission was not tested.",
			}
		}
	}
	return connectiontest.UnexpectedResponse()
}
