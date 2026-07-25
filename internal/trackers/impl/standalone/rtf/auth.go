// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// resolveAPIKey validates configured RTF API auth and persists a refreshed token when credentials are used.
// Callers must complete no-upload eligibility gates before invoking it.
func resolveAPIKey(ctx context.Context, req trackers.PreparationInput, baseURL string) (string, error) {
	apiKey := strings.TrimSpace(req.TrackerConfig.APIKey)
	if apiKey != "" {
		valid, err := testAPIKey(ctx, baseURL, apiKey)
		if err == nil && valid {
			return apiKey, nil
		}
		if strings.TrimSpace(req.TrackerConfig.Username) == "" || strings.TrimSpace(req.TrackerConfig.Password) == "" {
			if err != nil {
				return "", fmt.Errorf("trackers: RTF API key validation failed and username/password not configured: %w", err)
			}
			return "", errors.New("trackers: RTF API key invalid and username/password not configured")
		}
	}
	if strings.TrimSpace(req.TrackerConfig.Username) == "" || strings.TrimSpace(req.TrackerConfig.Password) == "" {
		return "", errors.New("trackers: RTF missing api_key or username/password")
	}
	refreshed, err := refreshAPIKey(ctx, baseURL, req.TrackerConfig)
	if err != nil {
		return "", err
	}
	if err := persistRefreshedAPIKey(ctx, req.Runtime.DBPath, refreshed); err != nil && req.Logger != nil {
		req.Logger.Warnf("trackers: RTF failed to persist refreshed API key: %v", err)
	}
	return refreshed, nil
}

// ResolveSessionForTrackerAuthLogin validates RTF API auth or refreshes and
// persists the API key with configured credentials for tracker-auth checks.
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
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey != "" {
		valid, err := testAPIKey(ctx, baseURL, apiKey)
		if err == nil && valid {
			return nil
		}
		if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
			if err != nil {
				return fmt.Errorf("trackers: RTF API key validation failed and username/password not configured: %w", err)
			}
			return errors.New("trackers: RTF API key invalid and username/password not configured")
		}
	}
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return errors.New("trackers: RTF missing api_key or username/password")
	}
	refreshed, err := refreshAPIKey(ctx, baseURL, cfg)
	if err != nil {
		return err
	}
	if err := persistRefreshedAPIKey(ctx, dbPath, refreshed); err != nil {
		return err
	}
	return nil
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

func persistRefreshedAPIKey(ctx context.Context, dbPath string, token string) error {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return errors.New("trackers: RTF persist refreshed API key: db path not configured")
	}
	repo, err := servicedb.OpenContext(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("trackers: RTF persist refreshed API key open db: %w", err)
	}
	defer repo.Close()
	cfg, err := config.LoadFromDatabase(ctx, repo)
	if err != nil {
		return fmt.Errorf("trackers: RTF persist refreshed API key load config: %w", err)
	}
	if cfg.Trackers.Trackers == nil {
		cfg.Trackers.Trackers = map[string]config.TrackerConfig{}
	}
	trackerKey := "RTF"
	for key := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(key), "RTF") {
			trackerKey = key
			break
		}
	}
	trackerCfg := cfg.Trackers.Trackers[trackerKey]
	trackerCfg.APIKey = strings.TrimSpace(token)
	cfg.Trackers.Trackers[trackerKey] = trackerCfg
	if err := config.SaveToDatabase(ctx, cfg, repo); err != nil {
		return fmt.Errorf("trackers: RTF persist refreshed API key: %w", err)
	}
	return nil
}
