// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestMetadataRequirementMatrix(t *testing.T) {
	t.Parallel()
	registry := newMetadataRegistry(t)
	tests := []struct {
		name     string
		tracker  string
		category string
		ids      api.ExternalIdentity
		metadata api.SourceScopedMetadata
		warning  bool
		fail     bool
	}{
		{
			name:     "unit3d tmdb",
			tracker:  "AITHER",
			ids:      api.ExternalIdentity{TMDBID: 1},
			metadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release"}},
		},
		{
			name:    "unit3d id only",
			tracker: "AITHER",
			ids:     api.ExternalIdentity{TMDBID: 1},
			fail:    true,
		},
		{
			name:    "unit3d missing",
			tracker: "AITHER",
			fail:    true,
		},
		{
			name:    "ptp imdb",
			tracker: "PTP",
			ids:     api.ExternalIdentity{IMDBID: 1234567},
		},
		{
			name:    "ptp warning",
			tracker: "PTP",
			warning: true,
		},
		{
			name:     "hdb movie imdb",
			tracker:  "HDB",
			category: "movie",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
		},
		{
			name:     "hdb movie tvdb rejected",
			tracker:  "HDB",
			category: "movie",
			ids:      api.ExternalIdentity{TVDBID: 2},
			fail:     true,
		},
		{
			name:     "hdb tv imdb",
			tracker:  "HDB",
			category: "tv",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
		},
		{
			name:     "hdb tv tvdb",
			tracker:  "HDB",
			category: "tv",
			ids:      api.ExternalIdentity{TVDBID: 2},
		},
		{
			name:     "hdb tv missing",
			tracker:  "HDB",
			category: "tv",
			fail:     true,
		},
		{
			name:     "nbl tvmaze",
			tracker:  "NBL",
			category: "tv",
			ids:      api.ExternalIdentity{TVmazeID: 3},
			metadata: api.SourceScopedMetadata{TVmaze: &api.TVmazeMetadata{TVmazeID: 3, Name: "Example Series"}},
		},
		{
			name:     "nbl id only",
			tracker:  "NBL",
			category: "tv",
			ids:      api.ExternalIdentity{TVmazeID: 3},
			fail:     true,
		},
		{
			name:     "nbl wrong provider",
			tracker:  "NBL",
			category: "tv",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
			fail:     true,
		},
		{
			name:     "ant tmdb",
			tracker:  "ANT",
			category: "movie",
			ids:      api.ExternalIdentity{TMDBID: 1},
			metadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release"}},
		},
		{
			name:     "ant imdb rejected",
			tracker:  "ANT",
			category: "movie",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
			fail:     true,
		},
		{
			name:     "bhd imdb",
			tracker:  "BHD",
			category: "movie",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
			metadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"}},
		},
		{
			name:     "bhd imdb id only",
			tracker:  "BHD",
			category: "movie",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
			fail:     true,
		},
		{
			name:     "bhd tmdb rejected",
			tracker:  "BHD",
			category: "movie",
			ids:      api.ExternalIdentity{TMDBID: 1},
			metadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release"}},
			fail:     true,
		},
		{
			name:     "mtv movie imdb",
			tracker:  "MTV",
			category: "movie",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
			metadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"}},
		},
		{
			name:     "mtv movie tmdb",
			tracker:  "MTV",
			category: "movie",
			ids:      api.ExternalIdentity{TMDBID: 1},
			metadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release"}},
		},
		{
			name:     "mtv movie id only",
			tracker:  "MTV",
			category: "movie",
			ids:      api.ExternalIdentity{TMDBID: 1},
			fail:     true,
		},
		{
			name:     "mtv tv complete",
			tracker:  "MTV",
			category: "tv",
			ids:      api.ExternalIdentity{TMDBID: 1, TVDBID: 2},
			metadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Series"}, TVDB: &api.TVDBMetadata{TVDBID: 2, NameEnglish: "Example Series"}},
		},
		{
			name:     "mtv tv blank title",
			tracker:  "MTV",
			category: "tv",
			ids:      api.ExternalIdentity{TMDBID: 1, TVDBID: 2},
			metadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Series"}, TVDB: &api.TVDBMetadata{TVDBID: 2}},
			fail:     true,
		},
		{
			name:     "mtv tv mismatched title metadata",
			tracker:  "MTV",
			category: "tv",
			ids:      api.ExternalIdentity{TMDBID: 1, TVDBID: 2},
			metadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Series"}, TVDB: &api.TVDBMetadata{TVDBID: 3, Name: "Example Series"}},
			fail:     true,
		},
		{
			name:     "mtv tv tvdb identity rejected",
			tracker:  "MTV",
			category: "tv",
			ids:      api.ExternalIdentity{TVDBID: 2},
			metadata: api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{TVDBID: 2, Name: "Example Series"}},
			fail:     true,
		},
		{
			name:     "btn imdb",
			tracker:  "BTN",
			category: "tv",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
			metadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Series"}},
		},
		{
			name:     "btn tvdb",
			tracker:  "BTN",
			category: "tv",
			ids:      api.ExternalIdentity{TVDBID: 2},
			metadata: api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{TVDBID: 2, Name: "Example Series"}},
		},
		{
			name:     "btn id only",
			tracker:  "BTN",
			category: "tv",
			ids:      api.ExternalIdentity{TVDBID: 2},
			fail:     true,
		},
		{
			name:     "btn tmdb rejected",
			tracker:  "BTN",
			category: "tv",
			ids:      api.ExternalIdentity{TMDBID: 1},
			fail:     true,
		},
		{
			name:     "ar movie imdb",
			tracker:  "AR",
			category: "movie",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
			metadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{
				IMDBID: 1234567,
				Title:  "Example Release",
				Cover:  "https://img.example/poster.jpg",
			}},
		},
		{
			name:     "ar movie tmdb",
			tracker:  "AR",
			category: "movie",
			ids:      api.ExternalIdentity{TMDBID: 1},
			metadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
				TMDBID: 1,
				Title:  "Example Release",
				Poster: "https://img.example/poster.jpg",
			}},
		},
		{
			name:     "ar movie missing poster",
			tracker:  "AR",
			category: "movie",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
			metadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"}},
			fail:     true,
		},
		{
			name:     "ar movie tvdb rejected",
			tracker:  "AR",
			category: "movie",
			ids:      api.ExternalIdentity{TVDBID: 2},
			fail:     true,
		},
		{
			name:     "ar tv imdb",
			tracker:  "AR",
			category: "tv",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
			metadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{
				IMDBID: 1234567,
				Title:  "Example Series",
				Cover:  "https://img.example/poster.jpg",
			}},
		},
		{
			name:     "ar tv tmdb",
			tracker:  "AR",
			category: "tv",
			ids:      api.ExternalIdentity{TMDBID: 1},
			metadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
				TMDBID: 1,
				Title:  "Example Series",
				Poster: "https://img.example/poster.jpg",
			}},
		},
		{
			name:     "ar tv tvdb",
			tracker:  "AR",
			category: "tv",
			ids:      api.ExternalIdentity{TVDBID: 2},
			metadata: api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{
				TVDBID: 2,
				Name:   "Example Series",
				Poster: "https://img.example/poster.jpg",
			}},
		},
		{
			name:     "ar tv tvmaze rejected",
			tracker:  "AR",
			category: "tv",
			ids:      api.ExternalIdentity{TVmazeID: 3},
			metadata: api.SourceScopedMetadata{TVmaze: &api.TVmazeMetadata{
				TVmazeID: 3,
				Name:     "Example Series",
				Poster:   "https://img.example/poster.jpg",
			}},
			fail: true,
		},
		{
			name:     "ar tv missing",
			tracker:  "AR",
			category: "tv",
			fail:     true,
		},
		{
			name:     "spd tmdb",
			tracker:  "SPD",
			category: "movie",
			ids:      api.ExternalIdentity{TMDBID: 1},
			metadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release"}},
		},
		{
			name:     "spd imdb",
			tracker:  "SPD",
			category: "movie",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
			metadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"}},
		},
		{
			name:     "spd id only",
			tracker:  "SPD",
			category: "movie",
			ids:      api.ExternalIdentity{TMDBID: 1},
			fail:     true,
		},
		{
			name:     "thr imdb",
			tracker:  "THR",
			category: "movie",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
			metadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"}},
		},
		{
			name:     "tvc tmdb",
			tracker:  "TVC",
			category: "tv",
			ids:      api.ExternalIdentity{TMDBID: 1},
			metadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Series"}},
		},
		{
			name:     "tl imdb",
			tracker:  "TL",
			category: "tv",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
			metadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Series"}},
		},
		{
			name:     "bjs tmdb",
			tracker:  "BJS",
			category: "movie",
			ids:      api.ExternalIdentity{TMDBID: 1},
			metadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release"}},
		},
		{
			name:     "bjs imdb rejected",
			tracker:  "BJS",
			category: "movie",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
			metadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"}},
			fail:     true,
		},
		{
			name:     "bjs tmdb id only",
			tracker:  "BJS",
			category: "movie",
			ids:      api.ExternalIdentity{TMDBID: 1},
			fail:     true,
		},
		{
			name:     "z movie imdb",
			tracker:  "AZ",
			category: "movie",
			ids:      api.ExternalIdentity{IMDBID: 1234567},
		},
		{
			name:     "z movie tvdb rejected",
			tracker:  "CZ",
			category: "movie",
			ids:      api.ExternalIdentity{TVDBID: 2},
			fail:     true,
		},
		{
			name:     "z tv tvdb",
			tracker:  "PHD",
			category: "tv",
			ids:      api.ExternalIdentity{TVDBID: 2},
		},
		{
			name:    "czteam imdb",
			tracker: "CZT",
			ids:     api.ExternalIdentity{IMDBID: 1234567},
		},
		{
			name:    "czteam tmdb rejected",
			tracker: "CZT",
			ids:     api.ExternalIdentity{TMDBID: 1},
			fail:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			meta := api.RuleSubject{Identity: tc.ids, ProviderMetadata: tc.metadata}
			meta.Identity.Category, _ = api.NormalizeCanonicalCategory(tc.category)
			failures, evaluated := evaluateMetadataRequirementsWithRegistry(registry, tc.tracker, meta)
			if !evaluated {
				t.Fatal("expected metadata policy evaluation")
			}
			if tc.warning {
				if len(failures) != 1 || failures[0].Severity != api.RuleFailureSeverityWarning {
					t.Fatalf("expected one warning, got %#v", failures)
				}
				return
			}
			if got := api.HasBlockingRuleFailures(failures); got != tc.fail {
				t.Fatalf("blocking=%t, want %t; failures=%#v", got, tc.fail, failures)
			}
		})
	}
}

func TestMetadataRequirementRejectsStaleSourceData(t *testing.T) {
	t.Parallel()
	meta := api.RuleSubject{
		SourcePath: "current",
		Identity: api.ExternalIdentity{
			SourcePath: "stale",
			Category:   "movie",
			TMDBID:     1,
		},
	}
	failures, _ := evaluateMetadataRequirementsWithRegistry(newMetadataRegistry(t), "ANT", meta)
	if !api.HasBlockingRuleFailures(failures) {
		t.Fatalf("expected stale ID to fail, got %#v", failures)
	}
}

func TestMetadataRequirementRejectsMismatchedProviderSnapshot(t *testing.T) {
	t.Parallel()
	meta := api.RuleSubject{
		Identity: api.ExternalIdentity{Category: "movie", TMDBID: 1},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{TMDBID: 2, Title: "Example Release"},
		},
	}
	failures, _ := evaluateMetadataRequirementsWithRegistry(newMetadataRegistry(t), "ANT", meta)
	if !api.HasBlockingRuleFailures(failures) {
		t.Fatalf("expected mismatched TMDB snapshot to fail, got %#v", failures)
	}
}

func newMetadataRegistry(t *testing.T) *Registry {
	t.Helper()
	registry := NewRegistry()
	policy := &TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []MetadataRequirement{{Scope: MetadataScopeMovie, AnyOf: []MetadataField{MetadataFieldTMDB}}}}
	if err := registry.RegisterDescriptor(Descriptor{
		Name:       "ANT",
		Definition: stubDefinition{name: "ANT"},
		Metadata:   policy,
	}); err != nil {
		t.Fatalf("register ANT metadata policy: %v", err)
	}
	bhdPolicy := &TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []MetadataRequirement{{Scope: MetadataScopeMovie, AnyOf: []MetadataField{MetadataFieldIMDB}}}}
	if err := registry.RegisterDescriptor(Descriptor{
		Name:       "BHD",
		Definition: stubDefinition{name: "BHD"},
		Metadata:   bhdPolicy,
	}); err != nil {
		t.Fatalf("register BHD metadata policy: %v", err)
	}
	btnPolicy := &TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []MetadataRequirement{{Scope: MetadataScopeTV, AnyOf: []MetadataField{MetadataFieldIMDB, MetadataFieldTVDB}}}}
	if err := registry.RegisterDescriptor(Descriptor{
		Name:       "BTN",
		Definition: stubDefinition{name: "BTN"},
		Metadata:   btnPolicy,
	}); err != nil {
		t.Fatalf("register BTN metadata policy: %v", err)
	}
	hdbPolicy := &TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []MetadataRequirement{
		{Scope: MetadataScopeMovie, AnyOf: []MetadataField{MetadataFieldIMDBIDOnly}},
		{Scope: MetadataScopeTV, AnyOf: []MetadataField{MetadataFieldIMDBIDOnly, MetadataFieldTVDBIDOnly}},
	}}
	if err := registry.RegisterDescriptor(Descriptor{
		Name:       "HDB",
		Definition: stubDefinition{name: "HDB"},
		Metadata:   hdbPolicy,
	}); err != nil {
		t.Fatalf("register HDB metadata policy: %v", err)
	}
	register := func(name string, policy TrackerMetadataPolicy) {
		t.Helper()
		if err := registry.RegisterDescriptor(Descriptor{
			Name:       name,
			Definition: stubDefinition{name: name},
			Metadata:   &policy,
		}); err != nil {
			t.Fatalf("register %s metadata policy: %v", name, err)
		}
	}
	register("AITHER", TrackerMetadataPolicy{Requirements: []MetadataRequirement{{Scope: MetadataScopeAny, AnyOf: []MetadataField{MetadataFieldTMDB}}}})
	register("PTP", TrackerMetadataPolicy{Requirements: []MetadataRequirement{{
		Scope:    MetadataScopeAny,
		AnyOf:    []MetadataField{MetadataFieldIMDBIDOnly},
		Severity: api.RuleFailureSeverityWarning,
	}}})
	register("NBL", TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []MetadataRequirement{{Scope: MetadataScopeTV, AnyOf: []MetadataField{MetadataFieldTVmaze}}}})
	register("MTV", TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []MetadataRequirement{
		{Scope: MetadataScopeAny, AnyOf: []MetadataField{MetadataFieldTMDB, MetadataFieldIMDB}},
		{Scope: MetadataScopeTV, AnyOf: []MetadataField{MetadataFieldTVDBTitle}},
	}})
	register("AR", TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []MetadataRequirement{
		{Scope: MetadataScopeMovie, AnyOf: []MetadataField{MetadataFieldTMDB, MetadataFieldIMDB}},
		{Scope: MetadataScopeTV, AnyOf: []MetadataField{MetadataFieldTMDB, MetadataFieldIMDB, MetadataFieldTVDB}},
		{Scope: MetadataScopeAny, AnyOf: []MetadataField{MetadataFieldPoster}},
	}})
	for _, name := range []string{"SPD", "THR", "TVC", "TL"} {
		register(name, TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []MetadataRequirement{{Scope: MetadataScopeAny, AnyOf: []MetadataField{MetadataFieldTMDB, MetadataFieldIMDB}}}})
	}
	register("BJS", TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []MetadataRequirement{{Scope: MetadataScopeAny, AnyOf: []MetadataField{MetadataFieldTMDB}}}})
	multiID := TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []MetadataRequirement{
		{Scope: MetadataScopeMovie, AnyOf: []MetadataField{MetadataFieldTMDBIDOnly, MetadataFieldIMDBIDOnly}},
		{Scope: MetadataScopeTV, AnyOf: []MetadataField{MetadataFieldTMDBIDOnly, MetadataFieldIMDBIDOnly, MetadataFieldTVDBIDOnly}},
	}}
	for _, name := range []string{"AZ", "CZ", "PHD"} {
		register(name, multiID)
	}
	register("CZT", TrackerMetadataPolicy{Requirements: []MetadataRequirement{{Scope: MetadataScopeAny, AnyOf: []MetadataField{MetadataFieldIMDBIDOnly}}}})
	return registry
}

func TestRegistryMetadataPolicyReturnsClonedRequirements(t *testing.T) {
	t.Parallel()
	registry := newMetadataRegistry(t)
	first, ok := registry.LookupMetadataPolicy("MTV")
	if !ok {
		t.Fatal("expected MTV policy")
	}
	first.Requirements[0].AnyOf[0] = MetadataFieldPoster
	second, _ := registry.LookupMetadataPolicy("MTV")
	if second.Requirements[0].AnyOf[0] != MetadataFieldTMDB {
		t.Fatalf("policy was mutated: %#v", second)
	}
}

func TestRegistryMetadataPolicyLookupNormalizationAndExactMatch(t *testing.T) {
	t.Parallel()
	registry := newMetadataRegistry(t)
	if _, ok := registry.LookupMetadataPolicy(" aither "); !ok {
		t.Fatal("expected normalized Unit3D policy lookup")
	}
	if _, ok := registry.LookupMetadataPolicy("AITHER_EXTRA"); ok {
		t.Fatal("unexpected prefix match for unknown tracker")
	}
}

func TestMetadataRequirementNeedsKnownCategory(t *testing.T) {
	t.Parallel()
	failures, evaluated := evaluateMetadataRequirementsWithRegistry(newMetadataRegistry(t), "HDB", api.RuleSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}})
	if !evaluated || len(failures) != 1 || failures[0].Rule != "require_metadata_category" || !api.HasBlockingRuleFailures(failures) {
		t.Fatalf("expected blocking category result, got %#v", failures)
	}
}

func TestTVDBTitleRequirementRejectsStaleProviderMetadata(t *testing.T) {
	t.Parallel()
	meta := api.RuleSubject{
		SourcePath: "current",
		Identity: api.ExternalIdentity{
			SourcePath: "current",
			Category:   "tv",
			TMDBID:     1,
			TVDBID:     2,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "stale",
			TVDB:       &api.TVDBMetadata{TVDBID: 2, Name: "Example Series"},
		},
	}
	failures, _ := evaluateMetadataRequirementsWithRegistry(newMetadataRegistry(t), "MTV", meta)
	found := false
	for _, failure := range failures {
		if failure.Rule == "require_tvdb_title" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected stale TVDB metadata failure, got %#v", failures)
	}
}

func TestPTPMetadataWarningDoesNotBlock(t *testing.T) {
	t.Parallel()
	failures := EvaluateRulesWithRegistry(context.Background(), newMetadataRegistry(t), "PTP", api.RuleSubject{Identity: api.ExternalIdentity{Category: "movie"}}, nil)
	if len(failures) != 1 || failures[0].Severity != api.RuleFailureSeverityWarning || api.HasBlockingRuleFailures(failures) {
		t.Fatalf("expected non-blocking PTP warning, got %#v", failures)
	}
}

func TestMetadataRequirementTMDBOrIMDbTrackersRejectIDsAlone(t *testing.T) {
	t.Parallel()
	for _, tracker := range []string{"SPD", "THR", "TVC", "TL"} {
		t.Run(tracker, func(t *testing.T) {
			t.Parallel()
			meta := api.RuleSubject{Identity: api.ExternalIdentity{
				Category: "tv",
				TMDBID:   1,
				IMDBID:   1234567,
			}}
			failures, evaluated := evaluateMetadataRequirementsWithRegistry(newMetadataRegistry(t), tracker, meta)
			if !evaluated || !api.HasBlockingRuleFailures(failures) {
				t.Fatalf("expected IDs alone to fail for %s, got %#v", tracker, failures)
			}
		})
	}
}

func TestARMetadataPosterMayComeFromDifferentProvider(t *testing.T) {
	t.Parallel()
	meta := api.RuleSubject{
		Identity: api.ExternalIdentity{
			Category: "movie",
			TMDBID:   1,
			IMDBID:   1234567,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{TMDBID: 1, Poster: "https://img.example/poster.jpg"},
			IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"},
		},
	}
	failures, _ := evaluateMetadataRequirementsWithRegistry(newMetadataRegistry(t), "AR", meta)
	if api.HasBlockingRuleFailures(failures) {
		t.Fatalf("expected IMDb identity plus TMDB poster to pass, got %#v", failures)
	}
}

func TestARMetadataPosterRejectsMismatchedSnapshot(t *testing.T) {
	t.Parallel()
	meta := api.RuleSubject{
		Identity: api.ExternalIdentity{
			Category: "tv",
			IMDBID:   1234567,
			TVmazeID: 3,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			IMDB:   &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Series"},
			TVmaze: &api.TVmazeMetadata{TVmazeID: 4, PosterMedium: "https://img.example/poster.jpg"},
		},
	}
	failures, _ := evaluateMetadataRequirementsWithRegistry(newMetadataRegistry(t), "AR", meta)
	if len(failures) != 1 || failures[0].Rule != "require_metadata_poster" || !api.HasBlockingRuleFailures(failures) {
		t.Fatalf("expected blocking poster failure, got %#v", failures)
	}
}

func TestARMetadataPosterRejectsStaleSnapshots(t *testing.T) {
	t.Parallel()
	meta := api.RuleSubject{
		SourcePath: "current",
		Identity: api.ExternalIdentity{
			SourcePath: "current",
			Category:   "movie",
			IMDBID:     1234567,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "stale",
			IMDB: &api.IMDBMetadata{
				IMDBID: 1234567,
				Title:  "Example Release",
				Cover:  "https://img.example/poster.jpg",
			},
		},
	}
	failures, _ := evaluateMetadataRequirementsWithRegistry(newMetadataRegistry(t), "AR", meta)
	for _, failure := range failures {
		if failure.Rule == "require_metadata_poster" && failure.Severity == api.RuleFailureSeverityBlocking {
			return
		}
	}
	t.Fatalf("expected stale poster failure, got %#v", failures)
}

func TestOnlyNBLDeclaresTVmazeIdentityRequirement(t *testing.T) {
	t.Parallel()
	registry := newMetadataRegistry(t)
	foundNBL := false
	for _, tracker := range registry.Names() {
		policy, ok := registry.LookupMetadataPolicy(tracker)
		if !ok {
			continue
		}
		for _, requirement := range policy.Requirements {
			if !containsMetadataField(requirement.AnyOf, MetadataFieldTVmaze) {
				continue
			}
			if tracker != "NBL" {
				t.Fatalf("tracker %s unexpectedly accepts TVmaze identity", tracker)
			}
			foundNBL = true
		}
	}
	if !foundNBL {
		t.Fatal("expected NBL TVmaze identity requirement")
	}
}

func containsMetadataField(fields []MetadataField, want MetadataField) bool {
	return slices.Contains(fields, want)
}
