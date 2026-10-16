// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestHDRMetadataAudioOptions(t *testing.T) {
	options, err := (HDRDecodedOptions{Audio: true}).Normalize()
	if err != nil || !options.Requested(HDRDecodedAudio) || options.Requested(HDRDecodedAutomatic) {
		t.Fatalf("metadata/audio selection: %+v %v", options, err)
	}
	for _, mode := range []HDRDecodedMode{HDRDecodedSceneSync, HDRDecodedActiveArea, HDRDecodedGrade, HDRDecodedColour} {
		if options.Requested(mode) {
			t.Fatal("audio-only requested a picture mode")
		}
	}
	if err := (HDRDecodedRetry{AnalysisAttemptID: "current", Modes: []HDRDecodedMode{HDRDecodedAudio}}).Validate(&options); err != nil {
		t.Fatal(err)
	}
	if err := (HDRDecodedRetry{AnalysisAttemptID: "current", Modes: []HDRDecodedMode{HDRDecodedAutomatic}}).Validate(&options); err == nil {
		t.Fatal("audio-only authorized a picture retry")
	}
	for _, invalid := range []HDRDecodedOptions{
		{Audio: true, Sampled: true}, {Audio: true, SceneSync: true}, {Audio: true, ActiveArea: true},
		{Audio: true, Comparison: &HDRComparisonOptions{
SourceID: "reference",
 FrameOffset: new(0),
 Crop: HDRCrop{Width: 192, Height: 112},
 Grade: true,
}},
	} {
		if _, err := invalid.Normalize(); err == nil {
			t.Fatal("audio-only accepted picture selection")
		}
	}
	canonical, err := (HDRDecodedOptions{Automatic: true, Audio: true}).Normalize()
	if err != nil || canonical.Audio || !canonical.Automatic {
		t.Fatal("automatic analysis retained redundant audio-only selection")
	}
}
