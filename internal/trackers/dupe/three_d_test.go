// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCandidateThreeDEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		entry  api.DupeEntry
		value  string
		status FactStatus
	}{
		{
			name:   "structured tag without marker",
			entry:  api.DupeEntry{Name: "Example.2026.1080p.BluRay-GRP", Flags: []string{"3D"}},
			value:  "3d",
			status: FactComplete,
		},
		{
			name:   "structured type",
			entry:  api.DupeEntry{Type: "Blu-ray 3D"},
			value:  "3d",
			status: FactComplete,
		},
		{
			name:   "canonical subtype",
			entry:  api.DupeEntry{ThreeD: "3D Half SBS", Name: "Example.2026.1080p.3D.BluRay-GRP"},
			value:  "3d",
			status: FactComplete,
		},
		{
			name:   "explicit 2D",
			entry:  api.DupeEntry{ThreeD: "2D"},
			value:  "2d",
			status: FactComplete,
		},
		{
			name:   "conflicting title",
			entry:  api.DupeEntry{ThreeD: "2D", Name: "Example.2026.1080p.3D.BluRay-GRP"},
			value:  "2d",
			status: FactContradictory,
		},
		{
			name:   "conflicting 2D title",
			entry:  api.DupeEntry{Flags: []string{"3D"}, Name: "Example.2026.1080p.2D.BluRay-GRP"},
			value:  "3d",
			status: FactContradictory,
		},
		{
			name:   "mixed structured labels",
			entry:  api.DupeEntry{Flags: []string{"2D", "3D"}},
			status: FactMissing,
		},
		{
			name:   "reversed mixed structured labels",
			entry:  api.DupeEntry{Flags: []string{"3D", "2D"}},
			status: FactMissing,
		},
		{
			name:   "mixed title presentation",
			entry:  api.DupeEntry{Name: "Example.2026.1080p.2D.3D.BluRay-GRP"},
			status: FactContradictory,
		},
		{
			name:   "structured 3D with mixed title presentation",
			entry:  api.DupeEntry{Type: "Blu-ray 3D", Name: "Example.2026.1080p.2D.3D.BluRay-GRP"},
			status: FactContradictory,
		},
		{
			name:   "title 2D marker",
			entry:  api.DupeEntry{Name: "Example.2026.1080p.2D.BluRay-GRP"},
			value:  "2d",
			status: FactPartial,
		},
		{
			name:   "title marker",
			entry:  api.DupeEntry{Name: "Example.2026.1080p.3D.BluRay-GRP"},
			value:  "3d",
			status: FactPartial,
		},
		{
			name:   "title subtype",
			entry:  api.DupeEntry{Name: "Example.2026.1080p.HSBS.BluRay-GRP"},
			value:  "3d",
			status: FactPartial,
		},
		{
			name:   "title contains 3D",
			entry:  api.DupeEntry{Name: "Example.3D.2026.1080p.BluRay-GRP"},
			status: FactMissing,
		},
		{
			name:   "episode title",
			entry:  api.DupeEntry{Name: "Example.S01E01.3D.Adventure.1080p.WEB-DL-GRP"},
			status: FactMissing,
		},
		{
			name:   "incidental substring",
			entry:  api.DupeEntry{Name: "Example.2026.1080p.3Days.BluRay-GRP"},
			status: FactMissing,
		},
		{
			name:   "group named 3D",
			entry:  api.DupeEntry{Name: "Example.2026.1080p.BluRay-3D"},
			status: FactMissing,
		},
		{
			name:   "group named 2D",
			entry:  api.DupeEntry{Name: "Example.2026.1080p.BluRay-2D"},
			status: FactMissing,
		},
		{
			name:   "missing marker is unknown",
			entry:  api.DupeEntry{Name: "Example.2026.1080p.BluRay-GRP", Flags: []string{}},
			status: FactMissing,
		},
		{
			name:   "ambiguous unbounded title",
			entry:  api.DupeEntry{Name: "Example 3D Adventure"},
			status: FactMissing,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeCandidateFacts(NormalizeCandidate(tc.entry, "EXAMPLE")).ThreeD
			if got.Value != tc.value || got.Status != tc.status {
				t.Fatalf("3D fact = %+v, want value %q status %q", got, tc.value, tc.status)
			}
		})
	}
}

func TestThreeDEvidenceReachesDuplicateComparison(t *testing.T) {
	for _, tc := range []struct {
		name, proposed string
		existing       api.DupeEntry
		want           api.DupeRelation
	}{
		{"3D versus 2D", "3D Half SBS", api.DupeEntry{ThreeD: "2D"}, api.DupeRelationCoexists},
		{"2D versus 3D", "2D", api.DupeEntry{ThreeD: "3D"}, api.DupeRelationCoexists},
		{"unknown existing", "3D", api.DupeEntry{}, api.DupeRelationInsufficientEvidence},
		{"unknown proposed", "", api.DupeEntry{ThreeD: "3D"}, api.DupeRelationInsufficientEvidence},
		{"conflicting 2D title", "2D", api.DupeEntry{Name: "Example.2026.1080p.2D.BluRay-GRP", Flags: []string{"3D"}}, api.DupeRelationManualReview},
		{"mixed structured labels", "2D", api.DupeEntry{Flags: []string{"2D", "3D"}}, api.DupeRelationInsufficientEvidence},
		{"title-only 3D", "2D", api.DupeEntry{Name: "Example.2026.1080p.HSBS.BluRay-GRP"}, api.DupeRelationInsufficientEvidence},
		{"title-only 2D", "3D", api.DupeEntry{Name: "Example.2026.1080p.2D.BluRay-GRP"}, api.DupeRelationInsufficientEvidence},
		{"mixed title", "2D", api.DupeEntry{Name: "Example.2026.1080p.2D.3D.BluRay-GRP", Type: "Blu-ray 3D"}, api.DupeRelationManualReview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := trackers.DupePolicy{ID: "synthetic-3d-v1", SlotDimensions: []trackers.DupeDimension{trackers.DupeDimensionThreeD}}
			target := api.TrackerDuplicateTarget{Names: []string{"Example proposed"}, ThreeD: tc.proposed}
			candidate := NormalizeCandidate(tc.existing, "EXAMPLE")
			result := Evaluate(target, []TrackerCandidate{candidate}, policy, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
			if got := result.Candidates[0].Relation; got != tc.want {
				t.Fatalf("relation = %s, want %s", got, tc.want)
			}
		})
	}
}
