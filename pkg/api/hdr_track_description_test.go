// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestHDRTrackDescriptionsKeepOrderedOwnershipAndBoundaryOptIn(t *testing.T) {
	target := HDRAnalysisTargetResult{
		Formats: []HDRFormatResult{{Format: HDRNativeDolbyVision, State: HDRExtractionComplete}},
		Artifacts: []HDRTypedArtifact{
			{
				ID:           "stats-b",
				AudioTrackID: "b",
				Kind:         HDRArtifactAudioStats,
				MIME:         "text/plain",
				Profile:      HDRAnalysisProfileV3,
			},
			{
				ID:           "wave-b",
				AudioTrackID: "b",
				Kind:         HDRArtifactAudioWaveform,
				MIME:         "image/png",
				Profile:      HDRAnalysisProfileV3,
			},
			{
				ID:           "stats-a",
				AudioTrackID: "a",
				Kind:         HDRArtifactAudioStats,
				MIME:         "text/plain",
				Profile:      HDRAnalysisProfileV3,
			},
			{
				ID:           "wave-a",
				AudioTrackID: "a",
				Kind:         HDRArtifactAudioWaveform,
				MIME:         "image/png",
				Profile:      HDRAnalysisProfileV3,
			},
			{
				ID:      "boundary",
				Kind:    HDRArtifactBoundaryDVSceneSheet,
				Format:  HDRNativeDolbyVision,
				MIME:    "image/png",
				Profile: HDRAnalysisProfileV3,
			},
		},
		Decoded: []HDRDecodedResult{
			{
				Mode:      HDRDecodedBoundarySync,
				State:     HDRDecodedInconclusive,
				Measured:  1,
				Artifacts: []PublicResourceID{"boundary"},
			},
			{
				Mode:      HDRDecodedAudio,
				State:     HDRDecodedInconclusive,
				Measured:  2,
				Artifacts: []PublicResourceID{"stats-a", "stats-b", "wave-a", "wave-b"},
				AudioTracks: []HDRAudioTrackResult{
					{
						TrackID:     "a",
						Ordinal:     1,
						State:       HDRDecodedInconclusive,
						PCMMeasured: true,
						Artifacts:   []PublicResourceID{"stats-a", "wave-a"},
					},
					{
						TrackID:     "b",
						Ordinal:     2,
						State:       HDRDecodedInconclusive,
						PCMMeasured: true,
						Artifacts:   []PublicResourceID{"stats-b", "wave-b"},
					},
				},
			},
		},
	}
	stats := target.AudioStatisticsEntries()
	images := target.DescriptionImages()
	if len(stats) != 2 || stats[0].ID != "stats-a" || stats[1].ID != "stats-b" || len(images) != 2 || images[0].ID != "wave-a" || images[1].ID != "wave-b" {
		t.Fatalf("wrong ordered default outputs: stats=%+v images=%+v", stats, images)
	}
	if len(target.SelectableImages()) != 3 {
		t.Fatal("boundary sheet missing from explicit selection")
	}
	if _, ok := target.AudioStatistics(); ok {
		t.Fatal("legacy singular reader consumed new track scope")
	}
	target.Artifacts[0].AudioTrackID = "a"
	if len(target.AudioStatisticsEntries()) != 1 {
		t.Fatal("aggregate artifact list overrode exact track ownership")
	}
}
