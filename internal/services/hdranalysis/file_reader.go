// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// fileSource preserves errors encountered by buffered read-ahead, including when the parser seeks past buffered bytes.
type fileSource struct {
	io.ReadSeeker
	readErr error
}

func (s *fileSource) Read(p []byte) (int, error) {
	n, err := s.ReadSeeker.Read(p)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return n, io.EOF
		}
		s.readErr = fmt.Errorf("read HDR source: %w", err)
		return n, s.readErr
	}
	return n, nil
}

// fileReader coalesces small Matroska discovery reads while preserving logical seek offsets.
// Progress follows the pinned extractor's structure-discovery and collection passes, never the size probe.
type fileReader struct {
	ctx            context.Context
	source         *fileSource
	buffer         *bufio.Reader
	position       int64
	size           int64
	probed         bool
	readDiscovery  bool
	collecting     bool
	lastPhase      string
	lastReport     time.Time
	lastLogPercent int
	logger         api.Logger
	report         func(Progress)
}

func newFileReader(ctx context.Context, source io.ReadSeeker, size int64, logger api.Logger, report func(Progress)) *fileReader {
	tracked := &fileSource{ReadSeeker: source}
	return &fileReader{
		ctx:    ctx,
		source: tracked,
		buffer: bufio.NewReaderSize(tracked, 128),
		size:   size,
		logger: logger,
		report: report,
	}
}

func (r *fileReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, fmt.Errorf("read HDR source: %w", err)
	}
	n, err := r.buffer.Read(p)
	r.position += int64(n)
	if r.probed && n > 0 {
		r.readDiscovery = true
		r.progress()
	}
	if r.source.readErr != nil {
		err = r.source.readErr
	}
	return n, err
}

func (r *fileReader) Seek(offset int64, whence int) (int64, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, fmt.Errorf("seek HDR source: %w", err)
	}
	if r.source.readErr != nil {
		return r.position, r.source.readErr
	}
	target := offset
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		target += r.position
	case io.SeekEnd:
		target += r.size
	default:
		return r.position, errors.New("invalid HDR source seek origin")
	}
	if target < 0 {
		return r.position, errors.New("invalid negative HDR source seek")
	}
	prior := r.position
	if target >= prior && target-prior <= int64(r.buffer.Buffered()) {
		if _, err := r.buffer.Discard(int(target - prior)); err != nil {
			return prior, fmt.Errorf("seek buffered HDR source: %w", err)
		}
	} else {
		position, err := r.source.Seek(target, io.SeekStart)
		if err != nil {
			return r.position, fmt.Errorf("seek HDR source: %w", err)
		}
		if position != target {
			return r.position, io.ErrUnexpectedEOF
		}
		r.buffer.Reset(r.source)
	}
	r.position = target
	if whence == io.SeekEnd {
		r.probed = true
		return target, nil
	}
	if !r.collecting && r.probed && r.readDiscovery && whence == io.SeekStart && target == 0 && prior > 0 {
		r.collecting = true
		r.buffer = bufio.NewReaderSize(r.source, 256*1024)
	}
	if r.probed && r.readDiscovery {
		r.progress()
	}
	return target, nil
}

func (r *fileReader) progress() {
	if r.size <= 0 || r.report == nil || r.lastPhase == "assembling_metadata" {
		return
	}
	phase := "inspecting_structure"
	if r.collecting {
		phase = "reading_metadata"
		if r.position >= r.size {
			phase = "assembling_metadata"
		}
	}
	now := time.Now()
	if phase == r.lastPhase && now.Sub(r.lastReport) < 250*time.Millisecond && r.position < r.size {
		return
	}
	percent := int(min(r.size, r.position) * 100 / r.size)
	completed := percent / 5
	message := fmt.Sprintf("Inspecting MKV structure: %d%%", percent)
	if r.collecting {
		phase = "reading_metadata"
		completed = 20 + percent*65/100
		message = fmt.Sprintf("Reading HDR10+ metadata: %.1f / %.1f MiB (%d%%)", float64(min(r.size, r.position))/(1<<20), float64(r.size)/(1<<20), percent)
		if r.position >= r.size {
			phase, message = "assembling_metadata", "Ordering HDR10+ presentation frames"
		}
	}
	if r.logger != nil && (phase != r.lastPhase || percent >= r.lastLogPercent+5) {
		r.lastLogPercent = percent - percent%5
		r.logger.Debugf("HDR analysis: phase=%s progress=%d bytes=%d total_bytes=%d", phase, r.lastLogPercent, min(r.size, r.position), r.size)
	}
	r.lastPhase, r.lastReport = phase, now
	r.report(Progress{
		Phase:     phase,
		Completed: completed,
		Total:     100,
		Message:   message,
	})
}
