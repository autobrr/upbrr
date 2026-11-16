// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"math"
	"slices"

	"github.com/autobrr/upbrr/pkg/api"
)

// SceneMeasurement contains scalar evidence only; decoder pixels are released
// as soon as the next motion pair and sparse tile statistics are available.
type SceneMeasurement struct {
	Time, Tick                        float64
	CutScore, Speed, Moving, Reliable float64
	Text                              bool
	Sparse                            *SceneSpatialMeasurement
}

// SceneTile describes the same active-picture tile across observed pictures.
type SceneTile struct {
	Region                              api.ComparisonSceneRegion
	Qualified                           bool
	OriginalDeviation, BlurredDeviation float64
}

// SceneSpatialMeasurement shares histograms and one blur for dark and flat.
type SceneSpatialMeasurement struct {
	TextRows                                                                 []int
	Width, Height                                                            int
	Active                                                                   api.ComparisonSceneRegion
	Mean, DarkFraction, NearBlackFraction, OriginalContrast, BlurredContrast float64
	VisibilityContrast                                                       float64
	Tiles                                                                    []SceneTile
	CropCertain                                                              bool
}

// ClassifyScenes applies the frozen profile to actual observations in one
// continuous window. A cut, gap or unavailable context cannot supply support.
func ClassifyScenes(points []SceneMeasurement, renderer string) []*api.ComparisonSceneEvidence {
	out := make([]*api.ComparisonSceneEvidence, len(points))
	blocked := make([]bool, len(points))
	for i, p := range points {
		blocked[i] = p.Text
		if p.Sparse != nil && len(p.Sparse.TextRows) > 0 {
			blocked[i] = true
		}
		for _, q := range points {
			if (q.CutScore >= 10 || q.Text || q.Sparse != nil && len(q.Sparse.TextRows) > 0) && math.Abs(q.Time-p.Time) <= .25 {
				blocked[i] = true
			}
		}
	}
	for i, p := range points {
		e := &api.ComparisonSceneEvidence{
			Profile:  SceneProfile,
			Renderer: renderer,
			Eligible: !blocked[i],
			Motion:   api.ComparisonSceneCategory{State: "inconclusive", Reason: "insufficient_support"},
			Dark:     api.ComparisonSceneCategory{State: "inconclusive", Reason: "sparse_observation_unavailable"},
			Flat:     api.ComparisonSceneCategory{State: "inconclusive", Reason: "sparse_observation_unavailable"},
		}
		out[i] = e
		if blocked[i] {
			e.Reason = "title_or_transition"
			continue
		}
		// V9 temporal support is trailing, within a one-second same-shot span.
		start := i
		for start > 0 && p.Time-points[start-1].Time <= 1+1e-9 && !blocked[start-1] && points[start].Time-points[start-1].Time <= .15+1e-9 {
			start--
		}
		positive, run, longest := 0.0, 0, 0
		for j := max(1, start+1); j <= i; j++ {
			q := points[j]
			dt := q.Time - points[j-1].Time
			if dt <= 0 || dt > .15+1e-9 {
				run = 0
				continue
			}
			if q.Speed >= .07 && q.Moving >= .015 && q.Reliable >= .08 && q.Moving >= .03*q.Reliable && (q.Moving >= .06 || q.Speed >= .10) {
				positive += dt
				run++
				longest = max(longest, run)
			} else {
				run = 0
			}
		}
		e.MovingSupport, e.ReliableSupport = p.Moving, p.Reliable
		e.Motion.Score = p.Speed
		e.Motion.SupportSeconds = positive
		e.Motion.SupportStartSeconds, e.Motion.SupportEndSeconds = points[start].Time, p.Time
		if p.Reliable >= .08 {
			e.Motion.State = "rejected"
			e.Motion.Reason = "insufficient_movement"
		}
		if positive >= .5-1e-9 && longest >= 4 && p.Time > points[start].Time && positive/(p.Time-points[start].Time) >= .65 {
			e.Motion.State, e.Motion.Reason = "verified", ""
		}
		s := p.Sparse
		if s == nil {
			continue
		}
		e.Width, e.Height, e.Active = s.Width, s.Height, s.Active
		e.Mean, e.DarkFraction, e.NearBlackFraction = s.Mean, s.DarkFraction, s.NearBlackFraction
		e.OriginalContrast, e.BlurredContrast = s.OriginalContrast, s.BlurredContrast
		if !s.CropCertain {
			e.Eligible = false
			e.Reason = "active_picture_uncertain"
			continue
		}
		if p.Time-points[0].Time < .9 || points[len(points)-1].Time-p.Time < .9 {
			e.Eligible = false
			e.Reason = "transition_context_unavailable"
			continue
		}
		if s.NearBlackFraction >= .98 || max(s.OriginalContrast, s.VisibilityContrast) < 8 {
			e.Eligible = false
			e.Reason = "blank_or_low_detail"
			continue
		}
		// Fade/exposure detection uses observed sparse means over both sides.
		var times, means []float64
		for j, q := range points {
			if math.Abs(q.Time-p.Time) <= 1+1e-9 && q.Sparse != nil && !blocked[j] {
				times = append(times, q.Time)
				means = append(means, q.Sparse.Mean)
			}
		}
		if len(times) >= 3 && times[0] <= p.Time-.9 && times[len(times)-1] >= p.Time+.9 && fadeCorrelation(times, means) {
			e.Eligible = false
			e.Reason = "fade_or_exposure_change"
			continue
		}
		neighbors := func(radius float64) []int {
			var indices []int
			for j, q := range points {
				if q.Sparse != nil && math.Abs(q.Time-p.Time) <= radius+1e-9 && !blocked[j] && q.Sparse.CropCertain && q.Sparse.Active == s.Active {
					indices = append(indices, j)
				}
			}
			return indices
		}
		validSupport := func(indices []int) bool {
			if len(indices) < 3 || points[indices[len(indices)-1]].Time-points[indices[0]].Time < .5-1e-9 {
				return false
			}
			for j := 1; j < len(indices); j++ {
				if points[indices[j]].Time-points[indices[j-1]].Time > .3+1e-9 {
					return false
				}
			}
			for j := indices[0]; j <= indices[len(indices)-1]; j++ {
				if blocked[j] || j > indices[0] && points[j].Time-points[j-1].Time > .15+1e-9 {
					return false
				}
			}
			return true
		}
		dark := neighbors(.6)
		dark = slices.DeleteFunc(dark, func(j int) bool {
			q := points[j].Sparse
			return q.DarkFraction < .8 || q.NearBlackFraction >= .98 || q.OriginalContrast < 8 || q.BlurredContrast < 6
		})
		e.Dark.Score = s.DarkFraction
		e.Dark.State, e.Dark.Reason = "rejected", "insufficient_dark_structure"
		if s.DarkFraction >= .8 && s.BlurredContrast >= 6 && validSupport(dark) {
			e.Dark.State, e.Dark.Reason = "verified", ""
			e.Dark.SupportStartSeconds, e.Dark.SupportEndSeconds = points[dark[0]].Time, points[dark[len(dark)-1]].Time
			e.Dark.SupportSeconds = e.Dark.SupportEndSeconds - e.Dark.SupportStartSeconds
		}
		flat := neighbors(.3)
		e.Flat.State, e.Flat.Reason = "inconclusive", "insufficient_support"
		if validSupport(flat) {
			mask := make([]bool, len(s.Tiles))
			for k, t := range s.Tiles {
				hits := 0
				for _, j := range flat {
					tiles := points[j].Sparse.Tiles
					if k < len(tiles) && tiles[k].Region == t.Region && tiles[k].Qualified {
						hits++
					}
				}
				mask[k] = t.Qualified && hits*5 >= len(flat)*4
			}
			e.Regions, e.FlatArea, e.FlatDeviation = flatComponents(s.Tiles, mask)
			e.Flat.Score = float64(e.FlatArea) / float64(max(1, s.Active.Area))
			e.Flat.SupportStartSeconds, e.Flat.SupportEndSeconds = points[flat[0]].Time, points[flat[len(flat)-1]].Time
			e.Flat.SupportSeconds = e.Flat.SupportEndSeconds - e.Flat.SupportStartSeconds
			e.Flat.State, e.Flat.Reason = "rejected", "insufficient_flat_area"
			if e.FlatArea*20 >= s.Active.Area {
				e.Flat.State, e.Flat.Reason = "verified", ""
			}
		}
	}
	// A native refinement may fall between sparse measurements. Transfer only
	// qualifications supported by verified observations on both sides in the
	// same shot and geometry; never extrapolate through a cut, gap or edge.
	for i, p := range points {
		if p.Sparse != nil || !out[i].Eligible {
			continue
		}
		before, after := -1, -1
		for j := i - 1; j >= 0 && p.Time-points[j].Time <= .3+1e-9; j-- {
			if blocked[j] {
				break
			}
			if points[j].Sparse != nil {
				before = j
				break
			}
		}
		for j := i + 1; j < len(points) && points[j].Time-p.Time <= .3+1e-9; j++ {
			if blocked[j] {
				break
			}
			if points[j].Sparse != nil {
				after = j
				break
			}
		}
		if before < 0 || after < 0 || !out[before].Eligible || !out[after].Eligible || out[before].Active != out[after].Active {
			out[i].Eligible = false
			out[i].Reason = "spatial_support_unavailable"
			continue
		}
		motion := out[i].Motion
		moving, reliable := out[i].MovingSupport, out[i].ReliableSupport
		cloned := *out[before]
		cloned.Motion = motion
		cloned.MovingSupport, cloned.ReliableSupport = moving, reliable
		if out[before].Dark.State != "verified" || out[after].Dark.State != "verified" {
			cloned.Dark.State, cloned.Dark.Reason = "inconclusive", "between_sparse_observations"
		}
		if out[before].Flat.State != "verified" || out[after].Flat.State != "verified" {
			cloned.Flat.State, cloned.Flat.Reason = "inconclusive", "between_sparse_observations"
		}
		out[i] = &cloned
	}
	return out
}

func fadeCorrelation(t, y []float64) bool {
	lo, hi := slices.Min(y), slices.Max(y)
	if hi-lo < 8 {
		return false
	}
	var st, sy, stt, syy, sty float64
	for i, v := range t {
		v -= t[0]
		st += v
		sy += y[i]
		stt += v * v
		syy += y[i] * y[i]
		sty += v * y[i]
	}
	n := float64(len(t))
	den := (n*stt - st*st) * (n*syy - sy*sy)
	return den > 0 && math.Abs((n*sty-st*sy)/math.Sqrt(den)) >= .95
}

func flatComponents(tiles []SceneTile, mask []bool) ([]api.ComparisonSceneRegion, int, float64) {
	type component struct {
		region    api.ComparisonSceneRegion
		deviation float64
	}
	var components []component
	visited := make([]bool, len(tiles))
	for i := range tiles {
		if !mask[i] || visited[i] {
			continue
		}
		queue := []int{i}
		visited[i] = true
		c := component{region: tiles[i].Region}
		c.region.Area = 0
		right, bottom := c.region.X+c.region.Width, c.region.Y+c.region.Height
		for position := 0; position < len(queue); position++ {
			k := queue[position]
			r := tiles[k].Region
			c.region.X = min(c.region.X, r.X)
			c.region.Y = min(c.region.Y, r.Y)
			right = max(right, r.X+r.Width)
			bottom = max(bottom, r.Y+r.Height)
			c.region.Area += r.Area
			c.deviation += float64(r.Area) * tiles[k].BlurredDeviation
			for j, s := range tiles {
				if !visited[j] && mask[j] && motionAbs(r.X-s.Region.X) <= 32 && motionAbs(r.Y-s.Region.Y) <= 32 {
					visited[j] = true
					queue = append(queue, j)
				}
			}
		}
		if len(queue) < 2 {
			continue
		}
		c.region.Width, c.region.Height = right-c.region.X, bottom-c.region.Y
		components = append(components, c)
	}
	slices.SortFunc(components, func(a, b component) int { return b.region.Area - a.region.Area })
	regions := make([]api.ComparisonSceneRegion, 0, min(4, len(components)))
	area := 0
	deviation := 0.0
	for _, c := range components[:min(4, len(components))] {
		regions = append(regions, c.region)
		area += c.region.Area
		deviation += c.deviation
	}
	if area > 0 {
		deviation /= float64(area)
	}
	return regions, area, deviation
}
