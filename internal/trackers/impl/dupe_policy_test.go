// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSourceBackedDupeOverlaysResolveDeterministically(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	completeSDR := api.HDRFacts{
		Formats: []api.HDRFormat{api.HDRFormatSDR},
		Origin:  api.HDREvidenceTrackerAPI,
		Status:  api.HDREvidenceComplete,
	}
	tests := []struct {
		name      string
		tracker   string
		target    api.TrackerDuplicateTarget
		candidate dupe.TrackerCandidate
		relation  api.DupeRelation
	}{
		{
			name:    "AITHER WEB direction",
			tracker: "AITHER",
			target: api.TrackerDuplicateTarget{
				Type:       "WEB-DL",
				Resolution: "1080p",
				HDR:        completeSDR,
			},
			candidate: dupe.TrackerCandidate{
				Type:       "WEBRip",
				Resolution: "1080p",
				HDR:        completeSDR,
			},
			relation: api.DupeRelationProposedTrumps,
		},
		{
			name:    "ANT HDR compatibility",
			tracker: "ANT",
			target: api.TrackerDuplicateTarget{
				Type:       "REMUX",
				Resolution: "2160p",
				HDR: api.HDRFacts{
					Formats: []api.HDRFormat{api.HDRFormatHDR10Plus},
					Origin:  api.HDREvidenceTrackerAPI,
					Status:  api.HDREvidenceComplete,
				},
			},
			candidate: dupe.TrackerCandidate{
				Type:       "REMUX",
				Resolution: "2160p",
				HDR: api.HDRFacts{
					Formats: []api.HDRFormat{api.HDRFormatHDR10},
					Origin:  api.HDREvidenceTrackerAPI,
					Status:  api.HDREvidenceComplete,
				},
			},
			relation: api.DupeRelationProposedTrumps,
		},
		{
			name:    "LUME WEB over broadcast",
			tracker: "LUME",
			target: api.TrackerDuplicateTarget{
				Type:       "WEB-DL",
				Resolution: "1080p",
				HDR:        completeSDR,
			},
			candidate: dupe.TrackerCandidate{
				Type:       "HDTV",
				Resolution: "1080p",
				HDR:        completeSDR,
			},
			relation: api.DupeRelationProposedTrumps,
		},
		{
			name:    "MTV provider coexistence",
			tracker: "MTV",
			target: api.TrackerDuplicateTarget{
				Type:       "WEB-DL",
				Resolution: "1080p",
				Provider:   "Example A",
				HDR:        completeSDR,
			},
			candidate: dupe.TrackerCandidate{
				Type:       "WEB-DL",
				Resolution: "1080p",
				Provider:   "Example B",
				HDR:        completeSDR,
			},
			relation: api.DupeRelationCoexists,
		},
		{
			name:    "SP season pack direction",
			tracker: "SP",
			target: api.TrackerDuplicateTarget{
				Type:       "WEB-DL",
				Resolution: "1080p",
				Season:     1,
				Episode:    2,
				HDR:        completeSDR,
			},
			candidate: dupe.TrackerCandidate{
				Type:       "WEB-DL",
				Resolution: "1080p",
				Season:     1,
				Pack:       true,
				HDR:        completeSDR,
			},
			relation: api.DupeRelationExistingPreferred,
		},
		{
			name:    "ULCX WEB direction",
			tracker: "ULCX",
			target: api.TrackerDuplicateTarget{
				Type:       "WEB-DL",
				Resolution: "1080p",
				HDR:        completeSDR,
			},
			candidate: dupe.TrackerCandidate{
				Type:       "WEBRip",
				Resolution: "1080p",
				HDR:        completeSDR,
			},
			relation: api.DupeRelationProposedTrumps,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			policy, ok := registry.LookupDupePolicy(test.tracker)
			if !ok {
				t.Fatalf("%s policy missing", test.tracker)
			}
			got := dupe.Evaluate(
				test.target,
				[]dupe.TrackerCandidate{test.candidate},
				policy,
				dupe.SearchEvidence{Complete: true},
			).Candidates[0]
			if got.Relation != test.relation {
				t.Fatalf("%s relation = %#v", test.tracker, got)
			}
		})
	}
}

func TestNamingOnlyTrackersUseCompatibilityDupePolicy(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	for _, tracker := range []string{"DP", "OE", "YUS"} {
		policy, ok := registry.LookupDupePolicy(tracker)
		if !ok || !strings.Contains(policy.ID, "/duplicate-compat/") || policy.SameSlotFallback == nil {
			t.Fatalf("%s compatibility policy = %#v, %t", tracker, policy, ok)
		}
	}
}
