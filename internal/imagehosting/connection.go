// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/pkg/api"
)

// TestConnection checks one image host without uploading, generating gallery
// tokens, or persisting sessions. The caller supplies the bounded no-redirect
// client. Results distinguish a verified read from an unavailable safe check.
func TestConnection(ctx context.Context, cfg config.ImageHostingConfig, host string, client *http.Client) api.ConnectionTestResult {
	if host == "imgbox" {
		return testImgbox(ctx, client)
	}
	if host == "zipline" {
		return testZipline(ctx, cfg, client)
	}
	if host == "samaritano" {
		return testSamaritano(ctx, cfg, client)
	}
	if host == "pixhost" {
		return testPixhost(ctx, client)
	}
	return ConnectionTestLimitation(host)
}

// ConnectionTestLimitation returns safe, static guidance for hosts without a
// verified probe, or an empty result when TestConnection can perform a check.
// Settings metadata uses the same guidance without sending credentials or HTTP.
func ConnectionTestLimitation(host string) api.ConnectionTestResult {
	switch host {
	case "imgbox", "zipline", "samaritano", "pixhost":
		return api.ConnectionTestResult{}
	}
	if host == "imgbb" {
		return connectionResult(
			"unsupported",
			"ImgBB's documented API only uploads images. Credentials cannot be safely verified here without creating an image; no request was sent.",
		)
	}
	// These uploaders use the hosts' published API v1.1 contract, which
	// documents only uploads, not a compatible read-only key check:
	// https://lensdump.com/api-v1, https://onlyimage.org/api-v1,
	// and https://ptscreens.com/api-v1. Passtheima.ge and utp.pm identify their
	// deployed Chevereto versions; their configured /api/1/upload contracts
	// match https://v4-docs.chevereto.com/api/1/file-upload.html.
	if host == "passtheimage" || host == "utppm" {
		return connectionResult(
			"unsupported",
			"This uploader uses the host's Chevereto /api/1/upload contract, without a verified read-only key check here. Credentials were not sent and no image was uploaded.",
		)
	}
	if host == "lensdump" || host == "onlyimage" || host == "ptscreens" {
		return connectionResult(
			"unsupported",
			"This uploader uses the host's API v1.1, which documents file uploads without a read-only key check. Credentials were not sent and no image was uploaded.",
		)
	}
	if host == "sharex" {
		return connectionResult(
			"unsupported",
			"ShareX upload URLs can point to arbitrary servers and provide no standard read-only credential check. Verify access with the server administrator; no upload request was sent.",
		)
	}
	return connectionResult("unverified", "A safe connection test has not yet been verified for this image host. No request was sent.")
}

func connectionResult(code, message string) api.ConnectionTestResult {
	return api.ConnectionTestResult{Code: code, Message: message}
}

func testPixhost(ctx context.Context, client *http.Client) api.ConnectionTestResult {
	// Use the documented public show-URL shape with positive numeric IDs.
	// The fixed synthetic name does not refer to any user-provided image.
	const syntheticImageURL = "https://pixhost.to/show/1/1_upbrr-connection-test.jpg"
	body, failure := connectiontest.Request(ctx, client, http.MethodPost, "https://api.pixhost.to/images/status",
		strings.NewReader(`{"urls":["`+syntheticImageURL+`"]}`), http.Header{"Content-Type": {"application/json"}})
	if failure.Code != "" {
		return failure
	}
	var response struct {
		Results []struct {
			URL    string `json:"url"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.Results) != 1 || response.Results[0].URL != syntheticImageURL {
		return connectiontest.UnexpectedResponse()
	}
	switch response.Results[0].Status {
	case "available", "deleted", "not_found", "pending":
		return connectionResult("success", "Pixhost returned a valid public image-status response. No credentials are required and no images were uploaded.")
	case "invalid":
		return connectionResult(
			"invalid_probe",
			"Pixhost rejected the connection test's synthetic image URL as invalid. Its API was reached, but this result does not verify a usable image-status check. No image was uploaded.",
		)
	default:
		return connectiontest.UnexpectedResponse()
	}
}

// testZipline uses the v3 authenticated count endpoint, whose handler only reads
// Prisma state. GET /api/user is deliberately excluded: v3 can refresh OAuth
// credentials there even for GET requests. Source (v3.7.13):
// https://github.com/diced/zipline/blob/v3.7.13/src/pages/api/user/paged.ts
func testZipline(ctx context.Context, cfg config.ImageHostingConfig, client *http.Client) api.ConnectionTestResult {
	base := strings.TrimSpace(cfg.ZiplineURL)
	if base == "" {
		return connectionResult("missing_configuration", "Enter the Zipline upload URL before testing.")
	}
	endpoint, err := url.Parse(base)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return connectionResult("invalid_configuration", "Enter the absolute Zipline upload URL before testing.")
	}
	// A custom upload path does not identify a documented API base. Do not
	// strip path segments or guess a companion authenticated endpoint.
	if endpoint.Path != "/api/upload" || endpoint.RawPath != "" {
		return connectionResult(
			"unsupported",
			"A safe test is available for the standard Zipline v3 /api/upload URL. Custom upload paths do not identify a verified read API; no credentials were sent.",
		)
	}
	key := strings.TrimSpace(cfg.ZiplineAPIKey)
	if !connectiontest.CredentialPresent(key) {
		return connectionResult("missing_configuration", "Enter the Zipline API key before testing.")
	}
	endpoint.Path = "/api/user/paged"
	endpoint.RawQuery = "count=true"
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, endpoint.String(), nil, http.Header{"Authorization": {key}})
	if failure.Code != "" {
		return failure
	}
	var response struct {
		Count *int `json:"count"`
	}
	if json.Unmarshal(body, &response) != nil || response.Count == nil || *response.Count < 0 {
		return connectiontest.UnexpectedResponse()
	}
	return connectionResult(
		"success",
		"Zipline authorized its v3 read-only file-count request. Upload permission was not tested and no credentials were refreshed.",
	)
}

// testImgbox performs only the existing anonymous session-page read. Upload
// token generation is deliberately excluded because it can allocate a gallery.
func testImgbox(ctx context.Context, client *http.Client) api.ConnectionTestResult {
	_, _, err := imgboxGetCsrfAndCookie(ctx, client, maxResponseBodyPreviewBytes)
	// Cancellation wins even if the limited session prefix parsed successfully.
	if ctx.Err() != nil {
		return connectiontest.RequestFailure(ctx, ctx.Err())
	}
	if err != nil {
		if _, networkFailure := errors.AsType[*url.Error](err); networkFailure {
			return connectiontest.RequestFailure(ctx, err)
		}
		return connectionResult(
			"unavailable",
			"Imgbox did not provide its expected anonymous session page. Check service, proxy and anti-bot challenge access, then retry.",
		)
	}
	return connectionResult(
		"success",
		"Imgbox returned its public anonymous session page. No credentials are required; no upload token, gallery or image was created.",
	)
}

// testSamaritano uses the deployed PicFast v0.22.2 authenticated image-list
// contract with a synthetic filename and one result. Its handler only reads
// image metadata; normal auth middleware updates token last-used audit time,
// without rotating tokens, refreshing credentials or creating content.
// Source: https://github.com/atbeta/picfast/tree/v0.22.2/internal/handler
// Contract: https://img.samaritano.cc/openapi.json (paths /images, /sharex/upload).
func testSamaritano(ctx context.Context, cfg config.ImageHostingConfig, client *http.Client) api.ConnectionTestResult {
	key := strings.TrimSpace(cfg.SamaritanoAPI)
	if !connectiontest.CredentialPresent(key) {
		return connectionResult("missing_configuration", "Enter the Samaritano API token before testing.")
	}
	endpoint := "https://img.samaritano.cc/api/v1/images?" + url.Values{
		"page":      {"1"},
		"page_size": {"1"},
		"keyword":   {"upbrr.connection.test.synthetic"},
	}.Encode()
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, endpoint, nil, http.Header{"Authorization": {"Bearer " + key}})
	if failure.Code != "" {
		return failure
	}
	var response struct {
		Status bool `json:"status"`
		Data   *struct {
			Items []map[string]any `json:"items"`
			Page  *int             `json:"page"`
			Size  *int             `json:"size"`
			Total *int64           `json:"total"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil || !response.Status || response.Data == nil {
		return connectiontest.UnexpectedResponse()
	}
	data := response.Data
	if data.Items == nil || data.Page == nil || *data.Page != 1 || data.Size == nil || *data.Size != 1 || data.Total == nil ||
		*data.Total < int64(len(data.Items)) ||
		len(data.Items) > 1 {
		return connectiontest.UnexpectedResponse()
	}
	for _, item := range data.Items {
		if item == nil {
			return connectiontest.UnexpectedResponse()
		}
	}
	return connectionResult(
		"success",
		"Samaritano accepted the API token for its PicFast read API. Upload permission was not tested; no image was uploaded and no token was refreshed.",
	)
}
