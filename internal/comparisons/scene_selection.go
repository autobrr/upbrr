// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"cmp"
	"errors"
	"fmt"
	"iter"
	"math"
	"slices"

	"github.com/autobrr/upbrr/pkg/api"
)

// SceneProfile freezes the adopted V9 and sparse shared dark/flat semantics.
const SceneProfile = "v9-dark-flat5-sparse-yuv420-linear1024-3"

// SelectionRange resolves protected programme times. Explicit inspected bounds
// are authoritative; automatic discovery cannot expand this range.
func SelectionRange(p api.ComparisonPreferences, duration float64, television bool) (api.ComparisonTimeInterval, []api.ComparisonTimeInterval, error) {
	if err := p.Validate(); err != nil {
		return api.ComparisonTimeInterval{}, nil, fmt.Errorf("comparison scene analysis: %w", err)
	}
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return api.ComparisonTimeInterval{}, nil, errors.New("comparison duration is unavailable")
	}
	start := .05 * duration
	if television {
		start = .1 * duration
	}
	window := api.ComparisonTimeInterval{Start: start, End: .9 * duration}
	var excluded []api.ComparisonTimeInterval
	if w := p.SelectionWindow; w != nil {
		if w.Start != nil {
			window.Start = *w.Start
		}
		if w.End != nil {
			window.End = *w.End
		}
		excluded = slices.Clone(w.Excluded)
	}
	if window.Start >= window.End || window.End > duration {
		return window, nil, errors.New("comparison selection is outside the main programme")
	}
	slices.SortFunc(excluded, func(a, b api.ComparisonTimeInterval) int { return cmp.Compare(a.Start, b.Start) })
	var normalized []api.ComparisonTimeInterval
	for _, e := range excluded {
		if e.End > duration {
			return window, nil, errors.New("comparison exclusion is outside the main programme")
		}
		if len(normalized) > 0 && e.Start <= normalized[len(normalized)-1].End {
			normalized[len(normalized)-1].End = max(normalized[len(normalized)-1].End, e.End)
		} else {
			normalized = append(normalized, e)
		}
	}
	return window, normalized, nil
}

// EligibleTime is used again after native refinement and explicit selection.
func EligibleTime(t float64, window api.ComparisonTimeInterval, excluded []api.ComparisonTimeInterval) bool {
	if math.IsNaN(t) || math.IsInf(t, 0) || t < window.Start || t > window.End {
		return false
	}
	return !slices.ContainsFunc(excluded, func(e api.ComparisonTimeInterval) bool { return t >= e.Start && t <= e.End })
}

// SceneQualified is the single category contract for labels and accounting.
func SceneQualified(e *api.ComparisonSceneEvidence, category int) bool {
	if e == nil || e.Profile != SceneProfile || !e.Eligible {
		return false
	}
	return sceneCategory(e, category).State == "verified"
}

func sceneCategory(e *api.ComparisonSceneEvidence, category int) api.ComparisonSceneCategory {
	switch category {
	case 0:
		return e.Motion
	case 1:
		return e.Dark
	default:
		return e.Flat
	}
}

func sceneLess(a, b FrameSample, category int) bool {
	x, y := a.Evidence, b.Evidence
	c, d := sceneCategory(x, category), sceneCategory(y, category)
	// Unavailable measurements sort after measured alternatives.
	if (c.State == "inconclusive") != (d.State == "inconclusive") {
		return c.State != "inconclusive"
	}
	if c.Score != d.Score {
		return c.Score > d.Score
	}
	if category == 0 {
		if x.MovingSupport != y.MovingSupport {
			return x.MovingSupport > y.MovingSupport
		}
		if x.ReliableSupport != y.ReliableSupport {
			return x.ReliableSupport > y.ReliableSupport
		}
	}
	if category == 1 {
		if x.Mean != y.Mean {
			return x.Mean < y.Mean
		}
		if x.BlurredContrast != y.BlurredContrast {
			return x.BlurredContrast > y.BlurredContrast
		}
	}
	if category == 2 && x.FlatDeviation != y.FlatDeviation {
		return x.FlatDeviation < y.FlatDeviation
	}
	if c.SupportSeconds != d.SupportSeconds {
		return c.SupportSeconds > d.SupportSeconds
	}
	if a.TimestampSeconds != b.TimestampSeconds {
		return a.TimestampSeconds < b.TimestampSeconds
	}
	return a.FrameID < b.FrameID
}

// SelectScene streams a completed inventory, retaining only the best qualified
// and fallback candidate for each setting. Ranking never truncates global input.
func SelectScene(
	frames iter.Seq[FrameSample],
	used map[string]bool,
	selected []MatchedRow,
	targets, achieved, remaining [3]int,
	separation float64,
) (FrameSample, int, bool) {
	var best [2][3]*FrameSample
	var general *FrameSample
	var previous *matchingFrame
	generalInformative := false
	distance := func(t float64) float64 {
		d := math.Inf(1)
		for _, r := range selected {
			d = min(d, math.Abs(t-r.MainSeconds))
		}
		return d
	}
	for f := range frames {
		picture := matchingFrame{frame: f, signature: matchingSignature(f.Luma)}
		// Unchanged pictures cannot register native time. Prefer an observed
		// main-picture change for general rows, then maximize temporal spread.
		// This is a search hint; every target still needs its full local proof.
		informative := previous != nil && len(picture.signature) > 0 && len(previous.signature) > 0 &&
			f.Occurrence == previous.frame.Occurrence && f.TimestampSeconds > previous.frame.TimestampSeconds &&
			f.TimestampSeconds-previous.frame.TimestampSeconds <= 1 && visualScore(picture, *previous) < .97
		if len(picture.signature) > 0 {
			previous = &picture
		}
		if used[f.FrameID] || f.Evidence == nil || !f.Evidence.Eligible || distance(f.TimestampSeconds) < separation {
			continue
		}
		for category := range 3 {
			for phase := range 2 {
				if phase == 0 && !SceneQualified(f.Evidence, category) {
					continue
				}
				if best[phase][category] == nil || sceneLess(f, *best[phase][category], category) {
					best[phase][category] = new(f)
				}
			}
		}
		if general == nil || informative && !generalInformative ||
			informative == generalInformative && distance(f.TimestampSeconds) > distance(general.TimestampSeconds) {
			general = new(f)
			generalInformative = informative
		}
	}
	for phase := range 2 {
		chosen := -1
		bestUnmet, bestTarget := 0, 1
		for c := range 3 {
			if remaining[c] <= 0 || targets[c] <= achieved[c] || best[phase][c] == nil {
				continue
			}
			unmet := targets[c] - achieved[c]
			if chosen < 0 || unmet*bestTarget > bestUnmet*targets[c] {
				chosen, bestUnmet, bestTarget = c, unmet, targets[c]
			}
		}
		if chosen >= 0 {
			return *best[phase][chosen], chosen, true
		}
	}
	if general != nil {
		return *general, -1, true
	}
	return FrameSample{}, -1, false
}
