// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"encoding/json/v2"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPartialAutomaticAudioRetainsDescriptionStatisticsWithoutNativeMetadata(t *testing.T) {
	report := newMeasurementReport(api.HDRDecodedAudio, DecodedInput{Fingerprint: "synthetic-audio"}, api.HDRDecodedOptions{Audio: true})
	stats := api.AudioAnalysisArtifact{
		ID:      "audio-stats",
		Variant: api.AudioAnalysisStats,
		Status:  api.StageStatusCompleted,
		Text:    "Pk lev dB: -6.02",
	}
	// publishTrack retains successful variants after variant-local publication failures.
	track := audioanalysis.TrackResult{
		Public: api.AudioAnalysisTrackResult{
			TrackID:      "primary-audio",
			Status:       api.StageStatusPartial,
			Channels:     1,
			SampleRate:   48000,
			SampleFrames: 1,
			Duration:     1.0 / 48000,
			Artifacts: []api.AudioAnalysisArtifact{
				{Variant: api.AudioAnalysisWaveform, Status: api.StageStatusFailed},
				{Variant: api.AudioAnalysisSpectrogram, Status: api.StageStatusFailed},
				stats,
			},
		},
		Artifacts: []audioanalysis.Artifact{{Public: stats}},
	}
	if err := captureAutomaticAudio(report, []audioanalysis.TrackResult{track}); err != nil {
		t.Fatal(err)
	}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	artifacts := MeasurementArtifacts(report)
	if len(artifacts) != 1 || artifacts[0].Kind != api.HDRArtifactAudioStats || report.Result.State != api.HDRDecodedInconclusive {
		t.Fatalf("retained partial audio: %+v artifacts=%+v", report.Result, artifacts)
	}
	artifacts[0].ID = "hdr-stats"
	report.Result.Artifacts = []api.PublicResourceID{artifacts[0].ID}
	target := api.HDRAnalysisTargetResult{Artifacts: artifacts, Decoded: []api.HDRDecodedResult{report.Result}}
	target.Status = target.AnalysisStatus()
	result := api.HDRAnalysisResult{
		ProfileVersion: api.HDRAnalysisProfileV2,
		Targets:        []api.HDRAnalysisTargetResult{target},
		Status:         api.NativeHDRAggregate([]api.HDRAnalysisTargetResult{target}),
	}
	if result.Status != api.StageStatusPartial || !result.CanIncludeDefaultImages() || len(target.SelectableImages()) != 0 {
		t.Fatalf("retained statistics lost description eligibility: %+v", result)
	}
	var output bytes.Buffer
	if err := (&Session{}).RenderMeasurementArtifact(t.Context(), report, api.HDRArtifactAudioStats, "Synthetic audio", &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != stats.Text {
		t.Fatalf("saved statistics changed: %q", output.String())
	}
}

func automaticReportFixture() *MeasurementReport {
	report := newMeasurementReport(api.HDRDecodedAutomatic,
		DecodedInput{Fingerprint: "synthetic-picture", Source: api.HDRDecodedSourceEvidence{Width: 192, Height: 112}}, api.HDRDecodedOptions{Automatic: true})
	report.Result.Profile, report.Result.State = AutomaticMeasurementProfile, api.HDRDecodedComplete
	report.Automatic = &AutomaticMeasurement{Frames: []AutomaticFrame{
		{
			PTS:        measurementTimestamp(pictureTime{Timescale: 25, Valid: true}),
			MaxRGBNits: 100,
			FALLNits:   50,
		},
		{
			PTS: measurementTimestamp(pictureTime{
				Value:     1,
				Timescale: 25,
				Valid:     true,
			}),
			MaxRGBNits: 100,
			FALLNits:   50,
		},
	}}
	report.Result.Selected, report.Result.Measured = 2, 2
	return report
}

func TestAutomaticReportRejectsUnverifiableEvidence(t *testing.T) {
	mutations := map[string]func(*MeasurementReport){
		"zero clock":         func(r *MeasurementReport) { r.Automatic.Frames[0].PTS.Timescale = "0" },
		"duplicate time":     func(r *MeasurementReport) { r.Automatic.Frames[1].PTS = r.Automatic.Frames[0].PTS },
		"impossible borders": func(r *MeasurementReport) { r.Automatic.Frames[0].Borders[0] = 192 },
		"orphan reference":   func(r *MeasurementReport) { r.Automatic.Reference = r.Automatic.Frames },
		"invalid scene": func(r *MeasurementReport) {
			r.Automatic.Scenes = []AutomaticScene{{Format: api.HDRNativeDolbyVision,
				Observations: []SceneObservation{{
					Candidate:  1,
					Noise:      math.NaN(),
					Confidence: "dominant_cut",
				}}}}
		},
		"invalid interval": func(r *MeasurementReport) {
			r.Automatic.Areas = []AreaObservation{{
				Start:      0,
				End:        3,
				Confidence: "unclear",
			}}
		},
		"orphan colour": func(r *MeasurementReport) {
			r.Automatic.Chromaticity[1] = []ChromaticityPoint{{
				Index: 1,
				X:     0.3,
				Y:     0.3,
			}}
		},
	}
	if err := automaticReportFixture().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			report := automaticReportFixture()
			mutate(report)
			if err := report.Validate(); err == nil {
				t.Fatal("accepted unverifiable evidence")
			}
		})
	}
	report := automaticReportFixture()
	report.Automatic.Areas = make([]AreaObservation, 121)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMeasurement(t.Context(), bytes.NewReader(encoded)); err == nil || !strings.Contains(err.Error(), "observation bounds") {
		t.Fatalf("typed allocation preceded array limit: %v", err)
	}
}

func TestAutomaticReferenceOmissionPreservesPrimary(t *testing.T) {
	report := automaticReportFixture()
	report.Options.Comparison = &api.HDRComparisonOptions{
		SourceID:    "synthetic-reference",
		FrameOffset: new(0),
		Crop:        api.HDRCrop{Width: 2, Height: 2},
		Grade:       true,
	}
	report.Automatic.Profile5 = true
	report.Automatic.Reference = slices.Clone(report.Automatic.Frames)
	report.Sources[1] = "synthetic-reference"
	report.Automatic.Chromaticity[1] = []ChromaticityPoint{{X: 0.3, Y: 0.3}}
	report.Result.Checks = []api.HDRCheckResult{{
		Code:    "reference_alignment",
		State:   api.HDRCheckPass,
		Message: "Paired reference evidence is available.",
	}}
	report.OmitAutomaticReference()
	report.OmitAutomaticReference()
	if len(report.Automatic.Frames) != 2 || len(report.Automatic.Reference) != 0 || len(report.Automatic.Chromaticity[1]) != 0 || report.Sources[1] != "" ||
		report.Result.State != api.HDRDecodedComplete || !slices.ContainsFunc(report.Result.Checks, func(c api.HDRCheckResult) bool { return c.Code == "optional_hdr10_reference" }) {
		t.Fatal("reference failure discarded primary checks or their optional-reference note")
	}
	changed := 0
	for _, check := range report.Result.Checks {
		if check.Code == "reference_alignment" {
			t.Fatal("omitted reference still claims paired evidence is available")
		}
		if check.Code == "reference_changed" {
			changed++
		}
	}
	if changed != 1 || report.Options.Comparison.SourceID != "synthetic-reference" || report.Options.Comparison.Crop.Width != 2 {
		t.Fatal("reference omission duplicated its note or changed checkpoint identity")
	}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticCropClaimsFollowMeasuredRegion(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		report := automaticReportFixture()
		report.Options.Comparison = &api.HDRComparisonOptions{
			SourceID:    "synthetic-reference",
			FrameOffset: new(0),
			Crop:        api.HDRCrop{Width: 2, Height: 2},
			Grade:       true,
		}
		pictures := &automaticPictures{}
		if explicit {
			pictures.crop = &report.Options.Comparison.Crop
		}
		if err := finishAutomaticReport(t.Context(), report, &NativeExtraction{}, pictures); err != nil {
			t.Fatal(err)
		}
		coverage, commonCrop := false, false
		for _, check := range report.Result.Checks {
			coverage = coverage || check.Code == "active_picture_coverage"
			commonCrop = commonCrop || check.Code == "complete_picture_measurement" && strings.Contains(check.Message, "explicit common crop")
		}
		if commonCrop != explicit || coverage == explicit {
			t.Fatalf("processing claims used requested rather than applied crop: explicit=%t commonCrop=%t coverage=%t", explicit, commonCrop, coverage)
		}
	}
}
