// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package providerid formats canonical numeric provider identifiers for
// external text contracts.
package providerid

import (
	"strconv"
	"strings"
)

const (
	imdbMinimumDigits = 7
	imdbPrefix        = "tt"
	imdbTitleBaseURL  = "https://www.imdb.com/title/"
)

// IMDb is the canonical numeric portion of an IMDb title identifier.
// Values less than or equal to zero represent absence.
type IMDb int

// Decimal returns the unpadded numeric identifier, or an empty string when absent.
func (id IMDb) Decimal() string {
	if id <= 0 {
		return ""
	}
	return strconv.Itoa(int(id))
}

// Digits returns at least seven numeric digits, or an empty string when absent.
// Identifiers longer than seven digits are preserved.
func (id IMDb) Digits() string {
	value := id.Decimal()
	if value == "" || len(value) >= imdbMinimumDigits {
		return value
	}
	return strings.Repeat("0", imdbMinimumDigits-len(value)) + value
}

// Prefixed returns the tt-prefixed padded identifier, or an empty string when absent.
func (id IMDb) Prefixed() string {
	value := id.Digits()
	if value == "" {
		return ""
	}
	return imdbPrefix + value
}

// URL returns the canonical IMDb title URL, or an empty string when absent.
func (id IMDb) URL() string {
	value := id.Prefixed()
	if value == "" {
		return ""
	}
	return imdbTitleBaseURL + value
}
