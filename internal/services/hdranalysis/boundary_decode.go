// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	dovi "github.com/Audionut/go-dovi-extract"

	"github.com/autobrr/upbrr/internal/services/ffmpegstream"
)

type boundaryWindowResult struct {
	diagnostics    string
	observations   map[int]SceneObservation
	pairs          map[int][]MeasurementThumbnail
	area           []BoundaryAreaSample
	areaThumbnails []MeasurementThumbnail
	previews       map[int][]byte
	invocation     BoundaryInvocation
	err            error
}

func boundaryThumbnail(index, candidate int, frame []byte) MeasurementThumbnail {
	thumbnail := MeasurementThumbnail{
		Index:     index,
		Candidate: candidate,
		Width:     sceneWidth,
		Height:    sceneHeight,
		Luma:      make([]byte, sceneWidth*sceneHeight),
	}
	for i := range thumbnail.Luma {
		//nolint:gosec // Rounded uint16 reduction is in [0,255].
		thumbnail.Luma[i] = byte((uint32(binary.LittleEndian.Uint16(frame[2*i:])) + 128) / 257)
	}
	return thumbnail
}

func scoreBoundary(candidate int, stamp pictureTime, ring [][]byte) SceneObservation {
	observation := SceneObservation{
		Candidate:  candidate,
		PTS:        measurementTimestamp(stamp),
		Confidence: "unclear",
	}
	lightFloor, _ := dovi.NitsToPQ(1)
	var bright [7]int
	for delta := range 7 {
		before, after := ring[delta], ring[delta+1]
		var difference int64
		for pixel := range sceneWidth * sceneHeight {
			a, b := int(binary.LittleEndian.Uint16(before[pixel*2:])), int(binary.LittleEndian.Uint16(after[pixel*2:]))
			difference += int64(max(a-b, b-a))
			if float64(max(a, b))/65535 >= lightFloor {
				bright[delta]++
			}
		}
		observation.Scores[delta] = float64(difference) / float64(sceneWidth*sceneHeight*65535)
	}
	ordered := observation.Scores
	slices.Sort(ordered[:])
	observation.Noise = max(.005, ordered[3])
	best := 0
	for i := 1; i < 7; i++ {
		if observation.Scores[i] > observation.Scores[best] {
			best = i
		}
	}
	observation.BestOffset = best - 3
	if bright[best]*100 >= sceneWidth*sceneHeight &&
		observation.Scores[best] > max(.02, 4*observation.Noise) && observation.Scores[best] >= 2*ordered[5] {
		observation.Confidence = "dominant_cut"
	}
	return observation
}

func boundaryPTSSelection(indices []int, times []pictureTime) (string, error) {
	values := make([]string, 0, len(indices))
	for _, index := range indices {
		ms, err := boundaryMilliseconds(times[index])
		if err != nil {
			return "", err
		}
		values = append(values, "eq(pts,"+strconv.FormatInt(ms, 10)+")")
	}
	return strings.Join(values, "+"), nil
}

func (d pictureDecoder) decodeBoundaryWindow(ctx context.Context, input DecodedInput, plan *BoundaryPlan, window BoundaryWindow, path string,
	proof boundaryClipProof, requested, threads int, reservation int64,
) boundaryWindowResult {
	return d.decodeBoundaryPictures(ctx, input, plan, window, path, proof, requested, threads, reservation, nil)
}

// previewIndices selects only displayed pictures after the luma pass has chosen pairs.
func (d pictureDecoder) decodeBoundaryPictures(ctx context.Context, input DecodedInput, plan *BoundaryPlan, window BoundaryWindow, path string,
	proof boundaryClipProof, requested, threads int, reservation int64, previewIndices []int,
) boundaryWindowResult {
	result := boundaryWindowResult{
		observations: make(map[int]SceneObservation),
		pairs:        make(map[int][]MeasurementThumbnail),
		invocation: BoundaryInvocation{
			ClipSHA256:       proof.sha256,
			CompressedBytes:  proof.bytes,
			VCLUnits:         proof.vcl,
			NALUnits:         proof.nals,
			RequestedThreads: requested,
			EffectiveThreads: threads,
			ReservedBytes:    reservation,
			State:            "failed",
		},
	}
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var indices, areaIndices []int
	for index := window.First; index <= window.Last; index++ {
		indices = append(indices, index)
		if slices.Contains(plan.areaSelected, index) {
			areaIndices = append(areaIndices, index)
		}
	}
	if len(previewIndices) > 0 {
		indices, areaIndices = previewIndices, nil
	}
	selected, err := boundaryPTSSelection(indices, plan.times)
	if err != nil {
		result.err = err
		return result
	}
	colour := decodedColourFilter(input.Source, false)
	graph := fmt.Sprintf("[0:V:0]showinfo@boundary=checksum=0,select='%s',%s,scale=%d:%d:flags=area[scene]", selected, colour, sceneWidth, sceneHeight)
	if len(areaIndices) > 0 {
		areaSelection, err := boundaryPTSSelection(areaIndices, plan.times)
		if err != nil {
			result.err = err
			return result
		}
		graph = fmt.Sprintf(
			"[0:V:0]showinfo@boundary=checksum=0,split=2[scene-in][area-in];[scene-in]select='%s',%s,scale=%d:%d:flags=area[scene];[area-in]select='%s',%s[area]",
			selected,
			colour,
			sceneWidth,
			sceneHeight,
			areaSelection,
			colour,
		)
	}
	pixelFormat := "gray16le"
	if len(previewIndices) > 0 {
		// Reduce before colour conversion; the full-resolution numeric branch is unchanged.
		graph = fmt.Sprintf("[0:V:0]showinfo@boundary=checksum=0,select='%s',scale=%d:%d:flags=area,%s[scene]",
			selected, sceneWidth, sceneHeight, automaticColourFilter(input.Source, false))
		pixelFormat = "gbrp16le"
	}
	args := []string{
		"-hide_banner",
		"-nostdin",
		"-nostats",
		"-loglevel",
		"info",
		"-y",
		"-copyts",
		"-filter_threads",
		"1",
		"-filter_complex_threads",
		"1",
		"-threads",
		strconv.Itoa(threads),
		"-hwaccel",
		"none",
		"-noautorotate",
		"-apply_cropping",
		"0",
		"-nofind_stream_info",
		"-export_side_data",
		"+film_grain",
		"-max_alloc",
		"268435456",
		"-max_pixels",
		strconv.Itoa(input.Source.Width * input.Source.Height),
		"-i",
		path,
		"-filter_complex",
		graph,
	}
	command := d.command
	if command == nil {
		command = exec.CommandContext
	}
	cmd := command(runCtx, d.executable, args...)
	diagnostics := &boundaryDiagnostic{source: input.Source}
	cmd.Stderr = diagnostics
	outputArguments := func(label string) []string {
		return []string{
			"-map",
			label,
			"-an",
			"-sn",
			"-dn",
			"-fps_mode",
			"passthrough",
			"-c:v",
			"rawvideo",
			"-threads:v",
			"1",
			"-pix_fmt",
			pixelFormat,
			"-f",
			"rawvideo",
		}
	}
	candidates, visuals := make(map[int]bool), make(map[int]bool)
	for _, format := range plan.report.Formats {
		for _, candidate := range boundaryCandidates(format) {
			candidates[candidate] = true
		}
		for _, visual := range format.VisualCandidates {
			visuals[visual.Candidate] = true
		}
	}
	outputs := []ffmpegstream.Output{{Arguments: outputArguments("[scene]"), Consume: func(reader io.Reader) error {
		ring := make([][]byte, 8)
		for i := range ring {
			ring[i] = make([]byte, sceneWidth*sceneHeight*2)
		}
		for position, index := range indices {
			if err := runCtx.Err(); err != nil {
				return fmt.Errorf("decode boundary window: %w", err)
			}
			frame := ring[position%8]
			if _, err := io.ReadFull(reader, frame); err != nil {
				return fmt.Errorf("read boundary preview: %w", err)
			}
			candidate := index - 3
			if position >= 7 && candidates[candidate] {
				ordered := make([][]byte, 8)
				for j := range ordered {
					ordered[j] = ring[(position-7+j)%8]
				}
				result.observations[candidate] = scoreBoundary(candidate, plan.times[candidate], ordered)
				if visuals[candidate] {
					result.pairs[candidate] = []MeasurementThumbnail{
						boundaryThumbnail(candidate-1, candidate, ordered[3]),
						boundaryThumbnail(candidate, candidate, ordered[4]),
					}
				}
			}
		}
		return boundaryOutputEOF(reader)
	}}}
	if len(previewIndices) > 0 {
		outputs[0].Consume = func(reader io.Reader) error {
			source := input.Source
			source.Width, source.Height = sceneWidth, sceneHeight
			preview, err := newAutomaticPictures(source, nil)
			if err != nil {
				return err
			}
			result.previews = make(map[int][]byte, len(indices))
			frame := make([]byte, sceneWidth*sceneHeight*6)
			for _, index := range indices {
				if err := runCtx.Err(); err != nil {
					return fmt.Errorf("decode boundary colour preview: %w", err)
				}
				if _, err := io.ReadFull(reader, frame); err != nil {
					return fmt.Errorf("read boundary colour preview: %w", err)
				}
				result.previews[index] = preview.thumbnail(frame, index).RGB
			}
			return boundaryOutputEOF(reader)
		}
	}
	if len(areaIndices) > 0 {
		outputs = append(outputs, ffmpegstream.Output{Arguments: outputArguments("[area]"), Consume: func(reader io.Reader) error {
			frame := make([]byte, input.Source.Width*input.Source.Height*2)
			for _, index := range areaIndices {
				if err := runCtx.Err(); err != nil {
					return fmt.Errorf("decode boundary window: %w", err)
				}
				if _, err := io.ReadFull(reader, frame); err != nil {
					return fmt.Errorf("read boundary raster: %w", err)
				}
				borders, bright := inferBorders(frame, input.Source.Width, input.Source.Height)
				result.area = append(result.area, BoundaryAreaSample{
					Index:        index,
					PTS:          measurementTimestamp(plan.times[index]),
					Borders:      borders,
					BrightEnough: bright,
				})
				for _, interval := range plan.areas {
					if index == interval.Start {
						luma := reduceSceneLuma(frame, input.Source.Width, input.Source.Height)
						thumbnail := MeasurementThumbnail{
							Index:     index,
							Candidate: index,
							Width:     sceneWidth,
							Height:    sceneHeight,
							Luma:      make([]byte, len(luma)),
						}
						for i, value := range luma {
							thumbnail.Luma[i] = byte(math.Round(value * 255))
						}
						result.areaThumbnails = append(result.areaThumbnails, thumbnail)
					}
				}
			}
			return boundaryOutputEOF(reader)
		}})
	}
	completed, runErr := ffmpegstream.Run(runCtx, cmd, outputs)
	result.err = errors.Join(
		runErr,
		completed.ProcessError,
		errors.Join(completed.Errors...),
		diagnostics.finish(plan.times[window.First:window.Last+1], proof.vcl),
	)
	result.diagnostics = string(diagnostics.tail)
	result.invocation.ExposedPictures = len(diagnostics.stamps)
	for _, stamp := range diagnostics.stamps {
		result.invocation.PTS = append(result.invocation.PTS, measurementTimestamp(stamp))
	}
	if result.err == nil {
		result.invocation.State = "complete"
	} else {
		result.observations, result.pairs, result.area, result.areaThumbnails = nil, nil, nil, nil
		result.previews = nil
		if ctx.Err() != nil {
			result.invocation.State = "canceled"
		}
	}
	return result
}

func boundaryOutputEOF(reader io.Reader) error {
	var extra [1]byte
	n, err := io.ReadFull(reader, extra[:])
	if n != 0 || !errors.Is(err, io.EOF) {
		return errDecodedOutput
	}
	return nil
}
