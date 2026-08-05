// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
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
				dupe.SearchEvidence{Complete: true, Pages: 1},
			).Candidates[0]
			if result.Relation != test.want {
				t.Fatalf("relation = %q, want %q; result=%#v", result.Relation, test.want, result)
			}
		})
	}
}

func TestLSTDuplicatePolicyMetadata(t *testing.T) {
	t.Parallel()

	policy := Profile().DupePolicy
	if policy == nil || policy.ID != "lst/duplicate/v3" || policy.EvidenceID != lstDupeEvidenceID {
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

func lstHDR(formats ...api.HDRFormat) api.HDRFacts {
	return api.HDRFacts{
		Formats: formats,
		Origin:  api.HDREvidenceMediaInfo,
		Status:  api.HDREvidenceComplete,
	}
}
