// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"encoding/binary"
	"math"
	"slices"

	dovi "github.com/Audionut/go-dovi-extract"
)

// AreaObservation compares L5 offsets and sampled picture borders over [Start, End).
// Offset arrays use left, right, top, bottom coded pixels; Delta is observed minus expected.
// Confidence describes the sampled evidence and is not an automatic media verdict.
type AreaObservation struct {
	Start      int    `json:"start"`
	End        int    `json:"end"`
	Known      bool   `json:"known"`
	Expected   [4]int `json:"expected"`
	Observed   [4]int `json:"observed"`
	Delta      [4]int `json:"delta"`
	Samples    int    `json:"samples"`
	Confidence string `json:"confidence"`
}

type areaInterval struct {
	AreaObservation
	indices []int
	borders [][4]int
	unclear bool
}

type areaMeasurement struct {
	intervals  []areaInterval
	selected   []int
	thumbnails []MeasurementThumbnail
}

func offsetsAt(a *dovi.Analysis, index int) ([4]int, bool) {
	dm := sceneDM(a, index)
	if dm == nil || dm.L5 == nil {
		return [4]int{}, false
	}
	l := dm.L5
	return [4]int{int(l.Left), int(l.Right), int(l.Top), int(l.Bottom)}, true
}

func planAreaMeasurement(a *dovi.Analysis) (*areaMeasurement, error) {
	plan := &areaMeasurement{}
	if a == nil || len(a.Pictures) == 0 {
		return plan, nil
	}
	start := 0
	for start < len(a.Pictures) {
		offsets, known := offsetsAt(a, start)
		end := start + 1
		for end < len(a.Pictures) {
			next, nextKnown := offsetsAt(a, end)
			if next != offsets || nextKnown != known {
				break
			}
			end++
		}
		interval := areaInterval{
			Start:      start,
			End:        end,
			Known:      known,
			Expected:   offsets,
			Confidence: "unclear",
		}
		if known {
			interval.indices = []int{start, start + (end-start-1)/2, end - 1}
			interval.indices = slices.Compact(interval.indices)
			plan.selected = append(plan.selected, interval.indices...)
		}
		if start > 0 {
			plan.selected = append(plan.selected, start-1, start)
		}
		plan.intervals = append(plan.intervals, interval)
		if len(plan.intervals) > 120 {
			return nil, errDecodedMapping
		}
		start = end
	}
	slices.Sort(plan.selected)
	plan.selected = slices.Compact(plan.selected)
	if len(plan.selected) > 120 {
		return nil, errDecodedMapping
	}
	return plan, nil
}

// inferBorders uses native-raster normalized PQ-coded luma. An entirely dark
// interior cannot establish a border, even when the outer rows are black.
func inferBorders(frame []byte, width, height int) ([4]int, bool) {
	blackPQ, _ := dovi.NitsToPQ(0.1)
	lightPQ, _ := dovi.NitsToPQ(1)
	black, light := uint16(math.Floor(blackPQ*65535)), uint16(math.Ceil(lightPQ*65535))
	value := func(x, y int) uint16 { return binary.LittleEndian.Uint16(frame[2*(y*width+x):]) }
	rowBlack := func(y int) bool {
		count := 0
		for x := range width {
			if value(x, y) <= black {
				count++
			}
		}
		return count*1000 >= width*995
	}
	columnBlack := func(x int) bool {
		count := 0
		for y := range height {
			if value(x, y) <= black {
				count++
			}
		}
		return count*1000 >= height*995
	}
	var borders [4]int
	for borders[0] < width && columnBlack(borders[0]) {
		borders[0]++
	}
	for borders[1] < width-borders[0] && columnBlack(width-1-borders[1]) {
		borders[1]++
	}
	for borders[2] < height && rowBlack(borders[2]) {
		borders[2]++
	}
	for borders[3] < height-borders[2] && rowBlack(height-1-borders[3]) {
		borders[3]++
	}
	area := int64(width-borders[0]-borders[1]) * int64(height-borders[2]-borders[3])
	if area <= 0 {
		return borders, false
	}
	var bright int64
	for y := borders[2]; y < height-borders[3]; y++ {
		for x := borders[0]; x < width-borders[1]; x++ {
			if value(x, y) >= light {
				bright++
			}
		}
	}
	return borders, bright*200 >= area
}

func (p *areaMeasurement) observe(index int, frame []byte, width, height int) {
	borders, unambiguous := inferBorders(frame, width, height)
	for i := range p.intervals {
		interval := &p.intervals[i]
		if !slices.Contains(interval.indices, index) {
			continue
		}
		interval.borders = append(interval.borders, borders)
		interval.unclear = interval.unclear || !unambiguous
		if interval.Expected[0]+interval.Expected[1] >= width || interval.Expected[2]+interval.Expected[3] >= height {
			interval.unclear = true
		}
	}
	// One representative thumbnail per sampled interval, at most twelve. The
	// report retains every interval's numbers independently of this display cap.
	if len(p.thumbnails) < 12 {
		for i, interval := range p.intervals {
			if interval.Known && index == interval.Start && (len(p.intervals) <= 12 || i%(max(1, len(p.intervals)/12)) == 0) {
				cue := reduceSceneLuma(frame, width, height)
				thumbnail := MeasurementThumbnail{
					Index:  index,
					Width:  sceneWidth,
					Height: sceneHeight,
					Luma:   make([]byte, len(cue)),
				}
				for j, value := range cue {
					thumbnail.Luma[j] = byte(math.Round(value * 255))
				}
				p.thumbnails = append(p.thumbnails, thumbnail)
				break
			}
		}
	}
}

func (p *areaMeasurement) finish() ([]AreaObservation, []MeasurementThumbnail, bool) {
	observations := make([]AreaObservation, len(p.intervals))
	complete := len(p.intervals) > 0
	for i, interval := range p.intervals {
		observation := interval.AreaObservation
		observation.Samples = len(interval.borders)
		unambiguous := observation.Known && observation.Samples >= 3 && !interval.unclear
		if len(interval.borders) > 0 {
			for side := range 4 {
				values := make([]int, len(interval.borders))
				for j, borders := range interval.borders {
					values[j] = borders[side]
				}
				slices.Sort(values)
				unambiguous = unambiguous && values[len(values)-1]-values[0] <= 1
				observation.Observed[side] = values[len(values)/2]
				observation.Delta[side] = observation.Observed[side] - observation.Expected[side]
			}
		}
		if unambiguous {
			observation.Confidence = "stable_border"
		}
		complete = complete && unambiguous
		observations[i] = observation
	}
	return observations, p.thumbnails, complete
}
