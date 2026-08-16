package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strconv"

	"golang.org/x/image/vector"
)

// The tray icon is a phone-style battery pictogram with the percentage beside
// it, rendered on a wide canvas.
//
// Fyne's desktop.App exposes only SetSystemTrayIcon / SetSystemTrayMenu with no
// text label next to the icon, so whatever the icon draws is all the panel
// shows. Panel indicators are scaled to the panel *height*, so a wide icon
// gains horizontal room for free and the digits get the full icon height
// instead of sharing a square with the bar. See trayrender.go for why this is
// rasterised to PNG rather than handed over as SVG.
//
// Digits are seven-segment shapes because there is no font rasteriser in play
// here — and Fyne's SVG renderer would not have drawn <text> either.

// TrayStyle selects what the panel indicator draws.
type TrayStyle string

const (
	TrayBarAndPercent TrayStyle = "bar+percent" // battery pictogram and number
	TrayBarOnly       TrayStyle = "bar"         // pictogram only
	TrayPercentOnly   TrayStyle = "percent"     // number only
)

// ParseTrayStyle maps a stored preference back to a style, falling back to the
// default for unknown values.
func ParseTrayStyle(s string) TrayStyle {
	switch TrayStyle(s) {
	case TrayBarOnly:
		return TrayBarOnly
	case TrayPercentOnly:
		return TrayPercentOnly
	default:
		return TrayBarAndPercent
	}
}

// Design-space geometry, height 100. Width depends on the style: a wide canvas
// is what buys the digits their full height (see the note above), but a
// single-element icon should stay compact instead of floating in dead space.
const (
	vbH = 100.0

	// The glyphs fill nearly the whole canvas height — an indicator that only
	// occupies the middle third of its box looks small next to the neighbouring
	// panel icons even when the box itself is large.
	bodyX  = 3.0
	bodyY  = 20.0
	bodyW  = 88.0
	bodyH  = 60.0
	stroke = 9.0
	inset  = 4.0
	nubW   = 9.0
	nubH   = 26.0

	digitsY  = 12.0
	digitsH  = 76.0
	digitGap = 7.0
	boltW    = 26.0

	// Output height. The tray scales to panel height anyway; this only sets
	// the resolution handed over, and 64 matches what Fyne uses for SVG.
	outH = 64
)

// layoutFor returns the design-space canvas width and the left edge of the
// number field for a style.
func layoutFor(style TrayStyle, charging bool) (canvasW, digitsX float64) {
	switch style {
	case TrayBarOnly:
		w := bodyX + bodyW + 2 + nubW + 4
		if charging {
			w += boltW
		}
		return w, 0
	case TrayPercentOnly:
		w := 100.0
		if charging {
			w += boltW
		}
		return w, 6
	default:
		w := 210.0
		if charging {
			w += boltW
		}
		return w, 108
	}
}

// segment names: a=top, b=top-right, c=bottom-right, d=bottom,
// e=bottom-left, f=top-left, g=middle.
var sevenSeg = map[byte]string{
	'0': "abcdef",
	'1': "bc",
	'2': "abged",
	'3': "abgcd",
	'4': "fgbc",
	'5': "afgcd",
	'6': "afgecd",
	'7': "abc",
	'8': "abcdefg",
	'9': "abcdfg",
	'-': "g",
}

// battColor maps a charge level to the usual traffic-light reading. Charging
// always reads green regardless of level, since the direction of travel is
// what matters then.
func battColor(pct int, charging, available bool) color.NRGBA {
	switch {
	case !available:
		return color.NRGBA{0x6E, 0x76, 0x86, 0xFF}
	case charging:
		return color.NRGBA{0x43, 0xD1, 0x7A, 0xFF}
	case pct <= 15:
		return color.NRGBA{0xF0, 0x57, 0x4B, 0xFF}
	case pct <= 35:
		return color.NRGBA{0xF5, 0xB4, 0x43, 0xFF}
	default:
		return color.NRGBA{0x4C, 0xC2, 0xFF, 0xFF}
	}
}

// trayIconPNG renders the indicator. available=false draws an empty grey shell
// and "--", which is what shows when the mouse is asleep or out of range.
func trayIconPNG(pct int, charging, available bool, style TrayStyle) []byte {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	col := battColor(pct, charging, available)
	text := strconv.Itoa(pct)
	if !available {
		text = "--"
	}

	vbW, digitsX := layoutFor(style, charging)
	s := float32(outH) / vbH // design space -> pixels
	outW := int(float64(outH)*vbW/vbH + 0.5)
	acc := image.NewAlpha(image.Rect(0, 0, outW, outH))

	addRR := func(x, y, w, h, r float64) {
		maxInto(acc, rasterize(outW, outH, func(ra *vector.Rasterizer) {
			rrPath(ra, float32(x)*s, float32(y)*s, float32(w)*s, float32(h)*s, float32(r)*s)
		}))
	}

	boltX := 0.0

	if style != TrayPercentOnly {
		// ---- battery shell: outer rounded rect minus its inner cut-out ----
		shell := rasterize(outW, outH, func(ra *vector.Rasterizer) {
			rrPath(ra, bodyX*s, bodyY*s, bodyW*s, bodyH*s, 18*s)
		})
		subFrom(shell, rasterize(outW, outH, func(ra *vector.Rasterizer) {
			rrPath(ra, (bodyX+stroke)*s, (bodyY+stroke)*s,
				(bodyW-2*stroke)*s, (bodyH-2*stroke)*s, (18-stroke)*s)
		}))
		maxInto(acc, shell)

		// contact nub
		addRR(bodyX+bodyW+2, bodyY+(bodyH-nubH)/2, nubW, nubH, 4)

		// ---- charge level ----
		pad := stroke + inset
		innerW := bodyW - 2*pad
		fillW := innerW * float64(pct) / 100
		if !available {
			fillW = 0
		} else if pct > 0 && fillW < 6 {
			fillW = 6 // keep a sliver visible at 1-2%
		}
		if fillW > 0 {
			addRR(bodyX+pad, bodyY+pad, fillW, bodyH-2*pad, 7)
		}
		boltX = bodyX + bodyW + 2 + nubW + 4
	}

	if style != TrayBarOnly {
		// ---- percentage ----
		n := float64(len(text))
		avail := vbW - digitsX - 4
		if charging {
			avail -= boltW
		}
		dw := (avail - digitGap*(n-1)) / n
		if max := digitsH * 0.62; dw > max {
			dw = max // otherwise a one-digit reading stretches out of shape
		}
		t := dw * 0.26
		if h := digitsH * 0.16; t > h {
			t = h
		}
		total := n*dw + digitGap*(n-1)
		if charging {
			total += boltW
		}
		x := digitsX + (vbW-4-digitsX-total)/2
		for i := 0; i < len(text); i++ {
			drawDigit(addRR, text[i], x, digitsY, dw, digitsH, t)
			x += dw + digitGap
		}
		boltX = x
	}
	x := boltX

	// lightning bolt, right of the digits
	if charging {
		xf := float32(x) * s
		maxInto(acc, rasterize(outW, outH, func(ra *vector.Rasterizer) {
			polyPath(ra, []float32{
				xf + 17*s, 14 * s,
				xf + 2*s, 52 * s,
				xf + 12*s, 52 * s,
				xf + 7*s, 86 * s,
				xf + 22*s, 46 * s,
				xf + 12*s, 46 * s,
			})
		}))
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, colorize(acc, col)); err != nil {
		return nil
	}
	return buf.Bytes()
}

// drawDigit emits the segment rectangles for one seven-segment glyph in the
// box (ox,oy)-(ox+dw,oy+h) with segment thickness t.
func drawDigit(addRR func(x, y, w, h, r float64), ch byte, ox, oy, dw, h, t float64) {
	segs, ok := sevenSeg[ch]
	if !ok {
		return
	}
	l := (h - 3*t) / 2 // length of a vertical segment
	for _, s := range segs {
		switch s {
		case 'a':
			addRR(ox+t, oy, dw-2*t, t, t/2)
		case 'g':
			addRR(ox+t, oy+(h-t)/2, dw-2*t, t, t/2)
		case 'd':
			addRR(ox+t, oy+h-t, dw-2*t, t, t/2)
		case 'f':
			addRR(ox, oy+t, t, l, t/2)
		case 'b':
			addRR(ox+dw-t, oy+t, t, l, t/2)
		case 'e':
			addRR(ox, oy+(h+t)/2, t, l, t/2)
		case 'c':
			addRR(ox+dw-t, oy+(h+t)/2, t, l, t/2)
		}
	}
}
