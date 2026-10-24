// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDuplicateSourceEvidencePrecedence(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*api.TrackerDuplicateTarget, *dupe.TrackerCandidate)
		want   api.DupeRelation
	}{
		{"protected trumpable", func(_ *api.TrackerDuplicateTarget, c *dupe.TrackerCandidate) { c.Trumpable = true }, api.DupeRelationExistingPreferred},
		{"exact", func(s *api.TrackerDuplicateTarget, c *dupe.TrackerCandidate) { c.Name = s.Names[0] }, api.DupeRelationExactDuplicate},
		{"distinct resolution", func(_ *api.TrackerDuplicateTarget, c *dupe.TrackerCandidate) {
			c.Resolution = "720p"
			c.Name = "Other Release"
		}, api.DupeRelationCoexists},
		{"distinct season", func(s *api.TrackerDuplicateTarget, c *dupe.TrackerCandidate) { s.Season = 1; c.Season = 2 }, api.DupeRelationCoexists},
		{"pack protected", func(s *api.TrackerDuplicateTarget, c *dupe.TrackerCandidate) {
			s.Season = 1
			s.Pack = true
			c.Season = 1
			c.Episode = 2
		}, api.DupeRelationExistingPreferred},
		{"different internal group", func(s *api.TrackerDuplicateTarget, _ *dupe.TrackerCandidate) {
			s.Evidence.InternalStatus = "internal"
			s.Evidence.Group = "OTHER"
		}, api.DupeRelationExistingPreferred},
		{"same group without proof", func(s *api.TrackerDuplicateTarget, _ *dupe.TrackerCandidate) {
			s.Evidence.InternalStatus = "internal"
			s.Repack = "REPACK"
		}, api.DupeRelationExistingPreferred},
		{"unverified internal flag", func(s *api.TrackerDuplicateTarget, _ *dupe.TrackerCandidate) { s.Evidence = api.DupeReleaseEvidence{} }, api.DupeRelationExistingPreferred},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := lstTarget("Example Movie 2026 1080p WEB-DL H264-TARGET", "WEBDL", "1080p", "", "H264", api.HDRFormatSDR)
			target.Evidence = api.DupeReleaseEvidence{InternalStatus: "non_internal", Group: "GRP"}
			candidate := lstCandidate("Example Movie 2026 1080p WEB-DL H264-OTHER", "WEBDL", "1080p", api.HDRFormatSDR)
			candidate.ID = "123"
			candidate.Evidence = api.DupeReleaseEvidence{InternalStatus: "internal", Group: "GRP"}
			test.change(&target, &candidate)
			got := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *duplicatePolicy(), dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			if got.Candidates[0].Relation != test.want {
				t.Fatalf("got %s (%v), want %s", got.Candidates[0].Relation, got.Candidates[0].Reasons, test.want)
			}
		})
	}
}

func TestReviewedDuplicateComparison(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*api.DupeEvidenceContext)
		want   api.DupeRelation
	}{
		{"superior alongside internal", func(c *api.DupeEvidenceContext) { c.Target.Comparisons[0].Quality = "superior" }, api.DupeRelationCoexists},
		{"equivalent noninternal blocked", func(c *api.DupeEvidenceContext) { c.Target.Comparisons[0].Quality = "equivalent" }, api.DupeRelationExistingPreferred},
		{"stale proof", func(c *api.DupeEvidenceContext) { c.Target.Comparisons[0].CandidateFingerprint = "stale" }, api.DupeRelationExistingPreferred},
		{"other candidate proof", func(c *api.DupeEvidenceContext) { c.Target.Comparisons[0].CandidateID = "other" }, api.DupeRelationExistingPreferred},
		{"unchecked proof", func(c *api.DupeEvidenceContext) { c.Target.Comparisons[0].Reviewed = false }, api.DupeRelationExistingPreferred},
		{"same group repack", func(c *api.DupeEvidenceContext) {
			c.Target.Evidence.InternalStatus = "internal"
			c.Target.Comparisons[0].Replacement = "repack"
		}, api.DupeRelationProposedTrumps},
		{"same group newer", func(c *api.DupeEvidenceContext) {
			c.Target.Evidence.InternalStatus = "internal"
			c.Target.Comparisons[0].Replacement = "newer"
		}, api.DupeRelationProposedTrumps},
		{"different group newer", func(c *api.DupeEvidenceContext) {
			c.Target.Evidence.InternalStatus = "internal"
			c.Target.Evidence.Group = "OTHER"
			c.Target.Comparisons[0].Replacement = "newer"
		}, api.DupeRelationExistingPreferred},
		{"status conflict", func(c *api.DupeEvidenceContext) { c.Target.Comparisons[0].Candidate.InternalStatus = "non_internal" }, api.DupeRelationManualReview},
		{"equivalent internal coexists", func(c *api.DupeEvidenceContext) {
			nonInternalCandidate(c)
			c.Target.Comparisons[0].Quality = "equivalent"
		}, api.DupeRelationCoexists},
		{"new superior internal", func(c *api.DupeEvidenceContext) { nonInternalCandidate(c); c.Target.Evidence.BackCatalogue = "no" }, api.DupeRelationProposedTrumps},
		{"back catalogue retained", func(c *api.DupeEvidenceContext) { nonInternalCandidate(c); c.Target.Evidence.BackCatalogue = "yes" }, api.DupeRelationCoexists},
		{"back catalogue documented defect", func(c *api.DupeEvidenceContext) {
			nonInternalCandidate(c)
			c.Target.Evidence.BackCatalogue = "yes"
			c.Trumpable = true
			c.Target.Comparisons[0].DefectImproved = true
		}, api.DupeRelationProposedTrumps},
		{"back catalogue tag alone", func(c *api.DupeEvidenceContext) {
			nonInternalCandidate(c)
			c.Target.Evidence.BackCatalogue = "yes"
			c.Trumpable = true
		}, api.DupeRelationCoexists},
	} {
		t.Run(test.name, func(t *testing.T) {
			context := reviewedComparisonContext()
			test.change(&context)
			got := compareDuplicateEvidence(context)
			if got.Relation != test.want {
				t.Fatalf("got %+v, want %s", got, test.want)
			}
		})
	}
}

func reviewedComparisonContext() api.DupeEvidenceContext {
	target := api.TrackerDuplicateTarget{
		Type:       "encode",
		Resolution: "1080p",
		HDR:        lstHDR(api.HDRFormatSDR),
		Evidence:   api.DupeReleaseEvidence{InternalStatus: "non_internal", Group: "GRP"},
	}
	candidate := target
	candidate.Evidence.InternalStatus = "internal"
	target.Comparisons = []api.DupeComparisonEvidence{{
		CandidateID:          "123",
		CandidateFingerprint: "current",
		Candidate:            candidate.Evidence,
		Reviewed:             true,
		Source:               "https://example.test/comparison",
		Quality:              "superior",
	}}
	return api.DupeEvidenceContext{
		SlotEvidenceComplete: true,
		Target:               target,
		Candidate:            candidate,
		CandidateID:          "123",
		CandidateFingerprint: "current",
		Relation:             api.DupeRelationSameSlot,
	}
}

func nonInternalCandidate(context *api.DupeEvidenceContext) {
	context.Target.Evidence.InternalStatus = "internal"
	context.Candidate.Evidence.InternalStatus = "non_internal"
	context.Target.Comparisons[0].Candidate.InternalStatus = "non_internal"
}

func TestHybridAndDiscEvidence(t *testing.T) {
	for _, test := range []struct {
		name                                   string
		targetSource, candidateSource, quality string
		want                                   api.DupeRelation
	}{
		{"native replaces hybrid", "native_disc", "web_hybrid", "equivalent", api.DupeRelationProposedTrumps},
		{"superior hybrid retained", "native_disc", "web_hybrid", "inferior", api.DupeRelationExistingPreferred},
		{"native preferred", "web_hybrid", "native_disc", "equivalent", api.DupeRelationExistingPreferred},
		{"superior hybrid alongside", "web_hybrid", "native_disc", "superior", api.DupeRelationCoexists},
		{"missing layer provenance", "web_hybrid", "", "superior", api.DupeRelationManualReview},
		{"missing quality", "native_disc", "web_hybrid", "unknown", api.DupeRelationManualReview},
	} {
		t.Run(test.name, func(t *testing.T) {
			context := reviewedComparisonContext()
			context.Candidate.Evidence.InternalStatus = "non_internal"
			context.Target.Comparisons[0].Candidate.InternalStatus = "non_internal"
			context.Target.Type, context.Candidate.Type = "remux", "remux"
			context.Target.Resolution, context.Candidate.Resolution = "2160p", "2160p"
			context.Target.HDR, context.Candidate.HDR = lstHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10), lstHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10)
			context.Target.Evidence.HDRLayerSource = test.targetSource
			context.Target.Comparisons[0].Candidate.HDRLayerSource = test.candidateSource
			context.Target.Comparisons[0].Quality = test.quality
			if got := compareDuplicateEvidence(context); got.Relation != test.want {
				t.Fatalf("got %+v, want %s", got, test.want)
			}
		})
	}
	for _, reversed := range []bool{false, true} {
		context := reviewedComparisonContext()
		context.Target.Type, context.Candidate.Type = "full_disc", "full_disc"
		context.Candidate.Evidence.InternalStatus = "non_internal"
		context.Target.Comparisons[0].Candidate.InternalStatus = "non_internal"
		context.Target.Evidence.DiscIdentity = "disc-identity"
		context.Target.Comparisons[0].Candidate.DiscIdentity = "disc-identity"
		context.Target.Evidence.DiscForm = "custom"
		context.Target.Comparisons[0].Candidate.DiscForm = "unmodified"
		want := api.DupeRelationExistingPreferred
		if reversed {
			context.Target.Evidence.DiscForm, context.Target.Comparisons[0].Candidate.DiscForm = "unmodified", "custom"
			want = api.DupeRelationProposedTrumps
		}
		if got := compareDuplicateEvidence(context); got.Relation != want {
			t.Fatalf("reversed=%v: %+v", reversed, got)
		}
		context.Target.Comparisons[0].Candidate.DiscIdentity = "distinct-disc"
		if got := compareDuplicateEvidence(context); got.Relation != api.DupeRelationCoexists {
			t.Fatalf("distinct discs: %+v", got)
		}
	}
}

func TestDuplicateComparisonMatchesCurrentCandidateFingerprint(t *testing.T) {
	target := lstTarget("Example Movie 2026 1080p WEB-DL H264-TARGET", "WEBDL", "1080p", "", "H264", api.HDRFormatSDR)
	target.Evidence = api.DupeReleaseEvidence{InternalStatus: "non_internal"}
	candidate := lstCandidate("Example Movie 2026 1080p WEB-DL H264-OTHER", "WEBDL", "1080p", api.HDRFormatSDR)
	candidate.ID = "123"
	candidate.Evidence = api.DupeReleaseEvidence{InternalStatus: "internal", Group: "GRP"}
	fingerprint, err := api.CanonicalWorkflowFingerprint(candidate)
	if err != nil {
		t.Fatal(err)
	}
	target.Comparisons = []api.DupeComparisonEvidence{{
		CandidateID:          candidate.ID,
		CandidateFingerprint: fingerprint,
		Candidate:            candidate.Evidence,
		Quality:              "superior",
		Reviewed:             true,
		Source:               "https://example.test/comparison",
	}}
	evaluate := func() api.DupeRelation {
		return dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *duplicatePolicy(), dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID}).Candidates[0].Relation
	}
	if got := evaluate(); got != api.DupeRelationCoexists {
		t.Fatalf("current proof = %s", got)
	}
	candidate.Trumpable = true
	if got := evaluate(); got != api.DupeRelationExistingPreferred {
		t.Fatalf("stale proof = %s", got)
	}
}

func TestWEBRipComparisonNeverTrumpsWEBDL(t *testing.T) {
	context := reviewedComparisonContext()
	context.Reason = "lst_webrip_comparison_required"
	context.Target.Type, context.Candidate.Type = "web_rip", "web_dl"
	context.Target.Comparisons[0].VisualImprovement = true
	if got := compareDuplicateEvidence(context); got.Relation != api.DupeRelationCoexists {
		t.Fatalf("proved visual improvement: %+v", got)
	}
	context.Target.Comparisons[0].VisualImprovement = false
	if got := compareDuplicateEvidence(context); got.Relation == api.DupeRelationCoexists || got.Relation == api.DupeRelationProposedTrumps {
		t.Fatalf("missing visual proof: %+v", got)
	}
}

func TestPersonalInternalBenefitIsIndependentOfQuality(t *testing.T) {
	context := reviewedComparisonContext()
	nonInternalCandidate(&context)
	context.Target.Evidence.PersonalRelease = true
	context.Target.Evidence.BackCatalogue = "no"
	context.Target.Comparisons[0].Quality = "equivalent"
	for _, test := range []struct {
		benefit string
		want    api.DupeRelation
	}{
		{"unknown", api.DupeRelationManualReview}, {"no", api.DupeRelationExistingPreferred}, {"yes", api.DupeRelationCoexists},
	} {
		context.Target.Comparisons[0].MeaningfulBenefit = test.benefit
		if got := compareDuplicateEvidence(context); got.Relation != test.want {
			t.Fatalf("benefit %s: %+v", test.benefit, got)
		}
	}
	context.Target.Evidence.BackCatalogue = "yes"
	context.Target.Comparisons[0].MeaningfulBenefit = "no"
	if got := compareDuplicateEvidence(context); got.Relation != api.DupeRelationCoexists {
		t.Fatalf("back catalogue exemption: %+v", got)
	}
}
