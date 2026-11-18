// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"errors"
	"fmt"
	"hash/adler32"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/autobrr/upbrr/internal/comparisons"
)

type comparisonRasterStreamKey struct{}

const comparisonSparseOutput = "comparison-statistics-output"

type comparisonCutMetric struct {
	pts   int64
	score float64
	seen  bool
}

type comparisonRasterFact struct {
	observation  ComparisonFrameObservation
	luma, pixels []byte
	spatial      *comparisons.SceneSpatialMeasurement
}

// Readers own one packet each; the coordinator alone accumulates measurements.
// Branches join by rational PTS, independently of their arrival order.
type comparisonRasterStream struct {
	ctx                                context.Context
	height, motionHeight, maxSparse    int
	retentionLimit                     int64
	matchAspect                        float64
	matchCrop                          *ComparisonCrop
	motionRecords, sparseRecords       chan ComparisonFrameObservation
	facts                              chan comparisonRasterFact
	done                               chan struct{}
	motion, sparse                     *comparisonRasterWriter
	cuts                               []comparisonCutMetric // diagnostic parser owns these until EOF
	measurements                       []comparisons.SceneMeasurement
	observations                       []ComparisonFrameObservation
	pixels                             [][]byte
	err                                error // coordinator-owned until done
	totalMotionBytes, totalSparseBytes int64
}

func (s *comparisonRasterStream) reset(ctx context.Context) {
	h, mh, aspect, crop, frames, limit := s.height, s.motionHeight, s.matchAspect, s.matchCrop, s.maxSparse, s.retentionLimit
	motionBytes, sparseBytes := s.totalMotionBytes, s.totalSparseBytes
	*s = comparisonRasterStream{
		ctx:              ctx,
		height:           h,
		motionHeight:     mh,
		matchAspect:      aspect,
		matchCrop:        crop,
		maxSparse:        frames,
		retentionLimit:   limit,
		motionRecords:    make(chan ComparisonFrameObservation, 16),
		sparseRecords:    make(chan ComparisonFrameObservation, 16),
		facts:            make(chan comparisonRasterFact, 1),
		done:             make(chan struct{}),
		totalMotionBytes: motionBytes,
		totalSparseBytes: sparseBytes,
	}
	s.motion = &comparisonRasterWriter{stream: s}
	s.sparse = &comparisonRasterWriter{stream: s, statistics: true}
}

func (s *comparisonRasterStream) start(ctx context.Context, cancel context.CancelCauseFunc) {
	s.ctx = ctx
	go func() {
		defer close(s.done)
		if err := s.collect(); err != nil {
			s.err = err
			cancel(err)
		}
	}()
}

func comparisonClockKey(o ComparisonFrameObservation) (string, error) {
	if o.TimeBaseNumerator <= 0 || o.TimeBaseDenominator <= 0 {
		return "", errors.New("comparison analysis lacks a rational clock")
	}
	n := new(big.Int).Mul(big.NewInt(o.PTS), big.NewInt(o.TimeBaseNumerator))
	return new(big.Rat).SetFrac(n, big.NewInt(o.TimeBaseDenominator)).RatString(), nil
}

func (s *comparisonRasterStream) observe(name string, o ComparisonFrameObservation) error {
	var records chan ComparisonFrameObservation
	switch name {
	case "showinfo@comparison_motion":
		records = s.motionRecords
	case "showinfo@comparison_stats":
		records = s.sparseRecords
	default:
		return nil
	}
	if o.PictureChecksum == "" || o.Width <= 0 || o.Height <= 0 {
		return errors.New("comparison analysis provenance is incomplete")
	}
	if _, err := comparisonClockKey(o); err != nil {
		return err
	}
	select {
	case records <- o:
		return nil
	case <-s.ctx.Done():
		return fmt.Errorf("comparison analysis interrupted: %w", context.Cause(s.ctx))
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
		s.cuts[len(s.cuts)-1].score, s.cuts[len(s.cuts)-1].seen = score, true
	}
	return nil
}

type comparisonRasterWriter struct {
	stream     *comparisonRasterStream
	statistics bool
	buffer     []byte
	count      int
	bytes      int64
	previous   ComparisonFrameObservation
	err        error
}

func (w *comparisonRasterWriter) Write(data []byte) (written int, returnedErr error) {
	defer func() {
		if w.err == nil && comparisonTerminalFailure(returnedErr) {
			w.err = returnedErr
		}
	}()
	s := w.stream
	size, records := 272*s.motionHeight, s.motionRecords
	if w.statistics {
		size, records = 1024*s.height*3/2, s.sparseRecords
	}
	count := len(data)
	for len(data) > 0 {
		n := min(size-len(w.buffer), len(data))
		w.buffer = append(w.buffer, data[:n]...)
		w.bytes += int64(n)
		data = data[n:]
		if len(w.buffer) < size {
			continue
		}
		var o ComparisonFrameObservation
		select {
		case record, ok := <-records:
			if !ok {
				if s.ctx.Err() != nil {
					return 0, fmt.Errorf("comparison raster interrupted: %w", context.Cause(s.ctx))
				}
				return 0, errors.New("comparison analysis raster lacks output provenance")
			}
			o = record
		case <-s.ctx.Done():
			return 0, fmt.Errorf("comparison raster interrupted: %w", context.Cause(s.ctx))
		}
		if w.count > 0 &&
			(o.TimeBaseNumerator != w.previous.TimeBaseNumerator || o.TimeBaseDenominator != w.previous.TimeBaseDenominator || o.PTS <= w.previous.PTS) {
			return 0, errors.New("comparison analysis reordered output provenance")
		}
		w.previous = o
		checksum, err := strconv.ParseUint(o.PictureChecksum, 16, 32)
		if err != nil || uint32(checksum) != comparisonRasterChecksum(w.buffer) {
			return 0, errors.New("comparison analysis raster changed its output witness")
		}
		fact := comparisonRasterFact{observation: o}
		if w.statistics {
			if o.Width != 1024 || o.Height != s.height || o.PixelFormat != "yuv420p" || w.count >= s.maxSparse {
				return 0, errors.New("comparison analysis changed sparse geometry or bounds")
			}
			fact.spatial, err = s.measureSparse(w.buffer)
			if err != nil {
				return 0, err
			}
		} else {
			if o.Width != 272 || o.Height != s.motionHeight || o.PixelFormat != "gray" || w.count >= 50000 {
				return 0, errors.New("comparison analysis changed motion geometry or bounds")
			}
			fact.luma, fact.pixels = make([]byte, 256*s.motionHeight), make([]byte, 256)
			for y := range s.motionHeight {
				copy(fact.luma[y*256:(y+1)*256], w.buffer[y*272:y*272+256])
			}
			for y := range 16 {
				copy(fact.pixels[y*16:(y+1)*16], w.buffer[y*272+256:y*272+272])
			}
		}
		select {
		case s.facts <- fact:
		case <-s.ctx.Done():
			return 0, fmt.Errorf("comparison raster interrupted: %w", context.Cause(s.ctx))
		}
		w.count++
		w.buffer = w.buffer[:0]
	}
	return count, nil
}

// Call only after both readers, the parser and the coordinator have joined.
// Cancellation-related pipe closure is not evidence failure; retained parser,
// raster and association errors must nevertheless survive parent cancellation.
func (s *comparisonRasterStream) failure(cause, diagnostic error) error {
	if comparisonTerminalFailure(cause) {
		return cause
	}
	for _, err := range []error{diagnostic, s.err, s.motion.err, s.sparse.err} {
		if comparisonTerminalFailure(err) {
			return err
		}
	}
	return cause
}

// FFmpeg showinfo starts Adler-32 at zero rather than RFC 1950's one.
func comparisonRasterChecksum(data []byte) uint32 {
	const modulus = 65521
	sum := adler32.Checksum(data)
	a := (sum&0xffff + modulus - 1) % modulus
	b := (sum>>16 + modulus - uint32(len(data)%modulus)) % modulus
	return b<<16 | a
}

func (s *comparisonRasterStream) measureSparse(data []byte) (*comparisons.SceneSpatialMeasurement, error) {
	left, right := make([]byte, 512*s.height*3/2), make([]byte, 512*s.height*3/2)
	for plane := range 3 {
		w, h, sourceOffset, destOffset := 512, s.height, 0, 0
		if plane > 0 {
			w, h = 256, s.height/2
			sourceOffset = 1024*s.height + (plane-1)*1024*s.height/4
			destOffset = 512*s.height + (plane-1)*512*s.height/4
		}
		for y := range h {
			copy(left[destOffset+y*w:destOffset+(y+1)*w], data[sourceOffset+y*w*2:sourceOffset+y*w*2+w])
			copy(right[destOffset+y*w:destOffset+(y+1)*w], data[sourceOffset+y*w*2+w:sourceOffset+(y+1)*w*2])
		}
	}
	return measureSceneRasters(s.ctx, left, right, s.height)
}

func (s *comparisonRasterStream) collect() error {
	indexes := map[string]int{}
	pending := map[string]*comparisons.SceneSpatialMeasurement{}
	var previous []byte
	previousTime := 0.0
	spatialBytes := int64(0)
	for {
		select {
		case <-s.ctx.Done():
			return fmt.Errorf("collect comparison rasters: %w", context.Cause(s.ctx))
		case fact, ok := <-s.facts:
			if !ok {
				if s.ctx.Err() != nil {
					return fmt.Errorf("collect comparison rasters: %w", context.Cause(s.ctx))
				}
				if len(pending) > 0 {
					return errors.New("comparison sparse raster has no native motion picture")
				}
				return nil
			}
			key, err := comparisonClockKey(fact.observation)
			if err != nil {
				return err
			}
			if fact.spatial != nil {
				spatialBytes += int64(len(fact.spatial.Tiles))*96 + int64(len(fact.spatial.TextRows))*8 + 512
				if i, ok := indexes[key]; ok {
					if s.measurements[i].Sparse != nil {
						return errors.New("duplicate comparison sparse raster")
					}
					s.measurements[i].Sparse = fact.spatial
				} else {
					if _, exists := pending[key]; exists {
						return errors.New("duplicate comparison sparse raster")
					}
					pending[key] = fact.spatial
				}
			} else {
				if _, exists := indexes[key]; exists {
					return errors.New("duplicate comparison motion raster")
				}
				p := comparisons.SceneMeasurement{
					Time:   fact.observation.TimestampSeconds,
					Tick:   float64(fact.observation.TimeBaseNumerator) / float64(fact.observation.TimeBaseDenominator),
					Sparse: pending[key],
				}
				delete(pending, key)
				if len(previous) > 0 {
					p.Speed, p.Moving, p.Reliable, p.Text = comparisons.MeasureMotion(previous, fact.luma, 256, s.motionHeight, p.Time-previousTime)
				}
				previous, previousTime = fact.luma, p.Time
				indexes[key] = len(s.measurements)
				s.measurements = append(s.measurements, p)
				s.observations = append(s.observations, fact.observation)
				s.pixels = append(s.pixels, fact.pixels)
			}
			limit := s.retentionLimit
			if limit <= 0 {
				limit = 112 << 20
			}
			if int64(len(s.measurements)+len(pending))*4096+spatialBytes > limit {
				return ErrComparisonFastInconclusive
			}
		}
	}
}

func (s *comparisonRasterStream) validate() error {
	if len(s.motion.buffer) > 0 || len(s.sparse.buffer) > 0 || len(s.motionRecords) > 0 || len(s.sparseRecords) > 0 || len(s.cuts) != len(s.measurements) {
		return errors.New("comparison analysis stream is incomplete")
	}
	previous := -1
	for i, m := range s.cuts {
		if !m.seen || m.pts != s.observations[i].PTS {
			return errors.New("comparison cut metrics changed native binding")
		}
		s.measurements[i].CutScore = m.score
		o := s.observations[i]
		expected := previous < 0 || float64(o.PTS-s.observations[previous].PTS)*float64(o.TimeBaseNumerator) >= .25*float64(o.TimeBaseDenominator)
		if expected != (s.measurements[i].Sparse != nil) {
			return errors.New("comparison sparse cadence changed native binding")
		}
		if expected {
			previous = i
		}
	}
	return nil
}

// Injected finite runners supply both outputs without binding a test listener.
func consumeInjectedAnalysis(ctx context.Context, s *comparisonRasterStream, result CommandResult, d *comparisonDiagnosticStream) error {
	workCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	s.start(workCtx, cancel)
	var readers sync.WaitGroup
	readers.Go(func() {
		_, err := d.Write(result.Stderr)
		if err == nil && workCtx.Err() == nil {
			_, err = d.result()
		}
		if err != nil {
			cancel(err)
		}
		close(s.motionRecords)
		close(s.sparseRecords)
	})
	for _, output := range []struct {
		writer *comparisonRasterWriter
		data   []byte
	}{{s.motion, result.Stdout}, {s.sparse, result.SparseOutput}} {
		readers.Go(func() {
			if _, err := output.writer.Write(output.data); err != nil {
				cancel(err)
			}
		})
	}
	readers.Wait()
	close(s.facts)
	<-s.done
	if err := s.failure(context.Cause(workCtx), d.err); err != nil {
		return fmt.Errorf("consume comparison analysis: %w", err)
	}
	return s.validate()
}

func comparisonSparseFrameLimit(seconds float64) int { return int(math.Ceil(seconds/.25)) + 1 }
