// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	rtfAPISessionStorageID  = "RTF_API_SESSION"
	rtfAPISessionTokenKey   = "token"
	rtfAPISessionBindingKey = "binding"
)

// resolveAPIKey validates configured RTF API auth and keeps any refreshed
// token in encrypted session storage. Callers must complete no-upload
// eligibility gates before invoking it.
func resolveAPIKey(ctx context.Context, req trackers.PreparationInput, baseURL string) (string, error) {
	return resolveRTFAPIKey(ctx, req.TrackerConfig, req.Runtime.DBPath, baseURL, req.Logger, false)
}

// ResolveSessionForTrackerAuthLogin validates RTF API auth or refreshes its
// encrypted API session with configured credentials for tracker-auth checks.
func ResolveSessionForTrackerAuthLogin(ctx context.Context, cfg config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
	return resolveSessionForTrackerAuthLoginAt(ctx, cfg, dbPath, api.TrackerAuthLoginRequest{}, defaultBaseURL)
}

func resolveSessionForTrackerAuthLoginAt(
	ctx context.Context,
	cfg config.TrackerConfig,
	dbPath string,
	_ api.TrackerAuthLoginRequest,
	baseURL string,
) error {
	_, err := resolveRTFAPIKey(ctx, cfg, dbPath, baseURL, nil, true)
	return err
}

func resolveRTFAPIKey(
	ctx context.Context,
	cfg config.TrackerConfig,
	dbPath string,
	baseURL string,
	logger api.Logger,
	requirePersistence bool,
) (string, error) {
	cached, cacheErr := loadCachedRTFAPIKey(ctx, dbPath, baseURL, cfg)
	if cacheErr != nil && logger != nil {
		logger.Warnf("trackers: RTF failed to load refreshed API session: %v", cacheErr)
	}

	candidates := make([]string, 0, 2)
	if cached != "" {
		candidates = append(candidates, cached)
	}
	if configured := rtfAPIKey(cfg); configured != "" && configured != cached {
		candidates = append(candidates, configured)
	}

	var validationErr error
	for _, apiKey := range candidates {
		valid, err := testAPIKey(ctx, baseURL, apiKey)
		if err == nil && valid {
			return apiKey, nil
		}
		if err != nil {
			validationErr = err
		}
	}
	if !rtfHasCredentials(cfg) {
		if validationErr != nil {
			return "", fmt.Errorf("trackers: RTF API key validation failed and username/password not configured: %w", validationErr)
		}
		if len(candidates) > 0 {
			return "", errors.New("trackers: RTF API key invalid and username/password not configured")
		}
		return "", errors.New("trackers: RTF missing api_key or username/password")
	}

	refreshed, err := refreshAPIKey(ctx, baseURL, cfg)
	if err != nil {
		return "", err
	}
	if err := persistRefreshedRTFAPIKey(ctx, dbPath, baseURL, cfg, refreshed); err != nil {
		if requirePersistence {
			return "", err
		}
		if logger != nil {
			logger.Warnf("trackers: RTF failed to persist refreshed API session: %v", err)
		}
	}
	return refreshed, nil
}

func loadCachedRTFAPIKey(ctx context.Context, dbPath string, baseURL string, cfg config.TrackerConfig) (string, error) {
	if strings.TrimSpace(dbPath) == "" {
		return "", nil
	}
	binding, err := rtfAPISessionBinding(baseURL, cfg)
	if err != nil {
		return "", err
	}
	values, err := cookies.LoadTrackerCookieMap(ctx, dbPath, rtfAPISessionStorageID)
	if errors.Is(err, cookies.ErrTrackerCookiesNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("trackers: RTF load API session: %w", err)
	}
	if values[rtfAPISessionBindingKey] != binding {
		return "", nil
	}
	return strings.TrimSpace(values[rtfAPISessionTokenKey]), nil
}

func persistRefreshedRTFAPIKey(ctx context.Context, dbPath string, baseURL string, cfg config.TrackerConfig, token string) error {
	if strings.TrimSpace(dbPath) == "" {
		return nil
	}
	binding, err := rtfAPISessionBinding(baseURL, cfg)
	if err != nil {
		return err
	}
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, rtfAPISessionStorageID, map[string]string{
		rtfAPISessionTokenKey:   strings.TrimSpace(token),
		rtfAPISessionBindingKey: binding,
	}); err != nil {
		return fmt.Errorf("trackers: RTF save API session: %w", err)
	}
	return nil
}

func rtfAPISessionBinding(baseURL string, cfg config.TrackerConfig) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("trackers: RTF invalid API session base URL")
	}
	origin := parsed.Scheme + "://" + parsed.Host
	value := strings.Join([]string{
		baseURL,
		origin,
		cfg.APIKey,
		cfg.PTPAPIKey,
		cfg.Username,
		cfg.Password,
	}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:]), nil
}

func testAPIKey(ctx context.Context, baseURL string, apiKey string) (bool, error) {
	testURL, err := joinURL(baseURL, "/api/test")
	if err != nil {
		return false, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		return false, fmt.Errorf("trackers: RTF create API test request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", strings.TrimSpace(apiKey))
	resp, err := newHTTPClient().Do(httpReq)
	if err != nil {
		return false, fmt.Errorf("trackers: RTF API test request: %w", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

func refreshAPIKey(ctx context.Context, baseURL string, cfg config.TrackerConfig) (string, error) {
	payload := map[string]string{
		"username": strings.TrimSpace(cfg.Username),
		"password": strings.TrimSpace(cfg.Password),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("trackers: RTF marshal API login payload: %w", err)
	}
	loginURL, err := joinURL(baseURL, "/api/login")
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("trackers: RTF create API login request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := newHTTPClient().Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("trackers: RTF API login request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("trackers: RTF API login failed status=%d", resp.StatusCode)
	}
	var decoded struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return "", fmt.Errorf("trackers: RTF decode API login response: %w", err)
	}
	token := strings.TrimSpace(decoded.Token)
	if token == "" {
		return "", errors.New("trackers: RTF API login response missing token")
	}
	return token, nil
}
