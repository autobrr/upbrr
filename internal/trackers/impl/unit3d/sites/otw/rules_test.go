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

func hasRule(failures []api.RuleFailure, rule string) bool {
	for _, failure := range failures {
		if failure.Rule == rule {
			return true
		}
	}
	return false
}
