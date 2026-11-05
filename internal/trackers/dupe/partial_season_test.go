// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"testing"

	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPartialSeasonDuplicateCoverage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		target    api.EpisodeCoverage
		candidate TrackerCandidate
		want      api.DupeRelation
	}{
		{"disjoint parts", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		}, TrackerCandidate{
			Name:      "Example.Show.S01.Part.2.1080p.WEB-DL-GRP",
			Season:    1,
			Pack:      true,
			Files:     []string{"Example.S01E09.mkv", "Example.S01E10.mkv"},
			FileCount: 2,
		}, api.DupeRelationCoexists},
		{"overlapping parts", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		}, TrackerCandidate{
			Name:      "Example.Show.S01.Part.2.1080p.WEB-DL-GRP",
			Season:    1,
			Pack:      true,
			Files:     []string{"Example.S01E02.mkv", "Example.S01E03.mkv"},
			FileCount: 2,
		}, api.DupeRelationSameSlot},
		{"unknown other part", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		}, TrackerCandidate{
			Name:   "Example.Show.S01.Part.2.1080p.WEB-DL-GRP",
			Season: 1,
			Pack:   true,
		}, api.DupeRelationInsufficientEvidence},
		{"unknown proposal", api.EpisodeCoverage{Part: "Part.1", Status: api.MetadataEvidenceStatusUnavailable}, TrackerCandidate{
			Name:      "Example.Show.S01.Part.2.1080p.WEB-DL-GRP",
			Season:    1,
			Pack:      true,
			Files:     []string{"Example.S01E09.mkv"},
			FileCount: 1,
		}, api.DupeRelationInsufficientEvidence},
		{"contradictory proposal", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1},
			Status:   api.MetadataEvidenceStatusContradictory,
		}, TrackerCandidate{
			Name:      "Example.Show.S01.Part.2.1080p.WEB-DL-GRP",
			Season:    1,
			Pack:      true,
			Files:     []string{"Example.S01E09.mkv"},
			FileCount: 1,
		}, api.DupeRelationManualReview},
		{"existing full season", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		}, TrackerCandidate{
			Name:   "Example.Show.S01.COMPLETE.1080p.WEB-DL-GRP",
			Season: 1,
			Pack:   true,
		}, api.DupeRelationExistingPreferred},
		{"existing full season with member inventory", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		}, TrackerCandidate{
			Name:      "Example.Show.S01.COMPLETE.1080p.WEB-DL-GRP",
			Season:    1,
			Pack:      true,
			Files:     []string{"Example.S01E01.mkv", "Example.S01E02.mkv", "Example.S01E03.mkv"},
			FileCount: 3,
		}, api.DupeRelationExistingPreferred},
		{"asserted season pack with optional member inventory", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		}, TrackerCandidate{
			Name:      "Example.Show.S01.1080p.WEB-DL-GRP",
			Season:    1,
			Pack:      true,
			Files:     []string{"Example.S01E09.mkv"},
			FileCount: 1,
		}, api.DupeRelationExistingPreferred},
		{"unknown member prevents disjointness", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1},
			Status:   api.MetadataEvidenceStatusPartial,
		}, TrackerCandidate{
			Name:      "Example.Show.S01.Part.2.1080p.WEB-DL-GRP",
			Season:    1,
			Pack:      true,
			Files:     []string{"Example.S01E09.mkv"},
			FileCount: 1,
		}, api.DupeRelationInsufficientEvidence},
		{"explicit candidate range", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		}, TrackerCandidate{
			Name:   "Example.Show.S01E09-E12.1080p.WEB-DL-GRP",
			Season: 1,
			Pack:   true,
		}, api.DupeRelationCoexists},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.candidate.Type = "WEB-DL"
			tc.candidate.Source = "WEB"
			tc.candidate.Resolution = "1080p"
			got := Evaluate(api.TrackerDuplicateTarget{
				Names:      []string{"Example.Show.S01.Part.1.1080p.WEB-DL-OTHER"},
				Season:     1,
				Pack:       true,
				Coverage:   tc.target,
				Type:       "WEB-DL",
				Source:     "WEB",
				Resolution: "1080p",
			}, []TrackerCandidate{tc.candidate}, trackerspkg.DupePolicy{}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
			if got.Candidates[0].Relation != tc.want {
				t.Fatalf("result=%+v want %s", got.Candidates[0], tc.want)
			}
			if tc.want == api.DupeRelationCoexists && (got.RequiresAction || got.Blocks) {
				t.Fatalf("disjoint candidate remains actionable: %+v", got)
			}
		})
	}
}

func TestSeasonlessPartialPackCannotProveCoexistence(t *testing.T) {
	t.Parallel()
	got := Evaluate(api.TrackerDuplicateTarget{Pack: true, Coverage: api.EpisodeCoverage{
		Part:     "Part.1",
		Episodes: []int{1, 2},
		Status:   api.MetadataEvidenceStatusComplete,
	}},
		[]TrackerCandidate{{
			Season:    1,
			Pack:      true,
			Name:      "Example.Show.S01.Part.2",
			Files:     []string{"Example.S01E01.mkv", "Example.S01E02.mkv"},
			FileCount: 2,
		}}, trackerspkg.DupePolicy{}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	if got.Candidates[0].Relation != api.DupeRelationInsufficientEvidence || !got.RequiresAction {
		t.Fatalf("unknown season proved disjointness: %+v", got)
	}
}

func TestCompleteSeasonProposalStillSupersedesIndividualEpisodes(t *testing.T) {
	t.Parallel()
	got := Evaluate(api.TrackerDuplicateTarget{
		Season: 1,
		Pack:   true,
		Coverage: api.EpisodeCoverage{
			CompleteSeason: true,
			Episodes:       []int{1, 2, 3},
			Status:         api.MetadataEvidenceStatusComplete,
		},
	},
		[]TrackerCandidate{{Season: 1, Episode: 2}}, trackerspkg.DupePolicy{}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	if got.Candidates[0].Relation != api.DupeRelationCoexists || got.RequiresAction || got.Blocks {
		t.Fatalf("complete season proposal lost containment: %+v", got)
	}
}

func TestCompleteSeasonIdentityIgnoresOptionalMemberEnumeration(t *testing.T) {
	t.Parallel()
	got := Evaluate(api.TrackerDuplicateTarget{
		Names:      []string{"Example.Show.S01.COMPLETE.VariantA-GRP"},
		Type:       "WEB-DL",
		Resolution: "1080p",
		Season:     1,
		Pack:       true,
		Coverage: api.EpisodeCoverage{
			CompleteSeason: true,
			Episodes:       []int{1, 2, 3},
			Status:         api.MetadataEvidenceStatusComplete,
		},
	},
		[]TrackerCandidate{{
			Season:     1,
			Pack:       true,
			Name:       "Example.Show.S01.COMPLETE.VariantB-GRP",
			Type:       "WEB-DL",
			Resolution: "1080p",
		}}, trackerspkg.DupePolicy{ExactMatchOnly: true}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	if got.Candidates[0].Relation != api.DupeRelationCoexists || got.RequiresAction || got.Blocks {
		t.Fatalf("complete season identity depends on file enumeration: %+v", got)
	}
}
