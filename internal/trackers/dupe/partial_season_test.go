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
		{"overlapping alternate video format", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		}, TrackerCandidate{
			Name:      "Example.Show.S01.Part.2.1080p.WEB-DL-GRP",
			Season:    1,
			Pack:      true,
			Files:     []string{"Example.S01E09.mkv", "Example.S01E02.m4v"},
			FileCount: 2,
		}, api.DupeRelationSameSlot},
		{"unidentified alternate video format", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		}, TrackerCandidate{
			Name:      "Example.Show.S01.Part.2.1080p.WEB-DL-GRP",
			Season:    1,
			Pack:      true,
			Files:     []string{"Example.S01E09.mkv", "unknown.m4v"},
			FileCount: 2,
		}, api.DupeRelationInsufficientEvidence},
		{"range with partial enumeration", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		}, TrackerCandidate{
			Name:      "Example.Show.S01E09-E12.1080p.WEB-DL-GRP",
			Season:    1,
			Pack:      true,
			Files:     []string{"Example.S01E09.mkv"},
			FileCount: 4,
		}, api.DupeRelationCoexists},
		{"range with unidentified files", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		}, TrackerCandidate{
			Name:      "Example.Show.S01E09-E12.1080p.WEB-DL-GRP",
			Season:    1,
			Pack:      true,
			Files:     []string{"unknown.mkv"},
			FileCount: 4,
		}, api.DupeRelationCoexists},
		{"range with contrary member", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		}, TrackerCandidate{
			Name:      "Example.Show.S01E09-E12.1080p.WEB-DL-GRP",
			Season:    1,
			Pack:      true,
			Files:     []string{"Example.S01E02.mkv"},
			FileCount: 4,
		}, api.DupeRelationManualReview},
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
		{"bracketed range contradicts files", api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		}, TrackerCandidate{
			Name:      "Example.Show.[S01E09-12].1080p.WEB-DL-GRP",
			Season:    1,
			Pack:      true,
			Files:     []string{"Example.Show.S01E02.mkv"},
			FileCount: 4,
		}, api.DupeRelationManualReview},
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

func TestPartialSeasonAndNumberedBatchPreserveMembershipGaps(t *testing.T) {
	t.Parallel()
	for _, direction := range []struct {
		name    string
		reverse bool
	}{
		{name: "numbered target"},
		{name: "numbered candidate", reverse: true},
	} {
		t.Run(direction.name, func(t *testing.T) {
			numbered := &api.DupeTVScope{Season: new(1), Episodes: []int{1, 3}}
			coverage := api.EpisodeCoverage{
				Part:     "Part.2",
				Episodes: []int{2},
				Status:   api.MetadataEvidenceStatusComplete,
			}
			target := api.TrackerDuplicateTarget{Season: 1, TVScope: numbered}
			candidate := TrackerCandidate{
				Name:      "Example.Show.S01.Part.2.1080p.WEB-DL-GRP",
				Season:    1,
				Pack:      true,
				Files:     []string{"Example.Show.S01E02.mkv"},
				FileCount: 1,
			}
			if direction.reverse {
				target = api.TrackerDuplicateTarget{
					Season:   1,
					Pack:     true,
					Coverage: coverage,
				}
				candidate = TrackerCandidate{Season: 1, TVScope: numbered}
			}
			got := Evaluate(target, []TrackerCandidate{candidate}, trackerspkg.DupePolicy{}, SearchEvidence{
				Complete:  true,
				WorkScope: WorkScopeProviderID,
			})
			if got.Candidates[0].Relation != api.DupeRelationCoexists || got.Blocks || got.RequiresAction {
				t.Fatalf("disjoint numbered batch and partial season remain actionable: %+v", got)
			}
		})
	}
}

func TestNumberedBatchRetainsContradictoryCoverage(t *testing.T) {
	t.Parallel()
	got := Evaluate(api.TrackerDuplicateTarget{
		Season:  1,
		Pack:    true,
		TVScope: &api.DupeTVScope{Season: new(1), Episodes: []int{1, 3}},
		Coverage: api.EpisodeCoverage{
			Episodes: []int{1, 3},
			Status:   api.MetadataEvidenceStatusContradictory,
		},
	}, []TrackerCandidate{{Season: 1, Episode: 2}}, trackerspkg.DupePolicy{}, SearchEvidence{
		Complete:  true,
		WorkScope: WorkScopeProviderID,
	})
	if got.Candidates[0].Relation != api.DupeRelationManualReview || !got.RequiresAction || got.Blocks {
		t.Fatalf("numbered membership discarded contradictory prepared coverage: %+v", got)
	}
}

func TestPartialSeasonZeroRequiresExplicitPresence(t *testing.T) {
	t.Parallel()
	for _, coverage := range []api.EpisodeCoverage{
		{
			Part:     "Part.1",
			Episodes: []int{1, 2},
			Status:   api.MetadataEvidenceStatusComplete,
		},
		{CompleteSeason: true, Status: api.MetadataEvidenceStatusUnavailable},
	} {
		for _, known := range []bool{false, true} {
			coverage.SeasonKnown = known
			got := Evaluate(api.TrackerDuplicateTarget{Pack: true, Coverage: coverage}, []TrackerCandidate{{
				Season:    1,
				Pack:      true,
				Name:      "Example.Show.S01.Part.2",
				Files:     []string{"Example.S01E01.mkv", "Example.S01E02.mkv"},
				FileCount: 2,
			}}, trackerspkg.DupePolicy{}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
			want := api.DupeRelationInsufficientEvidence
			if known {
				want = api.DupeRelationCoexists
			}
			if got.Candidates[0].Relation != want || got.RequiresAction == known || got.Blocks {
				t.Fatalf("relation=%s action=%t known=%t complete=%t", got.Candidates[0].Relation, got.RequiresAction, known, coverage.CompleteSeason)
			}
		}
	}
}

func TestPartialSeasonExactMembershipIsSymmetric(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		label   string
		members []int
		files   []string
	}{
		{"S01E01", []int{1}, []string{"Example.S01E01.mkv"}},
		{"S01E01-E02", []int{1, 2}, []string{"Example.S01E01.mkv", "Example.S01E02.mkv"}},
	} {
		part := api.EpisodeCoverage{
			Part:     "Part.1",
			Episodes: tc.members,
			Status:   api.MetadataEvidenceStatusComplete,
		}
		partTarget := api.TrackerDuplicateTarget{
			Names:      []string{"Example.S01.Part.1.1080p.WEB-DL-OTHER"},
			Season:     1,
			Pack:       true,
			Coverage:   part,
			Type:       "WEB-DL",
			Resolution: "1080p",
		}
		partCandidate := TrackerCandidate{
			Name:       "Example.S01.Part.1.1080p.WEB-DL-GRP",
			Season:     1,
			Pack:       true,
			Files:      tc.files,
			FileCount:  len(tc.files),
			Type:       "WEB-DL",
			Resolution: "1080p",
		}
		episodeTarget := api.TrackerDuplicateTarget{
			Names:      []string{"Example." + tc.label + ".1080p.WEB-DL-OTHER"},
			Season:     1,
			Episode:    1,
			Type:       "WEB-DL",
			Resolution: "1080p",
		}
		episodeCandidate := TrackerCandidate{
			Name:       "Example." + tc.label + ".1080p.WEB-DL-GRP",
			Season:     1,
			Episode:    1,
			Type:       "WEB-DL",
			Resolution: "1080p",
		}
		for _, pair := range []struct {
			target    api.TrackerDuplicateTarget
			candidate TrackerCandidate
		}{{partTarget, episodeCandidate}, {episodeTarget, partCandidate}} {
			got := Evaluate(pair.target, []TrackerCandidate{pair.candidate}, trackerspkg.DupePolicy{ExactMatchOnly: true}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
			if got.Candidates[0].Relation != api.DupeRelationCoexists || got.RequiresAction || got.Blocks {
				t.Fatalf("membership %s: %+v", tc.label, got)
			}
		}
	}
}

func TestDatedPackCoveragePreservesDailyScope(t *testing.T) {
	t.Parallel()
	coverage := api.EpisodeCoverage{
		Part:   "Part.1",
		Status: api.MetadataEvidenceStatusUnavailable,
	}
	dated := api.TrackerDuplicateTarget{
		Season:   1,
		Pack:     true,
		Date:     "2026-01-01",
		Coverage: coverage,
	}
	season := api.TrackerDuplicateTarget{
		Season:   1,
		Pack:     true,
		Coverage: api.EpisodeCoverage{CompleteSeason: true, Status: api.MetadataEvidenceStatusComplete},
	}
	conflicting := dated
	conflicting.Coverage.Status = api.MetadataEvidenceStatusContradictory
	titleConflict := dated
	titleConflict.Names = []string{"Example.S01"}
	for _, tc := range []struct {
		name      string
		target    api.TrackerDuplicateTarget
		candidate TrackerCandidate
		want      api.DupeRelation
	}{
		{"different date", dated, TrackerCandidate{Date: "2026-01-02"}, api.DupeRelationCoexists},
		{"daily versus season", dated, TrackerCandidate{Season: 1, Pack: true}, api.DupeRelationInsufficientEvidence},
		{"season versus daily", season, TrackerCandidate{
			Name:      "Example.Part.1",
			Season:    1,
			Pack:      true,
			Date:      "2026-01-01",
			Files:     []string{"unknown.mkv"},
			FileCount: 1,
		}, api.DupeRelationInsufficientEvidence},
		{"conflicting target coverage", conflicting, TrackerCandidate{Date: "2026-01-01"}, api.DupeRelationManualReview},
		{"conflicting candidate coverage", dated, TrackerCandidate{
			Name:      "Example.Part.1",
			Season:    1,
			Pack:      true,
			Date:      "2026-01-01",
			Files:     []string{"Example.S02E01.mkv"},
			FileCount: 1,
		}, api.DupeRelationManualReview},
		{"date and title conflict", titleConflict, TrackerCandidate{Date: "2026-01-01"}, api.DupeRelationManualReview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.target, []TrackerCandidate{tc.candidate}, trackerspkg.DupePolicy{}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
			if got.Candidates[0].Relation != tc.want {
				t.Fatalf("got=%+v want=%s", got, tc.want)
			}
			if tc.want == api.DupeRelationCoexists && (got.RequiresAction || got.Blocks) {
				t.Fatalf("distinct date remains actionable: %+v", got)
			}
		})
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
		[]TrackerCandidate{{Season: 1, Episode: 2}, {
			Name:      "Example.S01.Part.2",
			Season:    1,
			Pack:      true,
			Files:     []string{"Example.S01E09.mkv"},
			FileCount: 1,
		}}, trackerspkg.DupePolicy{}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	if got.Candidates[0].Relation != api.DupeRelationCoexists || got.Candidates[1].Relation != api.DupeRelationCoexists || got.RequiresAction || got.Blocks {
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

func TestDatedNumberedPacksRetainMemberComparisons(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, targetDate, candidateDate, title string
		members                                []string
		status                                 api.MetadataEvidenceStatus
		want                                   api.DupeRelation
	}{
		{"different annotations", "2026-01-01", "2026-01-02", "Example.S01.Part.2", []string{"Example.S01E02.mkv"}, api.MetadataEvidenceStatusComplete, api.DupeRelationSameSlot},
		{"dated target", "2026-01-01", "", "Example.S01.Part.2", []string{"Example.S01E02.mkv"}, api.MetadataEvidenceStatusComplete, api.DupeRelationSameSlot},
		{"dated candidate", "", "2026-01-02", "Example.S01.Part.2", []string{"Example.S01E02.mkv"}, api.MetadataEvidenceStatusComplete, api.DupeRelationSameSlot},
		{"disjoint members same date", "2026-01-01", "2026-01-01", "Example.S01.Part.2", []string{"Example.S01E09.mkv"}, api.MetadataEvidenceStatusComplete, api.DupeRelationCoexists},
		{"title range without files", "2026-01-01", "2026-01-02", "Example.S01E02-E03", nil, api.MetadataEvidenceStatusComplete, api.DupeRelationSameSlot},
		{"partial overlapping members", "2026-01-01", "2026-01-02", "Example.S01.Part.2", []string{"Example.S01E02.mkv"}, api.MetadataEvidenceStatusPartial, api.DupeRelationSameSlot},
		{"partial disjoint members", "2026-01-01", "2026-01-01", "Example.S01.Part.2", []string{"Example.S01E09.mkv"}, api.MetadataEvidenceStatusPartial, api.DupeRelationInsufficientEvidence},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := api.TrackerDuplicateTarget{
				Season: 1,
				Pack:   true,
				Date:   tc.targetDate,
				Coverage: api.EpisodeCoverage{
					Part:     "Part.1",
					Episodes: []int{1, 2},
					Status:   tc.status,
				},
			}
			candidate := TrackerCandidate{
				Name:      tc.title,
				Season:    1,
				Pack:      true,
				Date:      tc.candidateDate,
				Files:     tc.members,
				FileCount: len(tc.members),
			}
			got := Evaluate(target, []TrackerCandidate{candidate}, trackerspkg.DupePolicy{}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
			if got.Candidates[0].Relation != tc.want || (tc.want == api.DupeRelationCoexists && got.RequiresAction) || (tc.want != api.DupeRelationCoexists && !got.RequiresAction) {
				t.Fatalf("relation=%s action=%t want=%s", got.Candidates[0].Relation, got.RequiresAction, tc.want)
			}
		})
	}
}

func TestDatedCompleteSeasonsRetainSeasonIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		candidate TrackerCandidate
		want      api.DupeRelation
		derived   bool
	}{
		{"typed date", TrackerCandidate{
			Name:   "Example.S01.COMPLETE.VariantB",
			Season: 1,
			Pack:   true,
			Date:   "2026-01-02",
		}, api.DupeRelationSameSlot, false},
		{"title date and underscore", NormalizeCandidate(api.DupeEntry{Name: "Example.S01_COMPLETE.2026-01-02.VariantB", Pack: true}, ""), api.DupeRelationManualReview, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(api.TrackerDuplicateTarget{
				Names:    []string{"Example.S01.COMPLETE.VariantA"},
				Season:   1,
				Pack:     true,
				Date:     "2026-01-01",
				Coverage: api.EpisodeCoverage{CompleteSeason: true, Status: api.MetadataEvidenceStatusComplete},
			}, []TrackerCandidate{tc.candidate}, trackerspkg.DupePolicy{}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
			if tc.candidate.Season != 1 || tc.candidate.contentFromTitle != tc.derived || got.Candidates[0].Relation != tc.want || !got.RequiresAction {
				t.Fatalf("season=%d derived=%t relation=%s action=%t", tc.candidate.Season, tc.candidate.contentFromTitle, got.Candidates[0].Relation, got.RequiresAction)
			}
		})
	}
}

func TestNumberedCandidateRetainsConflictingFileSeason(t *testing.T) {
	t.Parallel()
	files := []string{"Example.S02E01.mkv"}
	candidate := NormalizeCandidate(api.DupeEntry{
		Name: "Example.S01",
 Season: 1,
 Pack: true,
		Files: files,
 FileCount: len(files),
		TVScope: NumberedTVScopeFromFiles(files, len(files), new(1)),
	}, "")
	got := Evaluate(api.TrackerDuplicateTarget{Season: 2, Episode: 2}, []TrackerCandidate{candidate}, trackerspkg.DupePolicy{}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	if got.Candidates[0].Relation != api.DupeRelationManualReview || !got.RequiresAction {
		t.Fatalf("contradictory file season escaped review: %+v", got)
	}
}

func TestNumberedMembershipConflictsWithExplicitTitleRange(t *testing.T) {
	t.Parallel()
	for _, reverse := range []bool{false, true} {
		target := api.TrackerDuplicateTarget{Season: 1, Episode: 2}
		candidate := TrackerCandidate{
Name: "Example.S01E01-E03",
 Season: 1,
 TVScope: &api.DupeTVScope{Season: new(1), Episodes: []int{1, 3}},
}
		if reverse {
			target = api.TrackerDuplicateTarget{
Names: []string{candidate.Name},
 Season: 1,
 TVScope: candidate.TVScope,
}
			candidate = TrackerCandidate{Season: 1, Episode: 2}
		}
		got := Evaluate(target, []TrackerCandidate{candidate}, trackerspkg.DupePolicy{}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
		if got.Candidates[0].Relation != api.DupeRelationManualReview || !got.RequiresAction {
			t.Fatalf("reverse=%t title range conflict escaped review: %+v", reverse, got)
		}
	}
}
