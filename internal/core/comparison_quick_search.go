// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"time"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

// A key retains native addressing facts but never the staged disc span list.
const comparisonQuickKeyBytes = 1152

// Only the initial and final archives are durable. Intermediate members belong
// exclusively to this search until its readers have finished.
type comparisonQuickSearch struct {
	base  *comparisonSceneInventory
	parts []*comparisonSceneInventory
	seen  map[string]comparisonKnownPicture
}

func (q *comparisonQuickSearch) memoryBytes() int64 {
	size := int64(len(q.seen)) * comparisonQuickKeyBytes
	for _, part := range q.parts {
		size += comparisonInventoryIndexBytes(part) + 1024
	}
	return size
}

func (q *comparisonQuickSearch) admits(next *comparisonSceneInventory, keys int) bool {
	var compressed, expanded int64
	var blocks int
	for _, part := range append(slices.Clone(q.parts), next) {
		compressed += part.Bytes
		blocks += len(part.Blocks)
		for _, block := range part.Blocks {
			expanded += block.Expanded
		}
	}
	return compressed <= comparisonInventoryLimit && expanded <= comparisonInventoryExpandedLimit && blocks <= comparisonInventoryBlockLimit &&
		q.memoryBytes()+int64(keys)*comparisonQuickKeyBytes+comparisonInventoryIndexBytes(next)+1024 <= 16<<20
}

func (q *comparisonQuickSearch) cleanup() {
	for i, part := range q.parts {
		if i > 0 {
			_ = os.Remove(part.Path)
		}
	}
}

func (q *comparisonQuickSearch) finish(ctx context.Context, owner string, retained *workflowMediaPrivateArtifacts) error {
	if len(q.parts) < 2 {
		return nil
	}
	defer q.cleanup()
	final := *retained.ComparisonInventory
	retained.ComparisonInventory = q.base
	final.segments, final.Blocks, final.IndexSHA256, final.Bytes = nil, nil, "", 0
	// Complete an already admitted prefix after the operation's cancellation.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	file, err := os.CreateTemp(owner, "scene-inventory-final-*")
	if err != nil {
		return fmt.Errorf("create final inventory: %w", err)
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(file.Name())
		}
	}()
	sum := sha256.New()
	for _, part := range q.parts {
		if err := copyComparisonMembers(ctx, io.MultiWriter(file, sum), part, &final); err != nil {
			return err
		}
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync final inventory: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close final inventory: %w", err)
	}
	final.SHA256, final.Path = hex.EncodeToString(sum.Sum(nil)), file.Name()
	sealComparisonInventoryIndex(&final)
	if err := validateComparisonInventoryIndex(&final); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("seal final inventory: %w", err)
	}
	if err := publishComparisonInventory(ctx, &final, owner); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("verify final comparison inventory: %w", err)
	}
	keep = true
	retained.ComparisonInventory = &final
	return nil
}

func copyComparisonMembers(ctx context.Context, destination io.Writer, part, final *comparisonSceneInventory) error {
	if err := validateComparisonInventoryIndex(part); err != nil {
		return err
	}
	file, err := os.Open(part.Path)
	if err != nil {
		return fmt.Errorf("open sealed comparison segment: %w", err)
	}
	defer file.Close()
	var size int64
	sum := sha256.New()
	for _, block := range part.Blocks {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("copy sealed comparison segment: %w", err)
		}
		if _, err := io.CopyN(io.MultiWriter(destination, sum), file, block.Bytes); err != nil {
			return fmt.Errorf("copy sealed comparison segment: %w", err)
		}
		size += block.Bytes
		block.Offset = final.Bytes
		final.Bytes += block.Bytes
		final.Blocks = append(final.Blocks, block)
	}
	stat, err := file.Stat()
	if err != nil || size != part.Bytes || stat.Size() != size || hex.EncodeToString(sum.Sum(nil)) != part.SHA256 {
		return errors.New("sealed comparison segment changed")
	}
	return nil
}

// Merge only the new sorted entries. The selector evaluates eligibility and
// neighboring content against the resulting chronological order on each pass.
func mergeSceneCandidates(base, more []comparisonSceneCandidate) []comparisonSceneCandidate {
	i, j := len(base)-1, len(more)-1
	base = append(base, more...)
	for end := len(base) - 1; j >= 0; end-- {
		if i >= 0 && compareSceneCandidates(base[i], more[j]) > 0 {
			base[end] = base[i]
			i--
		} else {
			base[end] = more[j]
			j--
		}
	}
	return base
}

// Stream Quick candidates in the same order as the compact index. Distributed
// windows can overlap or be appended out of order. Split dense ranges before
// returning anything from them so the selector sees each native record once.
func streamQuickRanking(ctx context.Context, ref *comparisonSceneInventory, owner string, visit func(comparisonSceneCandidate) bool) error {
	parts := ref.segments
	if len(parts) == 0 {
		parts = []*comparisonSceneInventory{ref}
	}
	var spans []api.ComparisonTimeInterval
	for _, part := range parts {
		for _, block := range part.Blocks {
			spans = append(spans, api.ComparisonTimeInterval{Start: block.Start, End: math.Nextafter(block.End, math.Inf(1))})
		}
	}
	var scan func(api.ComparisonTimeInterval) error
	active := true
	scan = func(span api.ComparisonTimeInterval) error {
		if !active {
			return nil
		}
		var batch []comparisonSceneCandidate
		const limit = 512
		if err := readComparisonInventoryRange(ctx, ref, owner, &span, func(frame screenshots.ComparisonSample) bool {
			batch = append(batch, comparisonRankCandidate(frame))
			return len(batch) <= limit
		}); err != nil {
			return err
		}
		if len(batch) > limit {
			mid := span.Start + (span.End-span.Start)/2
			if mid <= span.Start || mid >= span.End {
				return errors.New("comparison Quick ranking has too many records at one native time")
			}
			batch = nil
			if err := scan(api.ComparisonTimeInterval{Start: span.Start, End: mid}); err != nil {
				return err
			}
			return scan(api.ComparisonTimeInterval{Start: mid, End: span.End})
		}
		slices.SortFunc(batch, compareSceneCandidates)
		for _, candidate := range batch {
			if !visit(candidate) {
				active = false
				break
			}
		}
		return nil
	}
	for _, span := range mergeComparisonCoverage(spans, 0) {
		if err := scan(span); err != nil {
			return err
		}
	}
	return nil
}
