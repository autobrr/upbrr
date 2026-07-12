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
	tests := []struct {
		name     string
		tracker  string
		category string
		ids      api.ExternalIDs
		metadata api.ExternalMetadata
		warning  bool
		fail     bool
	}{
		{name: "unit3d tmdb", tracker: "AITHER", ids: api.ExternalIDs{TMDBID: 1}, metadata: api.ExternalMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release"}}},
		{name: "unit3d id only", tracker: "AITHER", ids: api.ExternalIDs{TMDBID: 1}, fail: true},
		{name: "unit3d missing", tracker: "AITHER", fail: true},
		{name: "ptp imdb", tracker: "PTP", ids: api.ExternalIDs{IMDBID: 1234567}},
		{name: "ptp warning", tracker: "PTP", warning: true},
		{name: "hdb movie imdb", tracker: "HDB", category: "movie", ids: api.ExternalIDs{IMDBID: 1234567}},
		{name: "hdb movie tvdb rejected", tracker: "HDB", category: "movie", ids: api.ExternalIDs{TVDBID: 2}, fail: true},
		{name: "hdb tv imdb", tracker: "HDB", category: "tv", ids: api.ExternalIDs{IMDBID: 1234567}},
		{name: "hdb tv tvdb", tracker: "HDB", category: "tv", ids: api.ExternalIDs{TVDBID: 2}},
		{name: "hdb tv missing", tracker: "HDB", category: "tv", fail: true},
		{name: "nbl tvmaze", tracker: "NBL", category: "tv", ids: api.ExternalIDs{TVmazeID: 3}, metadata: api.ExternalMetadata{TVmaze: &api.TVmazeMetadata{TVmazeID: 3, Name: "Example Series"}}},
		{name: "nbl id only", tracker: "NBL", category: "tv", ids: api.ExternalIDs{TVmazeID: 3}, fail: true},
		{name: "nbl wrong provider", tracker: "NBL", category: "tv", ids: api.ExternalIDs{IMDBID: 1234567}, fail: true},
		{name: "ant tmdb", tracker: "ANT", category: "movie", ids: api.ExternalIDs{TMDBID: 1}, metadata: api.ExternalMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release"}}},
		{name: "ant imdb rejected", tracker: "ANT", category: "movie", ids: api.ExternalIDs{IMDBID: 1234567}, fail: true},
		{name: "bhd imdb", tracker: "BHD", category: "movie", ids: api.ExternalIDs{IMDBID: 1234567}, metadata: api.ExternalMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"}}},
		{name: "bhd imdb id only", tracker: "BHD", category: "movie", ids: api.ExternalIDs{IMDBID: 1234567}, fail: true},
		{name: "bhd tmdb rejected", tracker: "BHD", category: "movie", ids: api.ExternalIDs{TMDBID: 1}, metadata: api.ExternalMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release"}}, fail: true},
		{name: "mtv movie imdb", tracker: "MTV", category: "movie", ids: api.ExternalIDs{IMDBID: 1234567}, metadata: api.ExternalMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"}}},
		{name: "mtv movie tmdb", tracker: "MTV", category: "movie", ids: api.ExternalIDs{TMDBID: 1}, metadata: api.ExternalMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release"}}},
		{name: "mtv movie id only", tracker: "MTV", category: "movie", ids: api.ExternalIDs{TMDBID: 1}, fail: true},
		{name: "mtv tv complete", tracker: "MTV", category: "tv", ids: api.ExternalIDs{TMDBID: 1, TVDBID: 2}, metadata: api.ExternalMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Series"}, TVDB: &api.TVDBMetadata{TVDBID: 2, NameEnglish: "Example Series"}}},
		{name: "mtv tv blank title", tracker: "MTV", category: "tv", ids: api.ExternalIDs{TMDBID: 1, TVDBID: 2}, metadata: api.ExternalMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Series"}, TVDB: &api.TVDBMetadata{TVDBID: 2}}, fail: true},
		{name: "mtv tv mismatched title metadata", tracker: "MTV", category: "tv", ids: api.ExternalIDs{TMDBID: 1, TVDBID: 2}, metadata: api.ExternalMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Series"}, TVDB: &api.TVDBMetadata{TVDBID: 3, Name: "Example Series"}}, fail: true},
		{name: "mtv tv tvdb identity rejected", tracker: "MTV", category: "tv", ids: api.ExternalIDs{TVDBID: 2}, metadata: api.ExternalMetadata{TVDB: &api.TVDBMetadata{TVDBID: 2, Name: "Example Series"}}, fail: true},
		{name: "btn imdb", tracker: "BTN", category: "tv", ids: api.ExternalIDs{IMDBID: 1234567}, metadata: api.ExternalMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Series"}}},
		{name: "btn tvdb", tracker: "BTN", category: "tv", ids: api.ExternalIDs{TVDBID: 2}, metadata: api.ExternalMetadata{TVDB: &api.TVDBMetadata{TVDBID: 2, Name: "Example Series"}}},
		{name: "btn id only", tracker: "BTN", category: "tv", ids: api.ExternalIDs{TVDBID: 2}, fail: true},
		{name: "btn tmdb rejected", tracker: "BTN", category: "tv", ids: api.ExternalIDs{TMDBID: 1}, fail: true},
		{name: "ar movie imdb", tracker: "AR", category: "movie", ids: api.ExternalIDs{IMDBID: 1234567}, metadata: api.ExternalMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release", Cover: "https://img.example/poster.jpg"}}},
		{name: "ar movie tmdb", tracker: "AR", category: "movie", ids: api.ExternalIDs{TMDBID: 1}, metadata: api.ExternalMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release", Poster: "https://img.example/poster.jpg"}}},
		{name: "ar movie missing poster", tracker: "AR", category: "movie", ids: api.ExternalIDs{IMDBID: 1234567}, metadata: api.ExternalMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"}}, fail: true},
		{name: "ar movie tvdb rejected", tracker: "AR", category: "movie", ids: api.ExternalIDs{TVDBID: 2}, fail: true},
		{name: "ar tv imdb", tracker: "AR", category: "tv", ids: api.ExternalIDs{IMDBID: 1234567}, metadata: api.ExternalMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Series", Cover: "https://img.example/poster.jpg"}}},
		{name: "ar tv tmdb", tracker: "AR", category: "tv", ids: api.ExternalIDs{TMDBID: 1}, metadata: api.ExternalMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Series", Poster: "https://img.example/poster.jpg"}}},
		{name: "ar tv tvdb", tracker: "AR", category: "tv", ids: api.ExternalIDs{TVDBID: 2}, metadata: api.ExternalMetadata{TVDB: &api.TVDBMetadata{TVDBID: 2, Name: "Example Series", Poster: "https://img.example/poster.jpg"}}},
		{name: "ar tv tvmaze rejected", tracker: "AR", category: "tv", ids: api.ExternalIDs{TVmazeID: 3}, metadata: api.ExternalMetadata{TVmaze: &api.TVmazeMetadata{TVmazeID: 3, Name: "Example Series", Poster: "https://img.example/poster.jpg"}}, fail: true},
		{name: "ar tv missing", tracker: "AR", category: "tv", fail: true},
		{name: "spd tmdb", tracker: "SPD", category: "movie", ids: api.ExternalIDs{TMDBID: 1}, metadata: api.ExternalMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release"}}},
		{name: "spd imdb", tracker: "SPD", category: "movie", ids: api.ExternalIDs{IMDBID: 1234567}, metadata: api.ExternalMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"}}},
		{name: "spd id only", tracker: "SPD", category: "movie", ids: api.ExternalIDs{TMDBID: 1}, fail: true},
		{name: "thr imdb", tracker: "THR", category: "movie", ids: api.ExternalIDs{IMDBID: 1234567}, metadata: api.ExternalMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"}}},
		{name: "tvc tmdb", tracker: "TVC", category: "tv", ids: api.ExternalIDs{TMDBID: 1}, metadata: api.ExternalMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Series"}}},
		{name: "tl imdb", tracker: "TL", category: "tv", ids: api.ExternalIDs{IMDBID: 1234567}, metadata: api.ExternalMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Series"}}},
		{name: "bjs tmdb", tracker: "BJS", category: "movie", ids: api.ExternalIDs{TMDBID: 1}, metadata: api.ExternalMetadata{TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release"}}},
		{name: "bjs imdb rejected", tracker: "BJS", category: "movie", ids: api.ExternalIDs{IMDBID: 1234567}, metadata: api.ExternalMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"}}, fail: true},
		{name: "bjs tmdb id only", tracker: "BJS", category: "movie", ids: api.ExternalIDs{TMDBID: 1}, fail: true},
		{name: "z movie imdb", tracker: "AZ", category: "movie", ids: api.ExternalIDs{IMDBID: 1234567}},
		{name: "z movie tvdb rejected", tracker: "CZ", category: "movie", ids: api.ExternalIDs{TVDBID: 2}, fail: true},
		{name: "z tv tvdb", tracker: "PHD", category: "tv", ids: api.ExternalIDs{TVDBID: 2}},
		{name: "czteam imdb", tracker: "CZT", ids: api.ExternalIDs{IMDBID: 1234567}},
		{name: "czteam tmdb rejected", tracker: "CZT", ids: api.ExternalIDs{TMDBID: 1}, fail: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			meta := api.PreparedMetadata{ExternalIDs: tc.ids, ExternalMetadata: tc.metadata}
			meta.ExternalIDs.Category = tc.category
			failures, evaluated := evaluateMetadataRequirements(tc.tracker, meta)
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
	meta := api.PreparedMetadata{
		SourcePath:  "current",
		ExternalIDs: api.ExternalIDs{SourcePath: "stale", Category: "movie", TMDBID: 1},
	}
	failures, _ := evaluateMetadataRequirements("ANT", meta)
	if !api.HasBlockingRuleFailures(failures) {
		t.Fatalf("expected stale ID to fail, got %#v", failures)
	}
}

func TestMetadataRequirementRejectsMismatchedProviderSnapshot(t *testing.T) {
	t.Parallel()
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "movie", TMDBID: 1},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{TMDBID: 2, Title: "Example Release"},
		},
	}
	failures, _ := evaluateMetadataRequirements("ANT", meta)
	if !api.HasBlockingRuleFailures(failures) {
		t.Fatalf("expected mismatched TMDB snapshot to fail, got %#v", failures)
	}
}

func TestMetadataPolicyForReturnsClonedRequirements(t *testing.T) {
	t.Parallel()
	first, ok := MetadataPolicyFor("HDB")
	if !ok {
		t.Fatal("expected HDB policy")
	}
	first.Requirements[0].AnyOf[0] = MetadataFieldTMDBIDOnly
	second, _ := MetadataPolicyFor("HDB")
	if second.Requirements[0].AnyOf[0] != MetadataFieldIMDBIDOnly {
		t.Fatalf("policy was mutated: %#v", second)
	}
}

func TestMetadataPolicyLookupNormalizationAndExactFamilyMatch(t *testing.T) {
	t.Parallel()
	if _, ok := MetadataPolicyFor(" aither "); !ok {
		t.Fatal("expected normalized Unit3D policy lookup")
	}
	if _, ok := MetadataPolicyFor("AITHER_EXTRA"); ok {
		t.Fatal("unexpected prefix match for unknown tracker")
	}
}

func TestMetadataRequirementNeedsKnownCategory(t *testing.T) {
	t.Parallel()
	failures, evaluated := evaluateMetadataRequirements("HDB", api.PreparedMetadata{ExternalIDs: api.ExternalIDs{IMDBID: 1234567}})
	if !evaluated || len(failures) != 1 || failures[0].Rule != "require_metadata_category" || !api.HasBlockingRuleFailures(failures) {
		t.Fatalf("expected blocking category result, got %#v", failures)
	}
}

func TestTVDBTitleRequirementRejectsStaleProviderMetadata(t *testing.T) {
	t.Parallel()
	meta := api.PreparedMetadata{
		SourcePath:  "current",
		ExternalIDs: api.ExternalIDs{SourcePath: "current", Category: "tv", TMDBID: 1, TVDBID: 2},
		ExternalMetadata: api.ExternalMetadata{
			SourcePath: "stale",
			TVDB:       &api.TVDBMetadata{TVDBID: 2, Name: "Example Series"},
		},
	}
	failures, _ := evaluateMetadataRequirements("MTV", meta)
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
	failures := EvaluateRules(context.Background(), "PTP", api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "movie"}}, nil)
	if len(failures) != 1 || failures[0].Severity != api.RuleFailureSeverityWarning || api.HasBlockingRuleFailures(failures) {
		t.Fatalf("expected non-blocking PTP warning, got %#v", failures)
	}
}

func TestMetadataRequirementTMDBOrIMDbTrackersRejectIDsAlone(t *testing.T) {
	t.Parallel()
	for _, tracker := range []string{"SPD", "THR", "TVC", "TL"} {
		t.Run(tracker, func(t *testing.T) {
			t.Parallel()
			meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "tv", TMDBID: 1, IMDBID: 1234567}}
			failures, evaluated := evaluateMetadataRequirements(tracker, meta)
			if !evaluated || !api.HasBlockingRuleFailures(failures) {
				t.Fatalf("expected IDs alone to fail for %s, got %#v", tracker, failures)
			}
		})
	}
}

func TestARMetadataPosterMayComeFromDifferentProvider(t *testing.T) {
	t.Parallel()
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "movie", TMDBID: 1, IMDBID: 1234567},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{TMDBID: 1, Poster: "https://img.example/poster.jpg"},
			IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"},
		},
	}
	failures, _ := evaluateMetadataRequirements("AR", meta)
	if api.HasBlockingRuleFailures(failures) {
		t.Fatalf("expected IMDb identity plus TMDB poster to pass, got %#v", failures)
	}
}

func TestARMetadataPosterRejectsMismatchedSnapshot(t *testing.T) {
	t.Parallel()
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "tv", IMDBID: 1234567, TVmazeID: 3},
		ExternalMetadata: api.ExternalMetadata{
			IMDB:   &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Series"},
			TVmaze: &api.TVmazeMetadata{TVmazeID: 4, PosterMedium: "https://img.example/poster.jpg"},
		},
	}
	failures, _ := evaluateMetadataRequirements("AR", meta)
	if len(failures) != 1 || failures[0].Rule != "require_metadata_poster" || !api.HasBlockingRuleFailures(failures) {
		t.Fatalf("expected blocking poster failure, got %#v", failures)
	}
}

func TestARMetadataPosterRejectsStaleSnapshots(t *testing.T) {
	t.Parallel()
	meta := api.PreparedMetadata{
		SourcePath:  "current",
		ExternalIDs: api.ExternalIDs{SourcePath: "current", Category: "movie", IMDBID: 1234567},
		ExternalMetadata: api.ExternalMetadata{
			SourcePath: "stale",
			IMDB:       &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release", Cover: "https://img.example/poster.jpg"},
		},
	}
	failures, _ := evaluateMetadataRequirements("AR", meta)
	for _, failure := range failures {
		if failure.Rule == "require_metadata_poster" && failure.Severity == api.RuleFailureSeverityBlocking {
			return
		}
	}
	t.Fatalf("expected stale poster failure, got %#v", failures)
}

func TestOnlyNBLDeclaresTVmazeIdentityRequirement(t *testing.T) {
	t.Parallel()
	foundNBL := false
	for tracker, policy := range trackerMetadataPolicies {
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
