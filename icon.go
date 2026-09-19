package main

import (
	"embed"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// Tray icon artwork. The SVG files in assets/nvidia are embedded and rasterized here with a
// tiny path renderer (no SVG library, so the exe stays self-contained):
//
//	replay.svg           Instant Replay logo      -> shown in green while monitoring is active
//	replay_disabled.svg  the same, struck through -> shown in red while paused
//
// Supported path commands: M L H V C S Q A Z (absolute and relative). Enough for these icons.

//go:embed assets/nvidia/replay.svg assets/nvidia/replay_disabled.svg
var iconFS embed.FS

var (
	colorActive = [3]byte{0x76, 0xB9, 0x00} // green: monitoring active
	colorPaused = [3]byte{0xA3, 0x16, 0x21} // red: paused
)

type point struct{ x, y float64 }

// shape is one <path>: its closed sub-paths flattened to polygons plus the fill rule.
type shape struct {
	polys   [][]point
	evenOdd bool
}

type glyph []shape

var (
	glyphActive = mustGlyph("assets/nvidia/replay.svg")
	glyphPaused = mustGlyph("assets/nvidia/replay_disabled.svg")
)

var (
	pathTagRe  = regexp.MustCompile(`<path\b([^>]*)/?>`)
	attrDRe    = regexp.MustCompile(`\bd="([^"]*)"`)
	attrRuleRe = regexp.MustCompile(`fill-rule="([^"]*)"`)
	numRe      = regexp.MustCompile(`^[-+]?(?:\d+\.?\d*|\.\d+)(?:[eE][-+]?\d+)?`)
)

func mustGlyph(name string) glyph {
	b, err := iconFS.ReadFile(name)
	if err != nil {
		panic(err)
	}
	var g glyph
	for _, m := range pathTagRe.FindAllStringSubmatch(string(b), -1) {
		d := attrDRe.FindStringSubmatch(m[1])
		if d == nil {
			continue
		}
		polys, err := parsePath(d[1])
		if err != nil {
			panic(fmt.Sprintf("%s: %v", name, err))
		}
		s := shape{polys: polys}
		if r := attrRuleRe.FindStringSubmatch(m[1]); r != nil && r[1] == "evenodd" {
			s.evenOdd = true
		}
		g = append(g, s)
	}
	if len(g) == 0 {
		panic(name + ": no paths")
	}
	return g
}

// ---- path parsing ----

type scanner struct {
	s string
	i int
}

func (sc *scanner) skipSep() {
	for sc.i < len(sc.s) && (sc.s[sc.i] == ' ' || sc.s[sc.i] == ',' || sc.s[sc.i] == '\n' || sc.s[sc.i] == '\t' || sc.s[sc.i] == '\r') {
		sc.i++
	}
}

// hasNumber reports whether the next token is a number (i.e. the command repeats).
func (sc *scanner) hasNumber() bool {
	sc.skipSep()
	return sc.i < len(sc.s) && numRe.MatchString(sc.s[sc.i:])
}

func (sc *scanner) num() (float64, error) {
	sc.skipSep()
	m := numRe.FindString(sc.s[sc.i:])
	if m == "" {
		return 0, fmt.Errorf("number expected at %d in %q", sc.i, sc.s)
	}
	sc.i += len(m)
	return strconv.ParseFloat(m, 64)
}

func (sc *scanner) flag() (bool, error) {
	sc.skipSep()
	if sc.i >= len(sc.s) || (sc.s[sc.i] != '0' && sc.s[sc.i] != '1') {
		return false, fmt.Errorf("flag expected at %d in %q", sc.i, sc.s)
	}
	f := sc.s[sc.i] == '1'
	sc.i++
	return f, nil
}

func (sc *scanner) nums(n int) ([]float64, error) {
	out := make([]float64, n)
	for k := range out {
		v, err := sc.num()
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

func parsePath(d string) ([][]point, error) {
	sc := &scanner{s: d}
	var polys [][]point
	var cur []point
	var pos, start, lastCtrl point
	var lastCmd byte
	flush := func() {
		if len(cur) > 2 {
			polys = append(polys, cur)
		}
		cur = nil
	}
	add := func(p point) { cur = append(cur, p); pos = p }

	for {
		sc.skipSep()
		if sc.i >= len(sc.s) {
			break
		}
		cmd := sc.s[sc.i]
		if (cmd >= 'a' && cmd <= 'z') || (cmd >= 'A' && cmd <= 'Z') {
			sc.i++
		} else {
			return nil, fmt.Errorf("command expected at %d in %q", sc.i, d)
		}
		rel := cmd >= 'a'
		up := cmd &^ 0x20
		off := func(p point) point {
			if rel {
				return point{pos.x + p.x, pos.y + p.y}
			}
			return p
		}
		if up == 'Z' {
			flush()
			pos = start
			lastCmd = 'Z'
			continue
		}
		first := true
		for first || sc.hasNumber() {
			first = false
			switch up {
			case 'M':
				v, err := sc.nums(2)
				if err != nil {
					return nil, err
				}
				flush()
				p := off(point{v[0], v[1]})
				add(p)
				start = p
				up = 'L' // further pairs are implicit line-tos
			case 'L':
				v, err := sc.nums(2)
				if err != nil {
					return nil, err
				}
				add(off(point{v[0], v[1]}))
			case 'H':
				v, err := sc.num()
				if err != nil {
					return nil, err
				}
				if rel {
					v += pos.x
				}
				add(point{v, pos.y})
			case 'V':
				v, err := sc.num()
				if err != nil {
					return nil, err
				}
				if rel {
					v += pos.y
				}
				add(point{pos.x, v})
			case 'C', 'S':
				var c1, c2, e point
				if up == 'C' {
					v, err := sc.nums(6)
					if err != nil {
						return nil, err
					}
					c1, c2, e = off(point{v[0], v[1]}), off(point{v[2], v[3]}), off(point{v[4], v[5]})
				} else {
					v, err := sc.nums(4)
					if err != nil {
						return nil, err
					}
					c1 = pos
					if lastCmd == 'C' || lastCmd == 'S' {
						c1 = point{2*pos.x - lastCtrl.x, 2*pos.y - lastCtrl.y}
					}
					c2, e = off(point{v[0], v[1]}), off(point{v[2], v[3]})
				}
				p0 := pos
				for k := 1; k <= 16; k++ {
					t := float64(k) / 16
					u := 1 - t
					add(point{
						u*u*u*p0.x + 3*u*u*t*c1.x + 3*u*t*t*c2.x + t*t*t*e.x,
						u*u*u*p0.y + 3*u*u*t*c1.y + 3*u*t*t*c2.y + t*t*t*e.y,
					})
				}
				lastCtrl = c2
			case 'Q':
				v, err := sc.nums(4)
				if err != nil {
					return nil, err
				}
				c, e := off(point{v[0], v[1]}), off(point{v[2], v[3]})
				p0 := pos
				for k := 1; k <= 12; k++ {
					t := float64(k) / 12
					u := 1 - t
					add(point{u*u*p0.x + 2*u*t*c.x + t*t*e.x, u*u*p0.y + 2*u*t*c.y + t*t*e.y})
				}
			case 'A':
				rx, err := sc.num()
				if err != nil {
					return nil, err
				}
				ry, _ := sc.num()
				rot, _ := sc.num()
				large, err := sc.flag()
				if err != nil {
					return nil, err
				}
				sweep, err := sc.flag()
				if err != nil {
					return nil, err
				}
				v, err := sc.nums(2)
				if err != nil {
					return nil, err
				}
				for _, p := range arcPoints(pos, rx, ry, rot, large, sweep, off(point{v[0], v[1]})) {
					add(p)
				}
			default:
				return nil, fmt.Errorf("unsupported command %q in %q", cmd, d)
			}
			lastCmd = up
		}
	}
	flush()
	return polys, nil
}

// arcPoints converts an SVG elliptical arc (endpoint form) into points along it,
// following the conversion in the SVG implementation notes (F.6.5).
func arcPoints(p0 point, rx, ry, rotDeg float64, large, sweep bool, p1 point) []point {
	if rx == 0 || ry == 0 || p0 == p1 {
		return []point{p1}
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	phi := rotDeg * math.Pi / 180
	cosP, sinP := math.Cos(phi), math.Sin(phi)
	dx, dy := (p0.x-p1.x)/2, (p0.y-p1.y)/2
	x1p, y1p := cosP*dx+sinP*dy, -sinP*dx+cosP*dy
	if l := x1p*x1p/(rx*rx) + y1p*y1p/(ry*ry); l > 1 {
		s := math.Sqrt(l)
		rx, ry = rx*s, ry*s
	}
	num := rx*rx*ry*ry - rx*rx*y1p*y1p - ry*ry*x1p*x1p
	den := rx*rx*y1p*y1p + ry*ry*x1p*x1p
	co := 0.0
	if den != 0 && num > 0 {
		co = math.Sqrt(num / den)
	}
	if large == sweep {
		co = -co
	}
	cxp, cyp := co*rx*y1p/ry, -co*ry*x1p/rx
	cx, cy := cosP*cxp-sinP*cyp+(p0.x+p1.x)/2, sinP*cxp+cosP*cyp+(p0.y+p1.y)/2
	ang := func(ux, uy, vx, vy float64) float64 {
		a := math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
		return a
	}
	th1 := ang(1, 0, (x1p-cxp)/rx, (y1p-cyp)/ry)
	dth := ang((x1p-cxp)/rx, (y1p-cyp)/ry, (-x1p-cxp)/rx, (-y1p-cyp)/ry)
	if !sweep && dth > 0 {
		dth -= 2 * math.Pi
	} else if sweep && dth < 0 {
		dth += 2 * math.Pi
	}
	const steps = 24
	out := make([]point, 0, steps)
	for k := 1; k <= steps; k++ {
		t := th1 + dth*float64(k)/steps
		x, y := rx*math.Cos(t), ry*math.Sin(t)
		out = append(out, point{cosP*x - sinP*y + cx, sinP*x + cosP*y + cy})
	}
	out[len(out)-1] = p1
	return out
}

// ---- rasterizing ----

// covers reports whether (x, y) is inside the shape (nonzero or even-odd fill rule).
func (s shape) covers(x, y float64) bool {
	wn, crossings := 0, 0
	for _, poly := range s.polys {
		for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
			a, b := poly[j], poly[i] // edge a -> b
			if (a.y > y) != (b.y > y) {
				xInt := a.x + (y-a.y)*(b.x-a.x)/(b.y-a.y)
				if x < xInt {
					crossings++
					if b.y > a.y {
						wn++
					} else {
						wn--
					}
				}
			}
		}
	}
	if s.evenOdd {
		return crossings%2 == 1
	}
	return wn != 0
}

func (g glyph) covers(x, y float64) bool {
	for _, s := range g {
		if s.covers(x, y) {
			return true
		}
	}
	return false
}

// renderIcon returns an n×n top-down BGRA image: the glyph in fg on an opaque black square.
// The glyphs use a 24 unit box and their artwork spans about 22 units (1..23). fill is the
// share of the icon that artwork should take: 1 = edge to edge, 0.78 = a visible black margin.
func renderIcon(n int, g glyph, fg [3]byte, fill float64) []byte {
	const ss = 4 // 4x4 supersampling for smooth edges at 16..32 px
	span := 22 / fill
	lo := 12 - span/2
	pix := make([]byte, n*n*4)
	for py := 0; py < n; py++ {
		for px := 0; px < n; px++ {
			hit := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := lo + (float64(px)+(float64(sx)+0.5)/ss)*span/float64(n)
					y := lo + (float64(py)+(float64(sy)+0.5)/ss)*span/float64(n)
					if g.covers(x, y) {
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

// How much of the icon the artwork fills: the tray icon keeps some black margin, the
// README logo is nearly edge to edge.
const (
	trayFill = 0.78
	logoFill = 0.96
)
