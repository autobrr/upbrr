// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package otw

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestFallbackMetadataRequiresCurrentTMDBUnavailableEvidence(t *testing.T) {
	t.Parallel()
	const sourcePath = "Example.Release.2026.1080p-GRP"
	subject := api.TrackerValidationSubject{
		SourcePath: sourcePath,
		Release:    api.ReleaseInfo{Genre: "Animation"},
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Generation: 6,
			Category:   api.CanonicalCategoryMovie,
			IMDBID:     1234567,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			Generation: 6,
			IMDB:       &api.IMDBMetadata{IMDBID: 1234567},
		},
	}

	failures, err := checkGenres(context.Background(), subject, nil)
	if err != nil {
		t.Fatalf("validate fallback: %v", err)
	}
	if !hasRule(failures, "preferred_metadata_provider") {
		t.Fatalf("missing preferred-provider failure: %#v", failures)
	}

	subject.ProviderMetadata.ProviderAvailability = []api.ProviderAvailabilityEvidence{{
		Provider: api.IdentityProviderTMDB,
		Status:   api.ProviderAvailabilityStatusNotFound,
		Source:   "tmdb_find/v1",
	}}
	failures, err = checkGenres(context.Background(), subject, nil)
	if err != nil {
		t.Fatalf("validate explicit fallback: %v", err)
	}
	if hasRule(failures, "preferred_metadata_provider") {
		t.Fatalf("explicit current TMDB-unavailable evidence was rejected: %#v", failures)
	}

	subject.Identity.TMDBID = 987650001
	failures, err = checkGenres(context.Background(), subject, nil)
	if err != nil {
		t.Fatalf("validate contradictory fallback: %v", err)
	}
	if !hasRule(failures, "preferred_metadata_provider") {
		t.Fatalf("TMDB-unavailable evidence contradicted by canonical ID was accepted: %#v", failures)
	}
}

func TestGenreRulesUseSharedProviderGenres(t *testing.T) {
	t.Parallel()

	const sourcePath = "Example.Release.2026.1080p-GRP"
	subject := api.TrackerValidationSubject{
		SourcePath: sourcePath,
		Release:    api.ReleaseInfo{Genre: "Adult"},
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Generation: 4,
			TMDBID:     1234567,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			Generation: 4,
			TMDB: &api.TMDBMetadata{
				TMDBID:   1234567,
				Title:    "Example Release",
				Genres:   "Animation",
				Keywords: "Adult",
			},
			TVmaze: &api.TVmazeMetadata{Genres: "Family"},
		},
	}

	failures, err := checkGenres(context.Background(), subject, nil)
	if err != nil {
		t.Fatalf("validate provider genres: %v", err)
	}
	for _, rule := range []string{"genre", "block_adult"} {
		if hasRule(failures, rule) {
			t.Fatalf("release genre or keyword caused %s failure: %#v", rule, failures)
		}
	}

	subject.ProviderMetadata.TVmaze.Genres = "Adult Animation"
	failures, err = checkGenres(context.Background(), subject, nil)
	if err != nil {
		t.Fatalf("validate adult provider genre: %v", err)
	}
	if !hasRule(failures, "block_adult") {
		t.Fatalf("shared adult genre was not blocked: %#v", failures)
	}
}

func hasRule(failures []api.RuleFailure, rule string) bool {
	for _, failure := range failures {
		if failure.Rule == rule {
			return true
		}
	}
	return false
}
