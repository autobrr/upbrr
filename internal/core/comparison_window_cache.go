// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"slices"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

type comparisonCachedWindow struct {
	source     int
	start, end float64
	renderer   string
	frames     []screenshots.ComparisonSample
}

// Windows live only within one attempt, whose source, crop and normalization
// facts are fixed. Keep the most recent window per source under the parent's
// shared retention allowance; no decoded pixels enter persisted calibration.
type comparisonWindowCache struct {
	windows []comparisonCachedWindow // least recently used first
	bytes   int64
}

func (c *comparisonWindowCache) trim(limit int64) {
	for len(c.windows) > 0 && c.bytes > max(0, limit) {
		c.bytes -= int64(len(c.windows[0].frames)) * 4096
		c.windows = slices.Delete(c.windows, 0, 1)
	}
}

func (c *comparisonWindowCache) get(source int, start, end float64, renderer string) ([]screenshots.ComparisonSample, []api.ComparisonTimeInterval) {
	index := slices.IndexFunc(c.windows, func(w comparisonCachedWindow) bool {
		return w.source == source && w.renderer == renderer && w.start < end && w.end > start
	})
	if index < 0 {
		return nil, []api.ComparisonTimeInterval{{Start: start, End: end}}
	}
	w := c.windows[index]
	c.windows = append(slices.Delete(c.windows, index, index+1), w)
	var frames []screenshots.ComparisonSample
	for _, frame := range w.frames {
		if frame.RelativeTimestampSeconds >= start && frame.RelativeTimestampSeconds < end {
			frames = append(frames, frame)
		}
	}
	if len(frames) == 0 {
		return nil, []api.ComparisonTimeInterval{{Start: start, End: end}}
	}
	var missing []api.ComparisonTimeInterval
	if start < w.start {
		missing = append(missing, api.ComparisonTimeInterval{Start: start, End: frames[0].RelativeTimestampSeconds})
	}
	if end > w.end {
		// Decode from the last cached native picture, not the rounded seek
		// boundary. The shared original witness is checked before deduplication.
		missing = append(missing, api.ComparisonTimeInterval{Start: frames[len(frames)-1].RelativeTimestampSeconds, End: end})
	}
	return frames, missing
}

func (c *comparisonWindowCache) put(source int, start, end float64, renderer string, frames []screenshots.ComparisonSample, limit int64) {
	if index := slices.IndexFunc(c.windows, func(w comparisonCachedWindow) bool { return w.source == source }); index >= 0 {
		c.bytes -= int64(len(c.windows[index].frames)) * 4096
		c.windows = slices.Delete(c.windows, index, index+1)
	}
	bytes := int64(len(frames)) * 4096
	c.trim(limit - bytes)
	if bytes == 0 || bytes > limit {
		return
	}
	c.windows = append(c.windows, comparisonCachedWindow{
		source:   source,
		start:    start,
		end:      end,
		renderer: renderer,
		frames:   slices.Clone(frames),
	})
	c.bytes += bytes
}
