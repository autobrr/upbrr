// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rmc

import "github.com/autobrr/upbrr/internal/trackers"

// Rules returns RMC's movie-only release eligibility requirement.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		RequireMovieOnly: true,
	}
}

// ValidationPolicy returns RMC's TMDB release-year cutoff, evaluated alongside
// mandatory Unit3D constructibility.
func ValidationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "unit3d-rmc-policy-v1",
		Check: checkRequirements,
	}
}
