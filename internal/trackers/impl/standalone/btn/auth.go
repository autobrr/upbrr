// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers"
	authtotp "github.com/autobrr/upbrr/internal/trackers/auth/totp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	btnDefaultBaseURL  = "https://backup.landof.tv"
	btnUploadPath      = "/upload.php"
	btnLoginPath       = "/login.php"
	btnAPIRPCURL       = "https://api.broadcasthe.net/"
	btnAPIJSONMaxBytes = 1024 * 1024
	btnTorrentMaxBytes = 8 * 1024 * 1024
)

// ErrSubmitted2FARejected marks a BTN failure after a submitted manual 2FA code
// reached the tracker and was rejected.
var ErrSubmitted2FARejected = trackers.ErrSubmitted2FARejected

var (
	errBTNCookiesMissing          = errors.New("trackers: BTN cookies not configured")
	errBTNSessionConfirmedInvalid = errors.New("trackers: BTN stored session confirmed invalid")
)

// validateBTNDryRunUploadAuth checks auth prerequisites needed before an upload
// can authenticate. Stored cookies must pass the same upload-page session gate
// as live upload; credential fallback remains local-only and does not log in or
// persist cookies during dry-run.
func validateBTNDryRunUploadAuth(ctx context.Context, req trackers.PreparationInput, uploadCtx uploadContext) error {
	values, cookieErr := loadBTNCookies(ctx, req.Runtime.DBPath)
	if cookieErr == nil && len(values) > 0 {
		client, err := newBTNClientWithCookies(uploadCtx.baseURL, values)
		if err != nil {
			return err
		}
		if err := validateBTNClientSession(ctx, client, uploadCtx.baseURL); err == nil {
			return nil
		} else if !errors.Is(err, errBTNSessionConfirmedInvalid) {
			return err
		} else if strings.TrimSpace(req.TrackerConfig.Username) == "" || strings.TrimSpace(req.TrackerConfig.Password) == "" {
			return err
		}
	}
	if cookieErr != nil && !errors.Is(cookieErr, errBTNCookiesMissing) {
		return cookieErr
	}
	if strings.TrimSpace(req.TrackerConfig.Username) == "" || strings.TrimSpace(req.TrackerConfig.Password) == "" {
		return errors.New("trackers: BTN cookie invalid/missing and username/password not configured")
	}
	return nil
}

// ensureBTNUploadSession validates imported BTN cookies before credential login.
// Credential login cookies are persisted only after the refreshed client reaches
// the upload page, so failed or incomplete logins do not replace stored auth.
func ensureBTNUploadSession(ctx context.Context, cfg config.TrackerConfig, dbPath string, uploadCtx uploadContext) (*http.Client, error) {
	values, cookieErr := loadBTNCookies(ctx, dbPath)
	if cookieErr == nil && len(values) > 0 {
		if client, err := newBTNClientWithCookies(uploadCtx.baseURL, values); err == nil {
			if err := validateBTNClientSession(ctx, client, uploadCtx.baseURL); err == nil {
				return client, nil
			} else if !errors.Is(err, errBTNSessionConfirmedInvalid) {
				return nil, err
			}
		}
	}
	if cookieErr != nil && !errors.Is(cookieErr, errBTNCookiesMissing) {
		return nil, cookieErr
	}
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, errors.New("trackers: BTN cookie invalid/missing and username/password not configured")
	}
	client, err := loginBTNSession(ctx, cfg, uploadCtx.baseURL, api.TrackerAuthLoginRequest{})
	if err != nil {
		return nil, err
	}
	if err := validateBTNClientSession(ctx, client, uploadCtx.baseURL); err != nil {
		return nil, err
	}
	if err := persistBTNCookies(ctx, dbPath, uploadCtx.baseURL, client.Jar); err != nil {
		return nil, fmt.Errorf("trackers: BTN persist cookies after successful login: %w", err)
	}
	return client, nil
}

// ResolveSessionForTrackerAuth validates BTN stored cookies or logs in with
// configured credentials. Credential login must produce reusable cookies before
// refreshed cookies are persisted.
func ResolveSessionForTrackerAuth(ctx context.Context, cfg config.TrackerConfig, dbPath string) error {
	return resolveSessionForTrackerAuthAt(ctx, cfg, dbPath, btnDefaultBaseURL)
}

func resolveSessionForTrackerAuthAt(ctx context.Context, cfg config.TrackerConfig, dbPath string, baseURL string) error {
	return resolveSessionForTrackerAuthLoginAt(ctx, cfg, dbPath, api.TrackerAuthLoginRequest{}, baseURL)
}

// ResolveSessionForTrackerAuthLogin validates BTN stored cookies or logs in
// with configured credentials. Refreshed cookies are persisted only after the
// upload page confirms the session. Manual login.Code is preferred over
// configured TOTP. Missing 2FA input preserves existing cookies; a rejected
// submitted code returns [ErrSubmitted2FARejected] before persistence.
func ResolveSessionForTrackerAuthLogin(ctx context.Context, cfg config.TrackerConfig, dbPath string, login api.TrackerAuthLoginRequest) error {
	return resolveSessionForTrackerAuthLoginAt(ctx, cfg, dbPath, login, btnDefaultBaseURL)
}

func resolveSessionForTrackerAuthLoginAt(
	ctx context.Context,
	cfg config.TrackerConfig,
	dbPath string,
	login api.TrackerAuthLoginRequest,
	baseURL string,
) error {
	err := validateBTNStoredCookies(ctx, baseURL, dbPath)
	if err == nil {
		return nil
	}
	if !errors.Is(err, errBTNCookiesMissing) && !errors.Is(err, errBTNSessionConfirmedInvalid) {
		return err
	}
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		if errors.Is(err, errBTNSessionConfirmedInvalid) {
			return err
		}
		return errors.New("trackers: BTN cookie invalid/missing and username/password not configured")
	}
	client, err := loginBTNSession(ctx, cfg, baseURL, login)
	if err != nil {
		return err
	}
	if err := validateBTNClientSession(ctx, client, baseURL); err != nil {
		if strings.TrimSpace(login.Code) != "" && errors.Is(err, errBTNSessionConfirmedInvalid) {
			return fmt.Errorf("trackers: BTN submitted 2FA validation failed: %w", ErrSubmitted2FARejected)
		}
		return err
	}
	if err := persistBTNCookies(ctx, dbPath, baseURL, client.Jar); err != nil {
		return fmt.Errorf("trackers: BTN persist cookies after successful login: %w", err)
	}
	return nil
}

// validateBTNStoredCookies checks persisted BTN cookies against the upload page.
// Confirmed logged-out evidence is returned distinctly so tracker auth can delete
// stale cookies; storage/decrypt failures and ambiguous remote/parser failures
// preserve stored cookies and block credential login.
func validateBTNStoredCookies(ctx context.Context, baseURL string, dbPath string) error {
	values, err := loadBTNCookies(ctx, dbPath)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return errBTNCookiesMissing
	}
	client, err := newBTNClientWithCookies(baseURL, values)
	if err != nil {
		return err
	}
	return validateBTNClientSession(ctx, client, baseURL)
}

// loginBTNSession performs the credential login step and leaves cookie
// persistence to callers after they validate the resulting upload session.
func loginBTNSession(ctx context.Context, cfg config.TrackerConfig, baseURL string, login api.TrackerAuthLoginRequest) (*http.Client, error) {
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, errors.New("trackers: BTN requires username/password")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN create login cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 45 * time.Second, Jar: jar}
	values := url.Values{}
	values.Set("username", strings.TrimSpace(cfg.Username))
	values.Set("password", strings.TrimSpace(cfg.Password))
	values.Set("keeplogged", "1")
	if code, err := resolveBTN2FACode(cfg, login); err == nil && code != "" {
		values.Set("codenumber", code)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+btnLoginPath, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "upbrr")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN login request: %w", err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if readErr != nil {
		return nil, fmt.Errorf("trackers: BTN read login response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		if strings.TrimSpace(login.Code) != "" && resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("trackers: BTN login failed status=%d: %w", resp.StatusCode, ErrSubmitted2FARejected)
		}
		return nil, fmt.Errorf("trackers: BTN login failed status=%d", resp.StatusCode)
	}
	bodyText := string(body)
	if btnLoginNeeds2FA(bodyText) {
		if strings.TrimSpace(login.Code) != "" {
			return nil, fmt.Errorf("trackers: BTN login failed: %w", ErrSubmitted2FARejected)
		}
		if _, err := resolveBTN2FACode(config.TrackerConfig{OTPURI: cfg.OTPURI}, api.TrackerAuthLoginRequest{}); err != nil {
			return nil, fmt.Errorf("trackers: BTN 2FA required: %w", err)
		}
	}
	if btnLoginFailed(bodyText) {
		if strings.TrimSpace(login.Code) != "" {
			return nil, fmt.Errorf("trackers: BTN login failed: %w", ErrSubmitted2FARejected)
		}
		return nil, errors.New("trackers: BTN login failed")
	}
	return client, nil
}

// validateBTNClientSession confirms the client can reach BTN's upload page.
// It treats explicit login redirects and logged-out markers as invalid session
// evidence while keeping layout misses and upstream failures transient.
func validateBTNClientSession(ctx context.Context, client *http.Client, baseURL string) error {
	if client == nil {
		return errors.New("trackers: BTN session client missing")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+btnUploadPath, nil)
	if err != nil {
		return fmt.Errorf("trackers: BTN upload auth request build: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("trackers: BTN upload auth request: %w", err)
	}
	defer resp.Body.Close()
	finalPath := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalPath = strings.ToLower(resp.Request.URL.EscapedPath())
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if readErr != nil {
		return fmt.Errorf("trackers: BTN read upload auth response: %w", readErr)
	}
	bodyText := string(body)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || strings.Contains(finalPath, "login") ||
		btnLoggedOutPage(bodyText) {
		return fmt.Errorf("%w: login required", errBTNSessionConfirmedInvalid)
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("trackers: BTN upload auth unavailable status=%d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("trackers: BTN upload auth failed status=%d", resp.StatusCode)
	}
	if !btnLooksLikeUploadPage(bodyText) {
		return errors.New("trackers: BTN upload auth page validation failed")
	}
	return nil
}

// loadBTNCookiesIntoJar best-effort seeds an upload client with persisted BTN
// cookies. Missing or unreadable cookies leave the caller's client unchanged.
func loadBTNCookiesIntoJar(ctx context.Context, client *http.Client, dbPath string, baseURL string) {
	if client == nil || client.Jar == nil {
		return
	}
	values, err := loadBTNCookies(ctx, dbPath)
	if err != nil {
		return
	}
	setBTNCookies(client.Jar, baseURL, values)
}

// loadBTNCookies reads persisted BTN cookies and maps only typed not-found
// results to the BTN missing-cookie sentinel. Storage, parse, and decrypt errors
// are returned with tracker context so callers can avoid replacing valid state.
func loadBTNCookies(ctx context.Context, dbPath string) (map[string]string, error) {
	values, err := cookies.LoadTrackerCookieMap(ctx, dbPath, "BTN")
	if err != nil {
		if errors.Is(err, cookies.ErrTrackerCookiesNotFound) {
			return nil, errBTNCookiesMissing
		}
		return nil, fmt.Errorf("trackers: %w", err)
	}
	return values, nil
}

// newBTNClientWithCookies creates a short-lived BTN client with a fresh cookie
// jar populated from the supplied stored cookie map.
func newBTNClientWithCookies(baseURL string, values map[string]string) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN create session cookie jar: %w", err)
	}
	setBTNCookies(jar, baseURL, values)
	return &http.Client{Timeout: 45 * time.Second, Jar: jar}, nil
}

// setBTNCookies mirrors stored BTN cookie values into jar for baseURL. Invalid
// base URLs or nil jars are ignored because callers treat cookie seeding as
// best-effort before explicit session validation.
func setBTNCookies(jar http.CookieJar, baseURL string, values map[string]string) {
	if jar == nil {
		return
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return
	}
	jarCookies := make([]*http.Cookie, 0, len(values))
	for name, value := range values {
		// #nosec G124 -- Outbound tracker jar cookie mirrors configured BTN session values.
		jarCookies = append(jarCookies, &http.Cookie{
			Name:   name,
			Value:  value,
			Domain: parsed.Hostname(),
			Path:   "/",
		})
	}
	jar.SetCookies(parsed, jarCookies)
}

// persistBTNCookies saves cookies extracted from a caller-validated BTN client jar.
func persistBTNCookies(ctx context.Context, dbPath string, baseURL string, jar http.CookieJar) error {
	values, err := btnCookiesFromJar(baseURL, jar)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return errors.New("trackers: BTN login returned no usable cookies")
	}
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "BTN", values); err != nil {
		return fmt.Errorf("trackers: BTN save cookies: %w", err)
	}
	return nil
}

// btnCookiesFromJar extracts non-empty BTN cookie names and values for baseURL
// after a caller has validated that the jar represents a usable session.
func btnCookiesFromJar(baseURL string, jar http.CookieJar) (map[string]string, error) {
	if jar == nil {
		return nil, errors.New("trackers: BTN login returned no cookie jar")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN parse cookie URL: %w", err)
	}
	out := make(map[string]string)
	for _, cookie := range jar.Cookies(parsed) {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		out[strings.TrimSpace(cookie.Name)] = cookie.Value
	}
	return out, nil
}

// resolveBTN2FACode returns a manually submitted code before falling back to
// the configured TOTP URI.
func resolveBTN2FACode(cfg config.TrackerConfig, login api.TrackerAuthLoginRequest) (string, error) {
	if code := strings.TrimSpace(login.Code); code != "" {
		return code, nil
	}
	return resolve2FACode(strings.TrimSpace(cfg.OTPURI))
}

// btnLoginNeeds2FA recognizes BTN login responses that render the manual 2FA
// challenge field instead of accepting the submitted credentials.
func btnLoginNeeds2FA(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "name=\"codenumber\"") ||
		strings.Contains(lower, "name='codenumber'")
}

// btnLoginFailed recognizes explicit BTN credential or submitted-code failures
// in successful HTTP responses.
func btnLoginFailed(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "invalid login") ||
		strings.Contains(lower, "incorrect password") ||
		strings.Contains(lower, "invalid code") ||
		strings.Contains(lower, "incorrect code") ||
		strings.Contains(lower, "login failed")
}

func resolve2FACode(otpURI string) (string, error) {
	code, err := authtotp.FromURI(otpURI)
	if err != nil {
		return "", fmt.Errorf("trackers: BTN generate TOTP: %w", err)
	}
	return code, nil
}
