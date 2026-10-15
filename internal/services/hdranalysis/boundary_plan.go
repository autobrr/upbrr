// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	dovi "github.com/Audionut/go-dovi-extract"

	"github.com/autobrr/upbrr/pkg/api"
)

// BoundaryVisualCandidate ranks a metadata boundary for a visual preview.
// Candidate is the zero-based frame index after the boundary; Stratum and Rank
// are zero-based temporal-bin and preference indices.
type BoundaryVisualCandidate struct {
	Candidate int  `json:"candidate"`
	Stratum   int  `json:"stratum"`
	Rank      int  `json:"rank"`
	Attempted bool `json:"attempted"`
}

// BoundaryFormat retains candidates, observations and preview pairs for one
// native format. Selected counts the union of numeric and visual candidates;
// Measured and Coverage describe that selection, not the entire source timeline.
type BoundaryFormat struct {
	Format               api.HDRNativeFormat       `json:"format"`
	NumericCandidates    []int                     `json:"numericCandidates"`
	VisualCandidates     []BoundaryVisualCandidate `json:"visualCandidates"`
	Observations         []SceneObservation        `json:"observations"`
	Thumbnails           []MeasurementThumbnail    `json:"thumbnails"`
	EligibleCandidates   int                       `json:"eligibleCandidates"`
	MissingNumericStrata int                       `json:"missingNumericStrata"`
	MissingVisualStrata  int                       `json:"missingVisualStrata"`
	Selected             int                       `json:"selected"`
	Measured             int                       `json:"measured"`
	Coverage             api.HDRCoverage           `json:"coverage"`
	Reason               string                    `json:"reason,omitempty"`
}

// BoundaryInvocation records preparation or decoding work for a finite window,
// including compressed-clip identity, exposed timestamps and resource charges.
// EffectiveThreads is zero for clip preparation; RequestedThreads preserves user intent.
type BoundaryInvocation struct {
	ClipSHA256       string                 `json:"clipSha256,omitempty"`
	CompressedBytes  int64                  `json:"compressedBytes,omitempty"`
	PTS              []MeasurementTimestamp `json:"pts"`
	VCLUnits         int                    `json:"vclUnits"`
	NALUnits         int                    `json:"nalUnits"`
	ExposedPictures  int                    `json:"exposedPictures"`
	RequestedThreads int                    `json:"requestedThreads"`
	EffectiveThreads int                    `json:"effectiveThreads"`
	ReservedBytes    int64                  `json:"reservedBytes"`
	State            string                 `json:"state"`
}

// BoundaryWindow is an inclusive range of zero-based presentation-frame indices
// with the receipts for its clip preparation, measurement and optional previews.
type BoundaryWindow struct {
	First       int                  `json:"first"`
	Last        int                  `json:"last"`
	Invocations []BoundaryInvocation `json:"invocations"`
	Reason      string               `json:"reason,omitempty"`
}

// BoundaryReport retains per-format evidence and the sorted union of admitted
// frame indices. ChargedVCL counts VCL units charged to the bounded work budget.
type BoundaryReport struct {
	Formats              []BoundaryFormat `json:"formats"`
	SelectedFrameIndices []int            `json:"selectedFrameIndices"`
	Windows              []BoundaryWindow `json:"windows"`
	ChargedVCL           int              `json:"chargedVCL"`
}

// BoundaryAreaSample records one active-area observation at Index and PTS.
// Borders contains left, right, top and bottom widths in measurement pixels;
// BrightEnough indicates sufficient visible content for border inference.
type BoundaryAreaSample struct {
	Index        int                  `json:"index"`
	PTS          MeasurementTimestamp `json:"pts"`
	Borders      [4]int               `json:"borders"`
	BrightEnough bool                 `json:"brightEnough"`
}

type boundaryAreaInterval struct {
	AreaObservation
	indices []int
	initial []int
}

// BoundaryPlan is compact evidence derived before native parser storage is released.
// All visual alternatives and all five distinct area samples are admitted up front.
type BoundaryPlan struct {
	report             BoundaryReport
	times              []pictureTime
	areas              []boundaryAreaInterval
	areaSelected       []int
	areaTotal          int
	unsupportedFormats int
}

type boundaryCandidate struct {
	index    int
	score    float64
	position float64
}

func rankBoundaryCandidates(format api.HDRNativeFormat, values [][2]float64, known, refresh []bool, times []pictureTime) BoundaryFormat {
	result := BoundaryFormat{Format: format, Coverage: api.HDRCoverageUnknown}
	var candidates []boundaryCandidate
	duration := newBoundarySeconds(times[len(times)-1], times[0])
	previous := 0
	for i := 1; i < len(values); i++ {
		if !known[i] || !known[i-1] || values[i] == values[i-1] && !refresh[i] {
			continue
		}
		if i < 4 || i+3 >= len(values) {
			continue
		}
		seconds := newBoundarySeconds(times[i], times[previous])
		position := newBoundarySeconds(times[i], times[0])
		score := math.Abs(
			math.Log1p(values[i][0])-math.Log1p(values[i-1][0]),
		) + math.Abs(
			math.Log1p(values[i][1])-math.Log1p(values[i-1][1]),
		) + .35*math.Log1p(
			seconds,
		)
		candidates = append(candidates, boundaryCandidate{i, score, position / duration})
		previous = i
	}
	result.EligibleCandidates = len(candidates)
	rank := func(a, b boundaryCandidate) int {
		if order := cmp.Compare(b.score, a.score); order != 0 {
			return order
		}
		return cmp.Compare(a.index, b.index)
	}
	for stratum := range 60 {
		var group []boundaryCandidate
		for _, c := range candidates {
			if min(59, int(c.position*60)) == stratum {
				group = append(group, c)
			}
		}
		if len(group) == 0 {
			result.MissingNumericStrata++
			continue
		}
		slices.SortFunc(group, rank)
		result.NumericCandidates = append(result.NumericCandidates, group[0].index)
	}
	for stratum := range 12 {
		var group []boundaryCandidate
		for _, c := range candidates {
			position := c.position
			if position >= .03 && position <= .97 && min(11, int((position-.03)/.94*12)) == stratum {
				group = append(group, c)
			}
		}
		if len(group) == 0 {
			result.MissingVisualStrata++
			continue
		}
		slices.SortFunc(group, func(a, b boundaryCandidate) int {
			an, bn := slices.Contains(result.NumericCandidates, a.index), slices.Contains(result.NumericCandidates, b.index)
			if an != bn {
				if an {
					return -1
				}
				return 1
			}
			return rank(a, b)
		})
		for index, c := range group[:min(4, len(group))] {
			result.VisualCandidates = append(result.VisualCandidates, BoundaryVisualCandidate{
				Candidate: c.index,
				Stratum:   stratum,
				Rank:      index,
			})
		}
	}
	result.Selected = len(boundaryCandidates(result))
	return result
}

func newBoundarySeconds(a, b pictureTime) float64 {
	if !a.Valid || !b.Valid || a.Timescale == 0 || b.Timescale == 0 {
		return 0
	}
	delta := a.rational()
	value, _ := delta.Sub(delta, b.rational()).Float64()
	return value
}

func boundaryCandidates(format BoundaryFormat) []int {
	indices := slices.Clone(format.NumericCandidates)
	for _, visual := range format.VisualCandidates {
		indices = append(indices, visual.Candidate)
	}
	slices.Sort(indices)
	return slices.Compact(indices)
}

// PlanBoundary copies only timestamps, bounded candidate ownership and L5 intervals.
func PlanBoundary(input DecodedInput, area bool) (*BoundaryPlan, error) {
	if input.Source.RawHEVC || strings.Split(input.Source.DolbyVisionProfile, ".")[0] == "5" {
		return nil, errDecodedMapping
	}
	times, err := decodedTimeline(input)
	if err != nil {
		return nil, fmt.Errorf("plan boundary analysis: %w", err)
	}
	if len(times) < 8 {
		return nil, errDecodedMapping
	}
	// Every source picture must have a unique timestamp after finite-clip
	// quantization, including preroll outside selected windows.
	var previous int64
	for i, stamp := range times {
		ms, err := boundarySignedMilliseconds(stamp)
		if err != nil || i > 0 && ms <= previous {
			return nil, errDecodedMapping
		}
		previous = ms
	}
	plan := &BoundaryPlan{times: times}
	for _, native := range input.Native.Results {
		if native.State != api.HDRExtractionComplete {
			if native.Requested || native.Advertised && native.State != api.HDRExtractionNotAnalyzed {
				plan.unsupportedFormats++
			}
			continue
		}
		values := make([][2]float64, len(times))
		known := make([]bool, len(times))
		refresh := make([]bool, len(times))
		if native.Format == api.HDRNativeDolbyVision {
			for i := range times {
				if dm := sceneDM(input.Native.DolbyVision, i); dm != nil && dm.L1 != nil {
					maximum, e1 := dovi.PQToNits(float64(dm.L1.MaxPQ) / 4095)
					average, e2 := dovi.PQToNits(float64(dm.L1.AvgPQ) / 4095)
					if e1 != nil || e2 != nil {
						return nil, errDecodedMapping
					}
					values[i], known[i], refresh[i] = [2]float64{maximum, average}, true, dm.SceneRefresh == 1
				}
			}
		} else if input.Native.HDR10Plus != nil {
			model, err := input.Native.HDR10Plus.PlotMetadata()
			if err != nil {
				return nil, fmt.Errorf("plan boundary analysis: %w", err)
			}
			if len(model.Frames) != len(times) {
				return nil, errDecodedMapping
			}
			for i, frame := range model.Frames {
				values[i] = [2]float64{float64(slices.Max(frame.MaxSCL)) / 10, float64(frame.AverageRGB) / 10}
				known[i] = true
			}
		}
		format := rankBoundaryCandidates(native.Format, values, known, refresh, times)
		plan.report.Formats = append(plan.report.Formats, format)
		for _, index := range boundaryCandidates(format) {
			plan.report.Windows = append(plan.report.Windows, BoundaryWindow{First: index - 4, Last: index + 3})
		}
	}
	if area && input.Native.DolbyVision != nil {
		visitBoundaryAreas(input.Native.DolbyVision, func(_ int, _ boundaryAreaInterval) { plan.areaTotal++ })
		var selectedIntervals []int
		for selected := range min(24, plan.areaTotal) {
			index := selected
			if plan.areaTotal > 24 {
				index = selected * (plan.areaTotal - 1) / 23
			}
			selectedIntervals = append(selectedIntervals, index)
		}
		visitBoundaryAreas(input.Native.DolbyVision, func(index int, interval boundaryAreaInterval) {
			if !slices.Contains(selectedIntervals, index) {
				return
			}
			span := interval.End - interval.Start - 1
			interval.initial = slices.Compact([]int{interval.Start, interval.Start + span/2, interval.End - 1})
			interval.indices = []int{interval.Start, interval.Start + span/4, interval.Start + span/2, interval.Start + 3*span/4, interval.End - 1}
			interval.indices = slices.Compact(interval.indices)
			plan.areas = append(plan.areas, interval)
			plan.areaSelected = append(plan.areaSelected, interval.indices...)
			for _, frame := range interval.indices {
				plan.report.Windows = append(plan.report.Windows, BoundaryWindow{First: frame, Last: frame})
			}
		})
	}
	if err := plan.finishWindows(); err != nil {
		return nil, fmt.Errorf("plan boundary analysis: %w", err)
	}
	return plan, nil
}

// SelectModes removes work already satisfied by retained measurements before
// launching any child. A plan is single-use and must not be changed during decode.
func (plan *BoundaryPlan) SelectModes(boundary, area bool) error {
	plan.report.Windows, plan.report.SelectedFrameIndices = nil, nil
	if !boundary {
		plan.report.Formats = nil
	}
	if !area {
		plan.areas, plan.areaSelected = nil, nil
		plan.areaTotal = 0
	}
	for _, format := range plan.report.Formats {
		for _, candidate := range boundaryCandidates(format) {
			plan.report.Windows = append(plan.report.Windows, BoundaryWindow{First: candidate - 4, Last: candidate + 3})
		}
	}
	for _, frame := range plan.areaSelected {
		plan.report.Windows = append(plan.report.Windows, BoundaryWindow{First: frame, Last: frame})
	}
	return plan.finishWindows()
}

func (plan *BoundaryPlan) finishWindows() error {
	slices.SortFunc(plan.report.Windows, func(a, b BoundaryWindow) int {
		if order := cmp.Compare(a.First, b.First); order != 0 {
			return order
		}
		return cmp.Compare(a.Last, b.Last)
	})
	var merged []BoundaryWindow
	for _, window := range plan.report.Windows {
		for frame := window.First; frame <= window.Last; frame++ {
			plan.report.SelectedFrameIndices = append(plan.report.SelectedFrameIndices, frame)
		}
		if len(merged) > 0 && window.First <= merged[len(merged)-1].Last {
			merged[len(merged)-1].Last = max(merged[len(merged)-1].Last, window.Last)
		} else {
			merged = append(merged, window)
		}
	}
	if len(plan.report.Windows) > boundaryMaxWindows {
		return errDecodedMapping
	}
	plan.report.Windows = merged
	slices.Sort(plan.report.SelectedFrameIndices)
	plan.report.SelectedFrameIndices = slices.Compact(plan.report.SelectedFrameIndices)
	if len(plan.report.SelectedFrameIndices) > boundaryMaxFrames {
		return errDecodedMapping
	}
	return nil
}

func visitBoundaryAreas(analysis *dovi.Analysis, visit func(int, boundaryAreaInterval)) {
	index := 0
	for start := 0; start < len(analysis.Pictures); {
		offsets, known := offsetsAt(analysis, start)
		end := start + 1
		for end < len(analysis.Pictures) {
			next, nextKnown := offsetsAt(analysis, end)
			if next != offsets || nextKnown != known {
				break
			}
			end++
		}
		visit(index, boundaryAreaInterval{
			Start:      start,
			End:        end,
			Known:      known,
			Expected:   offsets,
			Confidence: "unclear",
		})
		start = end
		index++
	}
}
