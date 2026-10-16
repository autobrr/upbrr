// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"encoding/binary"
	"math"
	"math/rand/v2"

	dovi "github.com/Audionut/go-dovi-extract"
)

// Linear BT.2020 RGB to CIE XYZ, derived from ITU-R BT.2020-2 primaries
// and D65 (x=0.3127, y=0.3290). Values operate on absolute linear nits.
var gradeRGBToXYZ = [3][3]float64{
	{0.6369580483012914, 0.1446169035862083, 0.1688809751641721},
	{0.2627002120112671, 0.6779980715188708, 0.05930171646986196},
	{0, 0.02807269304908743, 1.060985057710791},
}

const gradeHistogramBins = 4096

// GradeFrameStatistics records crop validity and absolute luminance in nits.
// MaxRGBNits and MeanYNits use valid 2x2 linear-light averages; pixel counters
// distinguish original samples, reduced samples and endpoint-roundoff corrections.
type GradeFrameStatistics struct {
	OriginalPixels int     `json:"originalPixels"`
	InvalidPixels  int     `json:"invalidPixels"`
	RoundoffPixels int     `json:"roundoffPixels"`
	ReducedPixels  int     `json:"reducedPixels"`
	ReducedInvalid int     `json:"reducedInvalid"`
	MaxRGBNits     float64 `json:"maxRGBNits"`
	MeanYNits      float64 `json:"meanYNits"`
}

// GradeDifference compares eligible reduced pixels in normalized PQ, in R/G/B order.
// SignedMeanPQ is B minus A. P95AbsolutePQ uses the upper edge of a 4096-bin histogram.
type GradeDifference struct {
	EligiblePixels int        `json:"eligiblePixels"`
	SignedMeanPQ   [3]float64 `json:"signedMeanPQ"`
	MeanAbsolutePQ [3]float64 `json:"meanAbsolutePQ"`
	P95AbsolutePQ  [3]float64 `json:"p95AbsolutePQ"`
	MaxAbsolutePQ  [3]float64 `json:"maxAbsolutePQ"`
}

// GradeObservation pairs original presentation indices and timestamps under the explicit frame offset.
type GradeObservation struct {
	IndexA     int                  `json:"indexA"`
	IndexB     int                  `json:"indexB"`
	PTSA       MeasurementTimestamp `json:"ptsA"`
	PTSB       MeasurementTimestamp `json:"ptsB"`
	A          GradeFrameStatistics `json:"a"`
	B          GradeFrameStatistics `json:"b"`
	Difference GradeDifference      `json:"difference"`
	Complete   bool                 `json:"complete"`
}

// ChromaticityPoint stores CIE x/y for a sampled pixel in the reduced common crop.
// Index is the original presentation index; Pixel is the row-major reduced-pixel index.
type ChromaticityPoint struct {
	Index int     `json:"index"`
	Pixel int     `json:"pixel"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

type gradeMeasurement struct {
	observations []GradeObservation
	points       [2][]ChromaticityPoint
}

func gradeLuminance(rgb [3]float64) float64 {
	return gradeRGBToXYZ[1][0]*rgb[0] + gradeRGBToXYZ[1][1]*rgb[1] + gradeRGBToXYZ[1][2]*rgb[2]
}

// Linear conversion may round just beyond the representable endpoints. The
// calibrated 0.01-nit endpoint tolerance is counted separately from invalid RGB.
func gradePixel(frame []byte, width, height, x, y int) ([3]float64, bool, bool) {
	var rgb [3]float64
	roundoff := false
	for channel, plane := range [3]int{2, 0, 1} { // raw gbrpf32le plane order
		offset := 4 * (plane*width*height + y*width + x)
		value := float64(math.Float32frombits(binary.LittleEndian.Uint32(frame[offset:])))
		if math.IsNaN(value) || math.IsInf(value, 0) || value < -0.01 || value > 10000.01 {
			return rgb, false, false
		}
		roundoff = roundoff || value < 0 || value > 10000
		rgb[channel] = min(10000, max(0, value))
	}
	return rgb, true, roundoff
}

func gradeReduce(frame []byte, width, height, x, y int, stats *GradeFrameStatistics) ([3]float64, bool) {
	var rgb [3]float64
	valid := true
	for dy := range 2 {
		for dx := range 2 {
			pixel, ok, roundoff := gradePixel(frame, width, height, x+dx, y+dy)
			if !ok {
				stats.InvalidPixels++
				valid = false
			}
			if roundoff {
				stats.RoundoffPixels++
			}
			for channel, value := range pixel {
				rgb[channel] += value / 4
			}
		}
	}
	stats.ReducedPixels++
	if !valid {
		stats.ReducedInvalid++
		return rgb, false
	}
	stats.MaxRGBNits = max(stats.MaxRGBNits, rgb[0], rgb[1], rgb[2])
	stats.MeanYNits += gradeLuminance(rgb)
	return rgb, true
}

// Colour reservoirs are bounded independently for each of at most twelve pairs.
// The deterministic generator makes the selected source-pixel positions auditable.
type colourReservoir struct {
	points []ChromaticityPoint
	limit  int
	seen   int
	random *rand.Rand
}

func (r *colourReservoir) observe(index, pixel int, rgb [3]float64) {
	if r.limit == 0 || gradeLuminance(rgb) < 1 {
		return
	}
	var xyz [3]float64
	for i, row := range gradeRGBToXYZ {
		for j, value := range row {
			xyz[i] += value * rgb[j]
		}
	}
	total := xyz[0] + xyz[1] + xyz[2]
	point := ChromaticityPoint{
		Index: index,
		Pixel: pixel,
		X:     xyz[0] / total,
		Y:     xyz[1] / total,
	}
	r.seen++
	if len(r.points) < r.limit {
		r.points = append(r.points, point)
	} else if selected := r.random.IntN(r.seen); selected < r.limit {
		r.points[selected] = point
	}
}

func gradeColourBudget(position, count int) int {
	representatives := min(12, count)
	for i := range representatives {
		selected := 0
		if representatives > 1 {
			selected = i * (count - 1) / (representatives - 1)
		}
		if selected == position {
			budget := 4096 / representatives
			if i < 4096%representatives {
				budget++
			}
			return budget
		}
	}
	return 0
}

func (p *gradeMeasurement) observe(position int, request pictureDecodeRequest, frame []byte) {
	width, height := request.crop.Width, request.crop.Height
	indexA, indexB := request.selected[0][position], request.selected[1][position]
	observation := GradeObservation{
		IndexA: indexA,
		IndexB: indexB,
		PTSA:   measurementTimestamp(request.times[0][indexA]),
		PTSB:   measurementTimestamp(request.times[1][indexB]),
		A:      GradeFrameStatistics{OriginalPixels: width * height},
		B:      GradeFrameStatistics{OriginalPixels: width * height},
	}
	budget := gradeColourBudget(position, len(request.selected[0]))
	reservoirs := [2]colourReservoir{}
	for i := range reservoirs {
		// Deterministic reservoir sampling is reproducible measurement selection, not security randomness.
		//nolint:gosec // Stable PCG seeds make persisted measurements reproducible.
		reservoirs[i] = colourReservoir{limit: budget, random: rand.New(rand.NewPCG(uint64(indexA)+0x6864726772616465, uint64(indexB)+uint64(i)))}
	}
	var histogram [3][gradeHistogramBins + 1]uint64
	for y := 0; y < height; y += 2 {
		for x := 0; x < width; x += 2 {
			a, validA := gradeReduce(frame, 2*width, height, x, y, &observation.A)
			b, validB := gradeReduce(frame, 2*width, height, width+x, y, &observation.B)
			pixel := y/2*(width/2) + x/2
			if validA {
				reservoirs[0].observe(indexA, pixel, a)
			}
			if validB {
				reservoirs[1].observe(indexB, pixel, b)
			}
			if !validA || !validB || max(gradeLuminance(a), gradeLuminance(b)) < 1 {
				continue
			}
			difference := &observation.Difference
			difference.EligiblePixels++
			for channel := range 3 {
				pqA, _ := dovi.NitsToPQ(a[channel])
				pqB, _ := dovi.NitsToPQ(b[channel])
				delta := pqB - pqA
				absolute := math.Abs(delta)
				difference.SignedMeanPQ[channel] += delta
				difference.MeanAbsolutePQ[channel] += absolute
				difference.MaxAbsolutePQ[channel] = max(difference.MaxAbsolutePQ[channel], absolute)
				histogram[channel][min(gradeHistogramBins, int(absolute*gradeHistogramBins))]++
			}
		}
	}
	observation.Complete = observation.Difference.EligiblePixels > 0
	for _, stats := range []*GradeFrameStatistics{&observation.A, &observation.B} {
		valid := stats.ReducedPixels - stats.ReducedInvalid
		if valid > 0 {
			stats.MeanYNits /= float64(valid)
		}
		observation.Complete = observation.Complete && stats.InvalidPixels*10000 <= stats.OriginalPixels
	}
	if count := observation.Difference.EligiblePixels; count > 0 {
		for channel := range 3 {
			observation.Difference.SignedMeanPQ[channel] /= float64(count)
			observation.Difference.MeanAbsolutePQ[channel] /= float64(count)
			threshold, cumulative := uint64((count*95+99)/100), uint64(0)
			for bin, value := range histogram[channel] {
				cumulative += value
				if cumulative >= threshold {
					observation.Difference.P95AbsolutePQ[channel] = min(observation.Difference.MaxAbsolutePQ[channel], float64(bin+1)/gradeHistogramBins)
					break
				}
			}
		}
	}
	for i := range reservoirs {
		p.points[i] = append(p.points[i], reservoirs[i].points...)
	}
	p.observations = append(p.observations, observation)
}
