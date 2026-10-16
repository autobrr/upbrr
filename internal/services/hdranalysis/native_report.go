// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"strconv"

	dovi "github.com/Audionut/go-dovi-extract"

	"github.com/autobrr/upbrr/pkg/api"
)

const MaxReportBytes = MaxAutomaticMeasurementBytes

// nativeReportDVSummary preserves native observations while exporting the track
// identity losslessly to JSON consumers with floating-point numbers.
type nativeReportDVSummary struct {
	dovi.Summary
	TrackID string `json:"track_id"`
}

// WriteReport exports bounded derived observations, without private paths or raw
// extraction records. Refresh markers and L1 statistics describe metadata only.
func (e *NativeEvidence) WriteReport(ctx context.Context, out io.Writer, peak api.HDRPeakSource, playlist string,
	target api.HDRAnalysisTargetResult, measurements []*MeasurementReport,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write HDR report: %w", err)
	}
	summary := e.reportSummary
	profile := api.HDRAnalysisProfileV2
	for _, decoded := range target.Decoded {
		if decoded.Coverage != "" {
			profile = api.HDRAnalysisProfileV3
		}
	}
	var included []*MeasurementReport
	for _, measurement := range measurements {
		if err := measurement.Validate(); err != nil {
			return err
		}
		if measurement.Schema == "hdr-measurement-v2" {
			profile = api.HDRAnalysisProfileV3
			if measurement.Result.Mode == api.HDRDecodedAudio {
				continue
			}
		}
		included = append(included, measurement)
	}
	for _, artifact := range target.Artifacts {
		if artifact.Profile == api.HDRAnalysisProfileV3 {
			profile = api.HDRAnalysisProfileV3
		}
	}
	if e.DolbyVision != nil {
		value, err := e.summarizeDV()
		if err != nil {
			return err
		}
		summary = &nativeReportDVSummary{Summary: value, TrackID: strconv.FormatUint(value.TrackID, 10)}
	}
	report := struct {
		Profile      string                 `json:"profile"`
		Status       api.StageStatus        `json:"status"`
		Formats      []api.HDRFormatResult  `json:"formats"`
		Decoded      []api.HDRDecodedResult `json:"decoded,omitempty"`
		PeakSource   api.HDRPeakSource      `json:"peakSource"`
		Playlist     string                 `json:"playlist,omitempty"`
		DolbyVision  *nativeReportDVSummary `json:"dolbyVision,omitempty"`
		Measurements []*MeasurementReport   `json:"measurements,omitempty"`
	}{profile, target.AnalysisStatus(), target.Formats, target.Decoded, peak, playlist, summary, included}
	if err := json.MarshalWrite(&boundedWriter{output: out, remaining: MaxReportBytes}, report); err != nil {
		return fmt.Errorf("encode bounded HDR report: %w", err)
	}
	return nil
}
