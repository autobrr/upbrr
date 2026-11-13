// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTTVRequiresSeriesMetadataBeforePreparation(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"none", "tmdb", "tvdb", "tvmaze"} {
		subject := api.RuleSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV}}
		if provider == "tmdb" {
			subject.Identity.TMDBID = 100001
			subject.ProviderMetadata.TMDB = &api.TMDBMetadata{TMDBID: 100001}
		}
		if provider == "tvdb" {
			subject.Identity.TVDBID = 100001
			subject.ProviderMetadata.TVDB = &api.TVDBMetadata{TVDBID: 100001}
		}
		if provider == "tvmaze" {
			subject.Identity.TVmazeID = 100001
			subject.ProviderMetadata.TVmaze = &api.TVmazeMetadata{TVmazeID: 100001}
		}
		failures, err := trackers.EvaluateRulesWithRegistry(t.Context(), registry, "ANT", subject, nil)
		if err != nil {
			t.Fatal(err)
		}
		blocked := slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
			return f.Rule == "require_metadata_id" && f.Disposition == api.RuleDispositionStrict
		})
		if blocked != (provider == "none" || provider == "tvmaze") {
			t.Fatalf("TV metadata readiness with provider=%s: %+v", provider, failures)
		}
	}
}
