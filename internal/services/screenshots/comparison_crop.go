// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"regexp"
	"strconv"
)

// ComparisonCrop is decoded black-border evidence used for matching only.
// It never changes the native frame witness, display facts or captured pixels.
type ComparisonCrop struct{ X, Y, Width, Height int }

// ValidFor excludes out-of-bounds or small scene-content crops.
func (c ComparisonCrop) ValidFor(width, height int) bool {
	return width > 0 && height > 0 && c.X >= 0 && c.Y >= 0 && c.Width >= width*3/4 && c.Height >= height*3/4 && c.X <= width-c.Width && c.Y <= height-c.Height
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
	if source.Input == nil {
		return nil, nil
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
				budget.RemainingEncodedBytes -= count
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
