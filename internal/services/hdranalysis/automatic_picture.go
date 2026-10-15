// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"runtime"
	"slices"
	"strconv"
	"sync"

	dovi "github.com/Audionut/go-dovi-extract"

	"github.com/autobrr/upbrr/pkg/api"
)

const AutomaticMeasurementProfile = "hdr-auto-pq16-linear-area-v1"
const SampledMeasurementProfile = "hdr-auto-pq16-sampled-linear-area-v1"

// AutomaticFrame measures one presentation picture. FALL is the average of
// per-pixel MaxRGB after exact 2x2 linear-light reduction of the active picture.
// Metadata pairs are maximum/average in 10-bit PQ codes, with presence explicit.
type AutomaticFrame struct {
	Index              int                  `json:"index,omitempty"`
	PTS                MeasurementTimestamp `json:"pts"`
	MaxRGBNits         float64              `json:"maxRGBNits"`
	FALLNits           float64              `json:"fallNits"`
	MeanRGBPQ          [3]float64           `json:"meanRGBPQ"`
	ColourPixels       int                  `json:"colourPixels"`
	ChromaticityMean   [2]float64           `json:"chromaticityMean"`
	ChromaticityPixels int                  `json:"chromaticityPixels"`
	Borders            [4]int               `json:"borders"`
	BorderKnown        bool                 `json:"borderKnown"`
	SceneScore         float64              `json:"sceneScore"`
	Bright             bool                 `json:"bright"`
	DVPQ               [2]float64           `json:"dvPQ"`
	DVKnown            bool                 `json:"dvKnown"`
	HDR10PlusPQ        [2]float64           `json:"hdr10plusPQ"`
	HDR10PlusKnown     bool                 `json:"hdr10plusKnown"`
}

// AutomaticScene retains a separate sync result and contact sheet per format.
type AutomaticScene struct {
	Format       api.HDRNativeFormat    `json:"format"`
	Observations []SceneObservation     `json:"observations"`
	Thumbnails   []MeasurementThumbnail `json:"thumbnails"`
}

// AutomaticMeasurement retains measured curves and bounded visual samples,
// so rendering never requires another source read or video decode.
type AutomaticMeasurement struct {
	Sampling     *AutomaticSampling     `json:"sampling,omitempty"`
	Frames       []AutomaticFrame       `json:"frames"`
	Reference    []AutomaticFrame       `json:"reference,omitempty"`
	Scenes       []AutomaticScene       `json:"scenes,omitempty"`
	Areas        []AreaObservation      `json:"areas,omitempty"`
	Chromaticity [2][]ChromaticityPoint `json:"chromaticity"`
	Profile5     bool                   `json:"profile5"`
}

// AutomaticSampling records actual source coverage, independently of the number
// of selected RGB pictures. Cadence/origins prove paired source-frame alignment.
type AutomaticSampling struct {
	Step    int                     `json:"step"`
	Frames  [2]int                  `json:"frames"`
	Origins [2]MeasurementTimestamp `json:"origins"`
	Cadence *MeasurementTimestamp   `json:"cadence,omitempty"`
}

type cachedCut struct {
	index  int
	score  float64
	frames []MeasurementThumbnail
}

type automaticPictures struct {
	selectedOnly  bool
	sampleFirst   int
	sampleStep    int
	preview       *automaticPictures
	width, height int
	gray          []byte
	frames        []AutomaticFrame
	previous      []float64
	cuts          []cachedCut
	bucketWidth   int
	lut           [65536]float64
	chroma        []ChromaticityPoint
	chromaStep    int
	chromaBucket  int
	chromaOffset  int
	recent        []MeasurementThumbnail
	crop          *api.HDRCrop
}

func newAutomaticPictures(source api.HDRDecodedSourceEvidence, crop *api.HDRCrop) (*automaticPictures, error) {
	if source.Width < 2 || source.Width > 8192 || source.Height < 2 || source.Height > 4320 || source.Width%2 != 0 || source.Height%2 != 0 ||
		source.PixelAspectNumerator != 1 || source.PixelAspectDenominator != 1 || source.VideoStreams < 1 ||
		source.VideoStreamOrdinal < 0 || source.VideoStreamOrdinal >= source.VideoStreams || source.Codec != "hevc" ||
		source.BitDepth < 10 || source.BitDepth > 16 {
		return nil, errDecodedMapping
	}
	if int64(source.Width)*int64(source.Height)*8 > 256<<20 {
		return nil, errDecodedMapping
	}
	if crop != nil && (crop.Left < 0 || crop.Top < 0 || crop.Width < 2 || crop.Height < 2 || crop.Width%2 != 0 || crop.Height%2 != 0 ||
		crop.Left > source.Width-crop.Width || crop.Top > source.Height-crop.Height) {
		return nil, errDecodedMapping
	}
	p := &automaticPictures{
		width:       source.Width,
		height:      source.Height,
		gray:        make([]byte, source.Width*source.Height*2),
		bucketWidth: 64,
		crop:        crop,
		chromaStep:  720,
	}
	if numerator, err := strconv.ParseFloat(source.FrameRateNumerator, 64); err == nil {
		if denominator, err := strconv.ParseFloat(source.FrameRateDenominator, 64); err == nil &&
			finiteWithin(numerator/denominator, 1, 240) {
			p.chromaStep = int(math.Ceil(30 * numerator / denominator))
		}
	}
	p.chromaBucket = p.chromaStep
	for i := range p.lut {
		value, err := dovi.PQToNits(float64(i) / 65535)
		if err != nil {
			return nil, fmt.Errorf("create automatic PQ lookup: %w", err)
		}
		p.lut[i] = value
	}
	return p, nil
}

func (p *automaticPictures) pixel(data []byte, x, y int) ([3]float64, [3]uint16) {
	plane, offset := 2*p.width*p.height, 2*(y*p.width+x)
	codes := [3]uint16{binary.LittleEndian.Uint16(data[2*plane+offset:]), binary.LittleEndian.Uint16(data[offset:]),
		binary.LittleEndian.Uint16(data[plane+offset:])}
	return [3]float64{p.lut[codes[0]], p.lut[codes[1]], p.lut[codes[2]]}, codes
}

func (p *automaticPictures) thumbnail(data []byte, index int) MeasurementThumbnail {
	thumb := MeasurementThumbnail{
		Index:  index,
		Width:  sceneWidth,
		Height: sceneHeight,
		Luma:   make([]byte, sceneWidth*sceneHeight),
		RGB:    make([]byte, sceneWidth*sceneHeight*3),
	}
	// Uniform Reinhard preview followed by a BT.2020-to-BT.709 conversion.
	// These SDR thumbnails are visual cut evidence, never brightness evidence.
	for y := range sceneHeight {
		for x := range sceneWidth {
			rgb, codes := p.pixel(data, x*p.width/sceneWidth, y*p.height/sceneHeight)
			luminance := gradeLuminance(rgb)
			for channel := range 3 {
				rgb[channel] /= 100 + luminance
			}
			rec709 := [3]float64{1.660491*rgb[0] - .587641*rgb[1] - .072850*rgb[2],
				-.124550*rgb[0] + 1.132900*rgb[1] - .008349*rgb[2], -.018151*rgb[0] - .100579*rgb[1] + 1.118730*rgb[2]}
			pixel := y*sceneWidth + x
			for channel, value := range rec709 {
				thumb.RGB[3*pixel+channel] = byte(math.Round(255 * math.Pow(max(0, min(1, value)), 1/2.2)))
			}
			thumb.Luma[pixel] = byte(math.Round((.2627*float64(codes[0]) + .678*float64(codes[1]) + .0593*float64(codes[2])) / 257))
		}
	}
	return thumb
}

func (p *automaticPictures) observe(data []byte) error {
	return p.observeAt(data, len(p.frames))
}

func (p *automaticPictures) observeAt(data []byte, index int) error {
	if len(p.frames) >= MaxFrames || index < 0 || index >= MaxFrames || len(data) != p.width*p.height*6 {
		return errors.New("automatic picture measurement exceeds frame bounds")
	}
	if !p.selectedOnly {
		thumbnail := p.thumbnail(data, index)
		p.recent = append(p.recent, thumbnail)
		if len(p.recent) > 5 {
			p.recent = p.recent[1:]
		}
		for i := range p.cuts {
			if index > p.cuts[i].index && index <= p.cuts[i].index+3 {
				p.cuts[i].frames = append(p.cuts[i].frames, thumbnail)
			}
		}
	}
	gray := p.gray
	for offset := 0; offset < len(gray); offset += 2 {
		r, g, b := binary.LittleEndian.Uint16(data[2*len(gray)+offset:]), binary.LittleEndian.Uint16(data[offset:]),
			binary.LittleEndian.Uint16(data[len(gray)+offset:])
		luma := uint16(math.Round(0.2627*float64(r) + 0.678*float64(g) + 0.0593*float64(b)))
		binary.LittleEndian.PutUint16(gray[offset:], luma)
	}
	frame := AutomaticFrame{Index: index}
	frame.Borders, frame.BorderKnown = inferBorders(gray, p.width, p.height)
	if !frame.BorderKnown {
		frame.Borders = [4]int{}
	}
	if !p.selectedOnly {
		cue := reduceSceneLuma(gray, p.width, p.height)
		if p.previous != nil {
			floor, _ := dovi.NitsToPQ(1)
			bright := 0
			for i, value := range cue {
				frame.SceneScore += math.Abs(value - p.previous[i])
				if max(value, p.previous[i]) >= floor {
					bright++
				}
			}
			frame.SceneScore /= float64(len(cue))
			frame.Bright = bright*100 >= len(cue)
			p.cacheCut(index, frame.SceneScore)
		}
		p.previous = cue
	}
	left, right, top, bottom := 0, p.width, 0, p.height
	if frame.BorderKnown {
		left, right = frame.Borders[0], p.width-frame.Borders[1]
		top, bottom = frame.Borders[2], p.height-frame.Borders[3]
	}
	if p.crop != nil {
		left, right = p.crop.Left, p.crop.Left+p.crop.Width
		top, bottom = p.crop.Top, p.crop.Top+p.crop.Height
	}
	// Complete 2x2 footprints stay inside the measured active area. Odd border
	// offsets are recorded exactly; the at-most-one-pixel trim is explicit.
	right, bottom = right-(right-left)%2, bottom-(bottom-top)%2
	// shortcut: retain at most 16 distributed pixel clouds; all eligible pixels
	// contribute to each retained frame's mean, while scatter points are bounded.
	for index/p.chromaBucket >= 16 {
		p.chromaBucket *= 2
		kept := p.chroma[:0]
		lastBucket, lastFrame := -1, -1
		for _, point := range p.chroma {
			bucket := point.Index / p.chromaBucket
			if bucket != lastBucket || point.Index == lastFrame {
				kept = append(kept, point)
				lastBucket, lastFrame = bucket, point.Index
			}
		}
		p.chroma = kept
	}
	keepColour := !p.selectedOnly || (index-p.chromaOffset)%p.chromaStep == 0
	keepChroma := keepColour && (index-p.chromaOffset)%p.chromaStep == 0 &&
		(len(p.chroma) == 0 || p.chroma[len(p.chroma)-1].Index/p.chromaBucket != index/p.chromaBucket)
	stride := max(1, (right-left)*(bottom-top)/4096)
	workers := 1
	if (right-left)*(bottom-top) >= 512*512 {
		workers = min(4, runtime.GOMAXPROCS(0))
	}
	type rowResult struct {
		frame  AutomaticFrame
		points []ChromaticityPoint
		pixels int
	}
	results := make([]rowResult, workers)
	var group sync.WaitGroup
	for worker := range workers {
		first := top + 2*((bottom-top)/2*worker/workers)
		last := top + 2*((bottom-top)/2*(worker+1)/workers)
		measure := func() {
			results[worker].frame, results[worker].points, results[worker].pixels = p.measureRows(
				data,
				index,
				left,
				right,
				first,
				last,
				top,
				keepColour,
				keepChroma,
				stride,
			)
		}
		if workers == 1 {
			measure()
		} else {
			group.Go(measure)
		}
	}
	group.Wait()
	pixels := 0
	for _, result := range results {
		pixels += result.pixels
		frame.MaxRGBNits = max(frame.MaxRGBNits, result.frame.MaxRGBNits)
		frame.FALLNits += result.frame.FALLNits
		frame.ColourPixels += result.frame.ColourPixels
		frame.ChromaticityPixels += result.frame.ChromaticityPixels
		for channel := range 3 {
			frame.MeanRGBPQ[channel] += result.frame.MeanRGBPQ[channel]
		}
		for axis := range 2 {
			frame.ChromaticityMean[axis] += result.frame.ChromaticityMean[axis]
		}
	}
	if pixels == 0 {
		return errors.New("automatic measurement has no active pixels")
	}
	frame.FALLNits /= float64(pixels)
	for channel := range 3 {
		if frame.ColourPixels > 0 {
			frame.MeanRGBPQ[channel] /= float64(frame.ColourPixels)
		}
	}
	if frame.ChromaticityPixels > 0 {
		for axis := range 2 {
			frame.ChromaticityMean[axis] /= float64(frame.ChromaticityPixels)
		}
	}
	if keepChroma {
		retained := 0
		for _, result := range results {
			count := min(len(result.points), 4096-retained)
			p.chroma = append(p.chroma, result.points[:count]...)
			retained += count
		}
	}
	p.frames = append(p.frames, frame)
	return nil
}

// measureRows reads disjoint 2x2 footprints. Ordered reduction keeps results and
// retained cloud selection deterministic without copying the full RGB raster.
func (p *automaticPictures) measureRows(
	data []byte,
	index, left, right, top, bottom, origin int,
	keepColour, keepChroma bool,
	stride int,
) (AutomaticFrame, []ChromaticityPoint, int) {
	var frame AutomaticFrame
	var points []ChromaticityPoint
	pixels := 0
	for y := top; y < bottom; y += 2 {
		for x := left; x < right; x += 2 {
			var rgb, pq [3]float64
			for dy := range 2 {
				for dx := range 2 {
					values, codes := p.pixel(data, x+dx, y+dy)
					for channel := range 3 {
						rgb[channel] += values[channel] / 4
						if keepColour {
							pq[channel] += float64(codes[channel]) / (4 * 65535)
						}
					}
					if keepChroma && gradeLuminance(values) > .5 {
						var xyz [3]float64
						for axis := range 3 {
							for channel := range 3 {
								xyz[axis] += gradeRGBToXYZ[axis][channel] * values[channel]
							}
						}
						sum := xyz[0] + xyz[1] + xyz[2]
						frame.ChromaticityMean[0] += xyz[0] / sum
						frame.ChromaticityMean[1] += xyz[1] / sum
						frame.ChromaticityPixels++
						pixel := (y+dy-origin)*(right-left) + x + dx - left
						if pixel%stride == 0 && len(points) < 4096 {
							points = append(points, ChromaticityPoint{
								Index: index,
								Pixel: pixel,
								X:     xyz[0] / sum,
								Y:     xyz[1] / sum,
							})
						}
					}
				}
			}
			maximum := max(rgb[0], rgb[1], rgb[2])
			frame.MaxRGBNits = max(frame.MaxRGBNits, maximum)
			frame.FALLNits += maximum
			if keepColour && gradeLuminance(rgb) > 1 {
				for channel := range 3 {
					frame.MeanRGBPQ[channel] += pq[channel]
				}
				frame.ColourPixels++
			}
			pixels++
		}
	}
	return frame, points, pixels
}

func (p *automaticPictures) cacheCut(index int, score float64) {
	for index/p.bucketWidth >= 64 {
		p.bucketWidth *= 2
		merged := make([]cachedCut, 0, 64)
		for _, cut := range p.cuts {
			if len(merged) > 0 && merged[len(merged)-1].index/p.bucketWidth == cut.index/p.bucketWidth {
				if cut.score > merged[len(merged)-1].score {
					merged[len(merged)-1] = cut
				}
			} else {
				merged = append(merged, cut)
			}
		}
		p.cuts = merged
	}
	if len(p.cuts) > 0 && p.cuts[len(p.cuts)-1].index/p.bucketWidth == index/p.bucketWidth {
		if p.cuts[len(p.cuts)-1].score >= score {
			return
		}
		p.cuts = p.cuts[:len(p.cuts)-1]
	}
	cut := cachedCut{
		index:  index,
		score:  score,
		frames: slices.Clone(p.recent),
	}
	p.cuts = append(p.cuts, cut)
}

func (a *AutomaticMeasurement) validate() error {
	total := len(a.Frames)
	if sampling := a.Sampling; sampling != nil {
		if sampling.Step < 1 || sampling.Step > 240 || sampling.Frames[0] < 1 || sampling.Frames[0] > MaxFrames ||
			sampling.Frames[1] < 0 || sampling.Frames[1] > MaxFrames {
			return errors.New("invalid automatic sampling coverage")
		}
		total = sampling.Frames[0]
		for source, frames := range [][]AutomaticFrame{a.Frames, a.Reference} {
			if source == 1 && len(frames) == 0 {
				if sampling.Frames[1] != 0 || sampling.Origins[1] != (MeasurementTimestamp{}) || sampling.Cadence != nil {
					return errors.New("orphan automatic reference coverage")
				}
				continue
			}
			first := 0
			if source == 1 && len(frames) > 0 {
				first = frames[0].Index
			}
			if first < 0 || first >= sampling.Step || len(frames) != automaticSampleCount(sampling.Frames[source], first, sampling.Step) {
				return errors.New("incomplete automatic sampling coverage")
			}
			if len(frames) > 0 {
				origin, err := automaticTime(sampling.Origins[source])
				if err != nil {
					return err
				}
				stamp, err := automaticTime(frames[0].PTS)
				if source == 0 && (err != nil || stamp.rational().Cmp(origin.rational()) != 0) {
					return errors.New("sampled primary origin differs from its first picture")
				}
			}
			for index, frame := range frames {
				if frame.Index != first+index*sampling.Step {
					return errors.New("sampled picture indices differ from declared coverage")
				}
			}
		}
	}
	if len(a.Frames) == 0 || len(a.Frames) > MaxFrames || len(a.Reference) > MaxFrames || len(a.Scenes) > 2 || len(a.Areas) > 120 ||
		len(a.Chromaticity[0]) > 65536 || len(a.Chromaticity[1]) > 65536 {
		return errors.New("automatic measurement exceeds bounds")
	}
	for _, frames := range [][]AutomaticFrame{a.Frames, a.Reference} {
		var previous pictureTime
		for i, frame := range frames {
			stamp, err := automaticTime(frame.PTS)
			if err != nil || i > 0 && stamp.rational().Cmp(previous.rational()) <= 0 {
				return errors.New("invalid automatic presentation times")
			}
			previous = stamp
			if frame.ColourPixels < 0 || frame.ColourPixels > 8192*4320/4 || frame.ChromaticityPixels < 0 || frame.ChromaticityPixels > 8192*4320 {
				return errors.New("invalid automatic colour coverage")
			}
			for _, value := range frame.ChromaticityMean {
				if !finiteWithin(value, 0, 1) {
					return errors.New("invalid automatic chromaticity mean")
				}
			}
			if len(frame.PTS.Value) > 21 || len(frame.PTS.Timescale) > 20 || !finiteWithin(frame.MaxRGBNits, 0, 10000) ||
				!finiteWithin(frame.FALLNits, 0, frame.MaxRGBNits+0.0001) || !finiteWithin(frame.SceneScore, 0, 1) {
				return errors.New("invalid automatic frame measurement")
			}
			for _, value := range frame.MeanRGBPQ {
				if !finiteWithin(value, 0, 1) {
					return errors.New("invalid automatic RGB measurement")
				}
			}
			for _, value := range frame.Borders {
				if value < 0 || value > 8192 {
					return errors.New("invalid automatic border measurement")
				}
			}
			for _, values := range [][2]float64{frame.DVPQ, frame.HDR10PlusPQ} {
				for _, value := range values {
					if !finiteWithin(value, 0, 1023) {
						return errors.New("invalid automatic metadata overlay")
					}
				}
			}
		}
	}
	formats := make(map[api.HDRNativeFormat]bool)
	for _, scene := range a.Scenes {
		if formats[scene.Format] || !slices.Contains([]api.HDRNativeFormat{api.HDRNativeDolbyVision, api.HDRNativeHDR10Plus}, scene.Format) ||
			len(scene.Observations) > 60 || len(scene.Thumbnails) > 24 {
			return errors.New("invalid automatic scene evidence")
		}
		formats[scene.Format] = true
		for _, observation := range scene.Observations {
			if err := validateSceneObservation(observation, total); err != nil {
				return err
			}
		}
		for _, thumbnail := range scene.Thumbnails {
			if thumbnail.Index < 0 || thumbnail.Index >= total || thumbnail.Candidate < 0 || thumbnail.Candidate >= total ||
				thumbnail.Width != sceneWidth || thumbnail.Height != sceneHeight || len(thumbnail.Luma) != sceneWidth*sceneHeight ||
				len(thumbnail.RGB) != 0 && len(thumbnail.RGB) != sceneWidth*sceneHeight*3 {
				return errors.New("invalid automatic contact sheet")
			}
		}
	}
	for _, area := range a.Areas {
		if err := validateAreaObservation(area, total); err != nil {
			return err
		}
	}
	for source, points := range a.Chromaticity {
		frames := a.Frames
		if source == 1 {
			frames = a.Reference
		}
		for _, point := range points {
			total := len(frames)
			if a.Sampling != nil {
				total = a.Sampling.Frames[source]
				if _, found := slices.BinarySearchFunc(frames, point.Index, func(frame AutomaticFrame, index int) int { return frame.Index - index }); !found {
					return errors.New("chromaticity has no measured source frame")
				}
			}
			if err := validateChromaticityPoint(point, total, 8192*4320); err != nil {
				return err
			}
		}
	}
	return nil
}
