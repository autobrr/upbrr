// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparationstate

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSeasonEpisodeHelpers(t *testing.T) {
	t.Parallel()

	state := State{
		SeasonInt: 9,
		Release: api.ReleaseInfo{
			Season:  1,
			Episode: 2,
		},
	}

	canonicalSeason, canonicalEpisode := state.CanonicalSeasonEpisode()
	if canonicalSeason != 9 || canonicalEpisode != 0 {
		t.Fatalf("canonical season/episode = %d/%d, want 9/0", canonicalSeason, canonicalEpisode)
	}

	fallbackSeason, fallbackEpisode := state.SeasonEpisodeWithParsedFallback()
	if fallbackSeason != 9 || fallbackEpisode != 2 {
		t.Fatalf("fallback season/episode = %d/%d, want 9/2", fallbackSeason, fallbackEpisode)
	}
	if !state.HasTVSeasonEpisodeSignal() {
		t.Fatal("expected parsed release episode to provide TV signal")
	}
}
