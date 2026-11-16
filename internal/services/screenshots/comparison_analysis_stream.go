// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/comparisons"
)

type comparisonRasterStreamKey struct{}
type comparisonRasterRecord struct {
	kind        string
	observation ComparisonFrameObservation
}
type comparisonCutMetric struct {
	pts   int64
	score float64
	seen  bool
}

// comparisonRasterStream joins named branch provenance to output provenance
// before consuming a frame. Only one packed raster and the previous unblurred
// motion raster are retained; metrics and 16x16 witnesses are bounded scalars.
type comparisonRasterStream struct {
	ctx                  context.Context
	height, motionHeight int
	matchAspect          float64
	matchCrop            *ComparisonCrop
	buffer, previous     []byte
	previousTime         float64
	records              chan comparisonRasterRecord
	branches             map[string]comparisonRasterRecord
	cuts                 []comparisonCutMetric
	measurements         []comparisons.SceneMeasurement
	observations         []ComparisonFrameObservation
	pixels               [][]byte
	err                  error
	count                int
}

func (s *comparisonRasterStream) reset(ctx context.Context) {
	h, mh, aspect, crop := s.height, s.motionHeight, s.matchAspect, s.matchCrop
	*s = comparisonRasterStream{
		ctx:          ctx,
		height:       h,
		motionHeight: mh,
		matchAspect:  aspect,
		matchCrop:    crop,
		records:      make(chan comparisonRasterRecord, 16),
		branches:     map[string]comparisonRasterRecord{},
	}
}

func rasterRecordKey(o ComparisonFrameObservation) string {
	return fmt.Sprintf("%.6f/%s", o.TimestampSeconds, o.PictureChecksum)
}

func (s *comparisonRasterStream) observe(name string, o ComparisonFrameObservation) error {
	if strings.HasPrefix(name, "showinfo@") && (o.PictureChecksum == "" || o.Width <= 0 || o.Height <= 0) {
		return errors.New("comparison analysis provenance is incomplete")
	}
	if name == "showinfo@comparison_motion" || name == "showinfo@comparison_stats" {
		key := name + "/" + rasterRecordKey(o)
		if _, exists := s.branches[key]; exists {
			return errors.New("comparison analysis has conflicting branch identities")
		}
		s.branches[key] = comparisonRasterRecord{name, o}
		if len(s.branches) > 128 {
			return comparisonDecoderLimit("analysis_branch_limit")
		}
	}
	if name != "showinfo@comparison_output" {
		return nil
	}
	key := ""
	var record comparisonRasterRecord
	for candidate, value := range s.branches {
		expected := 2 * value.observation.TimestampSeconds
		if value.kind == "showinfo@comparison_stats" {
			expected += float64(value.observation.TimeBaseNumerator) / float64(value.observation.TimeBaseDenominator)
		}
		if value.observation.PictureChecksum == o.PictureChecksum && math.Abs(o.TimestampSeconds-expected) <= 1e-6 {
			if key != "" {
				return errors.New("comparison analysis has ambiguous output provenance")
			}
			key, record = candidate, value
		}
	}
	if key == "" {
		return errors.New("comparison analysis output has no matching branch identity")
	}
	delete(s.branches, key)
	select {
	case s.records <- record:
		return nil
	case <-s.ctx.Done():
		return fmt.Errorf("comparison scene analysis: %w", s.ctx.Err())
	}
}

var comparisonMetricHeader = regexp.MustCompile(`^frame:(\d+)\s+pts:(-?\d+)\s+pts_time:`)

func (s *comparisonRasterStream) metric(line string) error {
	_, body, ok := strings.Cut(line, "] ")
	if !ok {
		return errors.New("invalid comparison metric record")
	}
	if header := comparisonMetricHeader.FindStringSubmatch(body); len(header) > 0 {
		n, err := strconv.Atoi(header[1])
		if err != nil || n != len(s.cuts) || len(s.cuts) >= 50000 {
			return comparisonDecoderLimit("analysis_metric_limit")
		}
		if len(s.cuts) > 0 && !s.cuts[len(s.cuts)-1].seen {
			return errors.New("missing comparison cut metric")
		}
		pts, err := strconv.ParseInt(header[2], 10, 64)
		if err != nil {
			return fmt.Errorf("comparison scene analysis: %w", err)
		}
		s.cuts = append(s.cuts, comparisonCutMetric{pts: pts})
		return nil
	}
	if strings.HasPrefix(body, "lavfi.scd.score=") {
		if len(s.cuts) == 0 || s.cuts[len(s.cuts)-1].seen {
			return errors.New("duplicate or unbound comparison cut metric")
		}
		score, err := strconv.ParseFloat(strings.TrimPrefix(body, "lavfi.scd.score="), 64)
		if err != nil || !comparisonFinite(score) || score < 0 {
			return errors.New("invalid comparison cut metric")
		}
		s.cuts[len(s.cuts)-1].score = score
		s.cuts[len(s.cuts)-1].seen = true
	}
	return nil
}

func (s *comparisonRasterStream) Write(data []byte) (int, error) {
	count := len(data)
	size := 1024 * s.height * 3 / 2
	for len(data) > 0 {
		n := min(size-len(s.buffer), len(data))
		s.buffer = append(s.buffer, data[:n]...)
		data = data[n:]
		if len(s.buffer) < size {
			continue
		}
		var record comparisonRasterRecord
		select {
		case record = <-s.records:
		case <-s.ctx.Done():
			s.err = s.ctx.Err()
			return 0, s.err
		}
		if err := s.consume(record); err != nil {
			s.err = err
			return 0, err
		}
		s.buffer = s.buffer[:0]
		s.count++
		if s.count > 100000 {
			s.err = comparisonDecoderLimit("analysis_picture_limit")
			return 0, s.err
		}
	}
	return count, nil
}

func (s *comparisonRasterStream) consume(record comparisonRasterRecord) error {
	o := record.observation
	if o.Width != 1024 || o.Height != s.height || o.PixelFormat != "yuv420p" {
		return errors.New("comparison analysis changed output geometry")
	}
	if record.kind == "showinfo@comparison_motion" {
		luma := make([]byte, 256*s.motionHeight)
		for y := range s.motionHeight {
			copy(luma[y*256:(y+1)*256], s.buffer[y*1024:y*1024+256])
		}
		p := comparisons.SceneMeasurement{Time: o.TimestampSeconds, Tick: float64(o.TimeBaseNumerator) / float64(o.TimeBaseDenominator)}
		if len(s.previous) > 0 {
			p.Speed, p.Moving, p.Reliable, p.Text = comparisons.MeasureMotion(s.previous, luma, 256, s.motionHeight, p.Time-s.previousTime)
		}
		s.previous, s.previousTime = luma, p.Time
		s.measurements = append(s.measurements, p)
		if int64(len(s.measurements))*4096 > 112<<20 {
			return comparisonDecoderLimit("analysis_retention_limit")
		}
		s.observations = append(s.observations, o)
		// This independent FFmpeg reduction uses the same common-content
		// geometry as bounded matching; it never feeds motion or scene statistics.
		pixels := make([]byte, 256)
		for y := range 16 {
			copy(pixels[y*16:(y+1)*16], s.buffer[y*1024+256:y*1024+272])
		}
		s.pixels = append(s.pixels, pixels)
		return nil
	}
	// interleave emits the full-rate motion branch first on a tied PTS.
	i := len(s.measurements) - 1
	if i < 0 || math.Abs(s.measurements[i].Time-o.TimestampSeconds) > 1e-6 || s.measurements[i].Sparse != nil {
		return errors.New("unpaired comparison sparse raster")
	}
	left, right := make([]byte, 512*s.height*3/2), make([]byte, 512*s.height*3/2)
	for plane := range 3 {
		w, h, sourceOffset, destOffset := 512, s.height, 0, 0
		if plane > 0 {
			w, h = 256, s.height/2
			sourceOffset = 1024*s.height + (plane-1)*1024*s.height/4
			destOffset = 512*s.height + (plane-1)*512*s.height/4
		}
		for y := range h {
			copy(left[destOffset+y*w:destOffset+(y+1)*w], s.buffer[sourceOffset+y*w*2:sourceOffset+y*w*2+w])
			copy(right[destOffset+y*w:destOffset+(y+1)*w], s.buffer[sourceOffset+y*w*2+w:sourceOffset+(y+1)*w*2])
		}
	}
	spatial, err := measureSceneRasters(left, right, s.height)
	if err != nil {
		return err
	}
	s.measurements[i].Sparse = spatial

	return nil
}

func (s *comparisonRasterStream) validate() error {
	if len(s.buffer) > 0 || len(s.branches) > 0 || len(s.records) > 0 || len(s.cuts) != len(s.measurements) {
		return errors.New("comparison analysis stream is incomplete")
	}
	for i, m := range s.cuts {
		if !m.seen || m.pts != s.observations[i].PTS {
			return errors.New("comparison cut metrics changed native binding")
		}
		s.measurements[i].CutScore = m.score
	}
	// Actual native ticks verify sparse cadence, independently of FFmpeg's
	// floating-point select evaluation. Tick-boundary mistakes fail closed.
	previous := -1
	for i, p := range s.measurements {
		if p.Sparse != nil {
			if previous >= 0 {
				a, b := s.observations[previous], s.observations[i]
				if a.TimeBaseNumerator != b.TimeBaseNumerator || a.TimeBaseDenominator != b.TimeBaseDenominator || b.PTS-a.PTS < 1 ||
					float64(b.PTS-a.PTS)*float64(b.TimeBaseNumerator) < .25*float64(b.TimeBaseDenominator) {
					return errors.New("comparison sparse cadence is below 0.25 seconds")
				}
			}
			previous = i
		}
	}
	return nil
}

// consumeInjectedAnalysis preserves deterministic Runner seams without letting
// bounded stdout buffering become the native production transport.
func consumeInjectedAnalysis(ctx context.Context, s *comparisonRasterStream, data []byte, d *comparisonDiagnosticStream, stderr []byte) error {
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.ctx = workCtx
	d.cancel = cancel
	parse := make(chan error, 1)
	go func() {
		_, err := d.Write(stderr)
		if err == nil {
			_, err = d.result()
		}
		if err != nil {
			cancel()
		}
		parse <- err
	}()
	_, writeErr := s.Write(data)
	cancel()
	select {
	case parseErr := <-parse:
		if parseErr != nil && !errors.Is(parseErr, context.Canceled) {
			return parseErr
		}
		if writeErr != nil {
			return writeErr
		}
		return parseErr
	case <-ctx.Done():
		return fmt.Errorf("comparison scene analysis: %w", ctx.Err())
	}
}

// Keep the compiler checking the intended streaming writer contract.
var _ interface{ Write([]byte) (int, error) } = (*comparisonRasterStream)(nil)
