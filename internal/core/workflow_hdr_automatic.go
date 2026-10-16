// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func automaticHDRCaptureModes(ctx context.Context, instructions api.HDRAnalysisInstructions, native releaseworkflow.HDRExtractionRecord,
	store *releaseworkflow.HDRMeasurementStore,
) []api.HDRDecodedMode {
	if instructions.Decoded == nil || !instructions.Decoded.Automatic && !instructions.Decoded.Audio {
		return nil
	}
	var modes []api.HDRDecodedMode
	var decoder string
	var decoderErr error
	decoderChecked := false
	for _, mode := range []api.HDRDecodedMode{api.HDRDecodedAutomatic, api.HDRDecodedAudio} {
		if mode == api.HDRDecodedAudio && instructions.ProfileVersion == api.HDRAnalysisProfileV3 {
			continue // Prepared tracks have independent, audio-only recovery below.
		}
		if !instructions.Decoded.Requested(mode) {
			continue
		}
		identity, err := hdrMeasurementIdentity(native, *instructions.Decoded, mode)
		if err != nil {
			modes = append(modes, mode)
			continue
		}
		previous := latestHDRMeasurement(store, instructions.Release, native.TargetID, identity)
		if previous.ID == "" {
			modes = append(modes, mode)
			continue
		}
		if previous.Decoder != "" {
			if !decoderChecked {
				decoder, decoderErr = hdranalysis.CurrentDecoderFingerprint(ctx)
				decoderChecked = true
			}
			if decoderErr == nil && decoder != previous.Decoder {
				modes = append(modes, mode)
				continue
			}
		}
		if retry := instructions.DecodedRetry; retry != nil {
			for _, requested := range retry.Modes {
				if requested == mode && (store == nil || store.RetryAfterSequence == nil || previous.Sequence <= *store.RetryAfterSequence) {
					modes = append(modes, mode)
					break
				}
			}
		}
	}
	return modes
}

func (b workflowHDRAnalysisBuilder) captureAutomaticHDR(ctx context.Context, session *hdranalysis.Session,
	subject api.HDRAnalysisSubject, target api.HDRAnalysisTargetSubject, instructions api.HDRAnalysisInstructions,
	store *releaseworkflow.HDRMeasurementStore, directory string, otherBytes int64,
	prepared *hdranalysis.PreparedAudioCapture, modes []api.HDRDecodedMode,
) (*hdranalysis.NativeExtraction, []*hdranalysis.MeasurementReport, error) {
	options := *instructions.Decoded
	captureAudio := modes == nil || slices.Contains(modes, api.HDRDecodedAudio)
	if modes != nil && !slices.Contains(modes, api.HDRDecodedAutomatic) {
		options = api.HDRDecodedOptions{Audio: true, AllAudioTracks: options.AllAudioTracks}
	}
	input := hdranalysis.DecodedInput{Path: target.Path, Fingerprint: subject.SourceFingerprint}
	if target.Target.Evidence.Decoded != nil {
		input.Source = *target.Target.Evidence.Decoded
	}
	var reference *hdranalysis.DecodedInput
	var referenceErr error
	if options.Comparison != nil {
		if store == nil || store.Comparison == nil || store.Comparison.Ref.Release != subject.Release ||
			store.Comparison.Ref.ID != instructions.Decoded.Comparison.SourceID {
			referenceErr = releaseworkflow.ErrPrivateResourceUnavailable
		} else if referenceErr = verifyHDRComparison(ctx, *store.Comparison); referenceErr == nil {
			reference = &hdranalysis.DecodedInput{Path: store.Comparison.Path, Fingerprint: store.Comparison.Ref.Fingerprint}
			if store.Comparison.Evidence.Decoded != nil {
				reference.Source = *store.Comparison.Evidence.Decoded
			}
		}
	}
	var native *hdranalysis.NativeExtraction
	var reports []*hdranalysis.MeasurementReport
	var err error
	if prepared != nil {
		native, reports, err = session.AnalyzePreparedAutomatic(
			ctx,
			input,
			reference,
			options,
			instructions.Formats,
			directory,
			otherBytes,
			*prepared,
		)
	} else {
		native, reports, err = session.AnalyzeAutomatic(ctx, input, reference, options, instructions.Formats, directory, otherBytes, captureAudio)
	}
	if err != nil {
		return native, reports, fmt.Errorf("capture automatic HDR: %w", err)
	}
	if reference != nil {
		if err := verifyHDRComparison(ctx, *store.Comparison); err != nil {
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
					Message: "The optional HDR10 reference is unavailable; all supported primary-source checks were run.",
				})
			}
		}
	}
	return native, reports, nil
}

// A mode-wide retry grants fresh work only for unsuccessful target outcomes.
// Successful siblings keep their retained measurements and render independently.
func hdrTargetDecodedRetry(request *api.HDRDecodedRetry, prior *api.HDRAnalysisResult, targetID string) *api.HDRDecodedRetry {
	if request == nil || prior == nil {
		return nil
	}
	retry := &api.HDRDecodedRetry{AnalysisAttemptID: request.AnalysisAttemptID}
	for _, target := range prior.Targets {
		if target.TargetID != targetID {
			continue
		}
		for _, mode := range request.Modes {
			if slices.ContainsFunc(target.Decoded, func(result api.HDRDecodedResult) bool {
				return result.Mode == mode &&
					slices.Contains(
						[]api.HDRDecodedState{
							api.HDRDecodedFailed,
							api.HDRDecodedCanceled,
							api.HDRDecodedInterrupted,
							api.HDRDecodedUnsupported,
							api.HDRDecodedInconclusive,
						},
						result.State,
					)
			}) {
				retry.Modes = append(retry.Modes, mode)
			}
		}
	}
	if len(retry.Modes) == 0 {
		return nil
	}
	return retry
}
