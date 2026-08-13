package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// accentTheme is a dark theme tuned for this app so it looks deliberate rather
// than the stock Fyne palette. It forces the dark variant and overrides the
// colours and metrics; everything else delegates to the default theme.
//
// The palette is built around a near-black neutral ramp (surface00…surface30)
// with a single cyan-blue accent. Surfaces are separated by lightness only —
// no borders on large areas — which reads cleaner than the boxed-in look.
type accentTheme struct{}

var _ fyne.Theme = accentTheme{}

const (
	accent    = 0x4CC2FF // primary accent
	accentDim = 0x2E7FB8 // pressed / secondary accent

	surface00 = 0x08090C // window background
	surface10 = 0x101218 // cards
	surface20 = 0x171A22 // raised controls
	surface30 = 0x222633 // borders / hairlines

	textHi  = 0xF4F6FA // primary text
	textMid = 0x9AA3B5 // captions
	textLo  = 0x5F6779 // disabled / placeholder
)

func rgb(hex uint32) color.NRGBA {
	return color.NRGBA{R: byte(hex >> 16), G: byte(hex >> 8), B: byte(hex), A: 0xFF}
}

// rgba is rgb with an explicit alpha, for translucent overlays.
func rgba(hex uint32, a byte) color.NRGBA {
	c := rgb(hex)
	c.A = a
	return c
}

func (accentTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return rgb(accent)
	case theme.ColorNameFocus:
		return rgba(accent, 0x99)
	case theme.ColorNameSelection:
		return rgba(accent, 0x33)
	case theme.ColorNameHover:
		return rgba(accent, 0x1A)
	case theme.ColorNamePressed:
		return rgba(accentDim, 0x55)
	case theme.ColorNameBackground:
		return rgb(surface00)
	case theme.ColorNameOverlayBackground, theme.ColorNameMenuBackground:
		return rgb(surface10)
	case theme.ColorNameButton:
		return rgb(surface20)
	case theme.ColorNameDisabledButton:
		return rgb(surface10)
	case theme.ColorNameInputBackground:
		return rgb(surface10)
	case theme.ColorNameInputBorder:
		return rgb(surface30)
	case theme.ColorNameForeground:
		return rgb(textHi)
	case theme.ColorNamePlaceHolder:
		return rgb(textLo)
	case theme.ColorNameDisabled:
		return rgb(textLo)
	case theme.ColorNameSeparator:
		return rgba(0xFFFFFF, 0x12)
	case theme.ColorNameShadow:
		return color.NRGBA{0x00, 0x00, 0x00, 0x99}
	case theme.ColorNameScrollBar:
		return rgba(0xFFFFFF, 0x22)
	}
	return theme.DefaultTheme().Color(name, theme.VariantDark)
}

func (accentTheme) Font(s fyne.TextStyle) fyne.Resource { return theme.DefaultTheme().Font(s) }

func (accentTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

// Size widens the default metrics: more breathing room between controls and
// softer corners, which is most of what separates a modern panel from the
// stock cramped look.
func (accentTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 8
	case theme.SizeNameInnerPadding:
		return 12
	case theme.SizeNameLineSpacing:
		return 6
	case theme.SizeNameSelectionRadius:
		return 8
	case theme.SizeNameInputRadius:
		return 10
	case theme.SizeNameScrollBarSmall:
		return 4
	case theme.SizeNameSeparatorThickness:
		return 1
	case theme.SizeNameInputBorder:
		return 1
	}
	return theme.DefaultTheme().Size(name)
}
