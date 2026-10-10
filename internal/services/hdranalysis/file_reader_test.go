// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBufferedHDRSourcePreservesLogicalReadsAndSeeks(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 40000)
	source := &countingHDRReader{ReadSeeker: bytes.NewReader(data)}
	reader := newFileReader(t.Context(), source, int64(len(data)), nil, nil)
	want := bytes.NewReader(data)
	for _, step := range []struct {
		offset       int64
		whence, size int
	}{
		{0, io.SeekStart, 8}, {0, io.SeekCurrent, 1}, {37, io.SeekCurrent, 100},
		{-20, io.SeekCurrent, 12}, {-10, io.SeekEnd, 20}, {0, io.SeekStart, 1},
		{300000, io.SeekCurrent, 32768}, {2, io.SeekStart, 0},
	} {
		position, err := reader.Seek(step.offset, step.whence)
		expected, expectedErr := want.Seek(step.offset, step.whence)
		if position != expected || !errors.Is(err, expectedErr) {
			t.Fatalf("seek=%d,%v want=%d,%v", position, err, expected, expectedErr)
		}
		actual, expectedData := make([]byte, step.size), make([]byte, step.size)
		n, err := io.ReadFull(reader, actual)
		expectedN, expectedErr := io.ReadFull(want, expectedData)
		if n != expectedN || !errors.Is(err, expectedErr) || !bytes.Equal(actual[:n], expectedData[:expectedN]) {
			t.Fatalf("read=%d,%v want=%d,%v", n, err, expectedN, expectedErr)
		}
	}
	if source.reads >= 8 {
		t.Fatalf("small reads were not coalesced: %d", source.reads)
	}
}

type countingHDRReader struct {
	io.ReadSeeker
	reads int
}

func (r *countingHDRReader) Read(p []byte) (int, error) {
	r.reads++
	n, err := r.ReadSeeker.Read(p)
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	if err != nil {
		return n, fmt.Errorf("read counted source: %w", err)
	}
	return n, nil
}

type failingHDRReader struct {
	io.ReadSeeker
	cause error
}

func (r failingHDRReader) Read(p []byte) (int, error) {
	n, _ := r.ReadSeeker.Read(p)
	return n, r.cause
}

func TestBufferedHDRSourcePreservesReadAheadFailureAndCancellation(t *testing.T) {
	cause := errors.New("source read failed")
	reader := newFileReader(t.Context(), failingHDRReader{ReadSeeker: bytes.NewReader(make([]byte, 1024)), cause: cause}, 1024, nil, nil)
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, cause) {
		t.Fatalf("read-ahead failure hidden: %v", err)
	}
	if _, err := reader.Seek(512, io.SeekStart); !errors.Is(err, cause) {
		t.Fatalf("seek discarded read-ahead failure: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	reader = newFileReader(ctx, bytes.NewReader(make([]byte, 1024)), 1024, nil, nil)
	if _, err := reader.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("buffer ignored cancellation: %v", err)
	}
	if _, err := reader.Seek(1, io.SeekCurrent); !errors.Is(err, context.Canceled) {
		t.Fatalf("seek ignored cancellation: %v", err)
	}
}

func TestHDRFileProgressExcludesSizeProbeAndTracksBothPasses(t *testing.T) {
	var updates []Progress
	reader := newFileReader(t.Context(), bytes.NewReader(make([]byte, 1024)), 1024, nil, func(p Progress) { updates = append(updates, p) })
	if _, err := reader.Read(make([]byte, 8)); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("size probe emitted progress: %#v", updates)
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Seek(1024, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	foundDiscovery, foundCollection := false, false
	prior := 0
	for _, p := range updates {
		if p.Completed < prior || p.Completed >= 100 || p.Total != 100 || p.Message == "" {
			t.Fatalf("invalid extraction progress: %#v", updates)
		}
		prior = p.Completed
		foundDiscovery = foundDiscovery || p.Phase == "inspecting_structure"
		foundCollection = foundCollection || p.Phase == "reading_metadata"
	}
	if !foundDiscovery || !foundCollection || prior != 85 {
		t.Fatalf("passes not visible: %#v", updates)
	}
}

func TestHDRFileProgressStartsCollectionAfterEarlyDiscovery(t *testing.T) {
	const size = 1 << 20
	var updates []Progress
	reader := newFileReader(t.Context(), bytes.NewReader(make([]byte, size)), size, nil, func(p Progress) { updates = append(updates, p) })
	if _, err := reader.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(reader, make([]byte, 256)); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if !reader.collecting || len(updates) < 2 || updates[len(updates)-1].Phase != "reading_metadata" || updates[len(updates)-1].Completed != 20 {
		t.Fatalf("early discovery rewind did not start collection: %#v", updates)
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		t.Fatal(err)
	}
	prior := 0
	for _, p := range updates {
		if p.Completed < prior || p.Completed >= 100 || p.Total != 100 || p.Message == "" {
			t.Fatalf("invalid early-discovery progress: %#v", updates)
		}
		prior = p.Completed
	}
	if prior != 85 || updates[len(updates)-1].Phase != "assembling_metadata" {
		t.Fatalf("collection did not reach assembly: %#v", updates)
	}
}

type hdrProgressLogger struct {
	api.NopLogger
	entries []string
}

func (l *hdrProgressLogger) Debugf(format string, args ...any) {
	l.entries = append(l.entries, fmt.Sprintf(format, args...))
}

func TestHDRFileProgressLogsFivePercentIntervalsForBothPasses(t *testing.T) {
	const size = 10000
	logger := &hdrProgressLogger{}
	var updates []Progress
	reader := newFileReader(t.Context(), bytes.NewReader(make([]byte, size)), size, logger, func(p Progress) { updates = append(updates, p) })
	if _, err := reader.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	if len(logger.entries) != 0 {
		t.Fatalf("size probe logged progress: %v", logger.entries)
	}
	for pass := range 2 {
		if _, err := reader.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		if pass == 0 {
			if _, err := reader.Read(make([]byte, 1)); err != nil {
				t.Fatal(err)
			}
		}
		for percent := 1; percent <= 100; percent++ {
			reader.lastReport = time.Time{}
			if _, err := reader.Seek(int64(percent*size/100), io.SeekStart); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(logger.entries) != 42 || len(updates) <= len(logger.entries) {
		t.Fatalf("debug log cadence changed UI updates: logs=%d updates=%d", len(logger.entries), len(updates))
	}
	for index, entry := range logger.entries {
		phase := "inspecting_structure"
		if index >= 21 {
			phase = "reading_metadata"
		}
		if index == 41 {
			phase = "assembling_metadata"
		}
		want := fmt.Sprintf("phase=%s progress=%d ", phase, index%21*5)
		if !strings.Contains(entry, want) || !strings.Contains(entry, "total_bytes=10000") {
			t.Fatalf("progress log %d = %q, want %q", index, entry, want)
		}
	}
}
