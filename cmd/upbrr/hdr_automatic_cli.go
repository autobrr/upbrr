// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func standaloneAutomaticHDR(ctx context.Context, session *hdranalysis.Session, opts cliOptions, source, fingerprint, directory string,
	formats api.HDRAnalysisFormats,
	prepared *hdranalysis.PreparedAudioCapture,
) (*hdranalysis.NativeExtraction, []*hdranalysis.MeasurementReport, error) {
	inspect := func(path string) (hdranalysis.DecodedInput, error) {
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		identity, err := releaseworkflow.VerifyHDRComparisonSource(probeCtx, path, verifyStandaloneHDRInput)
		if err != nil {
			return hdranalysis.DecodedInput{}, fmt.Errorf("verify automatic HDR input: %w", err)
		}
		facts, err := hdranalysis.InspectFileSource(probeCtx, identity.CanonicalPath)
		if err != nil {
			return hdranalysis.DecodedInput{}, fmt.Errorf("probe automatic HDR input: %w", err)
		}
		if !facts.UniqueHEVC {
			return hdranalysis.DecodedInput{}, errors.New("automatic HDR analysis requires a unique HEVC video stream")
		}
		input := hdranalysis.DecodedInput{Path: identity.CanonicalPath, Fingerprint: identity.SourceVersion}
		if facts.Decoded != nil {
			input.Source = *facts.Decoded
		}
		return input, nil
	}
	input, err := inspect(source)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect automatic HDR input: %w", err)
	}
	if input.Fingerprint != fingerprint {
		return nil, nil, api.ErrActiveInputChanged
	}
	options, err := cliHDRDecoded(opts, "standalone-b")
	if err != nil {
		return nil, nil, err
	}
	if options == nil {
		return nil, nil, errors.New("standalone automatic HDR requires analysis options")
	}
	var reference *hdranalysis.DecodedInput
	var referenceErr error
	if options.Comparison != nil {
		value, err := inspect(opts.HDRCompareSource)
		referenceErr = err
		if err == nil {
			reference = &value
		}
	}
	var native *hdranalysis.NativeExtraction
	var reports []*hdranalysis.MeasurementReport
	if prepared != nil {
		native, reports, err = session.AnalyzePreparedAutomatic(ctx, input, reference, *options, formats, directory, 0, *prepared)
	} else {
		native, reports, err = session.AnalyzeAutomatic(ctx, input, reference, *options, formats, directory, 0)
	}
	if err != nil {
		return native, reports, fmt.Errorf("measure standalone automatic HDR: %w", err)
	}
	for _, selected := range []*hdranalysis.DecodedInput{&input, reference} {
		if selected == nil {
			continue
		}
		verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		current, err := releaseworkflow.VerifyHDRComparisonSource(verifyCtx, selected.Path, verifyStandaloneHDRInput)
		cancel()
		if err != nil || current.SourceVersion != selected.Fingerprint {
			if selected == &input || prepared != nil {
				native.Release()
				return nil, nil, api.ErrActiveInputChanged
			}
			for _, report := range reports {
				report.OmitAutomaticReference()
			}
		}
	}
	if referenceErr != nil {
		for _, report := range reports {
			if report.Result.Mode == api.HDRDecodedAutomatic {
				report.Result.Checks = append(report.Result.Checks, api.HDRCheckResult{
					Code:    "reference_unavailable",
					State:   api.HDRCheckUnsupported,
					Message: "The optional HDR10 reference is unavailable; supported primary-source checks were run.",
				})
			}
		}
	}
	return native, reports, nil
}

func publishStandaloneAutomaticHDR(ctx context.Context, session *hdranalysis.Session, directory string, reports []*hdranalysis.MeasurementReport,
	target *api.HDRAnalysisTargetResult,
) error {
	for _, report := range reports {
		path := filepath.Join(directory, string(report.Result.Mode)+".json")
		if err := hdranalysis.PublishFile(ctx, path, func(out io.Writer) error { return hdranalysis.WriteMeasurement(ctx, out, report) }); err != nil {
			return fmt.Errorf("publish automatic HDR checkpoint: %w", err)
		}
		result := report.Result
		for _, artifact := range hdranalysis.MeasurementArtifacts(report) {
			path := filepath.Join(directory, artifact.Filename())
			if err := hdranalysis.PublishFile(ctx, path, func(out io.Writer) error {
				return session.RenderMeasurementArtifact(ctx, report, artifact.Kind, "HDR analysis", out)
			}); err != nil {
				result.Reason = "render_unavailable"
			}
		}
		target.Decoded = append(target.Decoded, result)
	}
	return nil
}
