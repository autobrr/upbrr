// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestHDRKnownAudioAbsenceRequiresEmptyScope(t *testing.T) {
	absent := HDRDecodedResult{
		Mode:     HDRDecodedAudio,
		Profile:  AudioAnalysisProfileVersion,
		State:    HDRDecodedNotRun,
		Coverage: HDRCoverageComplete,
		Reason:   "no_audio",
	}
	if err := absent.Validate(); err != nil {
		t.Fatalf("known empty audio scope: %v", err)
	}
	for _, test := range []struct {
		name   string
		change func(*HDRDecodedResult)
	}{
		{"unknown absence", func(r *HDRDecodedResult) { r.Reason = "prepared_audio_scope_unavailable" }},
		{"unmeasured selection", func(r *HDRDecodedResult) { r.Selected = 1 }},
		{"claimed measurement", func(r *HDRDecodedResult) { r.Selected, r.Measured = 1, 1 }},
		{"artifact without track", func(r *HDRDecodedResult) { r.Artifacts = []PublicResourceID{"unowned"} }},
		{"track without selection", func(r *HDRDecodedResult) {
			r.AudioTracks = []HDRAudioTrackResult{{
				TrackID: "main",
				Ordinal: 1,
				State:   HDRDecodedNotRun,
			}}
		}},
		{"picture mode", func(r *HDRDecodedResult) { r.Mode = HDRDecodedActiveArea }},
		{"claimed completion", func(r *HDRDecodedResult) { r.State = HDRDecodedComplete }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := absent
			test.change(&invalid)
			if err := invalid.Validate(); err == nil {
				t.Fatal("accepted complete coverage without known empty audio scope")
			}
		})
	}
}
