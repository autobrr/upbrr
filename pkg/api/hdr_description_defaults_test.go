// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"slices"
	"strings"
	"testing"
)

func TestHDRInconclusiveAudioStatusWithoutNativeMetadata(t *testing.T) {
	for _, test := range []struct {
		name     string
		mode     HDRDecodedMode
		state    HDRDecodedState
		measured int
		want     StageStatus
	}{
		{"complete audio", HDRDecodedAudio, HDRDecodedComplete, 1, StageStatusPartial},
		{"partial audio", HDRDecodedAudio, HDRDecodedInconclusive, 1, StageStatusPartial},
		{"unmeasured audio", HDRDecodedAudio, HDRDecodedInconclusive, 0, StageStatusFailed},
		{"failed audio", HDRDecodedAudio, HDRDecodedFailed, 0, StageStatusFailed},
		{"canceled audio", HDRDecodedAudio, HDRDecodedCanceled, 1, StageStatusCanceled},
		{"interrupted audio", HDRDecodedAudio, HDRDecodedInterrupted, 1, StageStatusInterrupted},
		{"scene sync policy", HDRDecodedSceneSync, HDRDecodedInconclusive, 1, StageStatusFailed},
		{"active area policy", HDRDecodedActiveArea, HDRDecodedInconclusive, 1, StageStatusFailed},
		{"grade policy", HDRDecodedGrade, HDRDecodedInconclusive, 1, StageStatusFailed},
		{"colour policy", HDRDecodedColour, HDRDecodedInconclusive, 1, StageStatusFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := HDRAnalysisTargetResult{Decoded: []HDRDecodedResult{{
				Mode:     test.mode,
				State:    test.state,
				Measured: test.measured,
			}}}
			target.Status = target.AnalysisStatus()
			if target.Status != test.want || NativeHDRAggregate([]HDRAnalysisTargetResult{target}) != test.want {
				t.Fatalf("target=%s aggregate=%s want=%s", target.Status, NativeHDRAggregate([]HDRAnalysisTargetResult{target}), test.want)
			}
		})
	}
}

func TestHDRDefaultDescriptionIncludesPrimaryAudio(t *testing.T) {
	for _, state := range []HDRDecodedState{HDRDecodedComplete, HDRDecodedInconclusive, HDRDecodedFailed} {
		t.Run(string(state), func(t *testing.T) {
			target := HDRAnalysisTargetResult{
				Artifacts: []HDRTypedArtifact{
					{
						ID:   "wave",
						Kind: HDRArtifactAudioWaveform,
						MIME: "image/png",
					},
					{
						ID:   "spectrum",
						Kind: HDRArtifactAudioSpectrogram,
						MIME: "image/png",
					},
					{
						ID:   "unowned",
						Kind: HDRArtifactAudioWaveform,
						MIME: "image/png",
					},
					{
						ID:   "stats",
						Kind: HDRArtifactAudioStats,
						MIME: "text/plain",
					},
					{
						ID:   "picture",
						Kind: HDRArtifactPicturePlot,
						MIME: "image/png",
					},
				},
				Decoded: []HDRDecodedResult{
					{
						Mode:      HDRDecodedAudio,
						State:     state,
						Measured:  1,
						Artifacts: []PublicResourceID{"wave", "spectrum", "stats"},
					},
					{
						Mode:      HDRDecodedAutomatic,
						State:     HDRDecodedComplete,
						Measured:  1,
						Artifacts: []PublicResourceID{"picture"},
					},
				},
			}
			var defaults []PublicResourceID
			for _, image := range target.DescriptionImages() {
				defaults = append(defaults, image.ID)
			}
			want := []PublicResourceID{"wave", "spectrum"}
			if state == HDRDecodedFailed {
				want = nil
			}
			if !slices.Equal(defaults, want) {
				t.Fatalf("defaults=%v want=%v", defaults, want)
			}
			selectable := target.SelectableImages()
			if len(selectable) != len(want)+1 || selectable[len(selectable)-1].ID != "picture" {
				t.Fatalf("picture opt-in or duplicate audio selection: %+v", selectable)
			}
			result := HDRAnalysisResult{
				ProfileVersion: HDRAnalysisProfileV2,
				Status:         StageStatusPartial,
				Targets:        []HDRAnalysisTargetResult{target},
			}
			if result.CanIncludeDefaultImages() != (state != HDRDecodedFailed) {
				t.Fatalf("default inclusion with audio=%s", state)
			}
		})
	}
	statsOnly := HDRAnalysisResult{
		ProfileVersion: HDRAnalysisProfileV2,
		Status:         StageStatusPartial,
		Targets: []HDRAnalysisTargetResult{{
			Artifacts: []HDRTypedArtifact{{
				ID:   "stats",
				Kind: HDRArtifactAudioStats,
				MIME: "text/plain",
			}},
			Decoded: []HDRDecodedResult{{
				Mode:      HDRDecodedAudio,
				State:     HDRDecodedInconclusive,
				Measured:  1,
				Artifacts: []PublicResourceID{"stats"},
			}},
		}},
	}
	if !statsOnly.CanIncludeImages() || !statsOnly.CanIncludeDefaultImages() || len(statsOnly.Targets[0].SelectableImages()) != 0 {
		t.Fatal("saved primary-audio statistics were lost or offered for image hosting")
	}
}

func TestHDRNoAudioPreservesNativeCompletion(t *testing.T) {
	for _, coverage := range []HDRCoverage{"", HDRCoverageUnknown, HDRCoveragePartial} {
		t.Run(string(coverage), func(t *testing.T) {
			target := HDRAnalysisTargetResult{Formats: []HDRFormatResult{{State: HDRExtractionComplete}},
				Decoded: []HDRDecodedResult{{
					Mode: HDRDecodedAudio,
					State: HDRDecodedNotRun,
					Reason: "no_audio",
					Coverage: coverage,
				}}}
			target.Status = target.AnalysisStatus()
			if target.Status != StageStatusCompleted || NativeHDRAggregate([]HDRAnalysisTargetResult{target}) != StageStatusCompleted {
				t.Fatalf("absent audio downgraded completed metadata: %s", target.Status)
			}
			target.Decoded[0].Reason = "unavailable"
			if target.AnalysisStatus() != StageStatusPartial {
				t.Fatal("unavailable audio was treated as absent")
			}
		})
	}
}

func TestHDRDescriptionStatsRequireExactAuthority(t *testing.T) {
	assets := ExactMediaAssets{HDRStats: []HDRDescriptionStats{{
		ArtifactID: "stats",
		TargetID:   HDRTargetID("synthetic", ""),
		Text:       "Peak -1 dB",
	}}}
	if err := assets.Validate(); err == nil {
		t.Fatal("unbound statistics accepted")
	}
	assets.HDRAnalysis = &HDRAnalysisRef{ID: "hdr", Revision: 1}
	if err := assets.Validate(); err != nil {
		t.Fatal(err)
	}
	clone := assets.Clone()
	clone.HDRStats[0].Text = "changed"
	if assets.HDRStats[0].Text != "Peak -1 dB" {
		t.Fatal("cloned statistics share mutable storage")
	}
	assets.HDRStats = append(assets.HDRStats, assets.HDRStats[0])
	if err := assets.Validate(); err == nil {
		t.Fatal("duplicate statistics accepted")
	}
	assets.HDRStats = assets.HDRStats[:1]
	assets.HDRStats[0].Text = strings.Repeat("x", AudioAnalysisStatsMaxBytes+1)
	if err := assets.Validate(); err == nil {
		t.Fatal("unbounded statistics accepted")
	}
}
