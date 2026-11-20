// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"math"
)

// Validate checks request-local intent before any media work begins.
func (p ComparisonPreferences) Validate() error {
	if p.ScanMode != "" && p.ScanMode != "fast" && p.ScanMode != "deep" {
		return errors.New("comparison scan mode must be fast or deep")
	}
	count := p.Count
	if count == 0 {
		count = 4
	}
	if count < 1 || count > 16 || p.FlatCount < 0 || p.FlatCount > count {
		return errors.New("comparison counts must be from 1 to 16, with each scene target from 0 to the row count")
	}
	for _, target := range []*int{p.MotionCount, p.DarkCount} {
		if target != nil && (*target < 0 || *target > count) {
			return errors.New("comparison scene targets must fit within the row count")
		}
	}
	if w := p.SelectionWindow; w != nil {
		finite := func(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
		if len(w.Excluded) > 128 || w.Start != nil && !finite(*w.Start) || w.End != nil && !finite(*w.End) ||
			w.Start != nil && w.End != nil && *w.Start >= *w.End {
			return errors.New("comparison selection bounds must be finite, nonnegative and ordered")
		}
		for _, interval := range w.Excluded {
			if !finite(interval.Start) || !finite(interval.End) || interval.Start >= interval.End {
				return errors.New("comparison exclusions must be finite, nonnegative and ordered")
			}
		}
	}
	return nil
}

// ComparisonTimeInterval uses main programme-relative presentation seconds.
type ComparisonTimeInterval struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// ComparisonSelectionWindow restricts automatic and explicitly selected frames.
// Nil endpoints retain the default narrative margins.
type ComparisonSelectionWindow struct {
	Start    *float64                 `json:"start,omitempty"`
	End      *float64                 `json:"end,omitempty"`
	Excluded []ComparisonTimeInterval `json:"excluded,omitempty"`
}

// ComparisonSceneCategory keeps availability distinct from a ranking score.
type ComparisonSceneCategory struct {
	State               string  `json:"state"`
	Reason              string  `json:"reason,omitempty"`
	Score               float64 `json:"score"`
	SupportSeconds      float64 `json:"supportSeconds"`
	SupportStartSeconds float64 `json:"supportStartSeconds"`
	SupportEndSeconds   float64 `json:"supportEndSeconds"`
}

// ComparisonSceneRegion describes measured active-picture coordinates.
type ComparisonSceneRegion struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
	Area   int `json:"area"`
}

// ComparisonSceneEvidence describes the main picture, independently of matching
// confidence. Only verified categories count toward the requested mix.
type ComparisonSceneEvidence struct {
	Profile           string                  `json:"profile"`
	Renderer          string                  `json:"renderer"`
	Eligible          bool                    `json:"eligible"`
	Reason            string                  `json:"reason,omitempty"`
	Width             int                     `json:"width"`
	Height            int                     `json:"height"`
	Active            ComparisonSceneRegion   `json:"active"`
	Dark              ComparisonSceneCategory `json:"dark"`
	Motion            ComparisonSceneCategory `json:"motion"`
	Flat              ComparisonSceneCategory `json:"flat"`
	Mean              float64                 `json:"mean"`
	DarkFraction      float64                 `json:"darkFraction"`
	NearBlackFraction float64                 `json:"nearBlackFraction"`
	OriginalContrast  float64                 `json:"originalContrast"`
	BlurredContrast   float64                 `json:"blurredContrast"`
	MovingSupport     float64                 `json:"movingSupport"`
	ReliableSupport   float64                 `json:"reliableSupport"`
	FlatArea          int                     `json:"flatArea"`
	FlatDeviation     float64                 `json:"flatDeviation"`
	Regions           []ComparisonSceneRegion `json:"regions,omitempty"`
}

// ComparisonAnalysisSummary exposes coverage without paths or decoder witnesses.
type ComparisonAnalysisSummary struct {
	Version          int                      `json:"version"`
	Profile          string                   `json:"profile"`
	ScanMode         string                   `json:"scanMode"`
	Complete         bool                     `json:"complete"`
	RankingComplete  bool                     `json:"rankingComplete"`
	RequestedSeconds float64                  `json:"requestedSeconds"`
	CoveredSeconds   float64                  `json:"coveredSeconds"`
	EligibleSeconds  float64                  `json:"eligibleSeconds"`
	DurationSeconds  float64                  `json:"durationSeconds"`
	Selection        ComparisonTimeInterval   `json:"selection"`
	Excluded         []ComparisonTimeInterval `json:"excluded,omitempty"`
	Reasons          []string                 `json:"reasons,omitempty"`
}
