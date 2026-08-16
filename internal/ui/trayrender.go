package ui

import (
	"image"
	"image/color"

	"golang.org/x/image/vector"
)

// Rasterising helpers for the tray icon.
//
// The icon is drawn here rather than handed to Fyne as an SVG on purpose.
// Fyne's SetSystemTrayIcon renders SVG resources through
// c.Resize(fyne.NewSquareSize(systrayIconSize)) — a hard-coded square — so a
// wide SVG comes out letterboxed into a square and ends up *smaller* in the
// panel. Non-SVG resources skip that path and are passed to the tray as-is,
// so rendering our own PNG is what makes a wide icon possible at all.

// rasterize runs a path through the rasteriser and returns its coverage mask.
func rasterize(w, h int, path func(r *vector.Rasterizer)) *image.Alpha {
	r := vector.NewRasterizer(w, h)
	path(r)
	dst := image.NewAlpha(image.Rect(0, 0, w, h))
	r.Draw(dst, dst.Bounds(), image.Opaque, image.Point{})
	return dst
}

// rrPath traces a rounded rectangle. Corner radius is clamped to half the
// shorter side so a large radius degrades to a stadium shape instead of
// self-intersecting.
func rrPath(r *vector.Rasterizer, x, y, w, h, rad float32) {
	if rad > w/2 {
		rad = w / 2
	}
	if rad > h/2 {
		rad = h / 2
	}
	const kappa = 0.5523
	k := rad * kappa
	r.MoveTo(x+rad, y)
	r.LineTo(x+w-rad, y)
	r.CubeTo(x+w-rad+k, y, x+w, y+rad-k, x+w, y+rad)
	r.LineTo(x+w, y+h-rad)
	r.CubeTo(x+w, y+h-rad+k, x+w-rad+k, y+h, x+w-rad, y+h)
	r.LineTo(x+rad, y+h)
	r.CubeTo(x+rad-k, y+h, x, y+h-rad+k, x, y+h-rad)
	r.LineTo(x, y+rad)
	r.CubeTo(x, y+rad-k, x+rad-k, y, x+rad, y)
	r.ClosePath()
}

// polyPath traces a closed polygon from flat x,y pairs.
func polyPath(r *vector.Rasterizer, pts []float32) {
	if len(pts) < 6 {
		return
	}
	r.MoveTo(pts[0], pts[1])
	for i := 2; i+1 < len(pts); i += 2 {
		r.LineTo(pts[i], pts[i+1])
	}
	r.ClosePath()
}

// maxInto merges src into dst by taking the greater coverage per pixel, which
// unions shapes without the double-darkening of naive addition.
func maxInto(dst, src *image.Alpha) {
	for i := range dst.Pix {
		if src.Pix[i] > dst.Pix[i] {
			dst.Pix[i] = src.Pix[i]
		}
	}
}

// subFrom knocks src out of dst, used to hollow the battery shell.
func subFrom(dst, src *image.Alpha) {
	for i := range dst.Pix {
		if v := int(dst.Pix[i]) - int(src.Pix[i]); v > 0 {
			dst.Pix[i] = uint8(v)
		} else {
			dst.Pix[i] = 0
		}
	}
}

// colorize turns a coverage mask into a premultiplied RGBA image in col.
func colorize(mask *image.Alpha, col color.NRGBA) *image.RGBA {
	b := mask.Bounds()
	out := image.NewRGBA(b)
	for i, a := range mask.Pix {
		o := i * 4
		out.Pix[o+0] = uint8(int(col.R) * int(a) / 255)
		out.Pix[o+1] = uint8(int(col.G) * int(a) / 255)
		out.Pix[o+2] = uint8(int(col.B) * int(a) / 255)
		out.Pix[o+3] = a
	}
	return out
}
