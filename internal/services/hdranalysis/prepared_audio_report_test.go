// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"testing"

	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedAudioPreservesPCMWhenEveryVariantFails(t *testing.T) {
	input := DecodedInput{Fingerprint: "source"}
	authority := HDRAudioAuthority{
		SourceFingerprint:   "source",
		ResourceID:          "resource",
		ManifestFingerprint: "manifest",
		TrackID:             "prepared-track",
		Ordinal:             1,
		Codec:               "eac3",
		ChannelLayout:       "mono",
		SampleRate:          48000,
		Channels:            1,
		DRCDisabled:         true,
	}
	track := audioanalysis.TrackResult{PCMComplete: true, Public: api.AudioAnalysisTrackResult{
		TrackID:       authority.TrackID,
		Ordinal:       1,
		Codec:         authority.Codec,
		ChannelLayout: "mono",
		SampleRate:    48000,
		Channels:      1,
		SampleFrames:  48000,
		Duration:      1,
		Status:        api.StageStatusFailed,
		Artifacts: []api.AudioAnalysisArtifact{{Variant: api.AudioAnalysisWaveform, Status: api.StageStatusFailed},
			{Variant: api.AudioAnalysisSpectrogram, Status: api.StageStatusFailed}, {Variant: api.AudioAnalysisStats, Status: api.StageStatusFailed}},
	}}
	report, err := preparedAudioReport(input, api.HDRDecodedOptions{Audio: true, AllAudioTracks: true}, authority, DecoderIdentity{}, track)
	if err != nil {
		t.Fatal(err)
	}
	if report.Result.Measured != 1 || report.Result.State != api.HDRDecodedInconclusive || report.Result.Coverage != api.HDRCoveragePartial || len(MeasurementArtifacts(report)) != 0 {
		t.Fatalf("publication failure discarded PCM completion: %+v", report.Result)
	}
	track.PCMComplete = false
	report, err = preparedAudioReport(input, api.HDRDecodedOptions{Audio: true, AllAudioTracks: true}, authority, DecoderIdentity{}, track)
	if err != nil || report.Result.Measured != 0 || report.Audio == nil || report.Result.State != api.HDRDecodedFailed {
		t.Fatalf("failed decode acquired PCM authority: %+v %v", report, err)
	}
	if report.Audio.Track.SampleFrames != 0 || report.Audio.Track.Duration != 0 || len(report.Audio.Track.Artifacts) != 0 {
		t.Fatalf("failed decode retained unproven PCM metadata: %+v", report.Audio.Track)
	}
}
