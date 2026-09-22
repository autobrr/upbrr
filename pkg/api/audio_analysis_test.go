// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAudioAnalysisInstructionsNormalizeValidatesAndDetachesSelection(t *testing.T) {
	release := ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 2}
	trackIDs := []string{" track-1 ", "track-2"}
	variants := []AudioAnalysisVariant{AudioAnalysisWaveform, AudioAnalysisSpectrogram}
	normalized, err := (AudioAnalysisInstructions{
		Release:    release,
		ResourceID: " resource-1 ",
		Selection:  AudioAnalysisSelectionSelected,
		TrackIDs:   trackIDs,
		Variants:   variants,
	}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	trackIDs[0] = "changed"
	variants[0] = "changed"
	if normalized.ResourceID != "resource-1" || normalized.ProfileVersion != AudioAnalysisProfileVersion ||
		normalized.TrackIDs[0] != "track-1" || normalized.Variants[0] != AudioAnalysisWaveform ||
		normalized.ResourceLimits != (AudioAnalysisResourceLimits{
			DecoderThreads: AudioAnalysisDefaultDecoderThreads,
		}) {
		t.Fatalf("normalized instructions = %#v", normalized)
	}

	for _, test := range []struct {
		name   string
		mutate func(*AudioAnalysisInstructions)
	}{
		{name: "missing release", mutate: func(value *AudioAnalysisInstructions) { value.Release = ReleaseRef{} }},
		{name: "missing resource", mutate: func(value *AudioAnalysisInstructions) { value.ResourceID = "" }},
		{name: "invalid selection", mutate: func(value *AudioAnalysisInstructions) { value.Selection = "other" }},
		{name: "duplicate track", mutate: func(value *AudioAnalysisInstructions) { value.TrackIDs = []string{"track-1", "track-1"} }},
		{name: "duplicate variant", mutate: func(value *AudioAnalysisInstructions) {
			value.Variants = []AudioAnalysisVariant{AudioAnalysisWaveform, AudioAnalysisWaveform}
		}},
		{name: "unsupported profile", mutate: func(value *AudioAnalysisInstructions) { value.ProfileVersion = "audio-analysis-v1" }},
		{name: "excessive decoder threads", mutate: func(value *AudioAnalysisInstructions) {
			value.ResourceLimits.DecoderThreads = AudioAnalysisMaxDecoderThreads + 1
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := normalized
			test.mutate(&value)
			if _, err := value.Normalize(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestAudioAnalysisResultValidateAcceptsPartialAndRejectsInvalidTerminalShape(t *testing.T) {
	result := validAudioAnalysisResultForTest()
	result.Status = StageStatusPartial
	result.Tracks[0].Status = StageStatusPartial
	failure := AudioAnalysisFailure{Code: AudioAnalysisFailureOutput, Message: "could not publish analysis image"}
	result.Tracks[0].Artifacts[1] = AudioAnalysisArtifact{
		Variant: AudioAnalysisSpectrogram,
		Status:  StageStatusFailed,
		Failure: &failure,
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("validate partial result: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*AudioAnalysisResult)
		want   string
	}{
		{name: "duplicate track IDs", mutate: func(value *AudioAnalysisResult) {
			value.TrackIDs = []string{"track-1", "track-1"}
			value.Tracks = append(value.Tracks, value.Tracks[0])
		}},
		{name: "unknown failure code", mutate: func(value *AudioAnalysisResult) {
			value.Tracks[0].Artifacts[1].Failure = &AudioAnalysisFailure{Code: "mystery", Message: "safe"}
		}},
		{name: "empty failure message", mutate: func(value *AudioAnalysisResult) {
			value.Tracks[0].Artifacts[1].Failure = &AudioAnalysisFailure{Code: AudioAnalysisFailureOutput}
		}},
		{name: "completed track contains failure", mutate: func(value *AudioAnalysisResult) { value.Tracks[0].Status = StageStatusCompleted }},
		{
			name: "unknown artifact status",
			mutate: func(value *AudioAnalysisResult) {
				value.Tracks[0].Artifacts[0].Status = "mystery"
			},
			want: "artifact has unknown status",
		},
		{name: "unknown track status", mutate: func(value *AudioAnalysisResult) { value.Tracks[0].Status = "mystery" }},
		{name: "unknown result status", mutate: func(value *AudioAnalysisResult) { value.Status = "mystery" }},
		{name: "completion before creation", mutate: func(value *AudioAnalysisResult) {
			completed := value.CreatedAt.Add(-time.Second)
			value.CompletedAt = &completed
		}},
		{name: "duration uses interleaved values", mutate: func(value *AudioAnalysisResult) { value.Tracks[0].Duration *= 2 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := result
			value.TrackIDs = append([]string(nil), result.TrackIDs...)
			value.Tracks = append([]AudioAnalysisTrackResult(nil), result.Tracks...)
			value.Tracks[0].Artifacts = append([]AudioAnalysisArtifact(nil), result.Tracks[0].Artifacts...)
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("expected validation error")
			} else if test.want != "" && !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %q, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestAudioAnalysisResultValidatesStatisticsArtifact(t *testing.T) {
	result := validAudioAnalysisResultForTest()
	result.Variants = []AudioAnalysisVariant{AudioAnalysisStats}
	result.Tracks[0].Artifacts = []AudioAnalysisArtifact{{
		ID: "stats-1",
 Variant: AudioAnalysisStats,
 Status: StageStatusCompleted,
 Text: "DC offset   0.000000\n",
	}}
	if err := result.Validate(); err != nil {
		t.Fatalf("validate statistics result: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*AudioAnalysisArtifact)
	}{
		{name: "empty report", mutate: func(a *AudioAnalysisArtifact) { a.Text = "\n" }},
		{name: "oversized report", mutate: func(a *AudioAnalysisArtifact) { a.Text = strings.Repeat("x", AudioAnalysisStatsMaxBytes+1) }},
		{name: "image dimensions", mutate: func(a *AudioAnalysisArtifact) { a.Width, a.Height = 10, 10 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := result
			value.Tracks = append([]AudioAnalysisTrackResult(nil), result.Tracks...)
			value.Tracks[0].Artifacts = append([]AudioAnalysisArtifact(nil), result.Tracks[0].Artifacts...)
			test.mutate(&value.Tracks[0].Artifacts[0])
			if err := value.Validate(); err == nil {
				t.Fatal("expected invalid statistics artifact")
			}
		})
	}
	imageResult := validAudioAnalysisResultForTest()
	imageResult.Tracks[0].Artifacts[0].Text = "unexpected statistics"
	if err := imageResult.Validate(); err == nil {
		t.Fatal("expected image to reject statistics text")
	}
}

func TestAudioAnalysisResultContainsNoPrivatePathFields(t *testing.T) {
	result := validAudioAnalysisResultForTest()
	encodedBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.ToLower(string(encodedBytes))
	for _, forbidden := range []string{"localpath", "filepath", "videopath", "privatepath"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("serialized result contains private path field %q: %s", forbidden, encoded)
		}
	}
}

func validAudioAnalysisResultForTest() AudioAnalysisResult {
	created := time.Date(2026, time.September, 21, 1, 2, 3, 0, time.UTC)
	completed := created.Add(time.Second)
	return AudioAnalysisResult{
		ID:                  "analysis-1",
		WorkflowID:          "workflow-1",
		Revision:            3,
		Release:             ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 2},
		ResourceID:          "resource-1",
		ManifestFingerprint: "manifest-1",
		AttemptID:           "attempt-1",
		Selection:           AudioAnalysisSelectionPrimary,
		TrackIDs:            []string{"track-1"},
		Variants:            []AudioAnalysisVariant{AudioAnalysisWaveform, AudioAnalysisSpectrogram},
		ProfileVersion:      AudioAnalysisProfileVersion,
		Status:              StageStatusCompleted,
		Tracks: []AudioAnalysisTrackResult{{
			TrackID:      "track-1",
			Ordinal:      1,
			Channels:     2,
			SampleRate:   48_000,
			SampleFrames: 96_000,
			Duration:     2,
			Status:       StageStatusCompleted,
			Artifacts: []AudioAnalysisArtifact{
				{
					ID:      "waveform-1",
					Variant: AudioAnalysisWaveform,
					Status:  StageStatusCompleted,
					Width:   1812,
					Height:  340,
				},
				{
					ID:      "spectrogram-1",
					Variant: AudioAnalysisSpectrogram,
					Status:  StageStatusCompleted,
					Width:   3141,
					Height:  1105,
				},
			},
		}},
		CreatedAt:   created,
		CompletedAt: &completed,
		ExpiresAt:   created.Add(24 * time.Hour),
	}
}
