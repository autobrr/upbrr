// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAdapterResultDetachesNumberedTVScope(t *testing.T) {
	t.Parallel()
	entry := api.DupeEntry{TVScope: &api.DupeTVScope{Season: new(0), Episodes: []int{1, 3}}}
	result := Resolved([]api.DupeEntry{entry}, nil)
	*entry.TVScope.Season, entry.TVScope.Episodes[0] = 1, 2
	returned := result.Entries()
	*returned[0].TVScope.Season, returned[0].TVScope.Episodes[0] = 2, 4
	retained := result.Entries()[0].TVScope
	if *retained.Season != 0 || retained.Episodes[0] != 1 {
		t.Fatal("caller mutations changed retained API scope evidence")
	}
}

func TestResolvedPreservesUnknownSearchCompleteness(t *testing.T) {
	t.Parallel()

	search := Resolved(nil, nil).SearchEvidence()
	if search.Complete {
		t.Fatal("compatibility result unexpectedly claimed a complete search")
	}
	if search.Pages != 1 {
		t.Fatalf("compatibility result pages = %d, want 1", search.Pages)
	}
	if len(search.Warnings) != 1 || !strings.Contains(search.Warnings[0], "not evidenced") {
		t.Fatalf("compatibility result warnings = %q, want completeness warning", search.Warnings)
	}
}

func TestSearchEvidenceEffectiveCompleteRequiresAuthoritativeWorkScope(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		scope WorkScope
		want  bool
	}{
		{
name: "provider",
 scope: WorkScopeProviderID,
 want: true,
},
		{
name: "tracker group",
 scope: WorkScopeTrackerGroup,
 want: true,
},
		{name: "title", scope: WorkScopeTitle},
		{name: "unknown", scope: WorkScopeUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := (SearchEvidence{Complete: true, WorkScope: test.scope}).EffectiveComplete(); got != test.want {
				t.Fatalf("EffectiveComplete() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestCandidateProviderIdentityEvidence(t *testing.T) {
	t.Parallel()
	entry := api.DupeEntry{ProviderIDs: []api.TrackerProviderID{
		{Provider: "imdb", Value: "tt0012345"},
		{Provider: "tvdb", Value: "100001"},
		{Provider: "tmdb", Value: "200002"},
		{Provider: "tracker", Value: "300001"},
		{Provider: "mal", Value: ""},
		{Provider: "tvmaze", Value: "invalid"},
	}}
	candidate := NormalizeCandidate(entry, "EXAMPLE")
	fingerprint := candidateEvidenceFingerprint(candidate)
	entry.ProviderIDs[2].Value = "200001"
	if candidate.ProviderIDs[2].Value != "200002" || candidateEvidenceFingerprint(candidate) != fingerprint {
		t.Fatal("adapter mutation changed retained provider evidence")
	}
	identity := api.ExternalIdentity{
IMDBID: 12345,
 TVDBID: 100001,
 TMDBID: 200001,
 MALID: 400001,
 TVmazeID: 500001,
}
	if conflicts := providerIdentityConflicts(candidate.ProviderIDs, identity); len(conflicts) != 1 || conflicts[0] != "tmdb" {
		t.Fatalf("unexpected conflicts from equivalent, unsupported or missing provider IDs: %v", conflicts)
	}
	if conflicts := providerIdentityConflicts(candidate.ProviderIDs, api.ExternalIdentity{}); len(conflicts) != 0 {
		t.Fatalf("missing selected IDs establish conflict: %v", conflicts)
	}
	candidate.ProviderIDs[2].Value = "200001"
	if candidateEvidenceFingerprint(candidate) == fingerprint {
		t.Fatal("changed provider evidence retained the old fingerprint")
	}
}
