// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDR10PlusTrumpPreservesDolbyVisionSlot(t *testing.T) {
	t.Parallel()
	for _, withDV := range []bool{false, true} {
		for _, proposedPlus := range []bool{false, true} {
			base := api.HDRFacts{Status: api.HDREvidenceComplete, Formats: []api.HDRFormat{api.HDRFormatHDR10}}
			plus := api.HDRFacts{Status: api.HDREvidenceComplete, Formats: []api.HDRFormat{api.HDRFormatHDR10Plus}}
			if withDV {
				base.Formats = append(base.Formats, api.HDRFormatDolbyVision)
				plus.Formats = append(plus.Formats, api.HDRFormatDolbyVision)
			}
			targetHDR, candidateHDR := base, plus
			want := api.DupeRelationExistingPreferred
			if proposedPlus {
				targetHDR, candidateHDR = plus, base
				want = api.DupeRelationProposedTrumps
			}
			result := dupe.Evaluate(api.TrackerDuplicateTarget{
				Type:       "WEBDL",
				Source:     "WEB",
				Resolution: "2160p",
				VideoCodec: "H.265",
				HDR:        targetHDR,
			}, []dupe.TrackerCandidate{{
				CanonicalType: "WEBDL",
				Source:        "WEB",
				Resolution:    "2160p",
				Codec:         "H.265",
				HDR:           candidateHDR,
			}}, *Profile().DupePolicy, dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			if got := result.Candidates[0].Relation; got != want {
				t.Fatalf("DV=%t proposed_plus=%t: %#v, want %s", withDV, proposedPlus, result, want)
			}
		}
	}
}
