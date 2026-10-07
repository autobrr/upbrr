// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

const (
	baseURL      = "https://amigos-share.club"
	cookieDomain = "amigos-share.club"
	userAgent    = "upbrr"
	sourceFlag   = "ASC"

	uploadPagePath = "/torrents/upload"
	uploadPath     = "/torrents"
	torrentPath    = "/torrents/"

	screenshotUploadPath = "/torrents/screenshots"

	// Video-profile limits from the upload page: screenshotsMin 2,
	// screenshotsMax 6 and limits.imageKb 5120 for movies, series and anime.
	maxScreenshots = 6
	minScreenshots = 2
	maxImageBytes  = 5 << 20

	// Local safety caps, not part of the site contract.
	maxScreenshotAttempts    = 5
	defaultRetryAfterSeconds = 5
	maxRetryAfterSeconds     = 30
	maxResponseBytes         = 8 << 20
	maxDupeSearchPages       = 5
)
