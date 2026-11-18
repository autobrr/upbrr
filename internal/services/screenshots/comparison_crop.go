// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// ComparisonCrop is decoded black-border evidence used for matching only.
// It never changes the native frame witness, display facts or captured pixels.
type ComparisonCrop struct{ X, Y, Width, Height int }

// ValidFor excludes out-of-bounds or small scene-content crops.
func (c ComparisonCrop) ValidFor(width, height int) bool {
	return width > 0 && height > 0 && width <= 32768 && height <= 32768 && c.X >= 0 && c.Y >= 0 && c.Width > 0 && c.Height > 0 &&
		c.X <= width-c.Width && c.Y <= height-c.Height && int64(c.Width)*int64(c.Height)*2 >= int64(width)*int64(height)
}

var comparisonCropLine = regexp.MustCompile(`\bcrop=(\d+):(\d+):(\d+):(\d+)`)

// DetectComparisonActivePicture detects persistent encoded borders in one
// bounded calibration region. Other regions and rows still require independent
// temporal/visual proof; border detection itself authorizes no correspondence.
func (s *Service) DetectComparisonActivePicture(
	ctx context.Context,
	source ComparisonSource,
	start, duration float64,
	budget *ComparisonDecodeBudget,
) (*ComparisonCrop, error) {
	ctx, _ = comparisonProcessCounter(ctx, budget)
	if source.Input == nil {
		return s.detectFileComparisonActivePicture(ctx, source, start, duration, budget)
	}
	windows, err := discWindows(source, start, start+duration)
	if err != nil {
		return nil, err
	}
	executable, err := resolveFFmpeg()
	if err != nil {
		return nil, err
	}
	var united *ComparisonCrop
	for _, window := range windows {
		if budget != nil {
			for _, span := range window.Spans {
				count := span.End - span.Start
				if count < 0 || count > budget.RemainingEncodedBytes {
					return nil, ErrComparisonFastInconclusive
				}
				if err := budget.admit(count); err != nil {
					return nil, err
				}
			}
		}
		path, cleanup, err := stageDiscWindow(ctx, source, &window)
		if err != nil {
			return nil, err
		}
		args := []string{
			"-hide_banner",
			"-loglevel",
			"info",
			"-copyts",
			"-f",
			window.Format,
			"-i",
			path,
			"-map",
			"0:i:" + window.StreamID,
			"-an",
			"-sn",
			"-dn",
			"-vf",
			comparisonDiscWindowFilter(window) + ",cropdetect=round=2:skip=0:reset=0",
			"-frames:v",
			"50000",
			"-f",
			"null",
			"-",
		}
		if err := budget.admitMovieSeconds(float64(window.End90-window.Start90)/90000 + window.PrerollSeconds); err != nil {
			cleanup()
			return nil, err
		}
		result, runErr := (finiteComparisonRunner{base: s.runner, outputLimit: 64 << 10}).Run(ctx, executable, args, "")
		cleanup()
		if runErr != nil {
			return nil, runErr
		}
		if err := ffmpegFrameCorruptionError(result.Stderr); err != nil {
			return nil, err
		}
		matches := comparisonCropLine.FindAllSubmatch(result.Stderr, -1)
		if len(matches) == 0 {
			return nil, nil
		}
		last := matches[len(matches)-1]
		values := make([]int, 4)
		for i := range values {
			values[i], err = strconv.Atoi(string(last[i+1]))
			if err != nil {
				return nil, nil
			}
		}
		crop := ComparisonCrop{
			Width:  values[0],
			Height: values[1],
			X:      values[2],
			Y:      values[3],
		}
		if !crop.ValidFor(source.Width, source.Height) {
			return nil, nil
		}
		if united == nil {
			united = &crop
		} else {
			right, bottom := max(united.X+united.Width, crop.X+crop.Width), max(united.Y+united.Height, crop.Y+crop.Height)
			united.X, united.Y = min(united.X, crop.X), min(united.Y, crop.Y)
			united.Width, united.Height = right-united.X, bottom-united.Y
		}
	}
	if united != nil && united.X == 0 && united.Y == 0 && united.Width == source.Width && united.Height == source.Height {
		return nil, nil
	}
	return united, nil
}

func (s *Service) detectFileComparisonActivePicture(
	ctx context.Context,
	source ComparisonSource,
	start, duration float64,
	budget *ComparisonDecodeBudget,
) (*ComparisonCrop, error) {
	if !comparisonFinite(start) || !comparisonFinite(duration) || start < 0 || duration <= 0 || duration > 8 || source.Width <= 0 || source.Height <= 0 {
		return nil, comparisonDiscFailure("matching_crop")
	}
	path, err := comparisonLocalPath(source.Path)
	if err != nil {
		return nil, err
	}
	ctx, input, err := s.openComparisonFileInput(ctx, path, budget)
	if err != nil {
		return nil, err
	}
	defer input.close()
	path = input.url
	executable, err := resolveFFmpeg()
	if err != nil {
		return nil, err
	}
	filter := fmt.Sprintf("trim=start=%.9f:end=%.9f", source.StartTimeSeconds+start, source.StartTimeSeconds+start+duration)
	hdr := false
	for _, format := range source.HDRFacts.Formats {
		if format == api.HDRFormatSDR {
			continue
		}
		if !softwareTonemappingCompatible(source.HDRFacts) {
			// Border detection is optional. Keep the original geometry for
			// metadata-aware GPU matching; its own renderer and decoded Dolby
			// Vision checks still decide whether correspondence is supported.
			return nil, nil
		}
		hdr = true
		break
	}
	if hdr {
		filters, filterErr := s.softwareComparisonAnalysisFilters(ComparisonSampleRequest{Source: source})
		if filterErr != nil {
			return nil, filterErr
		}
		filter += "," + strings.Join(filters, ",")
	}
	if scale := buildPARScaleFilter(captureRequest{
		SourceWidth:  source.Width,
		SourceHeight: source.Height,
		WidthScale:   source.WidthScale,
		HeightScale:  source.HeightScale,
	}); scale != "" && !hdr {
		filter += "," + scale
	}
	w, h := source.DisplaySize()
	height := int(math.Round(float64(h)*512/float64(w)/2)) * 2
	if height < 32 || height > 2048 {
		return nil, comparisonDiscFailure("matching_crop")
	}
	filter += fmt.Sprintf(",scale=512:%d:flags=area:out_range=full,format=gray,cropdetect=limit=8:round=2:skip=0:reset=0", height)
	if err := budget.admitMovieSeconds(duration); err != nil {
		return nil, err
	}
	result, err := (finiteComparisonRunner{base: s.runner, outputLimit: 64 << 10}).Run(ctx, executable, []string{
		"-hide_banner", "-loglevel", "info", "-threads", "4", "-filter_threads", "4", "-copyts", "-ss", strconv.FormatFloat(start, 'f', 9, 64), "-i", path,
		"-map", "0:V:0", "-an", "-sn", "-dn", "-vf", filter, "-frames:v", "50000", "-f", "null", "-",
	}, "")
	if err != nil {
		return nil, err
	}
	if inputErr := input.failure(); inputErr != nil {
		return nil, inputErr
	}
	matches := comparisonCropLine.FindAllSubmatch(result.Stderr, -1)
	if len(matches) == 0 {
		return nil, nil
	}
	last := matches[len(matches)-1]
	values := make([]int, 4)
	for i := range values {
		values[i], err = strconv.Atoi(string(last[i+1]))
		if err != nil {
			return nil, nil
		}
	}
	// The analysis mask is in display coordinates; matching crops remain in
	// the native coded raster before the existing PAR conversion.
	left, top := values[2]*source.Width/512, values[3]*source.Height/height
	right := min(source.Width, (values[2]+values[0])*source.Width/512)
	bottom := min(source.Height, (values[3]+values[1])*source.Height/height)
	crop := &ComparisonCrop{
		X:      left,
		Y:      top,
		Width:  right - left,
		Height: bottom - top,
	}
	if !crop.ValidFor(source.Width, source.Height) || crop.X == 0 && crop.Y == 0 && crop.Width == source.Width && crop.Height == source.Height {
		return nil, nil
	}
	return crop, nil
}
