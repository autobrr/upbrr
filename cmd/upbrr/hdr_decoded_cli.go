// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func cliHDRComparisonSelection(opts cliOptions) *api.HDRComparisonSelection {
	if opts.HDRCompareSource == "" {
		return nil
	}
	return &api.HDRComparisonSelection{Path: opts.HDRCompareSource}
}

func verifyStandaloneHDRInput(ctx context.Context, input api.PrepareInput) (api.InputRecord, error) {
	verified, err := preparedrelease.VerifyInputSource(ctx, input)
	if err != nil {
		return api.InputRecord{}, fmt.Errorf("verify standalone HDR input: %w", err)
	}
	payload, err := json.Marshal(verified)
	if err != nil {
		return api.InputRecord{}, fmt.Errorf("encode standalone HDR identity: %w", err)
	}
	return api.InputRecord{
		CanonicalPath: preparedrelease.CanonicalSourceKey(verified.Manifest.SourcePath),
		SourceVersion: verified.Identity.Digest,
		Manifest:      payload,
	}, nil
}

func standaloneHDRMeasurements(ctx context.Context, session *hdranalysis.Session, opts cliOptions, source, directory, primaryFingerprint string,
	evidence *hdranalysis.NativeEvidence, target *api.HDRAnalysisTargetResult,
) ([]*hdranalysis.MeasurementReport, error) {
	options, err := cliHDRDecoded(opts, "standalone-b")
	if err != nil {
		return nil, err
	}
	if options == nil {
		return nil, nil
	}
	if (options.Automatic || options.Audio) && len(evidence.Timeline) > 0 {
		for _, mode := range []api.HDRDecodedMode{api.HDRDecodedAutomatic, api.HDRDecodedAudio} {
			if !options.Requested(mode) {
				continue
			}
			target.Decoded = append(target.Decoded, api.HDRDecodedResult{
				Mode:   mode,
				State:  api.HDRDecodedUnsupported,
				Reason: "unsupported_playlist",
			})
		}
		return nil, nil
	}
	defer func() {
		for _, mode := range []api.HDRDecodedMode{api.HDRDecodedSceneSync, api.HDRDecodedActiveArea, api.HDRDecodedGrade, api.HDRDecodedColour} {
			if !options.Requested(mode) {
				continue
			}
			found := false
			for _, result := range target.Decoded {
				found = found || result.Mode == mode
			}
			if !found {
				state, reason := api.HDRDecodedFailed, "measurement_unavailable"
				if ctx.Err() != nil {
					state, reason = api.HDRDecodedCanceled, "canceled"
				}
				target.Decoded = append(target.Decoded, api.HDRDecodedResult{
					Mode:   mode,
					State:  state,
					Reason: reason,
				})
			}
		}
	}()
	// Probe reports are private, short-lived metadata, separate from shareable output.
	probeRoot, err := os.MkdirTemp("", "upbrr-hdr-probe-")
	if err != nil {
		return nil, fmt.Errorf("create HDR probe directory: %w", err)
	}
	defer os.RemoveAll(probeRoot)
	var identities []api.InputRecord
	inspect := func(path string) (hdranalysis.DecodedInput, error) {
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		record, err := releaseworkflow.VerifyHDRComparisonSource(probeCtx, path, verifyStandaloneHDRInput)
		if err != nil {
			return hdranalysis.DecodedInput{}, fmt.Errorf("standalone HDR measurement: %w", err)
		}
		facts, err := metadata.InspectHDRFile(probeCtx, mediainfo.NewService(nil, nil), record.CanonicalPath, record.SourceVersion, probeRoot)
		if err != nil {
			return hdranalysis.DecodedInput{}, fmt.Errorf("standalone HDR measurement: %w", err)
		}
		identities = append(identities, record)
		input := hdranalysis.DecodedInput{Path: record.CanonicalPath, Fingerprint: record.SourceVersion}
		if facts.Decoded != nil {
			input.Source = *facts.Decoded
		}
		return input, nil
	}
	verify := func() error {
		verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		for _, identity := range identities {
			current, err := releaseworkflow.VerifyHDRComparisonSource(verifyCtx, identity.CanonicalPath, verifyStandaloneHDRInput)
			if err != nil || current.SourceVersion != identity.SourceVersion {
				return api.ErrActiveInputChanged
			}
		}
		return nil
	}
	primary := hdranalysis.DecodedInput{
		Path:        source,
		Native:      evidence,
		Fingerprint: "unsupported-playlist",
	}
	var comparison *hdranalysis.DecodedInput
	var comparisonErr error
	measurementOptions := *options
	if len(evidence.Timeline) == 0 {
		primary, err = inspect(source)
		if err != nil {
			return nil, fmt.Errorf("standalone HDR measurement: %w", err)
		}
		if primary.Fingerprint != primaryFingerprint {
			return nil, api.ErrActiveInputChanged
		}
		primary.Native = evidence
		if options.Comparison != nil {
			primaryIdentities := len(identities)
			comparison, comparisonErr = func() (*hdranalysis.DecodedInput, error) {
				input, err := inspect(opts.HDRCompareSource)
				if err != nil {
					return nil, err
				}
				used, err := evidence.RetainedBytes()
				if err != nil {
					return nil, fmt.Errorf("bound primary HDR evidence: %w", err)
				}
				native, err := session.ExtractNativeFileWithRetained(ctx, input.Path, api.HDRAnalysisFormatsBoth, used+128<<20)
				if err != nil {
					return nil, fmt.Errorf("extract comparison HDR evidence: %w", err)
				}
				input.Native, err = native.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
				native.Release()
				if err != nil {
					return nil, fmt.Errorf("prepare comparison HDR evidence: %w", err)
				}
				return &input, nil
			}()
			if comparisonErr != nil {
				if ctx.Err() != nil {
					return nil, fmt.Errorf("prepare standalone HDR comparison: %w", comparisonErr)
				}
				identities = identities[:primaryIdentities]
				for _, mode := range []api.HDRDecodedMode{api.HDRDecodedGrade, api.HDRDecodedColour} {
					if options.Requested(mode) {
						target.Decoded = append(target.Decoded, api.HDRDecodedResult{
							Mode:   mode,
							State:  api.HDRDecodedFailed,
							Reason: "comparison_source_unavailable",
						})
					}
				}
				measurementOptions.Comparison = nil
				if !measurementOptions.SceneSync && !measurementOptions.ActiveArea {
					return nil, fmt.Errorf("prepare standalone HDR comparison: %w", comparisonErr)
				}
			} else {
				defer comparison.Native.Release()
			}
		}
		if err := verify(); err != nil {
			return nil, fmt.Errorf("standalone HDR measurement: %w", err)
		}
	}
	var reports []*hdranalysis.MeasurementReport
	err = session.MeasureDecoded(ctx, primary, comparison, measurementOptions, 0, func(report *hdranalysis.MeasurementReport) error {
		report.Options = *options
		if err := verify(); err != nil {
			return err
		}
		path := filepath.Join(directory, string(report.Result.Mode)+".json")
		if err := hdranalysis.PublishFile(ctx, path, func(out io.Writer) error { return hdranalysis.WriteMeasurement(ctx, out, report) }); err != nil {
			return fmt.Errorf("retain standalone HDR measurement: %w", err)
		}
		reports = append(reports, report)
		result := report.Result
		if result.Measured > 0 && (result.State == api.HDRDecodedComplete || result.State == api.HDRDecodedInconclusive) {
			image := filepath.Join(directory, string(result.Mode)+".png")
			if err := hdranalysis.PublishFile(ctx, image, func(out io.Writer) error {
				return session.RenderMeasurement(ctx, report, "HDR picture measurement", out)
			}); err != nil {
				result.Reason = "render_unavailable"
			}
		}
		target.Decoded = append(target.Decoded, result)
		return nil
	})

	if err = errors.Join(err, comparisonErr); err != nil {
		return reports, fmt.Errorf("measure decoded HDR pictures: %w", err)
	}
	return reports, nil
}

func cliHDRDecoded(opts cliOptions, sourceID string) (*api.HDRDecodedOptions, error) {
	comparison := opts.HDRCompareSource != "" || opts.HDRCompareOffset != "" || opts.HDRCompareCrop != "" || opts.HDRGrade || opts.HDRColour
	pictures := opts.HDRPictures || opts.HDRFullFrames
	manual := opts.HDRSceneSync || opts.HDRActiveArea || comparison
	enabled := opts.HDRAnalysis || opts.HDRAnalysisOnly
	if opts.HDRBoundarySync && (opts.HDRMetadataOnly || opts.HDRAudioOnly || pictures || opts.HDRSceneSync || comparison) {
		return nil, errors.New("--hdr-boundary-sync cannot be combined with metadata-only, audio-only, automatic picture, legacy scene or comparison options")
	}
	if opts.HDRAudioOnly && (!enabled || opts.HDRMetadataOnly || opts.HDRBoundarySync || pictures || manual) {
		return nil, errors.New("--hdr-audio-only requires HDR analysis without metadata-only, picture or comparison options")
	}
	automatic := enabled && pictures && !opts.HDRMetadataOnly && !opts.HDRBoundarySync
	if pictures && !automatic {
		return nil, errors.New("--hdr-pictures and --hdr-full-frames require HDR analysis without metadata-only or boundary options")
	}
	audio := opts.HDRAudioOnly || enabled && !opts.HDRMetadataOnly && !opts.HDRBoundarySync && !pictures && !manual ||
		opts.HDRBoundarySync && opts.HDRBoundaryAudio
	if !automatic && !audio && !manual && !opts.HDRBoundarySync &&
		!opts.HDRAllAudioTracks && opts.HDRDecoderThreads == 0 {
		return nil, nil
	}
	options := api.HDRDecodedOptions{
		Automatic:           automatic,
		Audio:               audio,
		Sampled:             automatic && !opts.HDRFullFrames,
		SceneSync:           opts.HDRSceneSync,
		BoundarySync:        opts.HDRBoundarySync,
		AllAudioTracks:      opts.HDRAllAudioTracks,
		VideoDecoderThreads: opts.HDRDecoderThreads,
		ActiveArea:          opts.HDRActiveArea,
	}
	if comparison {
		if opts.HDRCompareSource == "" || opts.HDRCompareOffset == "" || opts.HDRCompareCrop == "" || (!opts.HDRGrade && !opts.HDRColour) {
			return nil, errors.New(
				"HDR comparison requires --hdr-compare-source, explicit --hdr-compare-offset, --hdr-compare-crop and --hdr-grade or --hdr-colour",
			)
		}
		offset, err := strconv.Atoi(opts.HDRCompareOffset)
		if err != nil {
			return nil, errors.New("--hdr-compare-offset requires an integer")
		}
		parts := strings.Split(opts.HDRCompareCrop, ",")
		if len(parts) != 4 {
			return nil, errors.New("--hdr-compare-crop requires left,top,width,height")
		}
		var values [4]int
		for i, part := range parts {
			values[i], err = strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				return nil, errors.New("--hdr-compare-crop requires four integers")
			}
		}
		options.Comparison = &api.HDRComparisonOptions{
			SourceID:    sourceID,
			FrameOffset: &offset,
			Crop: api.HDRCrop{
				Left:   values[0],
				Top:    values[1],
				Width:  values[2],
				Height: values[3],
			},
			Grade:  opts.HDRGrade,
			Colour: opts.HDRColour,
		}
	}
	if options.Comparison != nil && sourceID == "" {
		if err := (api.HDRComparisonSelection{Path: opts.HDRCompareSource}).Validate(&api.HDRAnalysisRequest{Decoded: &options}); err != nil {
			return nil, fmt.Errorf("HDR comparison: %w", err)
		}
	} else if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("HDR checks: %w", err)
	}
	return &options, nil
}
