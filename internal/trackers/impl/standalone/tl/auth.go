// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
)

func cookieClient(ctx context.Context, dbPath string) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: TL create cookie jar: %w", err)
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Jar:     jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	cookies, err := loadCookies(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	target, _ := url.Parse(baseURL)
	jar.SetCookies(target, cookies)
	return client, nil
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

func loadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
	values, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "TL", "torrentleech.org")
	if err != nil {
		return values, fmt.Errorf("trackers: TL load cookies: %w", err)
	}
	return values, nil
}
