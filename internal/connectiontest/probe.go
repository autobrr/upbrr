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
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	qbittorrent "github.com/autobrr/go-qbittorrent"

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
	body, failure := Request(ctx, client, http.MethodGet, endpoint.String(), nil, http.Header{"Referer": {referer.String()}})
	if failure.Code != "" {
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
		return nil, result("credentials_rejected", "The service rejected the credentials. Check the key, username or password.")
	case http.StatusForbidden:
		return nil, result("access_denied", "The service denied access. Check credentials, account permissions, IP restrictions and proxy access.")
	case http.StatusTooManyRequests:
		return nil, result("rate_limited", "The service is rate limiting requests. Wait before testing again.")
	case http.StatusFound, http.StatusSeeOther:
		if !login {
			return nil, UnexpectedResponse()
		}
	case http.StatusOK:
	default:
		return nil, result("unexpected_response", "The service did not return the expected API response. Check its URL, version and reverse proxy.")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return nil, RequestFailure(ctx, err)
	}
	if len(data) > responseLimit {
		return nil, result("unexpected_response", "The service response exceeded the connection test limit.")
	}
	return data, api.ConnectionTestResult{}
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
