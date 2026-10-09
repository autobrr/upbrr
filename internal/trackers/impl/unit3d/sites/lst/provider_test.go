// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLSTPresentationTitleProviderHDRPrecedence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		presentation string
		edition      string
	}{
		{presentation: "IMAX", edition: "IMAX"},
		{presentation: "Hybrid"},
		{presentation: "IMAX Hybrid", edition: "IMAX"},
	} {
		for _, proposedDV := range []bool{false, true} {
			t.Run(test.presentation+"/"+map[bool]string{false: "existing_dv", true: "proposed_dv"}[proposedDV], func(t *testing.T) {
				t.Parallel()
				target := lstTarget("Example Movie 2026 "+test.presentation+" 2160p DSNP WEB-DL HEVC-TARGET",
					"WEBDL", "2160p", "DSNP", "HEVC", api.HDRFormatHDR10)
				candidate := lstCandidate("Example Movie 2026 "+test.presentation+" REPACK 2160p DSNP WEB-DL HEVC-GRP",
					"WEBDL", "2160p", api.HDRFormatHDR10)
				// Structured presentation and media facts establish the same slot.
				target.Edition, candidate.Edition = test.edition, test.edition
				candidate.Codec = "HEVC"
				want, reason := api.DupeRelationExistingPreferred, "lst_existing_dv_hdr_trumps_hdr"
				if proposedDV {
					target.HDR = lstHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10)
					want, reason = api.DupeRelationProposedTrumps, "lst_proposed_dv_hdr_trumps_hdr"
				} else {
					candidate.HDR = lstHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10)
				}
				result := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *Profile().DupePolicy,
					dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
				got := result.Candidates[0]
				if got.Relation != want || len(got.Reasons) == 0 || got.Reasons[0].Code != reason || result.Blocks == proposedDV {
					t.Fatalf("relation=%s reasons=%v blocks=%t; want %s/%s", got.Relation, got.Reasons, result.Blocks, want, reason)
				}
				if got.Facts.Provider.Value != "dsnp" || got.Facts.Provider.Status != dupe.FactPartial ||
					got.Facts.Provider.Origin != dupe.FactOriginTrackerTitle || result.TargetFacts.Provider.Status != dupe.FactComplete {
					t.Fatalf("candidate provider=%#v target provider=%#v", got.Facts.Provider, result.TargetFacts.Provider)
				}
			})
		}
	}
}

func TestLSTPresentationTitlePreservesDistinctSlots(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		provider string
		edition  string
		reason   string
	}{
		{
			name:     "different provider",
			provider: "AMZN",
			edition:  "IMAX",
			reason:   "different_provider",
		},
		{
			name:     "different presentation",
			provider: "DSNP",
			edition:  "Open Matte",
			reason:   "different_edition",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			target := lstTarget("Example Movie 2026 IMAX Hybrid 1080p DSNP WEB-DL HEVC-TARGET",
				"WEBDL", "1080p", "DSNP", "HEVC", api.HDRFormatHDR10)
			target.Edition = "IMAX"
			candidate := lstCandidate("Example Movie 2026 "+test.edition+" Hybrid 1080p "+test.provider+" WEB-DL HEVC-GRP",
				"WEBDL", "1080p", api.HDRFormatHDR10)
			candidate.Edition, candidate.Codec = test.edition, "HEVC"
			result := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *Profile().DupePolicy,
				dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			got := result.Candidates[0]
			if got.Relation != api.DupeRelationCoexists || len(got.Reasons) == 0 || got.Reasons[0].Code != test.reason ||
				result.RequiresAction || result.Blocks {
				t.Fatalf("relation=%s reasons=%v action=%t blocks=%t", got.Relation, got.Reasons, result.RequiresAction, result.Blocks)
			}
		})
	}
}
