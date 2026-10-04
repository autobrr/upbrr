// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import "net/url"

// adjustSearchParams keeps ACM's provider search authoritative: its legacy API
// uses tmdb, and a name filter overrides that work binding. Query the full work
// and leave content-scope and variant comparisons to the duplicate evaluator.
// Reference: Upload-Assistant ACM.py at 38ce348520f5e076ef48b4842d9ff50c69a97ceb.
func adjustSearchParams(params url.Values) {
	params.Set("tmdb", params.Get("tmdbId"))
	params.Del("tmdbId")
	params.Del("name")
	params.Del("seasonNumber")
}
