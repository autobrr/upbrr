// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestULCXDuplicateSlots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		target    api.TrackerDuplicateTarget
		candidate dupe.TrackerCandidate
		want      api.DupeRelation
	}{
		{
			name:      "different WEB-DL provider coexists",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER-A", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "1080p", "PROVIDER-B", "AVC", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "different WEB-DL codec coexists",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "1080p", "PROVIDER", "HEVC", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "same WEB-DL slot requires action",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "1080p", "PROVIDER", "AVC", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationSameSlot,
		},
		{
			name:      "better WEB-DL HDR tier trumps",
			target:    ulcxTarget("WEBDL", "2160p", "PROVIDER", "H.265", ulcxHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10Plus)),
			candidate: ulcxCandidate("WEBDL", "2160p", "PROVIDER", "HEVC", ulcxHDR(api.HDRFormatHDR10Plus)),
			want:      api.DupeRelationProposedTrumps,
		},
		{
			name:      "DV HDR10 and HDR10 plus WEB-DLs coexist",
			target:    ulcxTarget("WEBDL", "2160p", "PROVIDER", "H.265", ulcxHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10)),
			candidate: ulcxCandidate("WEBDL", "2160p", "PROVIDER", "HEVC", ulcxHDR(api.HDRFormatHDR10Plus)),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "different encode codec coexists",
			target:    ulcxTarget("ENCODE", "1080p", "", "x264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("ENCODE", "1080p", "", "HEVC", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "same encode codec needs bitrate and cut review",
			target:    ulcxTarget("ENCODE", "1080p", "", "x264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("ENCODE", "1080p", "", "AVC", ulcxHDR(api.HDRFormatHDR10)),
			want:      api.DupeRelationManualReview,
		},
		{
			name:      "same resolution remux needs colour and cut review",
			target:    ulcxTarget("REMUX", "2160p", "", "H.265", ulcxHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10)),
			candidate: ulcxCandidate("REMUX", "2160p", "", "HEVC", ulcxHDR(api.HDRFormatHDR10)),
			want:      api.DupeRelationManualReview,
		},
		{
			name:      "same resolution full disc needs content review",
			target:    ulcxTarget("DISC", "1080p", "", "", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("DISC", "1080p", "", "", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationManualReview,
		},
		{
			name:      "missing WEB-DL provider is insufficient evidence",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationInsufficientEvidence,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result := dupe.Evaluate(
				test.target,
				[]dupe.TrackerCandidate{test.candidate},
				*Profile().DupePolicy,
				dupe.SearchEvidence{Complete: true, Pages: 1},
			).Candidates[0]
			if result.Relation != test.want {
				t.Fatalf("relation = %q, want %q; result=%#v", result.Relation, test.want, result)
			}
		})
	}
}

func TestULCXDuplicatePolicyMetadata(t *testing.T) {
	t.Parallel()

	policy := Profile().DupePolicy
	if policy == nil || policy.ID != "ulcx/duplicate/v3" || policy.EvidenceID != ulcxDupeEvidenceID ||
		policy.SizeVariancePercent != 0 {
		t.Fatalf("ULCX duplicate policy = %#v", policy)
	}
}

func ulcxTarget(
	typeValue string,
	resolution string,
	provider string,
	codec string,
	hdr api.HDRFacts,
) api.TrackerDuplicateTarget {
	return api.TrackerDuplicateTarget{
		Names:       []string{"Example.Release.2026.PROPOSED"},
		Type:        typeValue,
		Provider:    provider,
		Resolution:  resolution,
		VideoEncode: codec,
		HDR:         hdr,
	}
}

func ulcxCandidate(
	typeValue string,
	resolution string,
	provider string,
	codec string,
	hdr api.HDRFacts,
) dupe.TrackerCandidate {
	return dupe.NormalizeCandidate(api.DupeEntry{
		Name:          "Example.Release.2026.EXISTING",
		Type:          typeValue,
		CanonicalType: typeValue,
		Res:           resolution,
		Provider:      provider,
		Codec:         codec,
		HDR:           hdr,
	}, "ULCX")
}

func ulcxHDR(formats ...api.HDRFormat) api.HDRFacts {
	return api.HDRFacts{
		Formats: formats,
		Origin:  api.HDREvidenceMediaInfo,
		Status:  api.HDREvidenceComplete,
	}
}
