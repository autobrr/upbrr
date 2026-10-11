// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSPPackComparisonRequiresExplicitRiskReview(t *testing.T) {
	policy := Profile().DupePolicy
	result := dupe.Evaluate(api.TrackerDuplicateTarget{
		Category: "TV",
		Type:     "REMUX",
		Season:   1,
		Pack:     true,
	}, []dupe.TrackerCandidate{{
		Category: "TV",
		Type:     "REMUX",
		Season:   1,
		Episode:  2,
	}}, *policy, dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
	if !result.RequiresAction || result.Blocks || result.Candidates[0].Relation != api.DupeRelationManualReview || result.Candidates[0].Reasons[0].Code != "pack_audio_languages_unverified" {
		t.Fatalf("SP made an unsupported pack-trump claim: %#v", result)
	}
}

func TestSPResolutionDifferencesDoNotClaimTrumpEligibility(t *testing.T) {
	for _, proposedPack := range []bool{false, true} {
		for _, resolution := range []string{"720p", "1080p", ""} {
			target := api.TrackerDuplicateTarget{
				Category:   "TV",
				Type:       "WEBDL",
				Source:     "WEB",
				Resolution: "2160p",
				Season:     1,
				Episode:    2,
			}
			candidate := dupe.TrackerCandidate{
				Category:   "TV",
				Type:       "WEBDL",
				Source:     "WEB",
				Resolution: resolution,
				Season:     1,
				Episode:    2,
				Trumpable:  true,
			}
			if proposedPack {
				target.Pack, target.Episode = true, 0
			}
			result := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *Profile().DupePolicy, dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			got := result.Candidates[0]
			if resolution != "" {
				if got.Relation != api.DupeRelationCoexists || result.Blocks || result.RequiresAction {
					t.Fatalf("pack=%v resolution=%s: known resolution difference must coexist: %#v", proposedPack, resolution, result)
				}
			} else if got.Relation == api.DupeRelationProposedTrumps || !result.RequiresAction {
				t.Fatalf("pack=%v: unknown resolution claimed trump eligibility: %#v", proposedPack, result)
			}
		}
	}
}
