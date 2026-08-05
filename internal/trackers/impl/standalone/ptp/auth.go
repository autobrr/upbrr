// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers"
	authtotp "github.com/autobrr/upbrr/internal/trackers/auth/totp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	ptpBaseURL     = "https://passthepopcorn.me"
	ptpUploadPath  = "/upload.php"
	ptpTorrentPath = "/torrents.php"
	ptpLoginPath   = "/ajax.php?action=login"
	ptpCookieFile  = "PTP.json"
	ptpUserAgent   = "upbrr"

	ptpAuthResponseMaxBytes = 8 << 20
)

var (
	ptpAntiCsrfPattern         = regexp.MustCompile(`data-AntiCsrfToken="([^"]+)"`)
	ptpSuccessPattern          = regexp.MustCompile(`torrents\.php\?id=(\d+)&torrentid=(\d+)`)
	errPTPStoredSessionInvalid = errors.New("trackers: PTP stored session confirmed invalid")
	newPosterHTTPClient        = newPublicPosterHTTPClient
	reservedPosterPrefixes     = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("fe80::/10"),
	}
)

// ErrSubmitted2FARejected marks a PTP failure after a submitted manual 2FA code
// reached the tracker and was rejected.
var ErrSubmitted2FARejected = trackers.ErrSubmitted2FARejected

func resolveSession(ctx context.Context, trackerConfig config.TrackerConfig, dbPath string, baseURL string, logger api.Logger) (*http.Client, string, error) {
	return resolveSessionLogin(ctx, trackerConfig, dbPath, baseURL, logger, api.TrackerAuthLoginRequest{})
}

func resolveSessionLogin(
	ctx context.Context,
	trackerConfig config.TrackerConfig,
	dbPath string,
	baseURL string,
	logger api.Logger,
	login api.TrackerAuthLoginRequest,
) (*http.Client, string, error) {
	if logger == nil {
		logger = api.NopLogger{}
	}

	cookies, err := loadCookies(ctx, dbPath)
	if err == nil && len(cookies) > 0 {
		client, token, tokenErr := fetchAntiCsrfToken(ctx, baseURL, cookies)
		if tokenErr == nil {
			return client, token, nil
		}
		if !errors.Is(tokenErr, errPTPStoredSessionInvalid) {
			return nil, "", tokenErr
		}
		if strings.TrimSpace(trackerConfig.Username) == "" || strings.TrimSpace(trackerConfig.Password) == "" ||
			strings.TrimSpace(normalizedAnnounceURL(trackerConfig.AnnounceURL)) == "" {
			return nil, "", tokenErr
		}
	}
	if err != nil && !errors.Is(err, cookiepkg.ErrTrackerCookiesNotFound) {
		return nil, "", err
	}
	return loginAndFetchAntiCsrfToken(ctx, trackerConfig, dbPath, baseURL, logger, login)
}

// ResolveSessionForTrackerAuth validates PTP stored cookies or logs in with
// configured credentials. Credential login must produce an anti-CSRF token
// before refreshed cookies are persisted.
func ResolveSessionForTrackerAuth(ctx context.Context, trackerConfig config.TrackerConfig, dbPath string) error {
	return resolveSessionForTrackerAuthAt(ctx, trackerConfig, dbPath, ptpBaseURL)
}

func resolveSessionForTrackerAuthAt(ctx context.Context, trackerConfig config.TrackerConfig, dbPath string, baseURL string) error {
	return resolveSessionForTrackerAuthLoginAt(ctx, trackerConfig, dbPath, api.TrackerAuthLoginRequest{}, baseURL)
}

// ResolveSessionForTrackerAuthLogin validates PTP stored cookies or logs in
// with configured credentials. When PTP requires 2FA, login.Code is used before
// falling back to the configured OTP URI; if neither yields a code, the error
// is returned before stored cookies are replaced. A rejected submitted code
// returns [ErrSubmitted2FARejected] with the login-failed error before refreshed
// cookies are persisted.
func ResolveSessionForTrackerAuthLogin(ctx context.Context, trackerConfig config.TrackerConfig, dbPath string, login api.TrackerAuthLoginRequest) error {
	return resolveSessionForTrackerAuthLoginAt(ctx, trackerConfig, dbPath, login, ptpBaseURL)
}

func resolveSessionForTrackerAuthLoginAt(
	ctx context.Context,
	trackerConfig config.TrackerConfig,
	dbPath string,
	login api.TrackerAuthLoginRequest,
	baseURL string,
) error {
	_, _, err := resolveSessionLogin(ctx, trackerConfig, dbPath, baseURL, api.NopLogger{}, login)
	return err
}

func fetchAntiCsrfToken(ctx context.Context, baseURL string, cookies map[string]string) (*http.Client, string, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: PTP create session cookie jar: %w", err)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: PTP parse base URL: %w", err)
	}
	jarCookies := make([]*http.Cookie, 0, len(cookies))
	for name, value := range cookies {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		// #nosec G124 -- Outbound tracker jar cookie mirrors configured PTP session values.
		jarCookies = append(jarCookies, &http.Cookie{
			Name:   name,
			Value:  value,
			Path:   "/",
			Domain: parsed.Hostname(),
		})
	}
	jar.SetCookies(parsed, jarCookies)
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	token, err := requestAntiCsrfToken(ctx, client, baseURL)
	if err != nil {
		return nil, "", err
	}
	return client, token, nil
}

func loginAndFetchAntiCsrfToken(
	ctx context.Context,
	trackerConfig config.TrackerConfig,
	dbPath string,
	baseURL string,
	_ api.Logger,
	login api.TrackerAuthLoginRequest,
) (*http.Client, string, error) {
	username := strings.TrimSpace(trackerConfig.Username)
	password := strings.TrimSpace(trackerConfig.Password)
	announceURL := normalizedAnnounceURL(trackerConfig.AnnounceURL)
	if username == "" || password == "" || announceURL == "" {
		return nil, "", errors.New("trackers: PTP requires username, password, and announce_url")
	}
	passkey, err := passkeyFromAnnounce(announceURL)
	if err != nil {
		return nil, "", err
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: PTP create login cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	form := url.Values{
		"username":   {username},
		"password":   {password},
		"passkey":    {passkey},
		"keeplogged": {"1"},
	}
	loginURL := strings.TrimRight(baseURL, "/") + ptpLoginPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, "", fmt.Errorf("trackers: PTP build login request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("User-Agent", ptpUserAgent)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: PTP login request: %w", err)
	}
	defer resp.Body.Close()
	payload, err := decodePTPAuthResponse(resp, "login")
	if err != nil {
		return nil, "", err
	}
	switch strings.TrimSpace(stringFromAny(payload["Result"])) {
	case "Ok":
	case "TfaRequired":
		code, codeErr := resolvePTP2FACode(trackerConfig, login)
		if codeErr != nil {
			return nil, "", fmt.Errorf("trackers: PTP 2FA required: %w", codeErr)
		}
		form.Set("TfaType", "normal")
		form.Set("TfaCode", code)
		httpReq, err = http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, "", fmt.Errorf("trackers: PTP build 2FA request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		httpReq.Header.Set("User-Agent", ptpUserAgent)
		resp, err = client.Do(httpReq)
		if err != nil {
			return nil, "", fmt.Errorf("trackers: PTP 2FA request: %w", err)
		}
		defer resp.Body.Close()
		payload, err = decodePTPAuthResponse(resp, "2FA")
		if err != nil {
			return nil, "", err
		}
		if strings.TrimSpace(stringFromAny(payload["Result"])) != "Ok" {
			return nil, "", fmt.Errorf("trackers: PTP login failed: %w", ErrSubmitted2FARejected)
		}
	default:
		return nil, "", errors.New("trackers: PTP login failed")
	}

	token := strings.TrimSpace(stringFromAny(payload["AntiCsrfToken"]))
	if token == "" {
		return nil, "", errors.New("trackers: PTP login missing anti csrf token")
	}
	token, err = requestAntiCsrfToken(ctx, client, baseURL)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: PTP verify login session: %w", err)
	}
	if err := saveCookies(ctx, dbPath, client, baseURL); err != nil {
		return nil, "", fmt.Errorf("trackers: PTP persist login cookies: %w", err)
	}
	return client, token, nil
}

// resolvePTP2FACode prefers a submitted manual code so users can continue a
// browser-visible challenge when no reusable OTP URI is configured.
func resolvePTP2FACode(trackerConfig config.TrackerConfig, login api.TrackerAuthLoginRequest) (string, error) {
	if code := strings.TrimSpace(login.Code); code != "" {
		return code, nil
	}
	return resolve2FACode(trackerConfig.OTPURI)
}

func requestAntiCsrfToken(ctx context.Context, client *http.Client, baseURL string) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+ptpUploadPath, nil)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP build upload page request: %w", err)
	}
	httpReq.Header.Set("User-Agent", ptpUserAgent)
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP upload page: %w", err)
	}
	defer resp.Body.Close()
	body, err := readPTPAuthResponseBody(resp.Body)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP read upload page: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || ptpLoginPageResponse(resp, body) {
		return "", errPTPStoredSessionInvalid
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf(
			"trackers: PTP upload page unavailable status=%d response_kind=%s",
			resp.StatusCode,
			ptpAuthResponseKind(resp, body),
		)
	}
	matches := ptpAntiCsrfPattern.FindStringSubmatch(string(body))
	if len(matches) < 2 {
		return "", fmt.Errorf(
			"trackers: PTP upload page unavailable status=%d response_kind=%s",
			resp.StatusCode,
			ptpAuthResponseKind(resp, body),
		)
	}
	token := strings.TrimSpace(matches[1])
	if token == "" {
		return "", fmt.Errorf(
			"trackers: PTP upload page unavailable status=%d response_kind=%s",
			resp.StatusCode,
			ptpAuthResponseKind(resp, body),
		)
	}
	return token, nil
}

func decodePTPAuthResponse(resp *http.Response, stage string) (map[string]any, error) {
	body, err := readPTPAuthResponseBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("trackers: PTP read %s response: %w", stage, err)
	}
	responseKind := ptpAuthResponseKind(resp, body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf(
			"trackers: PTP %s unavailable status=%d response_kind=%s",
			stage,
			resp.StatusCode,
			responseKind,
		)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf(
			"trackers: PTP %s unavailable status=%d response_kind=%s",
			stage,
			resp.StatusCode,
			responseKind,
		)
	}
	return payload, nil
}

func readPTPAuthResponseBody(body io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(body, ptpAuthResponseMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read bounded response: %w", err)
	}
	if len(payload) > ptpAuthResponseMaxBytes {
		return nil, errors.New("response exceeds safe size limit")
	}
	return payload, nil
}

func ptpLoginPageResponse(resp *http.Response, body []byte) bool {
	if resp != nil && resp.Request != nil && resp.Request.URL != nil &&
		strings.Contains(strings.ToLower(resp.Request.URL.Path), "login") {
		return true
	}
	lower := bytes.ToLower(body)
	return bytes.Contains(lower, []byte(`name="username"`)) && bytes.Contains(lower, []byte(`name="password"`))
}

func ptpAuthResponseKind(resp *http.Response, body []byte) string {
	contentType := ""
	if resp != nil {
		contentType = strings.ToLower(resp.Header.Get("Content-Type"))
	}
	switch {
	case strings.Contains(contentType, "json"):
		return "json"
	case strings.Contains(contentType, "html"):
		return "html"
	case strings.HasPrefix(contentType, "text/"):
		return "text"
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "empty"
	}
	switch trimmed[0] {
	case '{', '[':
		return "json"
	case '<':
		return "html"
	default:
		return "unknown"
	}
}

func loadCookies(ctx context.Context, dbPath string) (map[string]string, error) {
	values, err := cookiepkg.LoadTrackerCookieMap(ctx, dbPath, "PTP")
	if err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	return values, nil
}

func saveCookies(ctx context.Context, dbPath string, client *http.Client, baseURL string) error {
	if client == nil || client.Jar == nil {
		return errors.New("trackers: PTP login returned no cookie jar")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("trackers: PTP parse cookie URL: %w", err)
	}
	cookies := make(map[string]string)
	for _, cookie := range client.Jar.Cookies(parsed) {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" {
			continue
		}
		if strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		cookies[strings.TrimSpace(cookie.Name)] = cookie.Value
	}
	if len(cookies) == 0 {
		return errors.New("trackers: PTP login returned no usable cookies")
	}
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "PTP", cookies); err != nil {
		return fmt.Errorf("trackers: PTP save cookies: %w", err)
	}
	return nil
}

func passkeyFromAnnounce(announceURL string) (string, error) {
	parsed, err := url.Parse(announceURL)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP parse announce URL: %w", err)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return "", errors.New("trackers: PTP failed to extract passkey from announce_url")
	}
	return parts[0], nil
}

func resolve2FACode(otpURI string) (string, error) {
	code, err := authtotp.FromURI(otpURI)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP generate TOTP: %w", err)
	}
	return code, nil
}

func normalizedAnnounceURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "http://please.passthepopcorn.me") {
		return strings.Replace(trimmed, "http://", "https://", 1)
	}
	return trimmed
}
