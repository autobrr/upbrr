// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLSTDuplicatePolicySlots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		target    api.TrackerDuplicateTarget
		candidate dupe.TrackerCandidate
		want      api.DupeRelation
	}{
		{
			name: "webdl provider slot",
			target: lstTarget(
				"Example.Release.2026.1080p.AMZN.WEB-DL.H.264.SDR-TARGET",
				"WEBDL", "1080p", "AMZN", "H.264", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.1080p.DSNP.WEB-DL.H.264.SDR-OTHER",
				"WEBDL", "1080p", api.HDRFormatSDR,
			),
			want: api.DupeRelationCoexists,
		},
		{
			name: "same webdl slot",
			target: lstTarget(
				"Example.Release.2026.1080p.AMZN.WEB-DL.H.264.SDR-TARGET",
				"WEBDL", "1080p", "AMZN", "H.264", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.1080p.AMZN.WEB-DL.H.264.SDR-OTHER",
				"WEBDL", "1080p", api.HDRFormatSDR,
			),
			want: api.DupeRelationSameSlot,
		},
		{
			// These alias cases require the real codes used by the title parser and API.
			//nolint:misspell // ADN is LST's exact provider code.
			name: "ADN API provider matches AND target",
			target: lstTarget(
				"Example.Release.2026.1080p.WEB-DL.H.264.SDR-TARGET",
				"WEBDL", "1080p", "AND", "H.264", api.HDRFormatSDR,
			),
			candidate: lstCandidateWithProvider(
				"Example.Release.2026.1080p.WEB-DL.H.264.SDR-OTHER",
				//nolint:misspell // ADN is LST's exact provider code.
				"WEBDL", "1080p", "ADN", api.HDRFormatSDR,
			),
			want: api.DupeRelationSameSlot,
		},
		{
			name: "HTSR API provider matches Hotstar target",
			target: lstTarget(
				"Example.Release.2026.1080p.WEB-DL.H.264.SDR-TARGET",
				"WEBDL", "1080p", "Hotstar", "H.264", api.HDRFormatSDR,
			),
			candidate: lstCandidateWithProvider(
				"Example.Release.2026.1080p.WEB-DL.H.264.SDR-OTHER",
				"WEBDL", "1080p", "HTSR", api.HDRFormatSDR,
			),
			want: api.DupeRelationSameSlot,
		},
		{
			name: "distinct structured webdl providers coexist",
			target: lstTarget(
				"Example.Release.2026.1080p.WEB-DL.H.264.SDR-TARGET",
				"WEBDL", "1080p", "PROVIDER_A", "H.264", api.HDRFormatSDR,
			),
			candidate: lstCandidateWithProvider(
				"Example.Release.2026.1080p.WEB-DL.H.264.SDR-OTHER",
				"WEBDL", "1080p", "PROVIDER_B", api.HDRFormatSDR,
			),
			want: api.DupeRelationCoexists,
		},
		{
			name: "webdl codec slot",
			target: lstTarget(
				"Example.Release.2026.1080p.AMZN.WEB-DL.H.264.SDR-TARGET",
				"WEBDL", "1080p", "AMZN", "H.264", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.1080p.AMZN.WEB-DL.H.265.SDR-OTHER",
				"WEBDL", "1080p", api.HDRFormatSDR,
			),
			want: api.DupeRelationCoexists,
		},
		{
			name: "webdl codec slot requires candidate provider",
			target: lstTarget(
				"Example.Release.2026.1080p.AMZN.WEB-DL.H.264.SDR-TARGET",
				"WEBDL", "1080p", "AMZN", "H.264", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.1080p.WEB-DL.H.265.SDR-OTHER",
				"WEBDL", "1080p", api.HDRFormatSDR,
			),
			want: api.DupeRelationInsufficientEvidence,
		},
		{
			name: "webdl codec slot requires provider evidence",
			target: lstTarget(
				"Example.Release.2026.1080p.WEB-DL.H.264.SDR-TARGET",
				"WEBDL", "1080p", "", "H.264", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.1080p.WEB-DL.H.265.SDR-OTHER",
				"WEBDL", "1080p", api.HDRFormatSDR,
			),
			want: api.DupeRelationInsufficientEvidence,
		},
		{
			name: "720p webdl has no alternate codec slot",
			target: lstTarget(
				"Example.Release.2026.720p.AMZN.WEB-DL.H.264.SDR-TARGET",
				"WEBDL", "720p", "AMZN", "H.264", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.720p.AMZN.WEB-DL.H.265.SDR-OTHER",
				"WEBDL", "720p", api.HDRFormatSDR,
			),
			want: api.DupeRelationSameSlot,
		},
		{
			name: "1080p webdl AV1 codec slot",
			target: lstTarget(
				"Example.Release.2026.1080p.WEB-DL.H.265.SDR-TARGET",
				"WEBDL", "1080p", "PROVIDER", "H.265", api.HDRFormatSDR,
			),
			candidate: lstCandidateWithProvider(
				"Example.Release.2026.1080p.WEB-DL.AV1.SDR-OTHER",
				"WEBDL", "1080p", "PROVIDER", api.HDRFormatSDR,
			),
			want: api.DupeRelationCoexists,
		},
		{
			name: "1440p webdl has no AV1 codec slot",
			target: lstTarget(
				"Example.Release.2026.1440p.WEB-DL.H.265.SDR-TARGET",
				"WEBDL", "1440p", "PROVIDER", "H.265", api.HDRFormatSDR,
			),
			candidate: lstCandidateWithProvider(
				"Example.Release.2026.1440p.WEB-DL.AV1.SDR-OTHER",
				"WEBDL", "1440p", "PROVIDER", api.HDRFormatSDR,
			),
			want: api.DupeRelationSameSlot,
		},
		{
			name: "HDR webdl has no H264 codec slot",
			target: lstTarget(
				"Example.Release.2026.1080p.WEB-DL.HDR.H.265-TARGET",
				"WEBDL", "1080p", "PROVIDER", "H.265", api.HDRFormatHDR10,
			),
			candidate: lstCandidateWithProvider(
				"Example.Release.2026.1080p.WEB-DL.HDR.H.264-OTHER",
				"WEBDL", "1080p", "PROVIDER", api.HDRFormatHDR10,
			),
			want: api.DupeRelationSameSlot,
		},
		{
			name: "webdl dv hdr trumps hdr",
			target: lstTarget(
				"Example.Release.2026.1080p.AMZN.WEB-DL.DV.HDR.H.265-TARGET",
				"WEBDL", "1080p", "AMZN", "H.265", api.HDRFormatDolbyVision, api.HDRFormatHDR10,
			),
			candidate: lstCandidate(
				"Example.Release.2026.1080p.AMZN.WEB-DL.HDR.H.265-OTHER",
				"WEBDL", "1080p", api.HDRFormatHDR10,
			),
			want: api.DupeRelationProposedTrumps,
		},
		{
			name: "webrip needs comparison against webdl",
			target: lstTarget(
				"Example.Release.2026.1080p.AMZN.WEBRip.H.264.SDR-TARGET",
				"WEBRIP", "1080p", "AMZN", "H.264", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.1080p.AMZN.WEB-DL.H.264.SDR-OTHER",
				"WEBDL", "1080p", api.HDRFormatSDR,
			),
			want: api.DupeRelationManualReview,
		},
		{
			name: "webdl keeps slot beside webrip",
			target: lstTarget(
				"Example.Release.2026.1080p.AMZN.WEB-DL.H.264.SDR-TARGET",
				"WEBDL", "1080p", "AMZN", "H.264", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.1080p.AMZN.WEBRip.H.264.SDR-OTHER",
				"WEBRIP", "1080p", api.HDRFormatSDR,
			),
			want: api.DupeRelationCoexists,
		},
		{
			name: "same encode slot needs quality review",
			target: lstTarget(
				"Example.Release.2026.1080p.BluRay.x264.SDR-TARGET",
				"ENCODE", "1080p", "", "x264", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.1080p.BluRay.x264.SDR-OTHER",
				"ENCODE", "1080p", api.HDRFormatSDR,
			),
			want: api.DupeRelationManualReview,
		},
		{
			name: "encode codec slot",
			target: lstTarget(
				"Example.Release.2026.1080p.BluRay.x264.SDR-TARGET",
				"ENCODE", "1080p", "", "x264", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.1080p.BluRay.x265.SDR-OTHER",
				"ENCODE", "1080p", api.HDRFormatSDR,
			),
			want: api.DupeRelationCoexists,
		},
		{
			name: "2160p encode AV1 codec slot",
			target: lstTarget(
				"Example.Release.2026.2160p.BluRay.x265.HDR-TARGET",
				"ENCODE", "2160p", "", "x265", api.HDRFormatHDR10,
			),
			candidate: lstCandidate(
				"Example.Release.2026.2160p.BluRay.AV1.HDR-OTHER",
				"ENCODE", "2160p", api.HDRFormatHDR10,
			),
			want: api.DupeRelationCoexists,
		},
		{
			name: "2160p encode has no H264 codec slot",
			target: lstTarget(
				"Example.Release.2026.2160p.BluRay.x265.SDR-TARGET",
				"ENCODE", "2160p", "", "x265", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.2160p.BluRay.x264.SDR-OTHER",
				"ENCODE", "2160p", api.HDRFormatSDR,
			),
			want: api.DupeRelationSameSlot,
		},
		{
			name: "AV1 encode DV HDR trump needs master review",
			target: lstTarget(
				"Example.Release.2026.2160p.BluRay.AV1.DV.HDR-TARGET",
				"ENCODE", "2160p", "", "AV1", api.HDRFormatDolbyVision, api.HDRFormatHDR10,
			),
			candidate: lstCandidate(
				"Example.Release.2026.2160p.BluRay.AV1.HDR-OTHER",
				"ENCODE", "2160p", api.HDRFormatHDR10,
			),
			want: api.DupeRelationManualReview,
		},
		{
			name: "same remux slot needs master review",
			target: lstTarget(
				"Example.Release.2026.1080p.BluRay.REMUX.AVC.SDR-TARGET",
				"REMUX", "1080p", "", "H.264", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.1080p.BluRay.REMUX.AVC.SDR-OTHER",
				"REMUX", "1080p", api.HDRFormatSDR,
			),
			want: api.DupeRelationManualReview,
		},
		{
			name: "remux dv hdr trump needs master review",
			target: lstTarget(
				"Example.Release.2026.2160p.BluRay.REMUX.DV.HDR.HEVC-TARGET",
				"REMUX", "2160p", "", "H.265", api.HDRFormatDolbyVision, api.HDRFormatHDR10,
			),
			candidate: lstCandidate(
				"Example.Release.2026.2160p.BluRay.REMUX.HDR.HEVC-OTHER",
				"REMUX", "2160p", api.HDRFormatHDR10,
			),
			want: api.DupeRelationManualReview,
		},
		{
			name: "same full disc slot needs content review",
			target: lstTarget(
				"Example.Release.2026.1080p.COMPLETE.BLURAY-TARGET",
				"DISC", "1080p", "", "", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.1080p.COMPLETE.BLURAY-OTHER",
				"DISC", "1080p", api.HDRFormatSDR,
			),
			want: api.DupeRelationManualReview,
		},
		{
			name: "2160p webdl same slot needs audio review",
			target: lstTarget(
				"Example.Release.2026.2160p.AMZN.WEB-DL.H.265.SDR-TARGET",
				"WEBDL", "2160p", "AMZN", "H.265", api.HDRFormatSDR,
			),
			candidate: lstCandidate(
				"Example.Release.2026.2160p.AMZN.WEB-DL.H.265.SDR-OTHER",
				"WEBDL", "2160p", api.HDRFormatSDR,
			),
			want: api.DupeRelationManualReview,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			policy := Profile().DupePolicy
			result := dupe.Evaluate(
				test.target,
				[]dupe.TrackerCandidate{test.candidate},
				*policy,
				dupe.SearchEvidence{
					Complete:  true,
					Pages:     1,
					WorkScope: dupe.WorkScopeProviderID,
				},
			).Candidates[0]
			if result.Relation != test.want {
				t.Fatalf("relation = %q, want %q; result=%#v", result.Relation, test.want, result)
			}
		})
	}
}

func TestLSTHybridRemuxHDRSlotReview(t *testing.T) {
	t.Parallel()

	for _, targetFormat := range []api.HDRFormat{api.HDRFormatHDR10, api.HDRFormatHDR10Plus} {
		candidateFormat := api.HDRFormatHDR10Plus
		if targetFormat == api.HDRFormatHDR10Plus {
			candidateFormat = api.HDRFormatHDR10
		}
		for _, test := range []struct {
			name                string
			targetType          string
			candidateType       string
			targetResolution    string
			candidateResolution string
			candidateHDRStatus  api.HDREvidenceStatus
			candidateEdition    string
			candidateRegion     string
			candidateThreeD     string
			want                api.DupeRelation
		}{
			{
				name:                "remux requires hybrid slot review",
				targetType:          "REMUX",
				candidateType:       "REMUX",
				targetResolution:    "2160p",
				candidateResolution: "2160p",
				want:                api.DupeRelationManualReview,
			},
			{
				name:                "webdl retains separate HDR slots",
				targetType:          "WEBDL",
				candidateType:       "WEBDL",
				targetResolution:    "2160p",
				candidateResolution: "2160p",
				want:                api.DupeRelationCoexists,
			},
			{
				name:                "encode retains separate HDR slots",
				targetType:          "ENCODE",
				candidateType:       "ENCODE",
				targetResolution:    "2160p",
				candidateResolution: "2160p",
				want:                api.DupeRelationCoexists,
			},
			{
				name:                "remux and encode coexist",
				targetType:          "REMUX",
				candidateType:       "ENCODE",
				targetResolution:    "2160p",
				candidateResolution: "2160p",
				want:                api.DupeRelationCoexists,
			},
			{
				name:                "encode and remux coexist",
				targetType:          "ENCODE",
				candidateType:       "REMUX",
				targetResolution:    "2160p",
				candidateResolution: "2160p",
				want:                api.DupeRelationCoexists,
			},
			{
				name:                "lower resolution remux retains HDR slots",
				targetType:          "REMUX",
				candidateType:       "REMUX",
				targetResolution:    "1080p",
				candidateResolution: "1080p",
				want:                api.DupeRelationCoexists,
			},
			{
				name:                "different remux resolutions coexist",
				targetType:          "REMUX",
				candidateType:       "REMUX",
				targetResolution:    "2160p",
				candidateResolution: "1080p",
				want:                api.DupeRelationCoexists,
			},
			{
				name:                "different cuts coexist",
				targetType:          "REMUX",
				candidateType:       "REMUX",
				targetResolution:    "2160p",
				candidateResolution: "2160p",
				candidateEdition:    "Extended",
				want:                api.DupeRelationCoexists,
			},
			{
				name:                "different regions coexist",
				targetType:          "REMUX",
				candidateType:       "REMUX",
				targetResolution:    "2160p",
				candidateResolution: "2160p",
				candidateRegion:     "B",
				want:                api.DupeRelationCoexists,
			},
			{
				name:                "different 3D presentations coexist",
				targetType:          "REMUX",
				candidateType:       "REMUX",
				targetResolution:    "2160p",
				candidateResolution: "2160p",
				candidateThreeD:     "3D",
				want:                api.DupeRelationCoexists,
			},
			{
				name:             "missing remux resolution needs evidence",
				targetType:       "REMUX",
				candidateType:    "REMUX",
				targetResolution: "2160p",
				want:             api.DupeRelationManualReview,
			},
			{
				name:                "partial HDR needs evidence",
				targetType:          "REMUX",
				candidateType:       "REMUX",
				targetResolution:    "2160p",
				candidateResolution: "2160p",
				candidateHDRStatus:  api.HDREvidencePartial,
				want:                api.DupeRelationManualReview,
			},
			{
				name:                "contradictory HDR needs review",
				targetType:          "REMUX",
				candidateType:       "REMUX",
				targetResolution:    "2160p",
				candidateResolution: "2160p",
				candidateHDRStatus:  api.HDREvidenceContradictory,
				want:                api.DupeRelationManualReview,
			},
		} {
			t.Run(string(targetFormat)+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				target := lstTarget("Example.Release.2026-TARGET", test.targetType, test.targetResolution,
					"PROVIDER", "H.265", api.HDRFormatDolbyVision, targetFormat)
				target.Edition = "Theatrical"
				target.Region = "A"
				target.ThreeD = "2D"
				candidate := lstCandidateWithProvider("Example.Release.2026-OTHER", test.candidateType,
					test.candidateResolution, "PROVIDER", api.HDRFormatDolbyVision, candidateFormat)
				candidate.Edition = test.candidateEdition
				candidate.Region = test.candidateRegion
				candidate.ThreeD = test.candidateThreeD
				if test.candidateHDRStatus != "" {
					candidate.HDR.Status = test.candidateHDRStatus
				}
				result := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *Profile().DupePolicy,
					dupe.SearchEvidence{
						Complete:  true,
						Pages:     1,
						WorkScope: dupe.WorkScopeProviderID,
					}).Candidates[0]
				if result.Relation != test.want {
					t.Fatalf("relation = %q, want %q; reasons=%#v", result.Relation, test.want, result.Reasons)
				}
				wantReason := "lst_hybrid_remux_hdr_slot_review"
				if test.candidateHDRStatus == api.HDREvidenceContradictory {
					wantReason = "lst_hdr_trump_requires_master_review"
				}
				hasReviewReason := slices.ContainsFunc(result.Reasons, func(reason api.DupeReason) bool {
					return reason.Code == wantReason
				})
				if hasReviewReason != (test.want == api.DupeRelationManualReview) {
					t.Fatalf("unexpected slot review reason: %#v", result.Reasons)
				}
			})
		}
	}
}

func TestLSTHybridRemuxPreservesSeasonPackContainment(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		targetPack bool
		want       api.DupeRelation
		wantBlocks bool
	}{
		{
			name:       "existing pack blocks episode",
			targetPack: false,
			want:       api.DupeRelationExistingPreferred,
			wantBlocks: true,
		},
		{
			name:       "proposed pack discards episode",
			targetPack: true,
			want:       api.DupeRelationCoexists,
			wantBlocks: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			target := lstTarget("Example.Release.2026-TARGET", "REMUX", "2160p", "", "H.265",
				api.HDRFormatDolbyVision, api.HDRFormatHDR10)
			candidate := lstCandidate("Example.Release.2026-OTHER", "REMUX", "2160p",
				api.HDRFormatDolbyVision, api.HDRFormatHDR10Plus)
			target.Season, candidate.Season = 1, 1
			target.Pack, candidate.Pack = test.targetPack, !test.targetPack
			if test.targetPack {
				candidate.Episode = 1
			} else {
				target.Episode = 1
			}
			result := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *Profile().DupePolicy,
				dupe.SearchEvidence{
					Complete:  true,
					Pages:     1,
					WorkScope: dupe.WorkScopeProviderID,
				})
			if result.Candidates[0].Relation != test.want || result.Blocks != test.wantBlocks || result.RequiresAction {
				t.Fatalf("relation=%q blocks=%t action=%t; want relation=%q blocks=%t action=false",
					result.Candidates[0].Relation, result.Blocks, result.RequiresAction, test.want, test.wantBlocks)
			}
		})
	}
}

func TestLSTDuplicatePolicyMetadata(t *testing.T) {
	t.Parallel()

	policy := Profile().DupePolicy
	if policy == nil || policy.ID != "lst/duplicate/v5" || policy.EvidenceID != lstDupeEvidenceID {
		t.Fatalf("LST duplicate policy = %#v", policy)
	}
}

func lstTarget(
	name string,
	typeValue string,
	resolution string,
	provider string,
	codec string,
	formats ...api.HDRFormat,
) api.TrackerDuplicateTarget {
	return api.TrackerDuplicateTarget{
		Names:       []string{name},
		Type:        typeValue,
		Provider:    provider,
		Resolution:  resolution,
		VideoEncode: codec,
		HDR:         lstHDR(formats...),
	}
}

func lstCandidate(name string, typeValue string, resolution string, formats ...api.HDRFormat) dupe.TrackerCandidate {
	return dupe.NormalizeCandidate(api.DupeEntry{
		Name:          name,
		Type:          typeValue,
		CanonicalType: typeValue,
		Res:           resolution,
		HDR:           lstHDR(formats...),
	}, "LST")
}

func lstCandidateWithProvider(name string, typeValue string, resolution string, provider string, formats ...api.HDRFormat) dupe.TrackerCandidate {
	candidate := lstCandidate(name, typeValue, resolution, formats...)
	candidate.Provider = provider
	return candidate
}

func lstHDR(formats ...api.HDRFormat) api.HDRFacts {
	return api.HDRFacts{
		Formats: formats,
		Origin:  api.HDREvidenceMediaInfo,
		Status:  api.HDREvidenceComplete,
	}
}
