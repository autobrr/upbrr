// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"strings"
	"testing"
	"time"
)

func TestHDRBoundaryColourProfileCompatibility(t *testing.T) {
	for _, profile := range []string{"hdr-boundary-pq16-v1", "hdr-boundary-pq16-colour-v2"} {
		result := HDRDecodedResult{
Mode: HDRDecodedBoundarySync,
 Profile: profile,
 State: HDRDecodedComplete,
			Coverage: HDRCoverageComplete,
 Selected: 1,
 Measured: 1,
}
		if err := result.Validate(); err != nil {
			t.Fatalf("profile %s: %v", profile, err)
		}
	}
}

func TestHDRBoundaryPublicationAcceptsAutoNativePlots(t *testing.T) {
	for _, selection := range []HDRAnalysisFormats{HDRAnalysisFormatsAuto, HDRAnalysisFormatsBoth, HDRAnalysisFormatsHDR10Plus, HDRAnalysisFormatsDolbyVision} {
		t.Run(string(selection), func(t *testing.T) {
			target := HDRAnalysisTargetResult{TargetID: HDRTargetID("synthetic", "")}
			boundary := HDRDecodedResult{
				Mode:     HDRDecodedBoundarySync,
				Profile:  "hdr-boundary-pq16-v1",
				State:    HDRDecodedComplete,
				Coverage: HDRCoverageComplete,
				Selected: 1,
				Measured: 1,
			}
			for _, format := range []HDRNativeFormat{HDRNativeHDR10Plus, HDRNativeDolbyVision} {
				selected := selection == HDRAnalysisFormatsAuto || selection == HDRAnalysisFormatsBoth ||
					selection == HDRAnalysisFormatsHDR10Plus && format == HDRNativeHDR10Plus ||
					selection == HDRAnalysisFormatsDolbyVision && format == HDRNativeDolbyVision
				result := HDRFormatResult{
					Format:   format,
					State:    HDRExtractionNotAnalyzed,
					Coverage: HDRCoverageUnknown,
				}
				if selected {
					result.State, result.Coverage, result.Frames = HDRExtractionComplete, HDRCoverageComplete, 2
					result.Requested = selection != HDRAnalysisFormatsAuto
					plot, sheet := HDRArtifactHDR10PlusPlot, HDRArtifactBoundaryHDR10PlusSceneSheet
					if format == HDRNativeDolbyVision {
						plot, sheet, result.L1Samples = HDRArtifactDolbyVisionL1Plot, HDRArtifactBoundaryDVSceneSheet, 2
					}
					for _, kind := range []HDRArtifactKind{plot, sheet} {
						width, height := HDRAnalysisWidth, HDRAnalysisHeight
						if kind == sheet {
							width, height = 1800, 900
							boundary.Artifacts = append(boundary.Artifacts, PublicResourceID(kind))
						}
						target.Artifacts = append(target.Artifacts, HDRTypedArtifact{
							ID:                     PublicResourceID(kind),
							Kind:                   kind,
							Format:                 format,
							MIME:                   "image/png",
							Width:                  width,
							Height:                 height,
							Profile:                HDRAnalysisProfileV3,
							MeasurementFingerprint: strings.Repeat("a", 64),
							RenderFingerprint:      strings.Repeat("b", 64),
						})
					}
				}
				target.Formats = append(target.Formats, result)
			}
			audio := HDRDecodedResult{
				Mode:     HDRDecodedAudio,
				Profile:  AudioAnalysisProfileVersion,
				State:    HDRDecodedComplete,
				Coverage: HDRCoverageComplete,
				Selected: 1,
				Measured: 1,
			}
			area := HDRDecodedResult{
				Mode:     HDRDecodedActiveArea,
				Profile:  "hdr-boundary-area-v1",
				State:    HDRDecodedComplete,
				Coverage: HDRCoverageComplete,
				Selected: 1,
				Measured: 1,
			}
			for _, kind := range []HDRArtifactKind{HDRArtifactAudioStats, HDRArtifactAudioWaveform, HDRArtifactAudioSpectrogram, HDRArtifactActiveAreaContactSheet} {
				artifact := HDRTypedArtifact{
					ID:                     PublicResourceID(kind),
					Kind:                   kind,
					Profile:                HDRAnalysisProfileV3,
					MIME:                   "image/png",
					MeasurementFingerprint: strings.Repeat("a", 64),
					RenderFingerprint:      strings.Repeat("b", 64),
				}
				artifact.Width, artifact.Height = kind.ImageDimensions()
				if kind == HDRArtifactActiveAreaContactSheet {
					area.Artifacts = append(area.Artifacts, artifact.ID)
				} else {
					artifact.AudioTrackID = "main"
					artifact.Width, artifact.Height = 3000, 1200
					if kind == HDRArtifactAudioStats {
						artifact.Width, artifact.Height, artifact.MIME = 0, 0, "text/plain"
					}
					audio.Artifacts = append(audio.Artifacts, artifact.ID)
				}
				target.Artifacts = append(target.Artifacts, artifact)
			}
			audio.AudioTracks = []HDRAudioTrackResult{{
				TrackID:      "main",
				Ordinal:      1,
				State:        HDRDecodedComplete,
				PCMMeasured:  true,
				Channels:     2,
				SampleRate:   48000,
				SampleFrames: 48000,
				Artifacts:    audio.Artifacts,
			}}
			target.Decoded = []HDRDecodedResult{boundary, audio, area}
			target.Status = target.AnalysisStatus()
			now := time.Now().UTC()
			result := HDRAnalysisResult{
				ID:                  "analysis",
				WorkflowID:          "workflow",
				Revision:            1,
				AttemptID:           "attempt",
				ManifestFingerprint: "manifest",
				Release:             ReleaseRef{SourcePath: "Synthetic.HDR.mkv", Generation: 1},
				CreatedAt:           now,
				CompletedAt:         &now,
				ProfileVersion:      HDRAnalysisProfileV3,
				Formats:             selection,
				PeakSource:          HDRPeakHistogram,
				Decoded: &HDRDecodedOptions{
					BoundarySync: true,
					Audio:        true,
					ActiveArea:   true,
				},
				TargetIDs: []string{target.TargetID},
				Targets:   []HDRAnalysisTargetResult{target},
				Status:    target.Status,
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("publish boundary sheets with native plots: %v", err)
			}
			for _, artifact := range target.Artifacts {
				if artifact.Kind.DecodedMode() != HDRDecodedBoundarySync {
					continue
				}
				if err := artifact.Validate(nil); err == nil {
					t.Fatal("boundary sheet accepted without native authority")
				}
				for _, state := range []HDRExtractionState{HDRExtractionNotAnalyzed, HDRExtractionAbsent, HDRExtractionFailed, HDRExtractionUnsupported, HDRExtractionCanceled, HDRExtractionInterrupted, HDRExtractionComplete} {
					// Even a completed state must carry valid native evidence.
					invalid := HDRFormatResult{
						Format:    artifact.Format,
						State:     state,
						Coverage:  HDRCoverageUnknown,
						Requested: true,
					}
					if err := artifact.Validate([]HDRFormatResult{invalid}); err == nil {
						t.Fatalf("boundary sheet accepted invalid native evidence: %s", state)
					}
				}
			}
		})
	}
}

func TestHDRBoundaryOptionsRejectConflictsBeforeNormalization(t *testing.T) {
	for _, options := range []HDRDecodedOptions{
		{BoundarySync: true, Automatic: true}, {BoundarySync: true, Sampled: true}, {BoundarySync: true, SceneSync: true},
		{BoundarySync: true, Comparison: &HDRComparisonOptions{}}, {BoundarySync: true, AllAudioTracks: true},
		{Audio: true, VideoDecoderThreads: 1}, {BoundarySync: true, VideoDecoderThreads: -1}, {BoundarySync: true, VideoDecoderThreads: 65},
	} {
		if _, err := options.Normalize(); err == nil {
			t.Fatalf("accepted conflicting options: %+v", options)
		}
	}
	for _, threads := range []int{0, 1, 64} {
		options := HDRDecodedOptions{
			BoundarySync:        true,
			Audio:               true,
			AllAudioTracks:      true,
			ActiveArea:          true,
			VideoDecoderThreads: threads,
		}
		normalized, err := options.Normalize()
		if err != nil || normalized != options {
			t.Fatalf("valid boundary request changed: %+v %v", normalized, err)
		}
	}
	options, err := (HDRDecodedOptions{
		Automatic:      true,
		Audio:          true,
		AllAudioTracks: true,
	}).Normalize()
	if err != nil || options.Audio || !options.AllAudioTracks {
		t.Fatalf("automatic normalization lost audio selection: %+v %v", options, err)
	}
}

func TestHDRNewFeaturesRequireV3WithoutChangingLegacyDefault(t *testing.T) {
	for _, options := range []*HDRDecodedOptions{nil, {Automatic: true}, {BoundarySync: true}, {Audio: true, AllAudioTracks: true}, {SceneSync: true, VideoDecoderThreads: 4}} {
		request := HDRAnalysisInstructions{
			Release:   ReleaseRef{SourcePath: "Synthetic.HDR.mkv", Generation: 1},
			TargetIDs: []string{HDRTargetID("synthetic", "")},
			Decoded:   options,
		}
		got, err := request.Normalize()
		want := HDRAnalysisProfileV2
		if options == nil {
			want = HDRAnalysisProfileVersion
		}
		if options != nil && options.RequiresV3() {
			want = HDRAnalysisProfileV3
		}
		if err != nil || got.ProfileVersion != want {
			t.Fatalf("profile=%s want=%s error=%v", got.ProfileVersion, want, err)
		}
		if want == HDRAnalysisProfileV3 {
			for _, legacy := range []string{HDRAnalysisProfileVersion, HDRAnalysisProfileV2} {
				request.ProfileVersion = legacy
				if _, err := request.Normalize(); err == nil {
					t.Fatalf("new options accepted by %s", legacy)
				}
			}
		}
	}
}

func TestHDRAudioPCMCompletionDoesNotAuthorizeMissingVariants(t *testing.T) {
	result := HDRDecodedResult{
		Mode:      HDRDecodedAudio,
		Profile:   AudioAnalysisProfileVersion,
		State:     HDRDecodedInconclusive,
		Coverage:  HDRCoveragePartial,
		Selected:  2,
		Measured:  1,
		Artifacts: []PublicResourceID{"wave"},
		AudioTracks: []HDRAudioTrackResult{
			{
				TrackID:      "first",
				Ordinal:      1,
				State:        HDRDecodedInconclusive,
				PCMMeasured:  true,
				Channels:     2,
				SampleRate:   48000,
				SampleFrames: 48000,
				Artifacts:    []PublicResourceID{"wave"},
			},
			{
				TrackID: "second",
				Ordinal: 2,
				State:   HDRDecodedFailed,
			},
		},
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	result.Coverage = HDRCoverageComplete
	if err := result.Validate(); err == nil {
		t.Fatal("missing variants and failed track counted as complete")
	}
	result.Coverage = HDRCoveragePartial
	result.AudioTracks[1].Artifacts = []PublicResourceID{"wave"}
	if err := result.Validate(); err == nil {
		t.Fatal("failed track borrowed another track's output")
	}
	result.AudioTracks[1].Artifacts = nil
	result.Measured = 2
	if err := result.Validate(); err == nil {
		t.Fatal("failed PCM counted as measured")
	}
}

func TestHDRExecutionCoverageIsSeparateFromUnclearMedia(t *testing.T) {
	target := HDRAnalysisTargetResult{Formats: []HDRFormatResult{{State: HDRExtractionComplete}},
		Decoded: []HDRDecodedResult{{
			Mode:     HDRDecodedBoundarySync,
			State:    HDRDecodedInconclusive,
			Coverage: HDRCoverageComplete,
			Selected: 1,
			Measured: 1,
		}}}
	if got := target.AnalysisStatus(); got != StageStatusCompleted {
		t.Fatalf("unclear media changed execution status: %s", got)
	}
	for _, reason := range []string{"render_unavailable", "render_identity_unavailable"} {
		target.Decoded[0].Reason = reason
		if got := target.AnalysisStatus(); got != StageStatusPartial {
			t.Fatalf("%s with complete measurement coverage was not partial: %s", reason, got)
		}
	}
	target.Decoded[0].Reason = ""
	target.Decoded[0].Coverage = HDRCoveragePartial
	if got := target.AnalysisStatus(); got != StageStatusPartial {
		t.Fatalf("omitted execution was complete: %s", got)
	}
}

func TestHDRTrackStatisticsFilename(t *testing.T) {
	for _, name := range []string{"audio-stats.txt", (HDRTypedArtifact{Kind: HDRArtifactAudioStats, AudioTrackID: "synthetic-track"}).Filename()} {
		if !HDRArtifactAudioStats.MatchesFilename(name) {
			t.Fatalf("valid statistics name rejected: %s", name)
		}
	}
	for _, name := range []string{"", "../audio-stats.txt", "track-other-audio-stats.txt", "track-" + strings.Repeat("z", 64) + "-audio-stats.txt", "track-" + strings.Repeat("a", 64) + "-audio-stats.txt.png"} {
		if HDRArtifactAudioStats.MatchesFilename(name) {
			t.Fatalf("invalid statistics name accepted: %s", name)
		}
	}
}
