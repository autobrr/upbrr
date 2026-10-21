// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestReviewedReplacementPreservesSlotUncertainty(t *testing.T) {
	for _, internal := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			change func(*api.TrackerDuplicateTarget, *dupe.TrackerCandidate)
			want   api.DupeRelation
		}{
			{"complete", func(*api.TrackerDuplicateTarget, *dupe.TrackerCandidate) {}, api.DupeRelationProposedTrumps},
			{"edition contradiction", func(_ *api.TrackerDuplicateTarget, c *dupe.TrackerCandidate) {
				c.Name = "Example Movie 2026 Extended 1080p WEB-DL H264-OTHER"
				c.Edition = "Theatrical"
			}, api.DupeRelationManualReview},
			{"codec contradiction", func(_ *api.TrackerDuplicateTarget, c *dupe.TrackerCandidate) { c.Codec = "HEVC" }, api.DupeRelationInsufficientEvidence},
			{"missing applicable edition", func(s *api.TrackerDuplicateTarget, _ *dupe.TrackerCandidate) { s.Edition = "Extended" }, ""},
			{"disjoint resolution", func(_ *api.TrackerDuplicateTarget, c *dupe.TrackerCandidate) {
				c.Name = "Example Movie 2026 720p WEB-DL H264-OTHER"
				c.Resolution = "720p"
			}, api.DupeRelationCoexists},
			{"disjoint content with contradictory codec", func(s *api.TrackerDuplicateTarget, c *dupe.TrackerCandidate) {
				s.Season = 1
				c.Season = 2
				c.Codec = "HEVC"
			}, api.DupeRelationCoexists},
		} {
			name := tc.name
			if internal {
				name += " same group"
			}
			t.Run(name, func(t *testing.T) {
				target := lstTarget("Example Movie 2026 1080p WEB-DL H264-TARGET", "WEBDL", "1080p", "", "H264", api.HDRFormatSDR)
				target.Evidence = api.DupeReleaseEvidence{
					InternalStatus: "internal",
					Group:          "GRP",
					BackCatalogue:  "no",
				}
				candidate := lstCandidate("Example Movie 2026 1080p WEB-DL H264-OTHER", "WEBDL", "1080p", api.HDRFormatSDR)
				candidate.ID = "123"
				target.Provider, candidate.Provider = "AMZN", "AMZN"
				candidate.Evidence = api.DupeReleaseEvidence{InternalStatus: "non_internal", Group: "GRP"}
				if internal {
					candidate.Evidence.InternalStatus = "internal"
				}
				tc.change(&target, &candidate)
				fingerprint, err := api.CanonicalWorkflowFingerprint(candidate)
				if err != nil {
					t.Fatal(err)
				}
				target.Comparisons = []api.DupeComparisonEvidence{
					{
						CandidateID:          candidate.ID,
						CandidateFingerprint: fingerprint,
						Candidate:            candidate.Evidence,
						Quality:              "superior",
						Replacement:          "newer",
						Reviewed:             true,
						Source:               "https://example.test/comparison",
					},
				}
				got := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *duplicatePolicy(), dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID}).Candidates[0]
				if tc.want == "" {
					if got.Relation == api.DupeRelationProposedTrumps {
						t.Fatalf("uncertain slot authorized replacement: %+v", got)
					}
				} else if got.Relation != tc.want {
					t.Fatalf("got %s (%v), want %s", got.Relation, got.Reasons, tc.want)
				}
			})
		}
	}
}

func TestCustomDiscReplacementRequiresCompleteWorkScope(t *testing.T) {
	for _, scenario := range []string{"complete", "title fallback", "content incomplete", "exact", "disjoint"} {
		t.Run(scenario, func(t *testing.T) {
			target := lstTarget("Example Movie 2026 1080p BluRay AVC-TARGET", "DISC", "1080p", "", "AVC", api.HDRFormatSDR)
			target.Evidence = api.DupeReleaseEvidence{
				InternalStatus: "non_internal",
				DiscIdentity:   "same-disc",
				DiscForm:       "unmodified",
			}
			candidate := lstCandidate("Example Movie 2026 1080p BluRay AVC-OTHER", "DISC", "1080p", api.HDRFormatSDR)
			candidate.ID = "disc"
			candidate.Evidence = api.DupeReleaseEvidence{
				InternalStatus: "non_internal",
				DiscIdentity:   "same-disc",
				DiscForm:       "custom",
			}
			search := dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID}
			want := api.DupeRelationProposedTrumps
			switch scenario {
			case "title fallback":
				search.WorkScope = dupe.WorkScopeTitle
				want = api.DupeRelationManualReview
			case "content incomplete":
				target.Season = 1
				candidate.Category = "TV"
				want = api.DupeRelationManualReview
			case "exact":
				candidate.Name = target.Names[0]
				want = api.DupeRelationExactDuplicate
			case "disjoint":
				target.Season = 1
				candidate.Season = 2
				want = api.DupeRelationCoexists
			}
			fingerprint, err := api.CanonicalWorkflowFingerprint(candidate)
			if err != nil {
				t.Fatal(err)
			}
			target.Comparisons = []api.DupeComparisonEvidence{
				{
					CandidateID:          candidate.ID,
					CandidateFingerprint: fingerprint,
					Candidate:            candidate.Evidence,
					Reviewed:             true,
					Source:               "https://example.test/disc",
				},
			}
			got := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *duplicatePolicy(), search).Candidates[0]
			if got.Relation != want {
				t.Fatalf("got %s (%v), want %s", got.Relation, got.Reasons, want)
			}
		})
	}
}
