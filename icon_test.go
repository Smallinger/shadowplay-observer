package main

import "testing"

func TestGlyphsParseAndRender(t *testing.T) {
	for name, g := range map[string]glyph{"active": glyphActive, "paused": glyphPaused} {
		if len(g) == 0 {
			t.Fatalf("%s: no shapes", name)
		}
		for _, n := range []int{16, 24, 32} {
			pix := renderIcon(n, g, colorActive)
			lit := 0
			for i := 0; i < n*n; i++ {
				if pix[i*4+1] > 0 { // green channel
					lit++
				}
				if pix[i*4+3] != 255 {
					t.Fatalf("%s/%d: pixel %d not opaque", name, n, i)
				}
			}
			// The logo covers roughly a quarter to two thirds of the icon.
			if lit < n*n/8 || lit > n*n*3/4 {
				t.Errorf("%s/%d: %d of %d pixels lit, looks wrong", name, n, lit, n*n)
			}
		}
	}
}

func TestPaintedCentre(t *testing.T) {
	// The centre dot of the active glyph sits at (12,12) of the 24 unit box.
	if !glyphActive.covers(12, 12) {
		t.Error("active glyph: centre dot missing")
	}
	// The ring's hollow between dot and ring must stay empty.
	if glyphActive.covers(12, 12-5.5) {
		t.Error("active glyph: gap between dot and ring is filled")
	}
}
