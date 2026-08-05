// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL              = "https://www.torrenthr.org"
	loginPagePath        = "/login.php"
	takeLoginPath        = "/takelogin.php"
	uploadURL            = baseURL + "/takeupload.php"
	sourceFlag           = "[https://www.torrenthr.org] TorrentHR.org"
	loginResponseMaxSize = 1024 * 1024
)

// ErrLoginFailed marks a completed THR login exchange that did not produce
// authenticated index-page evidence.
var ErrLoginFailed = errors.New("trackers: THR login failed")

func login(ctx context.Context, cfg config.TrackerConfig) (*http.Client, error) {
	return LoginSession(ctx, cfg)
}

// LoginSession performs THR's browser-style login flow. It first loads the
// login page into a cookie jar, carries all non-empty hidden form fields into
// the credential POST, follows the redirect to the authenticated page, and
// returns the live client only after the final index page contains a structured
// logout link on the same scheme and hostname.
func LoginSession(ctx context.Context, cfg config.TrackerConfig) (*http.Client, error) {
	return loginSessionAt(ctx, cfg, baseURL)
}

func loginSessionAt(ctx context.Context, cfg config.TrackerConfig, endpoint string) (*http.Client, error) {
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, errors.New("trackers: THR missing username/password")
	}
	resolvedBaseURL := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if resolvedBaseURL == "" {
		resolvedBaseURL = baseURL
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: THR create login cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	loginPageURL := resolvedBaseURL + loginPagePath
	pageReq, err := http.NewRequestWithContext(ctx, http.MethodGet, loginPageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: THR build login page request: %w", err)
	}
	pageReq.Header.Set("User-Agent", "upbrr")
	pageResp, err := client.Do(pageReq)
	if err != nil {
		return nil, fmt.Errorf("trackers: THR login page request: %w", err)
	}
	pageBody, readErr := readLoginResponse(pageResp)
	pageResp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("trackers: THR read login page: %w", readErr)
	}
	if pageResp.StatusCode < 200 || pageResp.StatusCode >= 300 {
		return nil, fmt.Errorf("trackers: THR login page failed status=%d", pageResp.StatusCode)
	}

	form := hiddenLoginFields(pageBody)
	form.Set("username", strings.TrimSpace(cfg.Username))
	form.Set("password", strings.TrimSpace(cfg.Password))
	form.Set("ssl", "yes")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resolvedBaseURL+takeLoginPath, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("trackers: THR build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "upbrr")
	req.Header.Set("Referer", loginPageURL)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trackers: THR login request: %w", err)
	}
	body, readErr := readLoginResponse(resp)
	resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("trackers: THR read login response: %w", readErr)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: status=%d", ErrLoginFailed, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("trackers: THR login response status=%d", resp.StatusCode)
	}
	var finalURL *url.URL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL
	}
	if !isAuthenticatedLoginPage(finalURL, body) {
		return nil, fmt.Errorf("%w: authenticated marker not found", ErrLoginFailed)
	}
	return client, nil
}

// isAuthenticatedLoginPage requires THR's post-login index path and a parsed
// logout anchor on the same scheme and hostname. Path fragments or incidental
// response text cannot independently prove authentication.
func isAuthenticatedLoginPage(finalURL *url.URL, body []byte) bool {
	if finalURL == nil || !strings.EqualFold(strings.Trim(finalURL.Path, "/"), "index.php") {
		return false
	}
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return false
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			if !strings.EqualFold(token.Data, "a") {
				continue
			}
			for _, attr := range token.Attr {
				if !strings.EqualFold(attr.Key, "href") {
					continue
				}
				candidate, err := url.Parse(strings.TrimSpace(attr.Val))
				if err != nil || !strings.EqualFold(strings.TrimPrefix(candidate.Path, "/"), "logout.php") {
					continue
				}
				if candidate.Scheme != "" && !strings.EqualFold(candidate.Scheme, finalURL.Scheme) {
					continue
				}
				if candidate.Host != "" && !strings.EqualFold(candidate.Hostname(), finalURL.Hostname()) {
					continue
				}
				return true
			}
		case html.TextToken, html.EndTagToken, html.CommentToken, html.DoctypeToken:
			continue
		}
	}
}

func readLoginResponse(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("empty response")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, loginResponseMaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if len(body) > loginResponseMaxSize {
		return nil, fmt.Errorf("response exceeds %d bytes", loginResponseMaxSize)
	}
	return body, nil
}

func hiddenLoginFields(body []byte) url.Values {
	fields := url.Values{}
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return fields
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			if !strings.EqualFold(token.Data, "input") {
				continue
			}
			name, value, inputType := "", "", ""
			for _, attr := range token.Attr {
				switch {
				case strings.EqualFold(attr.Key, "name"):
					name = strings.TrimSpace(attr.Val)
				case strings.EqualFold(attr.Key, "value"):
					value = attr.Val
				case strings.EqualFold(attr.Key, "type"):
					inputType = strings.TrimSpace(attr.Val)
				}
			}
			if strings.EqualFold(inputType, "hidden") && name != "" && value != "" {
				fields.Set(name, value)
			}
		case html.TextToken, html.EndTagToken, html.CommentToken, html.DoctypeToken:
			continue
		}
	}
}

func resolveAuthSession(ctx context.Context, cfg config.TrackerConfig, _ string, _ api.TrackerAuthLoginRequest) error {
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return errors.New("trackers: THR username/password not configured")
	}
	if _, err := LoginSession(ctx, cfg); err != nil {
		if errors.Is(err, ErrLoginFailed) {
			return &trackers.AuthResolutionError{
				Reason:           "login failed",
				ConfirmedInvalid: true,
				Err:              err,
			}
		}
		return &trackers.AuthResolutionError{
			Reason:    "remote login unavailable",
			Transient: true,
			Err:       err,
		}
	}
	return nil
}
