// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

// AudioMeasurementIdentity excludes picture options, native checkpoint IDs and
// primary/all membership while binding the original track, decoder and DSP.
func AudioMeasurementIdentity(report *MeasurementReport) (string, error) {
	if report == nil || report.AudioAuthority == nil {
		return "", errors.New("audio measurement lacks prepared authority")
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Authority HDRAudioAuthority
		Decoder   string
		Profile   string
	}{*report.AudioAuthority, report.Decoder.Fingerprint, api.AudioAnalysisProfileVersion})
	return string(fingerprint), err
}

func preparedAudioReport(input DecodedInput, options api.HDRDecodedOptions, authority HDRAudioAuthority, decoder DecoderIdentity,
	track audioanalysis.TrackResult,
) (*MeasurementReport, error) {
	report := newMeasurementReport(api.HDRDecodedAudio, input, options)
	decoder.RequestedThreads, decoder.EffectiveThreads = 0, nil
	report.Schema = "hdr-measurement-v2"
	report.AudioAuthority, report.Decoder = &authority, decoder
	report.Result.Selected, report.Result.Coverage = 1, api.HDRCoverageUnknown
	if err := captureAutomaticAudio(report, []audioanalysis.TrackResult{track}); err != nil {
		return nil, err
	}
	if report.Result.Measured == 1 {
		report.Result.Coverage = api.HDRCoveragePartial
		if report.Result.State == api.HDRDecodedComplete && len(track.Artifacts) == 3 {
			report.Result.Coverage = api.HDRCoverageComplete
		} else {
			report.Result.State = api.HDRDecodedInconclusive
		}
	}
	if err := report.Validate(); err != nil {
		return nil, fmt.Errorf("validate prepared audio measurement: %w", err)
	}
	return report, nil
}
