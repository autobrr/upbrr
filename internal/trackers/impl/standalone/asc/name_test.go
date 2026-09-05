// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestReleaseNamePolicyVersion(t *testing.T) {
	t.Parallel()
	if got := Profile().ReleaseNamePolicy.ID; got != "standalone/asc/v2" {
		t.Fatalf("ASC release-name policy = %q", got)
	}
}

func TestResolveDisplayTitleHonorsOmitAlternateTitle(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release: api.ReleaseInfo{Title: "Example Release"},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
			Title:         "Example Release",
			OriginalTitle: "Example Original",
		}},
		NamePresentation: api.ReleaseNamePresentation{
			Version:            api.ReleaseNamePresentationVersionV1,
			OmitAlternateTitle: true,
		},
	}
	if got := resolveDisplayTitle(meta); got != "Example Release" {
		t.Fatalf("display title = %q", got)
	}
}

func TestResolveUploadTitleOmitsEmptySeasonEpisodeDelimiter(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		Release:  api.ReleaseInfo{Title: "Example Show"},
		NamePresentation: api.ReleaseNamePresentation{
			Version:           api.ReleaseNamePresentationVersionV1,
			OmitSeasonEpisode: true,
		},
	}
	if got := resolveUploadTitle(meta); got != "Example Show" {
		t.Fatalf("upload title = %q", got)
	}
}

func TestReleaseNamePolicyPreservesDailyEpisodeIdentity(t *testing.T) {
	t.Parallel()

	input, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker: "ASC",
		Meta: api.UploadSubject{
			Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV},
			Release: api.ReleaseInfo{
				Title:   "Example Show",
				Season:  2,
				Episode: 3,
			},
			SeasonInt:        2,
			EpisodeInt:       3,
			SeasonStr:        "S02",
			EpisodeStr:       "E03",
			DailyEpisodeDate: "2026-02-03",
			NamePresentation: api.ReleaseNamePresentation{
				Version:      api.ReleaseNamePresentationVersionV1,
				UseDailyDate: true,
			},
		},
	}, Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatalf("resolve ASC daily name: %v", failure)
	}
	got, err := input.ReviewedUploadName()
	if err != nil {
		t.Fatalf("review ASC daily name: %v", err)
	}
	if got != "Example Show - 2026-02-03" {
		t.Fatalf("daily upload title = %q", got)
	}
}
