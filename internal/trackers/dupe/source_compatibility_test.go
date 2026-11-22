// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestEvaluateBluRaySourceEvidence(t *testing.T) {
	t.Parallel()
	target := api.TrackerDuplicateTarget{
		Names:      []string{"Example.Movie.2026.2160p.UHD.BluRay.REMUX.HEVC-GRP"},
		Type:       "REMUX",
		Source:     "BluRay",
		Resolution: "2160p",
	}
	candidate := TrackerCandidate{
		Name:       "Example.Movie.2026.2160p.UHD.BluRay.REMUX.HEVC-OTHER",
		Type:       "REMUX",
		Source:     "BluRay",
		Resolution: "2160p",
	}
	cases := []struct {
		name     string
		source   string
		title    string
		value    string
		status   FactStatus
		relation api.DupeRelation
	}{
		{
			name:     "explicit UHD qualifier in both names",
			source:   "BluRay",
			title:    candidate.Name,
			value:    "bluray",
			status:   FactComplete,
			relation: api.DupeRelationSameSlot,
		},
		{
			name:     "ordinary Blu-ray name",
			source:   "BluRay",
			title:    "Example.Movie.2026.2160p.BluRay.REMUX.HEVC-OTHER",
			value:    "bluray",
			status:   FactComplete,
			relation: api.DupeRelationSameSlot,
		},
		{
			name:     "generic title cannot discard structured UHD",
			source:   "UHD BluRay",
			title:    "Example.Movie.2026.2160p.BluRay.REMUX.HEVC-OTHER",
			value:    "uhd_bluray",
			status:   FactComplete,
			relation: api.DupeRelationSameSlot,
		},
		{
			name:     "genuine WEB conflict",
			source:   "WEB",
			title:    candidate.Name,
			value:    "web",
			status:   FactContradictory,
			relation: api.DupeRelationInsufficientEvidence,
		},
	}
	policy := trackers.DupePolicy{SlotDimensions: []trackers.DupeDimension{trackers.DupeDimensionSource}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := candidate
			c.Source, c.Name = tc.source, tc.title
			got := Evaluate(target, []TrackerCandidate{c}, policy, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
			fact := got.Candidates[0].Facts.Source
			if got.Candidates[0].Relation != tc.relation || got.TargetFacts.Source.Status != FactComplete || fact.Value != tc.value || fact.Status != tc.status {
				t.Fatalf("relation=%s, target source=%#v, candidate source=%#v, want %s with complete target source", got.Candidates[0].Relation, got.TargetFacts.Source, got.Candidates[0].Facts.Source, tc.relation)
			}
		})
	}
}
