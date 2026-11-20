// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import "math"

// motionRaster retains the adopted V9 search and early rejection profile.
// Its input is independently reduced, unblurred 256-wide luma.
type motionRaster struct{ width, height int }

// MeasureMotion compares consecutive actual pictures; absent texture remains
// unavailable to the temporal classifier rather than evidence of stillness.
func MeasureMotion(a, b []byte, width, height int, dt float64) (speed, support, reliable float64, text bool) {
	m := motionRaster{width, height}
	if len(b) != width*height || width <= 0 || height <= 0 {
		return 0, 0, 0, false
	}
	speed, support, reliable = m.displacement(a, b, dt)
	return speed, support, reliable, m.textlike(b)
}

func motionAbs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
func (m motionRaster) sad(a, b []byte, x, y, dx, dy, limit int) int {
	w := m.width
	sum := 0
	for j := range 8 {
		for i := range 8 {
			sum += motionAbs(int(a[(y+j)*w+x+i]) - int(b[(y+j+dy)*w+x+i+dx]))
		}
		if sum > limit {
			return sum
		}
	}
	return sum
}
func (m motionRaster) displacement(a, b []byte, dt float64) (float64, float64, float64) {
	w, h := m.width, m.height
	if w < 40 || h < 40 || len(a) != w*h || len(b) != w*h || dt <= 0 || dt > .15 {
		return 0, 0, 0
	}
	valid, moving, total := 0, 0, 0
	type vector struct {
		x, y   int
		dx, dy float64
	}
	var vectors []vector
	speed := 0.0
	for y := 10; y < h-18; y += 16 {
		for x := 10; x < w-18; x += 16 {
			total++
			sum, squared := 0, 0
			for j := 0; j < 8; j += 2 {
				for i := 0; i < 8; i += 2 {
					v := int(a[(y+j)*w+x+i])
					sum += v
					squared += v * v
				}
			}
			if float64(squared)/16-math.Pow(float64(sum)/16, 2) < 9 {
				continue
			}
			zero := m.sad(a, b, x, y, 0, 0, 1<<30)
			best, bx, by := zero, 0, 0

			for dy := -8; dy <= 8 && zero >= 64*3; dy += 2 {
				for dx := -8; dx <= 8; dx += 2 {
					v := m.sad(a, b, x, y, dx, dy, best)
					if v < best {
						best, bx, by = v, dx, dy
					}
				}
			}
			cx, cy := bx, by
			for dy := max(-8, cy-1); dy <= min(8, cy+1); dy++ {
				for dx := max(-8, cx-1); dx <= min(8, cx+1); dx++ {
					v := m.sad(a, b, x, y, dx, dy, best)
					if v < best {
						best, bx, by = v, dx, dy
					}
				}
			}
			// Photometric quality and explicit improvement reject noise/exposure guesses.
			variance := float64(squared)/16 - math.Pow(float64(sum)/16, 2)
			if best > 64*12 || float64(best)/64 > math.Sqrt(variance)*.75 {
				continue
			}
			fx, fy, explained := m.fine(a, b, x, y, bx, by)
			candidateSpeed := math.Hypot((float64(bx)+fx)/float64(w), (float64(by)+fy)/float64(h)) / dt
			if candidateSpeed < .025 || !((bx != 0 || by != 0) && best*4 < zero*3 || explained >= .4) {
				valid++
				continue
			}
			// Alternative displacements must not explain the same block equally well.
			alternative := 1 << 30
			for dy := -8; dy <= 8; dy += 2 {
				for dx := -8; dx <= 8; dx += 2 {
					if motionAbs(dx-bx) <= 1 && motionAbs(dy-by) <= 1 {
						continue
					}
					alternative = min(alternative, m.sad(a, b, x, y, dx, dy, alternative))
				}
			}
			if float64(alternative-best) < 64*max(.35, .05*math.Sqrt(variance)) {
				continue
			}
			rx, ry, _ := m.fine(b, a, x+bx, y+by, -bx, -by)
			if math.Hypot(fx+rx, fy+ry) > .6 {
				continue
			}
			valid++
			v := math.Hypot((float64(bx)+fx)/float64(w), (float64(by)+fy)/float64(h)) / dt
			if v >= .025 && ((bx != 0 || by != 0) && best*4 < zero*3 || explained >= .4) {
				vectors = append(vectors, vector{x, y, float64(bx) + fx, float64(by) + fy})
			}
		}
	}
	for i, a := range vectors {
		neighbors := 0
		for j, b := range vectors {
			if i == j || motionAbs(a.x-b.x) > 16 || motionAbs(a.y-b.y) > 16 {
				continue
			}
			if math.Hypot(a.dx-b.dx, a.dy-b.dy) <= max(.6, .5*math.Hypot(a.dx, a.dy)) {
				neighbors++
			}
		}
		if neighbors >= 2 {
			moving++
			speed += math.Hypot(a.dx/float64(w), a.dy/float64(h)) / dt
		}
	}
	if moving > 0 {
		speed /= float64(moving)
	}
	return speed, float64(moving) / float64(total), float64(valid) / float64(total)
}

// fine estimates subpixel displacement from the local image gradient. A local
// exposure offset is removed; noise must explain genuine spatial change.
func (m motionRaster) fine(a, b []byte, x, y, dx, dy int) (float64, float64, float64) {
	w := m.width
	var xx, yy, xy, xt, yt, tt, gxsum, gysum, tsum float64
	for j := range 8 {
		for i := range 8 {
			p := (y+j+dy)*w + x + i + dx
			gx := float64(int(b[p+1])-int(b[p-1])) / 2
			gy := float64(int(b[p+w])-int(b[p-w])) / 2
			d := float64(int(b[p]) - int(a[(y+j)*w+x+i]))
			xx += gx * gx
			yy += gy * gy
			xy += gx * gy
			xt += gx * d
			yt += gy * d
			tt += d * d
			gxsum += gx
			gysum += gy
			tsum += d
		}
	}
	xx -= gxsum * gxsum / 64
	yy -= gysum * gysum / 64
	xy -= gxsum * gysum / 64
	xt -= gxsum * tsum / 64
	yt -= gysum * tsum / 64
	tt -= tsum * tsum / 64
	det := xx*yy - xy*xy
	if xx+yy < 64 || det < .005*(xx+yy)*(xx+yy) || tt < .01 {
		return 0, 0, 0
	}
	fx := (xy*yt - yy*xt) / det
	fy := (xy*xt - xx*yt) / det
	if math.Abs(fx) > 1 || math.Abs(fy) > 1 {
		return 0, 0, 0
	}
	explained := -(fx*xt + fy*yt) / tt
	return fx, fy, explained
}

func (m motionRaster) textlike(a []byte) bool {
	w, h := m.width, m.height
	black, bright, edges := 0, 0, 0
	for y := range h {
		for x := range w {
			v := a[y*w+x]
			if v < 15 {
				black++
			}
			if v > 130 {
				bright++
			}
			if x > 0 && motionAbs(int(v)-int(a[y*w+x-1])) > 90 {
				edges++
			}
		}
	}
	return float64(black)/float64(len(a)) > .78 && float64(bright)/float64(len(a)) > .005 && float64(edges)/float64(len(a)) > .009
}
