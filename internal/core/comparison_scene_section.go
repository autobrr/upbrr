// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

const comparisonSectionSeconds = 32.0

func comparisonEligibleSection(frames []screenshots.ComparisonSample, anchor string) []screenshots.ComparisonSample {
	start, end := comparisons.SceneSectionBounds(comparisonSamples(frames), anchor)
	return frames[start:end]
}

// Quick discovery supplies a seed. Score its actual surrounding main scene in
// observed chunks, stopping at a guard, occurrence boundary or finite span.
// Deep uses its complete native inventory and never calls this decoder.
func scoreComparisonSection(
	ctx context.Context,
	inventory *comparisonSceneInventory,
	anchor screenshots.ComparisonSample,
	decode func(context.Context, float64, float64) ([]screenshots.ComparisonSample, error),
) ([]screenshots.ComparisonSample, error) {
	window := inventory.Summary.Selection
	center := anchor.RelativeTimestampSeconds
	start, end := max(window.Start, center-4), min(window.End, center+4)
	var frames []screenshots.ComparisonSample
	tick := 1e-9
	read := func(left, right float64) error {
		from, to := max(0, left-1), min(inventory.Summary.DurationSeconds, right+1)
		inventory.Summary.RequestedSeconds += to - from
		decoded, err := decode(ctx, from, to-from)
		if err != nil {
			inventory.Summary.RankingComplete = false
			inventory.Summary.Reasons = append(inventory.Summary.Reasons, "section_incomplete")
			return fmt.Errorf("score surrounding main scene: %w", err)
		}
		for _, frame := range decoded {
			time := frame.RelativeTimestampSeconds
			if frame.DurationTicks > 0 && frame.TimeBaseDenominator > 0 {
				tick = max(tick, float64(frame.TimeBaseNumerator)/float64(frame.TimeBaseDenominator))
				span := api.ComparisonTimeInterval{
					Start: time,
					End:   time + float64(frame.DurationTicks)*float64(frame.TimeBaseNumerator)/float64(frame.TimeBaseDenominator),
				}
				inventory.Covered = append(inventory.Covered, span)
				if frame.Evidence != nil && frame.Evidence.Eligible {
					inventory.Eligible = append(inventory.Eligible, span)
				}
			}
			if time >= left && time < right {
				frames = append(frames, frame)
			}
		}
		inventory.Covered = mergeComparisonCoverage(inventory.Covered, tick)
		inventory.Eligible = mergeComparisonCoverage(inventory.Eligible, tick)
		inventory.Summary.CoveredSeconds = 0
		for _, span := range inventory.Covered {
			inventory.Summary.CoveredSeconds += span.End - span.Start
		}
		inventory.Summary.EligibleSeconds = comparisonEligibleCoverage(inventory.Eligible, window, inventory.Summary.Excluded)
		frames, err = orderedComparisonSamples(frames)
		if err != nil {
			return err
		}
		return nil
	}
	if err := read(start, end); err != nil {
		return nil, err
	}
	for {
		section := comparisonEligibleSection(frames, anchor.FrameID)
		if len(section) == 0 {
			return nil, nil
		}
		left := section[0].FrameID == frames[0].FrameID && start > window.Start
		right := section[len(section)-1].FrameID == frames[len(frames)-1].FrameID && end < window.End
		remaining := comparisonSectionSeconds - (end - start)
		if !left && !right || remaining < .1 {
			return section, nil
		}
		if left && (!right || center-start <= end-center) {
			next := max(window.Start, start-min(8, remaining))
			if err := read(next, start); err != nil {
				return nil, err
			}
			start = next
		} else {
			next := min(window.End, end+min(8, remaining))
			if err := read(end, next); err != nil {
				return nil, err
			}
			end = next
		}
	}
}
