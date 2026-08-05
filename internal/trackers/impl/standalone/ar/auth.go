// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/services/db"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	arBaseURL    = "https://alpharatio.cc"
	arUploadURL  = arBaseURL + "/upload.php"
	arLoginURL   = arBaseURL + "/login.php"
	arBrowseURL  = arBaseURL + "/torrents.php"
	arAuthFile   = "AR_auth.txt"
	arAuthKeyKey = "auth_key"
	arUserAgent  = "upbrr"
	arSourceFlag = "AlphaRatio"
)

var (
	arLoginFailurePattern = regexp.MustCompile(`login\.php\?act=recover|Forgot your password`)
	arURLPattern          = regexp.MustCompile(`torrents\.php\?id=(\d+)(?:&torrentid=(\d+))?`)
	arDownloadPattern     = regexp.MustCompile(`torrents\.php\?action=download&id=(\d+)`)
)

func dryRunAuthProblem(ctx context.Context, cfg config.TrackerConfig, dbPath string) string {
	if _, err := os.Stat(authPath(dbPath)); err == nil {
		return ""
	}
	if cookies, err := loadCookies(ctx, dbPath); err == nil && len(cookies) > 0 {
		return ""
	}
	if strings.TrimSpace(cfg.Username) != "" && strings.TrimSpace(cfg.Password) != "" {
		return ""
	}
	return "missing valid AR cookies/auth and username/password are not configured"
}

// resolveSession returns an authenticated AR client and auth key from stored
// cookies/auth state or by logging in with configured credentials.
func resolveSession(ctx context.Context, cfg config.TrackerConfig, dbPath string, logger api.Logger) (*http.Client, string, error) {
	if logger == nil {
		logger = api.NopLogger{}
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: AR create cookie jar: %w", err)
	}
	base, _ := url.Parse(arBaseURL + "/")
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	if cookies, err := loadCookies(ctx, dbPath); err == nil && len(cookies) > 0 {
		jar.SetCookies(base, cookies)
		authKey, valid, authErr := validateSession(ctx, client, dbPath)
		if authErr == nil && valid {
			return client, authKey, nil
		}
	}
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, "", errors.New("trackers: AR cookie invalid/missing and username/password not configured")
	}
	if err := ensureLoginPersistenceAvailable(dbPath); err != nil {
		return nil, "", err
	}
	authKey, err := login(ctx, client, cfg, dbPath)
	if err != nil {
		return nil, "", err
	}
	if err := persistLoginAuth(ctx, dbPath, logger, jar.Cookies(base), authKey); err != nil {
		return nil, "", err
	}
	return client, authKey, nil
}

func persistLoginAuth(ctx context.Context, dbPath string, logger api.Logger, values []*http.Cookie, authKey string) error {
	return persistLoginAuthWithWriter(ctx, dbPath, logger, values, authKey, writeAuthKey)
}

// persistLoginAuthWithWriter persists login cookies before the encrypted AR
// auth key so cookie persistence failures cannot commit a new key alone. If
// auth key persistence fails after cookies were saved, it restores the previous
// cookie state before returning the auth key error.
func persistLoginAuthWithWriter(
	ctx context.Context,
	dbPath string,
	logger api.Logger,
	values []*http.Cookie,
	authKey string,
	write func(context.Context, string, string) error,
) error {
	previous, hadPrevious, err := snapshotLoginCookies(ctx, dbPath)
	if err != nil {
		return err
	}
	if err := persistLoginCookies(ctx, dbPath, logger, values); err != nil {
		return err
	}
	if err := write(ctx, dbPath, authKey); err != nil {
		if rollbackErr := restoreLoginCookies(context.WithoutCancel(ctx), dbPath, previous, hadPrevious); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("trackers: AR rollback login cookies: %w", rollbackErr))
		}
		return err
	}
	return nil
}

func snapshotLoginCookies(ctx context.Context, dbPath string) ([]*http.Cookie, bool, error) {
	values, err := loadCookies(ctx, dbPath)
	if err == nil {
		return values, len(values) > 0, nil
	}
	if errors.Is(err, cookiepkg.ErrAuthHelperUnavailable) || strings.Contains(strings.ToLower(err.Error()), "no cookies found") {
		return nil, false, nil
	}
	return nil, false, err
}

func restoreLoginCookies(ctx context.Context, dbPath string, previous []*http.Cookie, hadPrevious bool) error {
	if hadPrevious {
		return saveCookies(ctx, dbPath, previous)
	}
	if err := cookiepkg.DeleteTrackerCookies(ctx, dbPath, "AR"); err != nil {
		return fmt.Errorf("trackers: AR delete cookies: %w", err)
	}
	return nil
}

// persistLoginCookies saves encrypted cookies after login; persistence failures
// are returned so callers do not report login success without durable cookies.
func persistLoginCookies(ctx context.Context, dbPath string, logger api.Logger, values []*http.Cookie) error {
	_ = logger
	if len(usableHTTPCookies(values)) == 0 {
		return errors.New("trackers: AR login returned no usable cookies")
	}
	if err := saveCookies(ctx, dbPath, values); err != nil {
		return err
	}
	return nil
}

func ensureLoginPersistenceAvailable(dbPath string) error {
	if _, err := authmaterial.LoadFromDBPath(dbPath); err != nil {
		if errors.Is(err, authmaterial.ErrUnavailable) {
			return fmt.Errorf("trackers: AR encrypted auth storage unavailable before credential login: %w", cookiepkg.ErrAuthHelperUnavailable)
		}
		return fmt.Errorf("trackers: AR check encrypted auth storage before credential login: %w", err)
	}
	return nil
}

// validateSession checks the current AR session and refreshes encrypted auth
// state when the response exposes an auth key.
func validateSession(ctx context.Context, client *http.Client, dbPath string) (string, bool, error) {
	return validateSessionWithAuthKeyPersistence(ctx, client, dbPath, true)
}

// validateSessionAfterLogin checks the just-authenticated AR session without
// persisting the auth key before login cookies are durably saved.
func validateSessionAfterLogin(ctx context.Context, client *http.Client, dbPath string) (string, bool, error) {
	return validateSessionWithAuthKeyPersistence(ctx, client, dbPath, false)
}

func validateSessionWithAuthKeyPersistence(ctx context.Context, client *http.Client, dbPath string, persistAuthKey bool) (string, bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, arBrowseURL, nil)
	if err != nil {
		return "", false, fmt.Errorf("trackers: AR session validation request build: %w", err)
	}
	httpReq.Header.Set("User-Agent", arUserAgent)
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", false, fmt.Errorf("trackers: AR session validation request: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, fmt.Errorf("trackers: AR read session response: %w", err)
	}
	body := string(bodyBytes)
	if resp.StatusCode != http.StatusOK || arLoginFailurePattern.MatchString(body) {
		return "", false, nil
	}
	authKey := extractAuthKey(body)
	if authKey == "" {
		if authKey := readAuthKey(ctx, dbPath); authKey != "" {
			return authKey, true, nil
		}
		return "", false, nil
	}
	if !persistAuthKey {
		return authKey, true, nil
	}
	if err := writeAuthKey(ctx, dbPath, authKey); err != nil {
		return "", false, err
	}
	return authKey, true, nil
}

func login(ctx context.Context, client *http.Client, cfg config.TrackerConfig, dbPath string) (string, error) {
	values := url.Values{
		"username":   {strings.TrimSpace(cfg.Username)},
		"password":   {strings.TrimSpace(cfg.Password)},
		"keeplogged": {"1"},
		"login":      {"Login"},
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, arLoginURL, strings.NewReader(values.Encode()))
	if err != nil {
		return "", fmt.Errorf("trackers: AR login request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("User-Agent", arUserAgent)
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("trackers: AR login request: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("trackers: AR read login response: %w", err)
	}
	body := string(bodyBytes)
	if resp.StatusCode != http.StatusOK || arLoginFailurePattern.MatchString(body) {
		return "", errors.New("trackers: AR login failed")
	}
	authKey := extractAuthKey(body)
	if authKey != "" {
		return authKey, nil
	}
	authKey, valid, err := validateSessionAfterLogin(ctx, client, dbPath)
	if err != nil {
		return "", err
	}
	if !valid || authKey == "" {
		return "", errors.New("trackers: AR auth key not found after login")
	}
	return authKey, nil
}

func authPath(dbPath string) string {
	if strings.TrimSpace(dbPath) == "" {
		return ""
	}
	path, err := db.CookiePath(dbPath, arAuthFile)
	if err != nil {
		return ""
	}
	return path
}

// readAuthKey prefers encrypted AR auth state and falls back to the legacy
// plaintext auth key file.
func readAuthKey(ctx context.Context, dbPath string) string {
	if authKey, err := trackerauth.LoadAuthState(ctx, dbPath, "AR", arAuthKeyKey); err == nil && strings.TrimSpace(authKey) != "" {
		return strings.TrimSpace(authKey)
	}
	payload, err := os.ReadFile(authPath(dbPath))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(payload))
}

// writeAuthKey persists AR auth keys only to encrypted tracker auth state and
// deletes any legacy plaintext key after a successful write. If legacy cleanup
// fails, the encrypted write is rolled back so callers never commit split auth
// state. Existing legacy installs without web auth keep using the legacy path.
func writeAuthKey(ctx context.Context, dbPath string, authKey string) error {
	authKey = strings.TrimSpace(authKey)
	if authKey == "" {
		return nil
	}
	previous, err := snapshotEncryptedAuthKey(ctx, dbPath)
	if err != nil {
		return err
	}
	if !previous.available {
		return writeLegacyAuthKeyIfPresent(dbPath, authKey, previous.err)
	}
	if err := trackerauth.SaveAuthState(ctx, dbPath, "AR", arAuthKeyKey, authKey); err != nil {
		return fmt.Errorf("trackers: AR write encrypted auth key: %w", err)
	}
	if legacyPath := authPath(dbPath); legacyPath != "" {
		if err := os.Remove(legacyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			if rollbackErr := restoreEncryptedAuthKey(context.WithoutCancel(ctx), dbPath, previous); rollbackErr != nil {
				return errors.Join(
					fmt.Errorf("trackers: AR remove legacy auth key: %w", err),
					fmt.Errorf("trackers: AR rollback encrypted auth key: %w", rollbackErr),
				)
			}
			return fmt.Errorf("trackers: AR remove legacy auth key: %w", err)
		}
	}
	return nil
}

type encryptedAuthKeySnapshot struct {
	value     string
	existed   bool
	available bool
	err       error
}

func snapshotEncryptedAuthKey(ctx context.Context, dbPath string) (encryptedAuthKeySnapshot, error) {
	value, err := trackerauth.LoadAuthState(ctx, dbPath, "AR", arAuthKeyKey)
	if err == nil {
		return encryptedAuthKeySnapshot{
			value:     value,
			existed:   true,
			available: true,
		}, nil
	}
	if errors.Is(err, trackerauth.ErrAuthStateNotFound) {
		return encryptedAuthKeySnapshot{available: true}, nil
	}
	if errors.Is(err, cookiepkg.ErrAuthHelperUnavailable) {
		return encryptedAuthKeySnapshot{available: false, err: err}, nil
	}
	return encryptedAuthKeySnapshot{}, fmt.Errorf("trackers: AR snapshot encrypted auth key: %w", err)
}

func restoreEncryptedAuthKey(ctx context.Context, dbPath string, previous encryptedAuthKeySnapshot) error {
	if previous.existed {
		if err := trackerauth.SaveAuthState(ctx, dbPath, "AR", arAuthKeyKey, previous.value); err != nil {
			return fmt.Errorf("trackers: AR restore encrypted auth key: %w", err)
		}
		return nil
	}
	err := trackerauth.DeleteAuthState(ctx, dbPath, "AR", arAuthKeyKey)
	if errors.Is(err, trackerauth.ErrAuthStateNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("trackers: AR remove restored encrypted auth key: %w", err)
	}
	return nil
}

func writeLegacyAuthKeyIfPresent(dbPath string, authKey string, encryptedErr error) error {
	legacyPath := authPath(dbPath)
	if legacyPath == "" {
		return fmt.Errorf("trackers: AR write encrypted auth key: %w", encryptedErr)
	}
	info, statErr := os.Stat(legacyPath)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("trackers: AR write encrypted auth key: %w", encryptedErr)
		}
		return fmt.Errorf("trackers: AR stat legacy auth key: %w", statErr)
	}
	if info.IsDir() {
		return errors.New("trackers: AR legacy auth key path is a directory")
	}
	if err := os.WriteFile(legacyPath, []byte(authKey), 0o600); err != nil {
		return fmt.Errorf("trackers: AR write legacy auth key: %w", err)
	}
	return nil
}

func loadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
	values, err := cookiepkg.LoadTrackerHTTPCookies(ctx, dbPath, "AR", "alpharatio.cc")
	if err != nil {
		return values, fmt.Errorf("trackers: AR load cookies: %w", err)
	}
	return values, nil
}

func saveCookies(ctx context.Context, dbPath string, values []*http.Cookie) error {
	valid := usableHTTPCookies(values)
	if len(valid) == 0 {
		return errors.New("trackers: AR login returned no usable cookies")
	}
	if err := cookiepkg.SaveTrackerHTTPCookies(ctx, dbPath, "AR", valid); err != nil {
		return fmt.Errorf("trackers: AR save cookies: %w", err)
	}
	return nil
}

func usableHTTPCookies(values []*http.Cookie) []*http.Cookie {
	out := make([]*http.Cookie, 0, len(values))
	for _, cookie := range values {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		out = append(out, cookie)
	}
	return out
}

func extractAuthKey(body string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(body))
	for {
		//nolint:exhaustive // We only inspect tokens relevant to extracting the auth key.
		switch tokenizer.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			if !strings.EqualFold(token.Data, "a") {
				continue
			}
			href := ""
			for _, attr := range token.Attr {
				if strings.EqualFold(attr.Key, "href") {
					href = attr.Val
					break
				}
			}
			if href == "" || !strings.Contains(href, "auth=") {
				continue
			}
			parsed, err := url.Parse(href)
			if err != nil {
				continue
			}
			authKey := strings.TrimSpace(parsed.Query().Get("auth"))
			if authKey != "" {
				return authKey
			}
		}
	}
}
