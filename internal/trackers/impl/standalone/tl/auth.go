// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
)

func cookieClient(ctx context.Context, dbPath string, cfg config.TrackerConfig) (*http.Client, error) {
	return cookieClientWithTransport(ctx, dbPath, cfg, 30*time.Second, nil)
}

// Searches and form uploads use a fresh TL-only cookie jar and never follow
// redirects to login or another origin. Preserve the caller's transport and timeout.
func cookieClientWithTransport(
	ctx context.Context,
	dbPath string,
	cfg config.TrackerConfig,
	timeout time.Duration,
	transport http.RoundTripper,
) (*http.Client, error) {
	client, err := newCookieClient(timeout, transport)
	if err != nil {
		return nil, err
	}
	session, err := loadCookieSession(ctx, dbPath, cfg)
	if err != nil {
		return nil, err
	}
	target, _ := url.Parse(baseURL)
	client.Jar.SetCookies(target, session.Cookies)
	return client, nil
}

func newCookieClient(timeout time.Duration, transport http.RoundTripper) (*http.Client, error) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, fmt.Errorf("trackers: TL create cookie jar: %w", err)
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		Jar:       jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func announceKey(cfg config.TrackerConfig) string {
	return strings.TrimSpace(cfg.Passkey)
}

func announceList(cfg config.TrackerConfig) []string {
	passkey := strings.TrimSpace(cfg.Passkey)
	if passkey == "" {
		return nil
	}
	return []string{
		"https://tracker.torrentleech.org/a/" + passkey + "/announce",
		"https://tracker.tleechreload.org/a/" + passkey + "/announce",
	}
}

func cookieOwner(cfg config.TrackerConfig) string {
	identity := strings.TrimSpace(cfg.Username) + "\x00" + announceKey(cfg)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
}

func loadCookieSession(ctx context.Context, dbPath string, cfg config.TrackerConfig) (cookies.TrackerHTTPSession, error) {
	session, err := cookies.LoadTrackerHTTPSession(ctx, dbPath, "TL", "torrentleech.org", cookieOwner(cfg))
	if err != nil {
		return session, fmt.Errorf("trackers: TL load cookies: %w", err)
	}
	return session, nil
}
