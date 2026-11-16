// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"errors"
	"math"
	"slices"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/pkg/api"
)

// measureSceneRasters consumes two full-range planar 4:2:0 rasters. All sums
// fit int64 at admitted geometry. No pixels are retained in returned evidence.
func measureSceneRasters(original, blurred []byte, height int) (*comparisons.SceneSpatialMeasurement, error) {
	const width = 512
	if height < 32 || height > 2048 || height%2 != 0 || len(original) != width*height*3/2 || len(blurred) != len(original) {
		return nil, errors.New("invalid comparison analysis raster geometry")
	}
	active, certain := sceneActivePicture(original[:width*height], width, height)
	s := &comparisons.SceneSpatialMeasurement{
		Width:       width,
		Height:      height,
		Active:      active,
		CropCertain: certain,
	}
	if !certain {
		return s, nil
	}
	s.TextRows = sceneTextRows(original[:width*height], width, active)
	var histogram, blurHistogram [256]int
	sum, dark, black := int64(0), 0, 0
	for y := active.Y; y < active.Y+active.Height; y++ {
		for x := active.X; x < active.X+active.Width; x++ {
			v := original[y*width+x]
			histogram[v]++
			blurHistogram[blurred[y*width+x]]++
			sum += int64(v)
			if v < 64 {
				dark++
			}
			if v < 8 {
				black++
			}
		}
	}
	s.Mean = float64(sum) / float64(active.Area)
	s.DarkFraction = float64(dark) / float64(active.Area)
	s.NearBlackFraction = float64(black) / float64(active.Area)
	percentile := func(h [256]int, percent int) int {
		total := 0
		for v, n := range h {
			total += n
			if total >= max(1, active.Area*percent/100) {
				return v
			}
		}
		return 255
	}
	s.OriginalContrast = float64(percentile(histogram, 90) - percentile(histogram, 10))
	s.BlurredContrast = float64(percentile(blurHistogram, 90) - percentile(blurHistogram, 10))
	s.VisibilityContrast = float64(percentile(histogram, 99) - percentile(histogram, 1))
	for plane := range 2 {
		var chroma [256]int
		offset := width*height + plane*width*height/4
		for y := active.Y / 2; y < (active.Y+active.Height)/2; y++ {
			for x := active.X / 2; x < (active.X+active.Width)/2; x++ {
				chroma[original[offset+y*width/2+x]] += 4
			}
		}
		s.VisibilityContrast = max(s.VisibilityContrast, float64(percentile(chroma, 90)-percentile(chroma, 10)))
	}
	for y := active.Y; y < active.Y+active.Height; y += 32 {
		for x := active.X; x < active.X+active.Width; x += 32 {
			r := api.ComparisonSceneRegion{
				X:      x,
				Y:      y,
				Width:  min(32, active.X+active.Width-x),
				Height: min(32, active.Y+active.Height-y),
			}
			r.Area = r.Width * r.Height
			tile := comparisons.SceneTile{Region: r, Qualified: true}
			for plane := range 3 {
				pw, offset := width, 0
				tx, ty, tw, th := r.X, r.Y, r.Width, r.Height
				if plane > 0 {
					pw = width / 2
					offset = width*height + (plane-1)*width*height/4
					tx, ty = x/2, y/2
					tw, th = (r.Width+1)/2, (r.Height+1)/2
				}
				mean, sd := sceneTileStats(original[offset:], pw, tx, ty, tw, th)
				_, bsd := sceneTileStats(blurred[offset:], pw, tx, ty, tw, th)
				tile.OriginalDeviation = max(tile.OriginalDeviation, sd)
				tile.BlurredDeviation = max(tile.BlurredDeviation, bsd)
				if sd > 12 || bsd > 6 || plane == 0 && (mean < 16 || mean > 247) {
					tile.Qualified = false
				}
			}
			s.Tiles = append(s.Tiles, tile)
		}
	}
	return s, nil
}

// sceneTextRows finds aligned, small bright glyph-like components over either
// black or live-action backgrounds. A suspected text line is inconclusive for
// automatic selection; temporal blocking covers static and scrolling credits.
func sceneTextRows(p []byte, stride int, active api.ComparisonSceneRegion) []int {
	type glyph struct{ x, y, w, h int }
	var glyphs []glyph
	visited := make([]bool, len(p))
	for y := active.Y; y < active.Y+active.Height; y++ {
		for x := active.X; x < active.X+active.Width; x++ {
			index := y*stride + x
			if visited[index] || p[index] < 180 {
				continue
			}
			queue := []int{index}
			visited[index] = true
			left, right, top, bottom := x, x, y, y
			for i := 0; i < len(queue); i++ {
				position := queue[i]
				px, py := position%stride, position/stride
				left = min(left, px)
				right = max(right, px)
				top = min(top, py)
				bottom = max(bottom, py)
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						nx, ny := px+dx, py+dy
						if nx < active.X || nx >= active.X+active.Width || ny < active.Y || ny >= active.Y+active.Height {
							continue
						}
						n := ny*stride + nx
						if !visited[n] && p[n] >= 180 {
							visited[n] = true
							queue = append(queue, n)
						}
					}
				}
			}
			w, h := right-left+1, bottom-top+1
			if len(queue) >= 6 && len(queue) <= 500 && w >= 2 && w <= 32 && h >= 5 && h <= 36 && w <= 4*h {
				glyphs = append(glyphs, glyph{left, top, w, h})
			}
		}
	}
	slices.SortFunc(glyphs, func(a, b glyph) int { return a.x - b.x })
	var rows []int
	for _, anchor := range glyphs {
		count, left, right, last := 0, anchor.x, anchor.x, anchor.x
		for _, g := range glyphs {
			if math.Abs(float64(g.y-anchor.y)) > 5 || math.Abs(float64(g.h-anchor.h)) > 8 || g.x < anchor.x {
				continue
			}
			if g.x-last > 40 {
				break
			}
			count++
			right = g.x + g.w
			last = right
		}
		if count >= 6 && right-left >= active.Width/4 {
			center := anchor.y + anchor.h/2
			if !slices.ContainsFunc(rows, func(y int) bool { return math.Abs(float64(y-center)) < 12 }) {
				rows = append(rows, center)
			}
		}
	}
	return rows
}

func sceneTileStats(p []byte, stride, x, y, width, height int) (float64, float64) {
	var sum, squared int64
	for j := y; j < y+height; j++ {
		for i := x; i < x+width; i++ {
			v := int64(p[j*stride+i])
			sum += v
			squared += v * v
		}
	}
	n := float64(width * height)
	mean := float64(sum) / n
	// The selected reference rounds means and population deviations before
	// threshold comparisons (ties to even), including chroma and partial tiles.
	return math.Floor(mean + .5), math.RoundToEven(10*math.Sqrt(max(0, float64(squared)/n-mean*mean))) / 10
}

func sceneActivePicture(p []byte, w, h int) (api.ComparisonSceneRegion, bool) {
	rowBlack := func(y int) bool {
		n := 0
		for x := range w {
			if p[y*w+x] < 8 {
				n++
			}
		}
		return n*100 >= 99*w
	}
	columnBlack := func(x, top, bottom int) bool {
		n := 0
		for y := top; y < bottom; y++ {
			if p[y*w+x] < 8 {
				n++
			}
		}
		return n*100 >= 99*(bottom-top)
	}
	top, bottom, left, right := 0, h, 0, w
	for top < bottom && rowBlack(top) {
		top++
	}
	for bottom > top && rowBlack(bottom-1) {
		bottom--
	}
	if bottom <= top {
		return api.ComparisonSceneRegion{}, false
	}
	for left < right && columnBlack(left, top, bottom) {
		left++
	}
	for right > left && columnBlack(right-1, top, bottom) {
		right--
	}
	// Keep chroma alignment and exclude the blur footprint at credible bars.
	if top > 0 {
		top = (top + 5) / 2 * 2
	}
	if bottom < h {
		bottom = (bottom - 4) / 2 * 2
	}
	if left > 0 {
		left = (left + 5) / 2 * 2
	}
	if right < w {
		right = (right - 4) / 2 * 2
	}
	r := api.ComparisonSceneRegion{
		X:      left,
		Y:      top,
		Width:  right - left,
		Height: bottom - top,
	}
	r.Area = r.Width * r.Height
	return r, r.Width > 0 && r.Height > 0 && r.Area >= w*h/2
}
