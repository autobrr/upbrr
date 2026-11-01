// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package connectiontest performs bounded probes of draft integration settings
// without constructing application services, saving configuration, or creating
// remote content. Only explicitly verified provider operations may be used here.
package connectiontest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	qbittorrent "github.com/autobrr/go-qbittorrent"
	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	timeout       = 10 * time.Second
	responseLimit = 1 << 20
)

// Test checks exactly target using the draft's values and the normal process
// proxy/TLS defaults. Redirects are not followed, so credentials cannot travel
// to a different endpoint. All results are safe to display without redaction.
func Test(ctx context.Context, cfg config.Config, target api.ConnectionTestTarget) api.ConnectionTestResult {
	return Run(ctx, func(ctx context.Context, client *http.Client) api.ConnectionTestResult {
		return test(ctx, cfg, target, client)
	})
}

// Run bounds a provider-owned read-only probe and prevents credential-bearing
// requests from following redirects. The client has no persistent cookie jar.
func Run(ctx context.Context, probe func(context.Context, *http.Client) api.ConnectionTestResult) api.ConnectionTestResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	return probe(ctx, client)
}

func result(code, message string) api.ConnectionTestResult {
	return api.ConnectionTestResult{Code: code, Message: message}
}

func test(ctx context.Context, cfg config.Config, target api.ConnectionTestTarget, client *http.Client) api.ConnectionTestResult {
	if ctx.Err() != nil {
		return RequestFailure(ctx, ctx.Err())
	}
	switch target.Kind {
	case "tmdb":
		key := strings.TrimSpace(cfg.MainSettings.TMDBAPI)
		if !CredentialPresent(key) {
			return result("missing_configuration", "Enter a TMDB API key before testing.")
		}
		endpoint := "https://api.themoviedb.org/3/authentication?" + url.Values{"api_key": {key}}.Encode()
		body, failure := Request(ctx, client, http.MethodGet, endpoint, nil, nil)
		if failure.Code != "" {
			return failure
		}
		var response struct {
			Success    bool `json:"success"`
			StatusCode int  `json:"status_code"`
		}
		if json.Unmarshal(body, &response) != nil || !response.Success || response.StatusCode != 1 {
			return UnexpectedResponse()
		}
		return result("success", "TMDB accepted the API key.")
	case "sonarr", "radarr":
		return testArr(ctx, cfg.ArrIntegration, target, client)
	case "torrent_client":
		return testTorrentClient(ctx, cfg, target)
	case "srrdb":
		body, failure := Request(ctx, client, http.MethodGet, "https://api.srrdb.com/v1/search/r:upbrr.connection.test.synthetic", nil, nil)
		if failure.Code != "" {
			return failure
		}
		var response struct {
			Count   *int            `json:"resultsCount"`
			Results json.RawMessage `json:"results"`
		}
		if json.Unmarshal(body, &response) != nil || response.Count == nil || *response.Count < 0 || len(response.Results) == 0 || response.Results[0] != '[' {
			return UnexpectedResponse()
		}
		return result("success", "SRRDB returned a valid public search response. No credentials are required.")
	default:
		return result("invalid_configuration", "Choose a supported Settings integration to test.")
	}
}

func testTorrentClient(ctx context.Context, cfg config.Config, target api.ConnectionTestTarget) api.ConnectionTestResult {
	clientCfg, ok := cfg.TorrentClients[target.Instance]
	if !ok {
		return result("missing_configuration", "Choose a configured torrent-client instance to test.")
	}
	switch strings.ToLower(clientCfg.ClientType()) {
	case "qbit", "qbittorrent", "qui":
	case "watch":
		return result("unsupported", "A watch folder is local storage and has no remote connection to authenticate.")
	default:
		return result("unsupported", "This torrent-client type has no supported remote connection test.")
	}
	endpoint, err := url.Parse(clientCfg.QbitHost())
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.Fragment != "" ||
		endpoint.RawQuery != "" {
		return result("invalid_configuration", "Enter an HTTP or HTTPS qBittorrent or qui proxy URL, including its base path if needed.")
	}
	// Reuse the production client's transport, TLS resolution and ephemeral jar.
	client := qbittorrent.NewClient(qbittorrent.Config{
		TLSSkipVerify: clientCfg.QbitTLSSkipVerify(),
		Timeout:       int(timeout / time.Second),
	}).GetHTTPClient()
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	basePath := strings.TrimRight(endpoint.Path, "/")
	endpoint.RawPath = ""
	referer := *endpoint
	referer.User = nil
	if !clientCfg.UsesQuiProxy() {
		username, password := strings.TrimSpace(clientCfg.QbitUsername()), strings.TrimSpace(clientCfg.QbitPassword())
		if (username != "" && !CredentialPresent(username)) || (password != "" && !CredentialPresent(password)) {
			return result("missing_configuration", "Restore or enter this instance's credentials before testing.")
		}
		// Empty credentials preserve qBittorrent's configured authentication bypass.
		if username != "" || password != "" {
			endpoint.Path = basePath + "/api/v2/auth/login"
			body, failure := Request(ctx, client, http.MethodPost, endpoint.String(), strings.NewReader(url.Values{
				"username": {username},
				"password": {password},
			}.Encode()), http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Referer": {referer.String()}})
			if failure.Code != "" {
				return failure
			}
			if string(body) == "Fails." {
				return result("credentials_rejected", "qBittorrent rejected the username or password.")
			}
			if string(body) != "Ok." {
				return UnexpectedResponse()
			}
		}
	}
	endpoint.Path = basePath + "/api/v2/app/version"
	headers := make(http.Header)
	if !clientCfg.UsesQuiProxy() {
		headers.Set("Referer", referer.String())
	}
	// qui may forward Referer to qBittorrent, where the proxy origin fails CSRF
	// validation. Match production proxy reads, which do not send this header.
	body, failure := Request(ctx, client, http.MethodGet, endpoint.String(), nil, headers)
	if failure.Code != "" {
		if clientCfg.UsesQuiProxy() && failure.Code == "authentication_required" {
			return result(
				"authentication_required",
				"The qui proxy or its upstream returned HTTP 401 (Unauthorized). Check the client-proxy URL and key, instance access and upstream authentication. A regular qui API key does not authorize a client-proxy URL.",
			)
		}
		return failure
	}
	version := strings.TrimSpace(string(body))
	if len(version) < 4 || version[0] != 'v' || version[1] < '0' || version[1] > '9' || !strings.Contains(version, ".") ||
		strings.ContainsAny(version, "<>\r\n\t ") {
		return UnexpectedResponse()
	}
	return result("success", "qBittorrent authorized the application-version request. No torrents were added or changed.")
}

func testArr(ctx context.Context, cfg config.ArrIntegrationConfig, target api.ConnectionTestTarget, client *http.Client) api.ConnectionTestResult {
	var urls, keys [4]string
	if target.Kind == "sonarr" {
		urls = [4]string{cfg.SonarrURL, cfg.SonarrURL1, cfg.SonarrURL2, cfg.SonarrURL3}
		keys = [4]string{cfg.SonarrAPIKey, cfg.SonarrAPIKey1, cfg.SonarrAPIKey2, cfg.SonarrAPIKey3}
	} else {
		urls = [4]string{cfg.RadarrURL, cfg.RadarrURL1, cfg.RadarrURL2, cfg.RadarrURL3}
		keys = [4]string{cfg.RadarrAPIKey, cfg.RadarrAPIKey1, cfg.RadarrAPIKey2, cfg.RadarrAPIKey3}
	}
	if len(target.Instance) != 1 || target.Instance[0] < '0' || target.Instance[0] > '3' {
		return result("invalid_configuration", "Choose the primary application instance or one of its three additional instances.")
	}
	index := target.Instance[0] - '0'
	base, key := strings.TrimSpace(urls[index]), strings.TrimSpace(keys[index])
	if base == "" || !CredentialPresent(key) {
		return result("missing_configuration", "Enter this instance's URL and API key before testing.")
	}
	endpoint, err := url.Parse(base)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.RawQuery != "" ||
		endpoint.Fragment != "" {
		return result("invalid_configuration", "Enter an absolute HTTP or HTTPS application URL, including its base path if needed.")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v3/system/status"
	endpoint.RawPath = ""
	body, failure := Request(ctx, client, http.MethodGet, endpoint.String(), nil, http.Header{"X-Api-Key": {key}})
	if failure.Code != "" {
		return failure
	}
	var response struct {
		AppName string `json:"appName"`
		Version string `json:"version"`
	}
	if json.Unmarshal(body, &response) != nil || !strings.EqualFold(response.AppName, target.Kind) || strings.TrimSpace(response.Version) == "" {
		return UnexpectedResponse()
	}
	return result("success", "The application returned an authenticated API v3 status response.")
}

// CredentialPresent rejects empty values and unrestored secret placeholders.
func CredentialPresent(value string) bool {
	return value != "" && value != "[REDACTED]" && !strings.HasPrefix(value, "upbrr-enc:")
}

// Request sends one provider request with bounded response reading and safe errors.
// It accepts only HTTP 200; providers must still validate their response contract.
func Request(ctx context.Context, client *http.Client, method, endpoint string, body io.Reader, headers http.Header) ([]byte, api.ConnectionTestResult) {
	return request(ctx, client, method, endpoint, body, headers, false)
}

// SubmitLogin submits a provider's established login form using a caller-owned
// ephemeral cookie jar. Login redirects are accepted but never followed; callers
// must verify a separate protected page before reporting success.
func SubmitLogin(ctx context.Context, client *http.Client, endpoint string, values url.Values) api.ConnectionTestResult {
	_, failure := request(ctx, client, http.MethodPost, endpoint, strings.NewReader(values.Encode()),
		http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "User-Agent": {"upbrr"}}, true)
	return failure
}

func request(
	ctx context.Context,
	client *http.Client,
	method, endpoint string,
	body io.Reader,
	headers http.Header,
	login bool,
) ([]byte, api.ConnectionTestResult) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, result("invalid_configuration", "The connection URL or credentials are invalid. Review the entered settings.")
	}
	maps.Copy(req.Header, headers)
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "upbrr/connection-test")
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, RequestFailure(ctx, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return nil, result(
			"authentication_required",
			"The service returned HTTP 401 (Unauthorized). Check authentication and reverse-proxy access; this response alone does not identify which credential or layer rejected the request.",
		)
	case http.StatusForbidden:
		return nil, result(
			"access_denied",
			"The service returned HTTP 403 (Forbidden). Check account permissions, IP restrictions, authentication and proxy access.",
		)
	case http.StatusTooManyRequests:
		return nil, result("rate_limited", "The service returned HTTP 429 (Too Many Requests). Wait before testing again.")
	case http.StatusFound, http.StatusSeeOther:
		if !login {
			return nil, httpFailure(response.StatusCode)
		}
	case http.StatusOK:
	default:
		return nil, httpFailure(response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return nil, RequestFailure(ctx, err)
	}
	if len(data) > responseLimit {
		return nil, result("unexpected_response", "The service response exceeded the connection test limit.")
	}
	if failure := responseFailure(data); failure.Code != "" {
		return nil, failure
	}
	return data, api.ConnectionTestResult{}
}

func httpFailure(status int) api.ConnectionTestResult {
	if status >= 500 && status <= 599 {
		return result(
			"unavailable",
			fmt.Sprintf("The service or reverse proxy returned HTTP %d. Check service availability and upstream proxy connectivity, then retry.", status),
		)
	}
	if status >= 300 && status <= 399 {
		return result(
			"unexpected_response",
			fmt.Sprintf(
				"The service returned HTTP %d (redirect). Check the final service URL, login requirements and reverse proxy; connection tests do not follow redirects.",
				status,
			),
		)
	}
	return result("unexpected_response", fmt.Sprintf("The service returned HTTP %d. Check its URL, API version and request permissions.", status))
}

// responseFailure recognizes explicit error envelopes and prominent HTML error
// markers before provider success parsing. Remote text is never returned; unknown
// content remains subject to the provider's own strict response validation.
func responseFailure(body []byte) api.ConnectionTestResult {
	var envelope map[string]any
	if json.Unmarshal(body, &envelope) == nil && envelope != nil {
		success, hasSuccess := envelope["success"].(bool)
		failed := hasSuccess && !success
		status, _ := envelope["status"].(string)
		switch strings.ToLower(status) {
		case "error", "fail", "failed", "failure":
			failed = true
		}
		for _, field := range []string{"error", "errors"} {
			switch value := envelope[field].(type) {
			case bool:
				failed = failed || value
			case float64:
				failed = failed || value != 0
			case string:
				failed = failed || strings.TrimSpace(value) != ""
			case []any:
				failed = failed || len(value) != 0
			case map[string]any:
				failed = failed || len(value) != 0
			}
		}
		if failed {
			return result(
				"remote_error",
				"The service returned an API error response. Check the account's API access, request requirements and service status.",
			)
		}
		return api.ConnectionTestResult{}
	}
	if !strings.HasPrefix(strings.TrimSpace(string(body)), "<") {
		return api.ConnectionTestResult{}
	}
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return api.ConnectionTestResult{}
	}
	for node := range doc.Descendants() {
		if node.Type != html.ElementNode {
			continue
		}
		var danger, alert, errorClass bool
		for _, attr := range node.Attr {
			if attr.Key == "class" {
				for class := range strings.FieldsSeq(attr.Val) {
					danger = danger || class == "alert-danger"
					errorClass = errorClass || class == "error"
				}
			}
			alert = alert || (attr.Key == "role" && attr.Val == "alert")
		}
		notice := danger || (alert && errorClass)
		if node.Data != "title" && node.Data != "h1" && !notice {
			continue
		}
		var content strings.Builder
		for child := range node.Descendants() {
			if child.Type == html.TextNode {
				content.WriteString(child.Data)
				content.WriteByte(' ')
			}
		}
		text := strings.ToLower(strings.Join(strings.Fields(content.String()), " "))
		switch {
		case strings.Contains(text, "just a moment"), strings.Contains(text, "checking your browser"), strings.Contains(text, "verify you are human"):
			return result(
				"challenge_required",
				"The service returned a browser or anti-bot challenge. Check whether its API is accessible from this server and proxy.",
			)
		case strings.Contains(text, "service unavailable"),
			strings.Contains(text, "under maintenance"),
			strings.Contains(text, "internal server error"),
			strings.Contains(text, "bad gateway"):
			return result("unavailable", "The service returned an unavailable or maintenance page. Check service and reverse-proxy status, then retry.")
		case text == "access denied", text == "forbidden", text == "unauthorized":
			return result("access_denied", "The service returned an access-denied page. Check account permissions, IP restrictions and proxy access.")
		case text != "" && notice:
			return result("remote_error", "The service displayed an error notice. Check the service directly for account or availability issues, then retry.")
		}
	}
	return api.ConnectionTestResult{}
}

// RequestFailure classifies transport and context errors without exposing their contents.
func RequestFailure(ctx context.Context, err error) api.ConnectionTestResult {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return result("timeout", "The connection test timed out. Check the address, service and proxy availability.")
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return result("canceled", "The connection test was canceled.")
	}
	if timeout, ok := errors.AsType[net.Error](err); ok && timeout.Timeout() {
		return result("timeout", "The connection test timed out. Check the address, service and proxy availability.")
	}
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return result("tls_error", "The service certificate could not be verified. Check the certificate and configured TLS options.")
	}
	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return result("tls_error", "The service certificate could not be verified. Check the certificate and configured TLS options.")
	}
	return result("unreachable", "The service could not be reached. Check its address, service status and proxy settings.")
}

// UnexpectedResponse reports a service-contract mismatch without remote content.
func UnexpectedResponse() api.ConnectionTestResult {
	return result("unexpected_response", "The response did not identify the expected service. Check its URL, API version and reverse proxy.")
}

// CookieHeaders builds an ephemeral request header from a selected cookie
// snapshot. It neither changes the snapshot nor maintains a persistent jar.
func CookieHeaders(values map[string]string) http.Header {
	req := &http.Request{Header: make(http.Header)}
	for name, value := range values {
		if strings.TrimSpace(name) != "" && strings.TrimSpace(value) != "" {
			// #nosec G124 -- AddCookie builds an outbound Cookie header; browser Set-Cookie attributes do not apply.
			req.AddCookie(&http.Cookie{Name: name, Value: value})
		}
	}
	req.Header.Set("User-Agent", "upbrr")
	return req.Header
}
