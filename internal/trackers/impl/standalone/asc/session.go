// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"fmt"
	"net/http"

	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
)

// LoadCookies loads ASC cookies from shared storage for the ASC web domain.
// The legacy source-label return value is always empty. Callers must pass a
// valid non-nil context.
func LoadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, string, error) {
	loaded, err := cookiepkg.LoadTrackerHTTPCookies(ctx, dbPath, sourceFlag, "cliente.amigos-share.club")
	if err != nil {
		return nil, "", fmt.Errorf("trackers: %w", err)
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
