// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

// The index is bounded independently of compressed media. Its reservation is
// charged alongside the compact ranking index in the shared matching allowance.
const comparisonInventoryBlockLimit = 16384

type comparisonInventoryBlock struct {
	Offset, Bytes, Expanded int64
	Start, End              float64
	Frames                  int
	SHA256                  string
}

func comparisonInventoryIndexBytes(ref *comparisonSceneInventory) int64 {
	return int64(len(ref.Blocks)) * 160
}

func comparisonInventoryIndexDigest(ref *comparisonSceneInventory) string {
	data, err := json.Marshal(struct {
		FileSHA256 string
		Blocks     []comparisonInventoryBlock
	}{ref.SHA256, ref.Blocks})
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func sealComparisonInventoryIndex(ref *comparisonSceneInventory) {
	if len(ref.Blocks) > 0 {
		ref.IndexSHA256 = comparisonInventoryIndexDigest(ref)
	}
}

func validateComparisonInventoryIndex(ref *comparisonSceneInventory) error {
	if len(ref.Blocks) == 0 && ref.IndexSHA256 == "" {
		return nil // Legacy inventories gain their index during verified reuse.
	}
	if !ref.Compressed || len(ref.Blocks) == 0 || len(ref.Blocks) > comparisonInventoryBlockLimit ||
		ref.IndexSHA256 == "" || comparisonInventoryIndexDigest(ref) != ref.IndexSHA256 {
		return errors.New("comparison inventory index changed")
	}
	var offset, expanded int64
	for _, block := range ref.Blocks {
		digest, err := hex.DecodeString(block.SHA256)
		if block.Offset != offset || block.Bytes <= 0 || block.Bytes > ref.Bytes-offset ||
			block.Expanded <= 0 || block.Expanded > comparisonInventoryExpandedLimit-expanded ||
			block.Frames < 1 || block.Frames > 256 || block.Start < 0 || block.End < block.Start ||
			math.IsNaN(block.Start) || math.IsNaN(block.End) || math.IsInf(block.Start, 0) || math.IsInf(block.End, 0) ||
			err != nil || len(digest) != sha256.Size {
			return errors.New("comparison inventory index is invalid")
		}
		offset += block.Bytes
		expanded += block.Expanded
	}
	if offset != ref.Bytes {
		return errors.New("comparison inventory index is incomplete")
	}
	return nil
}

// A legacy archive is fully verified first. Building its small block directory
// then reads timestamps once and binds the directory to the same whole-file SHA.
// It never decodes the main video or changes a retained native witness.
func ensureComparisonInventoryIndex(ctx context.Context, ref *comparisonSceneInventory, owner string) error {
	if err := readComparisonInventory(ctx, ref, owner, func(screenshots.ComparisonSample) bool { return true }); err != nil {
		return err
	}
	if !ref.Compressed || ref.Bytes == 0 || len(ref.Blocks) > 0 {
		return nil
	}
	file, err := os.Open(ref.Path)
	if err != nil {
		return fmt.Errorf("open comparison inventory index: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	limited := &io.LimitedReader{R: io.TeeReader(file, hash), N: ref.Bytes}
	buffer := bufio.NewReader(limited)
	var blocks []comparisonInventoryBlock
	var expanded int64
	for {
		if _, err := buffer.Peek(1); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("read comparison inventory index: %w", err)
		}
		if len(blocks) >= comparisonInventoryBlockLimit {
			return errors.New("comparison inventory index exceeds its limit")
		}
		offset := ref.Bytes - limited.N - int64(buffer.Buffered())
		compressed, err := gzip.NewReader(buffer)
		if err != nil {
			return fmt.Errorf("open comparison inventory batch: %w", err)
		}
		compressed.Multistream(false)
		block, scanErr := scanComparisonInventoryBatch(ctx, compressed, comparisonInventoryExpandedLimit-expanded, nil, nil)
		closeErr := compressed.Close()
		if scanErr != nil {
			return scanErr
		}
		if closeErr != nil {
			return fmt.Errorf("close comparison inventory batch: %w", closeErr)
		}
		block.Offset = offset
		block.Bytes = ref.Bytes - limited.N - int64(buffer.Buffered()) - offset
		blockHash := sha256.New()
		if _, err := io.Copy(blockHash, io.NewSectionReader(file, block.Offset, block.Bytes)); err != nil {
			return fmt.Errorf("hash comparison inventory batch: %w", err)
		}
		block.SHA256 = hex.EncodeToString(blockHash.Sum(nil))
		expanded += block.Expanded
		blocks = append(blocks, block)
	}
	if limited.N != 0 || hex.EncodeToString(hash.Sum(nil)) != ref.SHA256 {
		return errors.New("comparison inventory content changed while indexing")
	}
	ref.Blocks = blocks
	sealComparisonInventoryIndex(ref)
	return validateComparisonInventoryIndex(ref)
}

func readIndexedComparisonInventory(
	ctx context.Context,
	file io.ReaderAt,
	ref *comparisonSceneInventory,
	window api.ComparisonTimeInterval,
	visit func(screenshots.ComparisonSample) bool,
) error {
	active := true
	for _, block := range ref.Blocks {
		if block.End < window.Start || block.Start >= window.End {
			continue
		}
		hash := sha256.New()
		input := bufio.NewReader(io.TeeReader(io.NewSectionReader(file, block.Offset, block.Bytes), hash))
		compressed, err := gzip.NewReader(input)
		if err != nil {
			return fmt.Errorf("open indexed comparison batch: %w", err)
		}
		compressed.Multistream(false)
		observed, scanErr := scanComparisonInventoryBatch(ctx, compressed, block.Expanded, &window, func(frame screenshots.ComparisonSample) bool {
			if active {
				active = visit(frame)
			}
			return active
		})
		closeErr := compressed.Close()
		if scanErr != nil {
			return scanErr
		}
		if closeErr != nil {
			return fmt.Errorf("close indexed comparison batch: %w", closeErr)
		}
		if _, err := input.Peek(1); !errors.Is(err, io.EOF) || observed.Frames != block.Frames || observed.Expanded != block.Expanded ||
			observed.Start != block.Start || observed.End != block.End || hex.EncodeToString(hash.Sum(nil)) != block.SHA256 {
			return errors.New("comparison inventory batch changed")
		}
	}
	return nil
}

func scanComparisonInventoryBatch(
	ctx context.Context,
	input io.Reader,
	limit int64,
	window *api.ComparisonTimeInterval,
	visit func(screenshots.ComparisonSample) bool,
) (comparisonInventoryBlock, error) {
	var block comparisonInventoryBlock
	scanner := bufio.NewScanner(io.LimitReader(input, limit+1))
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return block, fmt.Errorf("read comparison inventory batch: %w", err)
		}
		block.Expanded += int64(len(scanner.Bytes()) + 1)
		if block.Expanded > limit {
			return block, errors.New("comparison inventory batch expanded size exceeds its limit")
		}
		var header struct{ RelativeTimestampSeconds float64 }
		if err := json.Unmarshal(scanner.Bytes(), &header); err != nil || math.IsNaN(header.RelativeTimestampSeconds) ||
			math.IsInf(header.RelativeTimestampSeconds, 0) || header.RelativeTimestampSeconds < 0 {
			return block, errors.New("comparison inventory timestamp is invalid")
		}
		seconds := header.RelativeTimestampSeconds
		if block.Frames == 0 {
			block.Start, block.End = seconds, seconds
		}
		block.Start, block.End = min(block.Start, seconds), max(block.End, seconds)
		block.Frames++
		if visit == nil || window != nil && (seconds < window.Start || seconds >= window.End) {
			continue
		}
		frame, err := decodeComparisonInventoryFrame(scanner.Bytes())
		if err != nil {
			return block, err
		}
		visit(frame)
	}
	if err := scanner.Err(); err != nil {
		return block, fmt.Errorf("read comparison inventory batch: %w", err)
	}
	return block, nil
}
