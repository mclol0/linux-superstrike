package ui

import (
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

//go:embed brandmark.svg
var brandmarkSVG []byte

// logoCandidates are checked in order under the user's config dir. The first
// one that exists wins, so dropping a vendor logo there replaces the neutral
// built-in mark without rebuilding.
var logoCandidates = []string{
	"logo.svg", "logo.png", "logo.jpg", "logo.jpeg",
}

// logoResource returns the backdrop mark: a user-supplied logo from
// $XDG_CONFIG_HOME/superstrike/ (falling back to ~/.config/superstrike/) if one
// is present, otherwise the embedded neutral placeholder.
//
// The built-in mark is deliberately generic — this app is not affiliated with
// any hardware vendor, so no vendor trademark ships in the binary. Supplying
// your own file is an explicit choice you make locally.
func logoResource() fyne.Resource {
	if dir, err := os.UserConfigDir(); err == nil {
		for _, name := range logoCandidates {
			path := filepath.Join(dir, "superstrike", name)
			if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
				return fyne.NewStaticResource(name, data)
			}
		}
	}
	return fyne.NewStaticResource("brandmark.svg", brandmarkSVG)
}

// logoDir is where a user-supplied backdrop image lives.
func logoDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "superstrike")
}

// logoConfigPath is the canonical path for a user-supplied backdrop.
func logoConfigPath() string {
	dir := logoDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "logo.svg")
}

// currentLogoPath returns the user-supplied backdrop in use, or "" when the
// built-in mark is showing.
func currentLogoPath() string {
	dir := logoDir()
	if dir == "" {
		return ""
	}
	for _, name := range logoCandidates {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && st.Size() > 0 {
			return p
		}
	}
	return ""
}

// tildePath shortens $HOME to ~ so paths shown in the UI stay readable and
// don't leak the account name into screenshots.
func tildePath(p string) string {
	if p == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || !strings.HasPrefix(p, home) {
		return p
	}
	return "~" + strings.TrimPrefix(p, home)
}

// clearLogo removes any user-supplied backdrop, restoring the built-in mark.
func clearLogo() error {
	dir := logoDir()
	if dir == "" {
		return nil
	}
	for _, name := range logoCandidates {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// saveLogo stores data as the backdrop image, keeping the extension so the
// right decoder is used. Any previously stored image is removed first, so the
// candidate order can't leave a stale file winning.
func saveLogo(ext string, data []byte) error {
	dir := logoDir()
	if dir == "" {
		return errNoConfigDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := clearLogo(); err != nil {
		return err
	}
	ext = strings.ToLower(ext)
	if ext != ".svg" && ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		return errUnsupportedImage
	}
	return os.WriteFile(filepath.Join(dir, "logo"+ext), data, 0o644)
}

var (
	errNoConfigDir      = errors.New("no user config directory")
	errUnsupportedImage = errors.New("use an svg, png or jpg image")
)

// backdrop layers a page on top of the app's ambient background: a flat base,
// a soft accent glow in the upper left, and the brand mark bled into the lower
// right corner. Everything is far enough back that text stays readable.
func (a *App) backdrop(content fyne.CanvasObject) fyne.CanvasObject {
	base := canvas.NewRectangle(rgb(surface00))

	// Radial accent wash — gives the flat dark panel some depth.
	glow := canvas.NewRadialGradient(rgba(accent, 0x24), rgba(accent, 0x00))
	glow.CenterOffsetX = -0.28
	glow.CenterOffsetY = -0.34

	// Second, cooler wash from the opposite corner.
	glow2 := canvas.NewRadialGradient(rgba(accentDim, 0x1C), rgba(accentDim, 0x00))
	glow2.CenterOffsetX = 0.35
	glow2.CenterOffsetY = 0.40

	// Brand mark, pinned bottom-right and mostly dissolved into the background.
	mark := canvas.NewImageFromResource(logoResource())
	mark.FillMode = canvas.ImageFillContain
	mark.Translucency = 0.90
	a.backdropMark = mark // kept so a new image can be applied without a restart
	marked := container.NewBorder(
		nil, nil, nil,
		container.NewGridWrap(fyne.NewSize(300, 300), mark),
		nil,
	)
	pinned := container.NewBorder(nil, marked, nil, nil, nil)

	return container.NewStack(base, glow, glow2, pinned, content)
}

// reloadBackdrop swaps in whatever image is configured now. Called after the
// backdrop is changed in Settings so it applies immediately.
func (a *App) reloadBackdrop() {
	if a.backdropMark == nil {
		return
	}
	a.backdropMark.Resource = logoResource()
	a.backdropMark.Refresh()
}

// card wraps content in a rounded translucent surface. Cards carry no stroke —
// separation comes from the lightness step against the backdrop, which reads
// cleaner than outlining every box.
func card(content fyne.CanvasObject) *fyne.Container {
	bg := canvas.NewRectangle(rgba(0xFFFFFF, 0x0A))
	bg.CornerRadius = 16
	return container.NewStack(bg, container.NewPadded(content))
}

// accentCard is card() with a hairline accent edge, for the one tile per view
// that should draw the eye.
func accentCard(content fyne.CanvasObject) *fyne.Container {
	bg := canvas.NewRectangle(rgba(0xFFFFFF, 0x0A))
	bg.CornerRadius = 16
	bg.StrokeColor = rgba(accent, 0x40)
	bg.StrokeWidth = 1
	return container.NewStack(bg, container.NewPadded(content))
}

// sectionTitle is a small uppercase label used above groups of controls.
func sectionTitle(s string) *canvas.Text {
	t := canvas.NewText(s, rgb(textMid))
	t.TextSize = 11
	t.TextStyle = fyne.TextStyle{Bold: true}
	return t
}
