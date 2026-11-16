// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/autobrr/upbrr/internal/services/screenshots"
)

const comparisonInventoryExpandedLimit int64 = 1 << 30

var errComparisonInventoryFull = errors.New("comparison inventory limit exceeded")

// Each bounded batch is a complete gzip member. A full inventory can stop
// between batches without leaving a truncated compressed stream behind.
type comparisonInventoryEncoder struct {
	destination io.Writer
	ref         *comparisonSceneInventory
	buffer      comparisonInventoryBuffer
	writer      *gzip.Writer
	frames      int
	expanded    int64
}

type comparisonInventoryBuffer struct{ buffer bytes.Buffer }

func (b *comparisonInventoryBuffer) Write(p []byte) (int, error) {
	if len(p) > (8<<20)-b.buffer.Len() {
		return 0, errComparisonInventoryFull
	}
	n, err := b.buffer.Write(p)
	if err != nil {
		return n, fmt.Errorf("buffer comparison inventory: %w", err)
	}
	return n, nil
}

func (e *comparisonInventoryEncoder) write(frame screenshots.ComparisonSample) error {
	encoded, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("encode comparison inventory: %w", err)
	}
	if len(encoded) > 2<<20 || int64(len(encoded)+1) > comparisonInventoryExpandedLimit-e.expanded {
		return errComparisonInventoryFull
	}
	if e.writer == nil {
		e.writer = gzip.NewWriter(&e.buffer)
	}
	encoded = append(encoded, '\n')
	if _, err := e.writer.Write(encoded); err != nil {
		return fmt.Errorf("compress comparison inventory: %w", err)
	}
	e.expanded += int64(len(encoded))
	e.frames++
	if e.frames == 256 {
		return e.flush()
	}
	return nil
}

func (e *comparisonInventoryEncoder) flush() error {
	if e.writer == nil {
		return nil
	}
	err := e.writer.Close()
	e.writer = nil
	if err != nil {
		return fmt.Errorf("finish comparison inventory batch: %w", err)
	}
	if int64(e.buffer.buffer.Len()) > comparisonInventoryLimit-e.ref.Bytes {
		return errComparisonInventoryFull
	}
	if _, err := e.destination.Write(e.buffer.buffer.Bytes()); err != nil {
		return fmt.Errorf("write comparison inventory batch: %w", err)
	}
	e.ref.Bytes += int64(e.buffer.buffer.Len())
	e.buffer.buffer.Reset()
	e.frames = 0
	return nil
}
