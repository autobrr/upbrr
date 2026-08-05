// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import (
	"context"
	"fmt"
	"net/http"

	"github.com/autobrr/upbrr/internal/cookies"
)

func loadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
	values, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "IS", "immortalseed.me")
	if err != nil {
		return values, fmt.Errorf("trackers: IS load cookies: %w", err)
	}
	return values, nil
}
