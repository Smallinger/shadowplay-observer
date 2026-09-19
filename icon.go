package main

import "math"

// Tray icon artwork, drawn in code from assets/icon.svg (100x100 viewBox):
//
//	<circle cx=50 cy=50 r=13/>                                   centre dot
//	<path d="M 72.5 76.8 A 35 35 0 0 1 25.25 25.25 L 21 21 L 45 15 L 39 39 L 35.15 35.15
//	         A 21 21 0 0 0 63.5 66.1 Z"/>                        arrow, used twice (2nd rotated 180°)
//
// The arrow is an annular sector (r 21..35, from 50° to 225°, clockwise, y down) plus the
// arrow head polygon. Rendering it here avoids an SVG library and keeps the exe self-contained.

var (
	colorActive = [3]byte{0x76, 0xB9, 0x00} // green: monitoring active
	colorPaused = [3]byte{0xA3, 0x16, 0x21} // red: paused
)

var arrowHead = [][2]float64{{25.25, 25.25}, {21, 21}, {45, 15}, {39, 39}, {35.15, 35.15}}

func inPolygon(x, y float64, poly [][2]float64) bool {
	in := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		xi, yi, xj, yj := poly[i][0], poly[i][1], poly[j][0], poly[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

func inArrow(x, y float64) bool {
	dx, dy := x-50, y-50
	if r := math.Hypot(dx, dy); r >= 21 && r <= 35 {
		a := math.Atan2(dy, dx) * 180 / math.Pi
		if a < 0 {
			a += 360
		}
		if a >= 50 && a <= 225 {
			return true
		}
	}
	return inPolygon(x, y, arrowHead)
}

func inLogo(x, y float64) bool {
	if math.Hypot(x-50, y-50) <= 13 {
		return true
	}
	return inArrow(x, y) || inArrow(100-x, 100-y)
}

// renderIcon returns an n×n top-down BGRA image: the logo in fg on an opaque black square.
// The logo spans about 15..85 of the 100 unit box, so only 10..90 is shown to fill the icon.
func renderIcon(n int, fg [3]byte) []byte {
	const ss = 4 // 4x4 supersampling for smooth edges at 16..32 px
	const lo, span = 10.0, 80.0
	pix := make([]byte, n*n*4)
	for py := 0; py < n; py++ {
		for px := 0; px < n; px++ {
			hit := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := lo + (float64(px)+(float64(sx)+0.5)/ss)*span/float64(n)
					y := lo + (float64(py)+(float64(sy)+0.5)/ss)*span/float64(n)
					if inLogo(x, y) {
						hit++
					}
				}
			}
			cov := float64(hit) / (ss * ss)
			i := (py*n + px) * 4
			pix[i] = byte(float64(fg[2]) * cov) // B
			pix[i+1] = byte(float64(fg[1]) * cov)
			pix[i+2] = byte(float64(fg[0]) * cov) // R
			pix[i+3] = 255
		}
	}
	return pix
}
