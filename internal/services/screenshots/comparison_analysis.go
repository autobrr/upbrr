// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/autobrr/upbrr/internal/comparisons"
)

// AnalyzeComparisonSource shares one normalized decode across the independent
// V9 reduction and sparse paired dark/flat measurements. It returns compact
// native-bound anchors, never full-window analysis rasters.
func (s *Service) AnalyzeComparisonSource(ctx context.Context, request ComparisonSampleRequest) ([]ComparisonSample, error) {
	if _, native := s.runner.(commandRunner); native {
		executable, err := resolveFFmpeg()
		if err != nil {
			return nil, err
		}
		if err := comparisonAnalysisCapabilityFor(ctx, executable); err != nil {
			return nil, err
		}
	}
	w, h := request.Source.DisplaySize()
	if w <= 0 || h <= 0 {
		return nil, ErrComparisonNormalizationUnavailable
	}
	height := int(math.Round(float64(h)*512/float64(w)/2)) * 2
	motionHeight := int(math.Round(float64(h)*256/float64(w)/2)) * 2
	if height < 32 || height > 2048 || motionHeight < 32 || motionHeight > height {
		return nil, ErrComparisonNormalizationUnavailable
	}
	request.analysis = &comparisonRasterStream{
		height:         height,
		motionHeight:   motionHeight,
		matchAspect:    request.MatchAspectRatio,
		matchCrop:      request.MatchCrop,
		maxSparse:      comparisonSparseFrameLimit(request.DurationSeconds),
		retentionLimit: request.RetentionLimitBytes,
	}
	request.NativeFrames = true
	request.NormalizeSDR = true
	request.MatchAspectRatio = 0
	request.MatchCrop = nil
	request.StrideSeconds = .002
	return s.SampleComparisonSource(ctx, request)
}

// softwareComparisonAnalysisFilters resamples floating-point linear light before
// the expensive tone map. Both scene reductions start from this normalized
// picture independently. Native showinfo/replay precede these filters; capture
// continues to use the full-resolution renderer.
func (s *Service) softwareComparisonAnalysisFilters(request ComparisonSampleRequest) ([]string, error) {
	geometry := request.Source
	var filters []string
	if crop := request.MatchCrop; crop != nil {
		if !crop.ValidFor(geometry.Width, geometry.Height) {
			return nil, comparisonDiscFailure("matching_crop")
		}
		filters = append(filters, fmt.Sprintf("crop=%d:%d:%d:%d:exact=1", crop.Width, crop.Height, crop.X, crop.Y))
		geometry.Width, geometry.Height = crop.Width, crop.Height
	}
	w, h := geometry.DisplaySize()
	if w <= 0 || h <= 0 {
		return nil, ErrComparisonNormalizationUnavailable
	}
	width := min(1024, w)
	width -= width % 2
	height := int(math.Round(float64(h)*float64(width)/float64(w)/2)) * 2
	if width < 2 || height < 2 || height > 8192 {
		return nil, ErrComparisonNormalizationUnavailable
	}
	conversion := toneMappingFilters(captureRequest{
		ToneMap:   true,
		Algorithm: comparisonAnalysisAlgorithm,
		Desat:     comparisonAnalysisDesat,
	}, false)
	filters = append(filters, conversion[:2]...)
	filters = append(filters, fmt.Sprintf("zscale=width=%d:height=%d:filter=bilinear", width, height))
	filters = append(filters, conversion[2:]...)
	filters = append(filters, "setsar=1")
	if request.MatchAspectRatio > 0 && math.Abs(float64(width)/float64(height)-request.MatchAspectRatio) > 1e-6 {
		filters = append(
			filters,
			fmt.Sprintf("crop=w='min(iw,round(ih*%.9f/2)*2)':h='min(ih,round(iw/%.9f/2)*2)':exact=1", request.MatchAspectRatio, request.MatchAspectRatio),
		)
	}
	return filters, nil
}

func analysisSamples(result CommandResult, request ComparisonSampleRequest, window *comparisonDiscWindow) ([]ComparisonSample, error) {
	s := request.analysis
	if err := s.validate(); err != nil {
		return nil, err
	}
	native, err := comparisonObservations(result.Stderr)
	if err != nil || len(native) != len(s.measurements) {
		return nil, errors.New("comparison analysis lacks native frame identities")
	}
	renderer := "sdr"
	if request.Renderer != nil && request.Renderer.Mode != "" {
		renderer = request.Renderer.Mode
	}
	evidence := comparisons.ClassifyScenes(s.measurements, renderer)
	var samples []ComparisonSample
	for i, o := range native {
		nativeClock, nativeErr := comparisonClockKey(o)
		outputClock, outputErr := comparisonClockKey(s.observations[i])
		if nativeErr != nil || outputErr != nil || nativeClock != outputClock {
			return nil, errors.New("comparison analysis normalization changed native identity")
		}
		o.RelativeTimestampSeconds = o.TimestampSeconds - request.Source.StartTimeSeconds
		if window == nil {
			if err := bindFileObservation(request.Source, &o); err != nil {
				return nil, err
			}
		} else if err := bindDiscObservation(request.Source, *window, &o); err != nil {
			return nil, err
		}
		samples = append(samples, ComparisonSample{
			ComparisonFrameObservation: o,
			Pixels:                     s.pixels[i],
			Evidence:                   evidence[i],
			AnalysisAnchor:             s.measurements[i].Sparse != nil,
			NativeSequence:             true,
		})
	}
	return samples, nil
}
