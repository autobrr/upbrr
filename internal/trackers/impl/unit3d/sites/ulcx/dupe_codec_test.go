// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestULCXDuplicateCodecEvidence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		mediaInfo     string
		candidateName string
		targetCodec   string
		want          api.DupeRelation
	}{
		{
			name:        "format identifies the same AVC codec",
			mediaInfo:   "Video\nFormat : AVC\nCodec ID : V_MPEG4/ISO/AVC",
			targetCodec: "H.264",
			want:        api.DupeRelationSameSlot,
		},
		{
			name:        "container codec ID alone leaves codec unknown",
			mediaInfo:   "Video\nCodec ID : V_MPEG4/ISO/AVC",
			targetCodec: "H.264",
			want:        api.DupeRelationInsufficientEvidence,
		},
		{
			name:          "codec ID does not contradict partial title evidence",
			mediaInfo:     "Video\nCodec ID : avc3",
			candidateName: "Example.Release.2026.1080p.WEB-DL.H.264-GRP",
			targetCodec:   "H.264",
			want:          api.DupeRelationInsufficientEvidence,
		},
		{
			name:        "MPEG family does not create a different MPEG-2 slot",
			mediaInfo:   "Video\nFormat : MPEG Video\nFormat version : Version 2",
			targetCodec: "MPEG-2",
			want:        api.DupeRelationInsufficientEvidence,
		},
		{
			name:        "MPEG-4 family does not create a different XviD slot",
			mediaInfo:   "Video\nFormat : MPEG-4 Visual\nWriting library : XviD",
			targetCodec: "XviD",
			want:        api.DupeRelationInsufficientEvidence,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			target := ulcxTarget("WEBDL", "1080p", "PROVIDER", test.targetCodec, ulcxHDR(api.HDRFormatSDR))
			candidate := ulcxCandidate("WEBDL", "1080p", "PROVIDER", mediafacts.VideoCodecFromMediaInfoText(test.mediaInfo), ulcxHDR(api.HDRFormatSDR))
			if test.candidateName != "" {
				candidate.Name = test.candidateName
			}
			result := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *Profile().DupePolicy, dupe.SearchEvidence{
				Complete:  true,
				WorkScope: dupe.WorkScopeProviderID,
			}).Candidates[0]
			if result.Relation != test.want {
				t.Fatalf("relation = %s, want %s; reasons=%v", result.Relation, test.want, result.Reasons)
			}
		})
	}
}

func TestULCXHigh10CodecSharesAVCSlot(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		format string
		want   api.DupeRelation
	}{
		{format: "AVC", want: api.DupeRelationManualReview},
		{format: "HEVC", want: api.DupeRelationCoexists},
	} {
		t.Run(test.format, func(t *testing.T) {
			t.Parallel()
			target := ulcxTarget("ENCODE", "720p", "", "Hi10P x264", ulcxHDR(api.HDRFormatSDR))
			target.VideoCodec = "AVC"
			codec := mediafacts.VideoCodecFromMediaInfoText("Video\nFormat : " + test.format)
			candidate := ulcxCandidate("ENCODE", "720p", "", codec, ulcxHDR(api.HDRFormatSDR))
			assertULCXRelation(t, target, candidate, test.want)
		})
	}
}
