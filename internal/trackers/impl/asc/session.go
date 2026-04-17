// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"net/http"

	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
)

func LoadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	loaded, err := cookiepkg.LoadTrackerHTTPCookies(ctx, dbPath, sourceFlag, "cliente.amigos-share.club")
	if err != nil {
		return nil, "", err
	}
	return loaded, "", nil
}

func authProblem(ctx context.Context, dbPath string) string {
	cookies, _, err := LoadCookies(ctx, dbPath)
	if err == nil && len(cookies) > 0 {
		return ""
	}
	return "missing valid ASC cookies"
}
