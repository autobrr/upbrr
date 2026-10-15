// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"testing"

	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedAudioAggregatePreservesCancellation(t *testing.T) {
	for _, terminal := range []api.HDRDecodedState{api.HDRDecodedCanceled, api.HDRDecodedInterrupted} {
		for _, terminalFirst := range []bool{false, true} {
			outcome := api.HDRAnalysisTargetResult{}
			for i := range 2 {
				state := api.HDRDecodedFailed
				if (i == 0) == terminalFirst {
					state = terminal
				}
				report := &MeasurementReport{AudioAuthority: &HDRAudioAuthority{TrackID: []string{"first", "second"}[i], Ordinal: i + 1}}
				AppendAudioOutcome(&outcome, report, api.HDRDecodedResult{State: state})
			}
			if len(outcome.Decoded) != 1 || outcome.Decoded[0].State != terminal {
				t.Fatalf("lost terminal state: %+v", outcome.Decoded)
			}
			if err := outcome.Decoded[0].Validate(); err != nil {
				t.Fatal(err)
			}
			want := api.StageStatusCanceled
			if terminal == api.HDRDecodedInterrupted {
				want = api.StageStatusInterrupted
			}
			if got := outcome.AnalysisStatus(); got != want {
				t.Fatalf("status=%s, want %s", got, want)
			}
		}
	}
}

func TestPreparedAudioFailureRetainsTrackEvidence(t *testing.T) {
	for _, status := range []api.StageStatus{api.StageStatusFailed, api.StageStatusCanceled, api.StageStatusInterrupted} {
		failure := &api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureDecode, Message: "Synthetic failure."}
		want := api.HDRDecodedFailed
		if status == api.StageStatusCanceled {
			failure.Code, want = api.AudioAnalysisFailureCanceled, api.HDRDecodedCanceled
		}
		if status == api.StageStatusInterrupted {
			failure.Code, want = api.AudioAnalysisFailureInterrupted, api.HDRDecodedInterrupted
		}
		authority := HDRAudioAuthority{
			SourceFingerprint:   "source",
			ResourceID:          "resource",
			ManifestFingerprint: "manifest",
			TrackID:             "track",
			Ordinal:             1,
			Codec:               "aac",
			Channels:            2,
			ChannelLayout:       "stereo",
			SampleRate:          48000,
		}
		report, err := preparedAudioReport(DecodedInput{Fingerprint: "source"}, api.HDRDecodedOptions{Audio: true, AllAudioTracks: true}, authority, DecoderIdentity{},
			audioanalysis.TrackResult{Public: api.AudioAnalysisTrackResult{
				TrackID:       "track",
				Ordinal:       1,
				Codec:         "aac",
				Channels:      2,
				ChannelLayout: "stereo",
				SampleRate:    48000,
				Language:      "eng",
				Status:        status,
				Failure:       failure,
			}})
		if err != nil {
			t.Fatal(err)
		}
		var outcome api.HDRAnalysisTargetResult
		AppendAudioOutcome(&outcome, report, report.Result)
		track := outcome.Decoded[0].AudioTracks[0]
		if outcome.Decoded[0].State != want || track.Language != "eng" || track.Failure == nil || track.Failure.Code != failure.Code {
			t.Fatalf("lost track failure: %+v", outcome.Decoded[0])
		}
		report.Audio.Stats = "unowned measurements"
		if report.Validate() == nil {
			t.Fatal("unmeasured failure accepted amplitude statistics")
		}
	}
}
