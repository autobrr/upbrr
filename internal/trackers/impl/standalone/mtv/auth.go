// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
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

var mtvTokenPattern = regexp.MustCompile(`name="token"\s+value="([^"]{16,128})"`)

// ErrSubmitted2FARejected marks an MTV failure after a submitted manual 2FA code
// reached the tracker and was rejected.
var ErrSubmitted2FARejected = trackers.ErrSubmitted2FARejected

var errMTVAuthKeyNotFound = errors.New("trackers: MTV auth key not found")

func resolveAuthKey(ctx context.Context, baseURL string, cookies map[string]string) (string, *http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV create auth cookie jar: %w", err)
	}
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV parse base URL: %w", err)
	}
	jarCookies := make([]*http.Cookie, 0, len(cookies))
	for name, value := range cookies {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		// #nosec G124 -- Outbound tracker jar cookie mirrors configured MTV session values.
		jarCookies = append(jarCookies, &http.Cookie{
			Name:   name,
			Value:  value,
			Path:   "/",
			Domain: parsedBase.Hostname(),
		})
	}
	jar.SetCookies(parsedBase, jarCookies)

	client := &http.Client{Timeout: 20 * time.Second, Jar: jar}
	indexURL := strings.TrimRight(baseURL, "/") + mtvIndexPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV build auth request: %w", err)
	}
	req.Header.Set("User-Agent", mtvUserAgentWeb)

	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV auth request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("trackers: MTV auth key lookup failed %s: auth status %d", mtvResponseTrace(resp), resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV read auth response: %w", err)
	}
	auth := extractMTVAuthKey(string(body))
	if auth == "" {
		return "", nil, fmt.Errorf("%w: %s", errMTVAuthKeyNotFound, mtvResponseTrace(resp))
	}
	return auth, client, nil
}

func loadMTVCookies(ctx context.Context, dbPath string) (map[string]string, error) {
	values, err := cookiepkg.LoadTrackerCookieMap(ctx, dbPath, "MTV")
	if err != nil {
		return values, fmt.Errorf("trackers: MTV load cookies: %w", err)
	}
	return values, nil
}

func saveMTVCookies(ctx context.Context, dbPath string, values map[string]string) error {
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "MTV", values); err != nil {
		return fmt.Errorf("trackers: MTV save cookies: %w", err)
	}
	return nil
}

// ResolveSessionForTrackerAuth validates MTV stored cookies or logs in with
// configured credentials. After a successful login, cookie persistence failures
// are returned distinctly from remote authentication failures.
func ResolveSessionForTrackerAuth(ctx context.Context, cfg config.TrackerConfig, dbPath string) error {
	return resolveSessionForTrackerAuthAt(ctx, cfg, dbPath, mtvBaseURL)
}

func resolveSessionForTrackerAuthAt(ctx context.Context, cfg config.TrackerConfig, dbPath string, baseURL string) error {
	return resolveSessionForTrackerAuthLoginAt(ctx, cfg, dbPath, api.TrackerAuthLoginRequest{}, baseURL)
}

// ResolveSessionForTrackerAuthLogin validates MTV stored cookies or logs in
// with configured credentials. When MTV requires 2FA, login.Code is used before
// falling back to the configured OTP URI; if neither yields a code, the error
// is returned before stored cookies are replaced. A rejected submitted code can
// return [ErrSubmitted2FARejected] with the auth-key lookup failure before
// refreshed cookies are persisted. Response read errors are returned directly
// and are not classified as submitted-code rejections.
func ResolveSessionForTrackerAuthLogin(ctx context.Context, cfg config.TrackerConfig, dbPath string, login api.TrackerAuthLoginRequest) error {
	return resolveSessionForTrackerAuthLoginAt(ctx, cfg, dbPath, login, mtvBaseURL)
}

func resolveSessionForTrackerAuthLoginAt(
	ctx context.Context,
	cfg config.TrackerConfig,
	dbPath string,
	login api.TrackerAuthLoginRequest,
	baseURL string,
) error {
	cookies, err := loadMTVCookies(ctx, dbPath)
	if err == nil && len(cookies) > 0 {
		if _, _, err := resolveAuthKey(ctx, baseURL, cookies); err == nil {
			return nil
		} else if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
			return err
		}
	}
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return errors.New("trackers: MTV cookie invalid/missing and username/password not configured")
	}
	_, _, values, _, err := loginAndResolveAuthKey(ctx, cfg, baseURL, login)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return errors.New("trackers: MTV login returned no usable cookies")
	}
	if err := saveMTVCookies(ctx, dbPath, values); err != nil {
		return fmt.Errorf("trackers: MTV persist cookies after successful login: %w", err)
	}
	return nil
}

// loginAndResolveAuthKey performs MTV login, optional TOTP submission, and auth
// key discovery. MTV can return authkey in the final login response, so that
// body is checked before falling back to the index page. Missing form tokens
// and auth-key parser failures remain ordinary errors unless an auth-key miss
// follows a submitted manual code. Authenticated cookies and the effective base
// URL are returned only after auth-key discovery so upload can reuse any
// canonical host reached during login redirects.
func loginAndResolveAuthKey(
	ctx context.Context,
	cfg config.TrackerConfig,
	baseURL string,
	login api.TrackerAuthLoginRequest,
) (string, *http.Client, map[string]string, string, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return "", nil, nil, "", fmt.Errorf("trackers: MTV create login cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 25 * time.Second, Jar: jar}

	loginURL := strings.TrimRight(baseURL, "/") + "/login"
	loginReq, err := http.NewRequestWithContext(ctx, http.MethodGet, loginURL, nil)
	if err != nil {
		return "", nil, nil, "", fmt.Errorf("trackers: MTV build login page request: %w", err)
	}
	loginReq.Header.Set("User-Agent", mtvUserAgentWeb)
	loginResp, err := client.Do(loginReq)
	if err != nil {
		return "", nil, nil, "", fmt.Errorf("trackers: MTV login page request: %w", err)
	}
	loginBody, err := io.ReadAll(loginResp.Body)
	_ = loginResp.Body.Close()
	if err != nil {
		return "", nil, nil, "", fmt.Errorf("trackers: MTV read login page response: %w", err)
	}
	effectiveBaseURL := mtvEffectiveBaseURL(baseURL, loginResp)
	effectiveLoginURL := strings.TrimRight(effectiveBaseURL, "/") + "/login"
	match := mtvTokenPattern.FindStringSubmatch(string(loginBody))
	if len(match) < 2 {
		return "", nil, nil, "", errors.New("trackers: MTV login token not found")
	}
	token := strings.TrimSpace(match[1])

	form := url.Values{}
	form.Set("username", strings.TrimSpace(cfg.Username))
	form.Set("password", strings.TrimSpace(cfg.Password))
	form.Set("keeploggedin", "1")
	form.Set("cinfo", "1920|1080|24|0")
	form.Set("submit", "login")
	form.Set("iplocked", "1")
	form.Set("token", token)

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, effectiveLoginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", nil, nil, "", fmt.Errorf("trackers: MTV build login request: %w", err)
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("User-Agent", mtvUserAgentWeb)
	postResp, err := client.Do(postReq)
	if err != nil {
		return "", nil, nil, "", fmt.Errorf("trackers: MTV login request: %w", err)
	}
	body, err := io.ReadAll(postResp.Body)
	_ = postResp.Body.Close()
	if err != nil {
		return "", nil, nil, "", fmt.Errorf("trackers: MTV read login response: %w", err)
	}
	finalAuthBody := string(body)
	finalAuthTrace := mtvResponseTrace(postResp)

	submittedManualCode := false
	if postResp.Request != nil && postResp.Request.URL != nil && strings.Contains(postResp.Request.URL.Path, "/twofactor/login") {
		twoFactorTokenMatch := mtvTokenPattern.FindStringSubmatch(string(body))
		if len(twoFactorTokenMatch) < 2 {
			return "", nil, nil, "", errors.New("trackers: MTV 2FA token not found")
		}
		code, err := resolveMTV2FACode(cfg, login)
		if err != nil {
			return "", nil, nil, "", err
		}
		twoFactorForm := url.Values{}
		twoFactorForm.Set("token", twoFactorTokenMatch[1])
		twoFactorForm.Set("code", code)
		twoFactorForm.Set("submit", "login")
		twoReq, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			strings.TrimRight(effectiveBaseURL, "/")+"/twofactor/login",
			strings.NewReader(twoFactorForm.Encode()),
		)
		if err != nil {
			return "", nil, nil, "", fmt.Errorf("trackers: MTV build 2FA login request: %w", err)
		}
		twoReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		twoReq.Header.Set("User-Agent", mtvUserAgentWeb)
		twoResp, err := client.Do(twoReq)
		if err != nil {
			return "", nil, nil, "", fmt.Errorf("trackers: MTV 2FA login request: %w", err)
		}
		twoFactorBody, err := io.ReadAll(twoResp.Body)
		_ = twoResp.Body.Close()
		if err != nil {
			return "", nil, nil, "", fmt.Errorf("trackers: MTV read 2FA login response: %w", err)
		}
		finalAuthBody = string(twoFactorBody)
		finalAuthTrace = mtvResponseTrace(twoResp)
		submittedManualCode = strings.TrimSpace(login.Code) != ""
	}

	if auth := extractMTVAuthKey(finalAuthBody); auth != "" {
		cookieMap := cookiesFromJar(effectiveBaseURL, client.Jar)
		return auth, client, cookieMap, effectiveBaseURL, nil
	}
	auth, authedClient, err := resolveAuthKeyFromClient(ctx, effectiveBaseURL, client)
	if err != nil {
		if submittedManualCode && errors.Is(err, errMTVAuthKeyNotFound) {
			return "", nil, nil, "", fmt.Errorf(
				"trackers: MTV auth key not found after submitted 2FA final_%s: %w: %w",
				finalAuthTrace,
				err,
				ErrSubmitted2FARejected,
			)
		}
		if errors.Is(err, errMTVAuthKeyNotFound) {
			return "", nil, nil, "", fmt.Errorf("trackers: MTV auth key not found after login final_%s: %w", finalAuthTrace, err)
		}
		return "", nil, nil, "", fmt.Errorf("trackers: MTV auth key lookup after login final_%s: %w", finalAuthTrace, err)
	}
	cookieMap := cookiesFromJar(effectiveBaseURL, authedClient.Jar)
	return auth, authedClient, cookieMap, effectiveBaseURL, nil
}

func mtvEffectiveBaseURL(fallback string, resp *http.Response) string {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return strings.TrimRight(fallback, "/")
	}
	u := resp.Request.URL
	if strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		return strings.TrimRight(fallback, "/")
	}
	return u.Scheme + "://" + u.Host
}

func mtvResponseTrace(resp *http.Response) string {
	if resp == nil {
		return "path=unknown status=0"
	}
	path := "unknown"
	if resp.Request != nil && resp.Request.URL != nil {
		path = resp.Request.URL.EscapedPath()
		if strings.TrimSpace(path) == "" {
			path = "/"
		}
	}
	return fmt.Sprintf("path=%s status=%d", path, resp.StatusCode)
}

func extractMTVAuthKey(body string) string {
	const marker = "authkey="
	idx := strings.LastIndex(body, marker)
	if idx < 0 {
		return ""
	}
	raw := strings.TrimSpace(body[idx+len(marker):])
	if len(raw) < 32 {
		return ""
	}
	return raw[:32]
}

// resolveMTV2FACode prefers a submitted manual code so users can continue a
// browser-visible challenge when no reusable OTP URI is configured.
func resolveMTV2FACode(cfg config.TrackerConfig, login api.TrackerAuthLoginRequest) (string, error) {
	if code := strings.TrimSpace(login.Code); code != "" {
		return code, nil
	}
	code, err := authtotp.FromURI(strings.TrimSpace(cfg.OTPURI))
	if err != nil {
		return "", fmt.Errorf("trackers: MTV 2FA required but otp_uri invalid: %w", err)
	}
	return code, nil
}

func resolveAuthKeyFromClient(ctx context.Context, baseURL string, client *http.Client) (string, *http.Client, error) {
	indexURL := strings.TrimRight(baseURL, "/") + mtvIndexPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV build auth request: %w", err)
	}
	req.Header.Set("User-Agent", mtvUserAgentWeb)
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV auth request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("trackers: MTV auth key lookup failed %s: auth status %d", mtvResponseTrace(resp), resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV read auth response: %w", err)
	}
	auth := extractMTVAuthKey(string(body))
	if auth == "" {
		return "", nil, fmt.Errorf("%w: %s", errMTVAuthKeyNotFound, mtvResponseTrace(resp))
	}
	return auth, client, nil
}

func cookiesFromJar(baseURL string, jar http.CookieJar) map[string]string {
	if jar == nil {
		return nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}
	out := make(map[string]string)
	for _, cookie := range jar.Cookies(parsed) {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		out[strings.TrimSpace(cookie.Name)] = cookie.Value
	}
	return out
}
