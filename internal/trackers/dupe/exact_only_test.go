// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"testing"

	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestEvaluateExactMatchOnlyEvidence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		change func(*api.TrackerDuplicateTarget, *TrackerCandidate)
		want   api.DupeRelation
	}{
		{
			name: "distinct equal-size releases",
			want: api.DupeRelationCoexists,
		},
		{
			name: "named releases without file lists",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				target.FileNames, candidate.Files = nil, nil
			},
			want: api.DupeRelationCoexists,
		},
		{
			name: "complete primary files without release names",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				target.Names, candidate.Name = nil, ""
			},
			want: api.DupeRelationCoexists,
		},
		{
			name: "partial file list without release names",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				target.Names, candidate.Name = nil, ""
				candidate.FileCount = 2
			},
			want: api.DupeRelationInsufficientEvidence,
		},
		{
			name: "auxiliary files cannot prove identity",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				target.Names, candidate.Name = nil, ""
				target.FileNames = []string{"Example.Release.2026.VariantA.nfo"}
				candidate.Files = []string{"Example.Release.2026.VariantB.nfo"}
			},
			want: api.DupeRelationInsufficientEvidence,
		},
		{
			name: "missing candidate identity",
			change: func(_ *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				*candidate = TrackerCandidate{}
			},
			want: api.DupeRelationInsufficientEvidence,
		},
		{
			name: "distinct resolution survives missing identity",
			change: func(_ *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				candidate.Name, candidate.Files = "", nil
				candidate.Resolution = "720p"
			},
			want: api.DupeRelationCoexists,
		},
		{
			name: "missing target identity",
			change: func(target *api.TrackerDuplicateTarget, _ *TrackerCandidate) {
				target.Names, target.FileNames = nil, nil
			},
			want: api.DupeRelationInsufficientEvidence,
		},
		{
			name: "exact name despite different files",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				candidate.Name = target.Names[0]
			},
			want: api.DupeRelationExactDuplicate,
		},
		{
			name: "exact primary file despite different names",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				candidate.Files = target.FileNames
			},
			want: api.DupeRelationExactDuplicate,
		},
		{
			name: "exact name and primary filename with distinct known size",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				candidate.Name = target.Names[0]
				candidate.Files = target.FileNames
				candidate.SizeBytes++
			},
			want: api.DupeRelationExactDuplicate,
		},
		{
			name: "same primary filename with distinct known size",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				candidate.Files = target.FileNames
				candidate.SizeBytes++
			},
			want: api.DupeRelationCoexists,
		},
		{
			name: "format contradictions do not define identity",
			change: func(_ *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				candidate.Resolution = "720p"
			},
			want: api.DupeRelationCoexists,
		},
		{
			name: "unknown target TV scope",
			change: func(target *api.TrackerDuplicateTarget, _ *TrackerCandidate) {
				target.Season = 1
			},
			want: api.DupeRelationInsufficientEvidence,
		},
		{
			name: "unknown candidate TV scope",
			change: func(_ *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				candidate.Season = 1
			},
			want: api.DupeRelationInsufficientEvidence,
		},
		{
			name: "same episode variants",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				target.Season, candidate.Season = 1, 1
				target.Episode, candidate.Episode = 2, 2
			},
			want: api.DupeRelationCoexists,
		},
		{
			name: "same season pack variants",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				target.Season, candidate.Season = 1, 1
				target.Pack, candidate.Pack = true, true
			},
			want: api.DupeRelationCoexists,
		},
		{
			name: "episodes without known season",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				target.Episode, candidate.Episode = 2, 2
			},
			want: api.DupeRelationInsufficientEvidence,
		},
		{
			name: "existing pack contains episode",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				target.Season, candidate.Season = 1, 1
				target.Episode, candidate.Pack = 2, true
			},
			want: api.DupeRelationExistingPreferred,
		},
		{
			name: "proposed pack supersedes episode",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				target.Season, candidate.Season = 1, 1
				target.Pack, candidate.Episode = true, 2
			},
			want: api.DupeRelationCoexists,
		},
		{
			name: "pack containment without resolution evidence",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				target.Season, candidate.Season = 1, 1
				target.Episode, candidate.Pack = 2, true
				candidate.Resolution = ""
				candidate.Name = "Example.Series.VariantB-GRP"
			},
			want: api.DupeRelationInsufficientEvidence,
		},
		{
			name: "contradictory season overrides apparent disjoint scope",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				target.Season, target.Episode = 1, 2
				candidate.Name = "Example.Series.S01E02.1080p.WEB-DL-GRP"
				candidate.Season, candidate.Episode = 2, 2
			},
			want: api.DupeRelationManualReview,
		},
		{
			name: "contradictory pack overrides apparent containment",
			change: func(target *api.TrackerDuplicateTarget, candidate *TrackerCandidate) {
				target.Season, target.Episode = 1, 2
				candidate.Name = "Example.Series.S01E02.1080p.WEB-DL-GRP"
				candidate.Season, candidate.Pack = 1, true
			},
			want: api.DupeRelationManualReview,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			target := api.TrackerDuplicateTarget{
				Names:      []string{"Example.Release.2026.1080p.WEB-DL.VariantA-GRP"},
				FileNames:  []string{"Example.Release.2026.VariantA-GRP.mkv"},
				Type:       "WEB-DL",
				Resolution: "1080p",
				SizeBytes:  100,
			}
			candidate := TrackerCandidate{
				Name:       "Example.Release.2026.1080p.WEB-DL.VariantB-GRP",
				Files:      []string{"Example.Release.2026.VariantB-GRP.mkv"},
				Type:       "WEB-DL",
				Resolution: "1080p",
				SizeBytes:  100,
				SizeKnown:  true,
			}
			if test.change != nil {
				test.change(&target, &candidate)
			}
			got := Evaluate(target, []TrackerCandidate{candidate}, trackerspkg.DupePolicy{
				ID:             "example/duplicate/v1",
				EvidenceID:     "example-coexisting-releases",
				ExactMatchOnly: true,
			}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
			wantBlocks := test.want == api.DupeRelationExactDuplicate || test.want == api.DupeRelationExistingPreferred
			wantAction := !wantBlocks && test.want != api.DupeRelationCoexists
			if len(got.Candidates) != 1 || got.Candidates[0].Relation != test.want ||
				got.Blocks != wantBlocks || got.RequiresAction != wantAction || !got.Complete {
				t.Fatalf("relation=%s blocks=%t action=%t complete=%t, want %s", got.Candidates[0].Relation, got.Blocks, got.RequiresAction, got.Complete, test.want)
			}
		})
	}
}

func TestEvaluateExactMatchOnlyDefaultsOff(t *testing.T) {
	t.Parallel()

	got := Evaluate(api.TrackerDuplicateTarget{
		Names:      []string{"Example.Release.2026.1080p.WEB-DL.VariantA-GRP"},
		Type:       "WEB-DL",
		Resolution: "1080p",
	}, []TrackerCandidate{{
		Name:       "Example.Release.2026.1080p.WEB-DL.VariantB-GRP",
		Type:       "WEB-DL",
		Resolution: "1080p",
	}}, trackerspkg.DupePolicy{}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	if got.Candidates[0].Relation != api.DupeRelationSameSlot || got.Blocks || !got.RequiresAction || !got.Complete {
		t.Fatalf("default policy relation=%s blocks=%t action=%t", got.Candidates[0].Relation, got.Blocks, got.RequiresAction)
	}
}
