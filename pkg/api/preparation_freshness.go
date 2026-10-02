// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// ExternalFreshness controls whether preparation may reuse retained external
// provider facts, retry empty results on a new input load, or fetch current data.
type ExternalFreshness string

const (
	// ExternalFreshnessReuse permits retained provider facts to satisfy an
	// ordinary preparation request.
	ExternalFreshnessReuse ExternalFreshness = ""
	// ExternalFreshnessLoad permits a newly loaded input to retry retained empty
	// provider results while reusing successful facts and completed failures.
	ExternalFreshnessLoad ExternalFreshness = "load"
	// ExternalFreshnessRefresh requires current provider responses. It is a
	// one-shot collection control and does not change preparation compatibility.
	ExternalFreshnessRefresh ExternalFreshness = "refresh"
)

// RequiresRefresh reports whether provider collection must obtain current data.
func (freshness ExternalFreshness) RequiresRefresh() bool {
	return freshness == ExternalFreshnessRefresh
}
