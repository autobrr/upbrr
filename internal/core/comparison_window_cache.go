// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"cmp"
	"slices"
	"sync"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

type comparisonCachedWindow struct {
	classified bool
	source     int
	start, end float64
	renderer   string
	frames     []screenshots.ComparisonSample
}

// Each entry owns a contiguous native run from one physical request. Source,
// programme, crop and aspect are fixed for the lifetime of this attempt.
type comparisonWindowCache struct {
	windows     []comparisonCachedWindow // least recently used first
	bytes, peak int64
	evictions   int
}

func (c *comparisonWindowCache) trim(limit int64) {
	for len(c.windows) > 0 && c.bytes > max(0, limit) {
		i := max(0, slices.IndexFunc(c.windows, func(w comparisonCachedWindow) bool { return w.renderer == "" || w.renderer == "none" }))
		c.bytes -= int64(len(c.windows[i].frames)) * 4096
		c.windows = slices.Delete(c.windows, i, i+1)
		c.evictions++
	}
}

// reserve gives the current read priority over optional retained frames. Native
// diagnostics and classified rasters can report demand from different goroutines.
func (c *comparisonWindowCache) reserve(limit int64) func(int64) bool {
	var mu sync.Mutex
	return func(needed int64) bool {
		if needed > limit {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		c.trim(limit - needed)
		return true
	}
}

func (c *comparisonWindowCache) get(source int, start, end float64, renderer string) ([]screenshots.ComparisonSample, []api.ComparisonTimeInterval, error) {
	var hits []comparisonCachedWindow
	remaining := c.windows[:0]
	for _, w := range c.windows {
		if !w.classified && w.source == source && w.renderer == renderer && w.start < end && w.end > start {
			hits = append(hits, w)
		} else {
			remaining = append(remaining, w)
		}
	}
	remaining = append(remaining, hits...)
	c.windows = remaining
	slices.SortStableFunc(hits, func(a, b comparisonCachedWindow) int { return cmp.Compare(a.start, b.start) })
	var frames []screenshots.ComparisonSample
	var gaps []api.ComparisonTimeInterval
	covered, boundary := start, start
	var lastCovered screenshots.ComparisonSample
	for _, w := range hits {
		before := len(frames)
		for _, f := range w.frames {
			if f.RelativeTimestampSeconds >= start && f.RelativeTimestampSeconds < end {
				frames = append(frames, f)
			}
		}
		if len(frames) == before {
			continue
		}
		if w.start > covered+1e-9 && !comparisonRoundedNativeBoundary(lastCovered, frames[before]) {
			right := frames[before].RelativeTimestampSeconds
			if f := frames[before]; f.Replay != nil && f.Replay.File != nil {
				right = min(end, right+float64(f.TimeBaseNumerator)/float64(f.TimeBaseDenominator))
			}
			gaps = append(gaps, api.ComparisonTimeInterval{Start: boundary, End: right})
		}
		if w.end > covered {
			covered = w.end
			lastCovered = frames[len(frames)-1]
			boundary = lastCovered.RelativeTimestampSeconds
		}
	}
	if covered < end-1e-9 {
		gaps = append(gaps, api.ComparisonTimeInterval{Start: boundary, End: end})
	}
	frames, err := orderedComparisonSamples(frames)
	if err != nil {
		return nil, nil, err
	}
	bridged := false
	if len(frames) > 1 {
		for i := 1; i < len(frames); i++ {
			a, b := frames[i-1], frames[i]
			if !comparisonFileJoin(a.ComparisonFrameObservation, b.ComparisonFrameObservation) {
				bridged = true
				right := min(end, b.RelativeTimestampSeconds+float64(b.TimeBaseNumerator)/float64(b.TimeBaseDenominator))
				gaps = append(gaps, api.ComparisonTimeInterval{Start: a.RelativeTimestampSeconds, End: right})
			}
		}
	}
	if bridged {
		gaps = mergeComparisonCoverage(gaps, 0)
	}
	return frames, gaps, nil
}

// The origin of sparse spatial measurements and the classifier's temporal halo
// both depend on the exact decode interval. Only that interval can reproduce
// the same classifications. The caller trims the usable payload after lookup.
func (c *comparisonWindowCache) classified(start, end float64, renderer string) []screenshots.ComparisonSample {
	for i, w := range c.windows {
		if w.classified && w.start == start && w.end == end && w.renderer == renderer {
			c.windows = append(slices.Delete(c.windows, i, i+1), w)
			return w.frames
		}
	}
	return nil
}

func (c *comparisonWindowCache) retain(w comparisonCachedWindow, limit int64) {
	bytes := int64(len(w.frames)) * 4096
	if bytes == 0 || bytes > limit {
		return
	}
	c.trim(limit - bytes)
	w.frames = slices.Clone(w.frames)
	c.windows = append(c.windows, w)
	c.bytes += bytes
	c.peak = max(c.peak, c.bytes)
}

// A clock alone cannot connect two stretches of an ordinary file. The same
// decode slot sequence or an observed neighbor from either side can join them.
func comparisonFileJoin(a, b screenshots.ComparisonFrameObservation) bool {
	if a.Replay == nil || a.Replay.File == nil {
		return b.Replay == nil || b.Replay.File == nil
	}
	if b.Replay == nil || b.Replay.File == nil {
		return false
	}
	x, y := a.Replay.File, b.Replay.File
	if a.PTS >= b.PTS || a.Replay.Identity.InputFingerprint != b.Replay.Identity.InputFingerprint || a.Replay.Identity.StreamID != b.Replay.Identity.StreamID ||
		a.Replay.Identity.Occurrence != b.Replay.Identity.Occurrence ||
		a.TimeBaseNumerator != b.TimeBaseNumerator ||
		a.TimeBaseDenominator != b.TimeBaseDenominator ||
		x.InputStart != y.InputStart {
		return false
	}
	if x.PacketPosition != nil || y.PacketPosition != nil {
		if x.PacketPosition == nil || y.PacketPosition == nil {
			return false
		}
		if x.Next != nil && (x.Next.PTS != b.PTS || x.Next.Position != *y.PacketPosition) {
			return false
		}
		if y.Previous != nil && (y.Previous.PTS != a.PTS || y.Previous.Position != *x.PacketPosition) {
			return false
		}
		return x.Next != nil || y.Previous != nil
	}
	return x.Seek == y.Seek && x.Start == y.Start && x.Duration == y.Duration && x.FirstPTS == y.FirstPTS && x.LastPTS == y.LastPTS && y.Slot == x.Slot+1
}

func verifyComparisonFileJoins(frames []screenshots.ComparisonSample) error {
	for i := 1; i < len(frames); i++ {
		if !comparisonFileJoin(frames[i-1].ComparisonFrameObservation, frames[i].ComparisonFrameObservation) {
			return comparisonWitnessConflict()
		}
	}
	return nil
}

// A one-tick discrepancy may come from duration rounding, but only consecutive
// native observations within the same physical read establish that distinction.
func comparisonRoundedNativeBoundary(a, b screenshots.ComparisonSample) bool {
	if a.DurationTicks <= 1 || b.DurationTicks <= 1 || a.Replay == nil || b.Replay == nil ||
		a.TimeBaseNumerator != b.TimeBaseNumerator || a.TimeBaseDenominator != b.TimeBaseDenominator ||
		a.Replay.Identity.InputFingerprint != b.Replay.Identity.InputFingerprint || a.Replay.Identity.Occurrence != b.Replay.Identity.Occurrence ||
		a.Replay.Identity.StreamID != b.Replay.Identity.StreamID || b.PTS-a.PTS-a.DurationTicks != 1 {
		return false
	}
	left, right := a.Replay.File, b.Replay.File
	if left != nil || right != nil {
		return comparisonFileJoin(a.ComparisonFrameObservation, b.ComparisonFrameObservation)
	}
	x, y := a.Replay.Window, b.Replay.Window
	return left == nil && right == nil && x.EncodedSHA256 != "" && x.EncodedSHA256 == y.EncodedSHA256 && x.Start90 == y.Start90 && x.End90 == y.End90
}

func comparisonNativeClockAdjacent(a, b screenshots.ComparisonSample) bool {
	return a.DurationTicks > 0 && a.TimeBaseNumerator > 0 && a.TimeBaseDenominator > 0 &&
		a.TimeBaseNumerator == b.TimeBaseNumerator && a.TimeBaseDenominator == b.TimeBaseDenominator &&
		b.PTS > a.PTS && (b.PTS-a.PTS <= a.DurationTicks || comparisonRoundedNativeBoundary(a, b))
}

func (c *comparisonWindowCache) put(source int, start, end float64, renderer string, frames []screenshots.ComparisonSample, limit int64) {
	for i := 0; i < len(frames); {
		first := frames[i]
		i++
		if !first.NativeSequence || first.DurationTicks <= 0 || first.TimeBaseNumerator <= 0 || first.TimeBaseDenominator <= 0 {
			continue
		}
		begin := i - 1
		// Split at actual native clock gaps and authored occurrence boundaries.
		for i < len(frames) {
			a, b := frames[i-1], frames[i]
			if !b.NativeSequence || b.DurationTicks <= 0 || !comparisonNativeClockAdjacent(a, b) ||
				a.Identity != nil && (b.Identity == nil || a.Identity.Occurrence != b.Identity.Occurrence) ||
				!comparisonFileJoin(a.ComparisonFrameObservation, b.ComparisonFrameObservation) {
				break
			}
			i++
		}
		last := frames[i-1]
		tick := float64(last.TimeBaseNumerator) / float64(last.TimeBaseDenominator)
		left, right := max(start, first.RelativeTimestampSeconds), min(end, last.RelativeTimestampSeconds+float64(last.DurationTicks)*tick)
		if right <= left {
			continue
		}
		c.retain(comparisonCachedWindow{
			source:   source,
			start:    left,
			end:      right,
			renderer: renderer,
			frames:   frames[begin:i],
		}, limit)
	}
}
