// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"encoding/binary"
	"math"
	"math/big"
	"slices"
	"strconv"

	dovi "github.com/Audionut/go-dovi-extract"
)

const (
	sceneWidth              = 192
	sceneHeight             = 108
	SceneMeasurementProfile = "hdr-scene-v1"
	AreaMeasurementProfile  = "hdr-l5-v1"
	GradeMeasurementProfile = "hdr-grade-v1"
)

// MeasurementTimestamp preserves an exact rational timestamp as decimal integers.
// Value and Timescale are meaningful only when Known is true.
type MeasurementTimestamp struct {
	Value     string `json:"value"`
	Timescale string `json:"timescale"`
	Known     bool   `json:"known"`
}

func measurementTimestamp(stamp pictureTime) MeasurementTimestamp {
	if !stamp.Valid {
		return MeasurementTimestamp{}
	}
	return MeasurementTimestamp{
		Value:     strconv.FormatInt(stamp.Value, 10),
		Timescale: strconv.FormatUint(stamp.Timescale, 10),
		Known:     true,
	}
}

// MeasurementThumbnail retains reduced luma bytes for a sampled original picture index.
// Candidate associates the image with a scene window or active-area interval.
type MeasurementThumbnail struct {
	Candidate int    `json:"candidate,omitempty"`
	Index     int    `json:"index"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Luma      []byte `json:"luma"`
	RGB       []byte `json:"rgb,omitempty"`
}

// SceneObservation compares a metadata candidate with nearby decoded-picture changes.
// Scores correspond to offsets -3 through +3; BestOffset is a sampled estimate,
// not authority to shift metadata or reject the media automatically.
type SceneObservation struct {
	Candidate  int                  `json:"candidate"`
	PTS        MeasurementTimestamp `json:"pts"`
	Segment    int                  `json:"segment"`
	Scores     [7]float64           `json:"scores"`
	Noise      float64              `json:"noise"`
	BestOffset int                  `json:"bestOffset"`
	Confidence string               `json:"confidence"`
}

type sceneCandidate struct {
	SceneObservation
	refresh bool
	delta   int
	usable  bool
	pair    int
	best    float64
	bright  bool
}

type sceneMeasurement struct {
	candidates    []sceneCandidate
	selected      []int
	thumbnails    []MeasurementThumbnail
	previous      []float64
	previousIndex int
}

func sceneDM(a *dovi.Analysis, index int) *dovi.DM {
	if a == nil || index < 0 || index >= len(a.Pictures) {
		return nil
	}
	p := a.Pictures[index]
	if !p.HasRPU || uint64(p.RPUIndex) >= uint64(len(a.Records)) {
		return nil
	}
	return a.Records[p.RPUIndex].DM
}

func sceneStratum(times []pictureTime, index int) (int, bool) {
	position := new(big.Rat).SetFrac64(int64(index), int64(len(times)-1))
	if times[index].Valid {
		position.Sub(times[index].rational(), times[0].rational())
		span := new(big.Rat).Sub(times[len(times)-1].rational(), times[0].rational())
		if span.Sign() <= 0 {
			return 0, false
		}
		position.Quo(position, span)
	}
	start, end := big.NewRat(3, 100), big.NewRat(97, 100)
	if position.Cmp(start) < 0 || position.Cmp(end) > 0 {
		return 0, false
	}
	position.Sub(position, start).Mul(position, big.NewRat(6000, 94))
	stratum := new(big.Int).Quo(position.Num(), position.Denom())
	return min(59, int(stratum.Int64())), true
}

func planSceneMeasurement(a *dovi.Analysis, times []pictureTime) *sceneMeasurement {
	plan := &sceneMeasurement{previousIndex: -2}
	if a == nil || len(times) < 2 || len(times) != len(a.Pictures) {
		return plan
	}
	strata := make([]*sceneCandidate, 60)
	for i := range times {
		current, previous := sceneDM(a, i), sceneDM(a, i-1)
		if current == nil || previous == nil {
			continue
		}
		refresh, delta := current.SceneRefresh == 1, 0
		if current.L1 != nil && previous.L1 != nil {
			for _, pair := range [][2]uint16{{current.L1.MinPQ, previous.L1.MinPQ}, {current.L1.MaxPQ, previous.L1.MaxPQ}, {current.L1.AvgPQ, previous.L1.AvgPQ}} {
				delta = max(delta, int(math.Abs(float64(int(pair[0])-int(pair[1])))))
			}
		}
		if !refresh && delta == 0 {
			continue
		}
		stratum, ok := sceneStratum(times, i)
		if !ok {
			continue
		}
		prior := strata[stratum]
		if prior != nil && (prior.refresh && !refresh || prior.refresh == refresh && prior.delta >= delta) {
			continue
		}
		candidate := &sceneCandidate{
			Candidate:  i,
			PTS:        measurementTimestamp(times[i]),
			Segment:    stratum / 12,
			Confidence: "unclear",
			refresh:    refresh,
			delta:      delta,
			pair:       -1,
			usable:     true,
		}
		for frame := i - 4; frame <= i+3; frame++ {
			candidate.usable = candidate.usable && sceneDM(a, frame) != nil
		}
		strata[stratum] = candidate
	}
	for _, candidate := range strata {
		if candidate == nil {
			continue
		}
		plan.candidates = append(plan.candidates, *candidate)
		if candidate.usable {
			for index := candidate.Candidate - 4; index <= candidate.Candidate+3; index++ {
				plan.selected = append(plan.selected, index)
			}
		}
	}
	slices.Sort(plan.selected)
	plan.selected = slices.Compact(plan.selected)
	count := min(12, len(plan.candidates))
	for i := range count {
		index := 0
		if count > 1 {
			index = i * (len(plan.candidates) - 1) / (count - 1)
		}
		plan.candidates[index].pair = len(plan.thumbnails)
		for range 2 {
			plan.thumbnails = append(plan.thumbnails, MeasurementThumbnail{
				Candidate: plan.candidates[index].Candidate,
				Index:     -1,
				Width:     sceneWidth,
				Height:    sceneHeight,
				Luma:      make([]byte, sceneWidth*sceneHeight),
			})
		}
	}
	return plan
}

// reduceSceneLuma averages exact source-area footprints in nonlinear PQ-coded
// luma. Geometry reduction is only a scene cue; it never changes L5 coordinates.
func reduceSceneLuma(frame []byte, width, height int) []float64 {
	out := make([]float64, sceneWidth*sceneHeight)
	for oy := range sceneHeight {
		y0, y1 := oy*height/sceneHeight, ((oy+1)*height+sceneHeight-1)/sceneHeight
		for ox := range sceneWidth {
			x0, x1 := ox*width/sceneWidth, ((ox+1)*width+sceneWidth-1)/sceneWidth
			var sum float64
			for y := y0; y < y1; y++ {
				wy := min((y+1)*sceneHeight, (oy+1)*height) - max(y*sceneHeight, oy*height)
				for x := x0; x < x1; x++ {
					wx := min((x+1)*sceneWidth, (ox+1)*width) - max(x*sceneWidth, ox*width)
					sum += float64(binary.LittleEndian.Uint16(frame[2*(y*width+x):])) * float64(wx*wy)
				}
			}
			out[oy*sceneWidth+ox] = sum / (float64(width) * float64(height) * 65535)
		}
	}
	return out
}

func (p *sceneMeasurement) observe(index int, cue []float64) {
	if p.previousIndex == index-1 {
		var score float64
		lightFloor, _ := dovi.NitsToPQ(1)
		bright := 0
		for i, value := range cue {
			score += math.Abs(value - p.previous[i])
			if max(value, p.previous[i]) >= lightFloor {
				bright++
			}
		}
		score /= float64(len(cue))
		for i := range p.candidates {
			c := &p.candidates[i]
			difference := index - c.Candidate
			if !c.usable || difference < -3 || difference > 3 {
				continue
			}
			c.Scores[difference+3] = score
			if score > c.best {
				c.best = score
				c.bright = bright*100 >= len(cue)
				if c.pair < 0 {
					continue
				}
				for j, source := range [][]float64{p.previous, cue} {
					thumbnail := &p.thumbnails[c.pair+j]
					thumbnail.Index = index - 1 + j
					for k, value := range source {
						thumbnail.Luma[k] = byte(math.Round(value * 255))
					}
				}
			}
		}
	}
	p.previous, p.previousIndex = cue, index
}

func (p *sceneMeasurement) finish() ([]SceneObservation, []MeasurementThumbnail, bool) {
	observations := make([]SceneObservation, len(p.candidates))
	segments := make(map[int]bool)
	confident := 0
	allUsable := true
	for i, candidate := range p.candidates {
		observation := candidate.SceneObservation
		allUsable = allUsable && candidate.usable
		if candidate.usable {
			scores := observation.Scores
			bestIndex := 0
			for j := 1; j < len(scores); j++ {
				if scores[j] > scores[bestIndex] {
					bestIndex = j
				}
			}
			best := scores[bestIndex]
			slices.Sort(scores[:])
			observation.Noise = max(0.005, (scores[2]+scores[3])/2)
			observation.BestOffset = bestIndex - 3
			if candidate.bright && best > max(0.02, 4*observation.Noise) && best >= 2*scores[5] {
				observation.Confidence = "dominant_cut"
				confident++
				segments[observation.Segment] = true
			}
		}
		observations[i] = observation
	}
	var thumbnails []MeasurementThumbnail
	for _, thumbnail := range p.thumbnails {
		if thumbnail.Index >= 0 {
			thumbnails = append(thumbnails, thumbnail)
		}
	}
	p.previous = nil
	return observations, thumbnails, allUsable && confident >= 3 && len(segments) >= 3
}
