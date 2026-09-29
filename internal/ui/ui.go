// Package ui renders the Superstrike control panel with Fyne. The device is
// polled on a background goroutine every few seconds so the GUI never blocks on
// HID++ traffic, and all device operations are serialised through opMu so a
// multi-step profile write can't interleave with a refresh.
//
// Control model: the mouse is kept in Onboard mode and all DPI / polling-rate /
// haptics settings are edited via its onboard profiles, which apply instantly
// and persist on the device (the firmware's live DPI setter is a no-op, so this
// is the only thing that works — and what G HUB does under the hood).
package ui

import (
	_ "embed"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"superstrike/internal/hidpp"
)

//go:embed superstrike.svg
var superstrikeSVG []byte

//go:embed icon.svg
var iconSVG []byte

var (
	mouseRes = fyne.NewStaticResource("superstrike.svg", superstrikeSVG)
	iconRes  = fyne.NewStaticResource("icon.svg", iconSVG)
)

const refreshInterval = 3 * time.Second

// App owns the window, the connected device, and the cached UI state.
type App struct {
	fyne fyne.App
	win  fyne.Window

	opMu sync.Mutex // serialises all device operations
	dev  *hidpp.Device
	name string

	suppress   bool // guards programmatic Select changes from firing handlers
	permDenied bool // a Logitech node was found but couldn't be opened (udev)

	status *widget.Label
	tabs   *container.AppTabs

	// dashboard
	dName, dBatt, dBattPct *widget.Label
	tProfile, tDPI, tRate  *canvas.Text // big metric values
	batt                   *batteryGauge
	connDot                *canvas.Circle

	// measuredRate is the true polling rate (Hz) measured from HID input
	// reports; 0 when not yet measured. curPath is the connected hidraw path.
	measuredRate atomic.Int64
	curPath      atomic.Value // string

	// profiles
	profSelect *widget.Select
	profEditor *fyne.Container
	profiles   []hidpp.Profile
	curSector  int

	// buttons / haptics
	btnBox *fyne.Container
	hapBox *fyne.Container
}

// Run builds and shows the control panel; it blocks until the window closes.
func Run() {
	a := &App{fyne: app.NewWithID("com.superstrike.panel")}
	a.curPath.Store("")
	a.fyne.Settings().SetTheme(accentTheme{})
	a.fyne.SetIcon(iconRes)
	a.win = a.fyne.NewWindow("Superstrike Control")
	a.win.SetIcon(iconRes)
	a.win.Resize(fyne.NewSize(960, 720))

	a.status = widget.NewLabel("starting…")
	a.status.TextStyle = fyne.TextStyle{Monospace: true}

	a.tabs = container.NewAppTabs(
		container.NewTabItemWithIcon("Dashboard", theme.HomeIcon(), a.buildDashboard()),
		container.NewTabItemWithIcon("Profiles", theme.StorageIcon(), a.buildProfiles()),
		container.NewTabItemWithIcon("Buttons", theme.GridIcon(), a.buildButtons()),
		container.NewTabItemWithIcon("Haptics", theme.MediaPlayIcon(), a.buildHaptics()),
	)
	a.tabs.SetTabLocation(container.TabLocationLeading)
	a.tabs.OnSelected = a.onTabSelected

	statusBar := container.NewVBox(widget.NewSeparator(), a.status)
	a.win.SetContent(container.NewBorder(nil, statusBar, nil, nil, a.tabs))

	a.fyne.Lifecycle().SetOnStarted(func() {
		a.startAutoRefresh()
		a.startRateMonitor()
	})
	a.win.ShowAndRun()
}

// ---- async refresh & connection -------------------------------------------

func (a *App) startAutoRefresh() {
	go func() {
		for {
			a.tick()
			time.Sleep(refreshInterval)
		}
	}()
}

func (a *App) tick() {
	a.opMu.Lock()
	if a.dev == nil {
		a.connectLocked()
	} else if _, err := a.dev.Ping(); err != nil {
		a.dev.Close()
		a.dev = nil
		a.curPath.Store("")
		a.measuredRate.Store(0)
		a.connectLocked()
	}
	dev := a.dev
	permDenied := a.permDenied
	var (
		name     string
		batt     hidpp.Battery
		rate     int
		prof     hidpp.Profile
		haveProf bool
	)
	if dev != nil {
		name = a.name
		batt, _ = dev.Battery()
		rate, _, _ = dev.ReportRate()
		if p, err := dev.ActiveProfile(); err == nil {
			prof, haveProf = p, true
		}
	}
	a.opMu.Unlock()

	fyne.Do(func() {
		if dev == nil {
			a.setConnected(false)
			if permDenied {
				a.status.SetText("mouse found but no permission — install the udev rule (see README), then replug")
				a.dName.SetText("Permission denied")
			} else {
				a.status.SetText("no mouse detected — is it powered on and paired?")
				a.dName.SetText("Disconnected")
			}
			a.batt.set(0, false, false)
			a.dBattPct.SetText("—")
			a.dBatt.SetText("—")
			setText(a.tRate, "—")
			setText(a.tDPI, "—")
			setText(a.tProfile, "—")
			return
		}
		a.setConnected(true)
		a.status.SetText(fmt.Sprintf("connected: %s   [%s]", name, dev.Path))
		a.dName.SetText(name)
		a.batt.set(batt.Percent, batt.Charging, batt.Available)
		switch {
		case !batt.Available:
			a.dBattPct.SetText("n/a")
			a.dBatt.SetText("status unavailable")
		case batt.Charging:
			a.dBattPct.SetText(fmt.Sprintf("%d%%", batt.Percent))
			a.dBatt.SetText("Charging")
		default:
			a.dBattPct.SetText(fmt.Sprintf("%d%%", batt.Percent))
			a.dBatt.SetText("On battery")
		}
		// The rate monitor owns the polling-rate readout (true measured value);
		// fall back to the register read (accurate in onboard mode) until then.
		if a.measuredRate.Load() == 0 && rate > 0 {
			setText(a.tRate, fmt.Sprintf("%d Hz", rate))
		}
		if haveProf {
			if prof.DPIX == prof.DPIY {
				setText(a.tDPI, fmt.Sprintf("%d", prof.DPIX))
			} else {
				setText(a.tDPI, fmt.Sprintf("%d × %d", prof.DPIX, prof.DPIY))
			}
			if prof.Name != "" {
				setText(a.tProfile, prof.Name)
			} else {
				setText(a.tProfile, fmt.Sprintf("0x%04X", prof.Sector))
			}
		}
	})
}

// setText updates a canvas.Text value and repaints it.
func setText(t *canvas.Text, s string) {
	t.Text = s
	t.Refresh()
}

// connectLocked discovers the mouse, selects it, and forces Onboard mode.
// Caller must hold opMu.
func (a *App) connectLocked() {
	devs, permDenied, _ := hidpp.Discover()
	a.permDenied = permDenied
	if len(devs) == 0 {
		return
	}
	a.permDenied = false // we opened at least one device
	// Score candidates so we pick the actual mouse regardless of how it's
	// connected (wireless dongle / wired / Bluetooth — each enumerates with a
	// different name/PID/hidraw node). Prefer the Superstrike by name, prefer
	// anything that exposes onboard profiles (a configurable mouse), and avoid
	// the bare receiver or unrelated Logitech devices.
	pick := devs[0]
	best := -1 << 30
	for _, d := range devs {
		up := strings.ToUpper(d.Name)
		score := 0
		if strings.Contains(up, "SUPERSTRIKE") {
			score += 6
		}
		if strings.Contains(up, "PRO X") || strings.Contains(up, "X2") {
			score += 2
		}
		if strings.Contains(up, "RECEIVER") {
			score -= 4
		}
		if d.Has(hidpp.FeatOnboardProfile) { // confirms it's a configurable mouse
			score += 4
		}
		if score > best {
			best, pick = score, d
		}
	}
	for _, d := range devs {
		if d != pick {
			d.Close()
		}
	}
	a.dev = pick
	a.curPath.Store(pick.Path)
	// Keep the mouse in Onboard mode — the only mode where DPI/rate edits (via
	// the active profile) actually apply and persist on this firmware.
	_ = pick.SetOnboardMode(hidpp.OnboardModeOnboard)
	if n, err := pick.DeviceName(); err == nil && n != "" {
		a.name = n
	} else {
		a.name = pick.Name
	}
}

func (a *App) setConnected(ok bool) {
	if a.connDot == nil {
		return
	}
	if ok {
		a.connDot.FillColor = rgb(accent)
	} else {
		a.connDot.FillColor = color.NRGBA{0x80, 0x44, 0x44, 0xFF}
	}
	a.connDot.Refresh()
}

// runOp performs a device operation off the UI thread, reporting the result.
func (a *App) runOp(name string, fn func(*hidpp.Device) error, after func()) {
	go func() {
		var err error
		// Run under the op lock with deferred unlock + panic recovery so a bad
		// call can never deadlock the lock or crash the whole app.
		func() {
			a.opMu.Lock()
			defer a.opMu.Unlock()
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("internal error: %v", r)
				}
			}()
			if a.dev == nil {
				err = fmt.Errorf("no device connected")
			} else {
				err = fn(a.dev)
			}
		}()
		fyne.Do(func() {
			if err != nil {
				a.status.SetText(name + ": " + err.Error())
				dialog.ShowError(err, a.win)
				return
			}
			a.status.SetText(name + " ✓")
			if after != nil {
				after()
			}
		})
	}()
}

func (a *App) onTabSelected(ti *container.TabItem) {
	switch ti.Text {
	case "Profiles":
		a.loadProfiles()
	case "Buttons":
		a.loadButtons()
	case "Haptics":
		a.loadHaptics()
	}
}

// ---- small UI helpers -----------------------------------------------------

func caption(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.TextStyle = fyne.TextStyle{Bold: true}
	return l
}

// metricTile builds a card showing a small caption above a large accent value,
// with an optional italic hint line below.
func metricTile(captionText, hint string) (*fyne.Container, *canvas.Text) {
	cap := canvas.NewText(strings.ToUpper(captionText), rgb(0x7B8499))
	cap.TextSize = 11
	cap.TextStyle = fyne.TextStyle{Bold: true}
	val := canvas.NewText("—", rgb(accent))
	val.TextSize = 34
	val.TextStyle = fyne.TextStyle{Bold: true}
	items := []fyne.CanvasObject{cap, val}
	if hint != "" {
		h := canvas.NewText(hint, rgb(0x6B7180))
		h.TextSize = 11
		h.TextStyle = fyne.TextStyle{Italic: true}
		items = append(items, h)
	}
	body := container.NewPadded(container.NewVBox(items...))
	return container.NewStack(tileBG(), body), val
}

// tileBG is a subtle rounded surface behind a tile.
func tileBG() *canvas.Rectangle {
	r := canvas.NewRectangle(color.NRGBA{0xFF, 0xFF, 0xFF, 0x08})
	r.StrokeColor = color.NRGBA{0x5B, 0xC0, 0xFF, 0x33}
	r.StrokeWidth = 1
	r.CornerRadius = 12
	return r
}

// ---- Dashboard ------------------------------------------------------------

func (a *App) buildDashboard() fyne.CanvasObject {
	a.dName = widget.NewLabelWithStyle("—", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	a.dBatt = widget.NewLabelWithStyle("—", fyne.TextAlignCenter, fyne.TextStyle{})
	a.dBattPct = widget.NewLabelWithStyle("—", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	a.batt = newBatteryGauge()

	a.connDot = canvas.NewCircle(color.NRGBA{0x80, 0x44, 0x44, 0xFF})
	dot := container.NewGridWrap(fyne.NewSize(13, 13), a.connDot)

	title := canvas.NewText("PRO X 2  SUPERSTRIKE", rgb(0xF2F4F8))
	title.TextSize = 30
	title.TextStyle = fyne.TextStyle{Bold: true}
	subtitle := canvas.NewText("Linux control panel · HID++ direct", rgb(accent))
	subtitle.TextSize = 13
	subtitle.TextStyle = fyne.TextStyle{Italic: true}

	header := container.NewVBox(
		container.NewHBox(dot, layoutGap(6), title),
		subtitle,
	)

	profileTile, tProfile := metricTile("Active Profile", "")
	dpiTile, tDPI := metricTile("Current DPI", "")
	rateTile, tRate := metricTile("Polling Rate", "measured live — move the mouse")
	a.tProfile, a.tDPI, a.tRate = tProfile, tDPI, tRate

	// Battery as its own tile with the animated gauge.
	battCap := canvas.NewText("BATTERY", rgb(0x7B8499))
	battCap.TextSize = 11
	battCap.TextStyle = fyne.TextStyle{Bold: true}
	battStack := container.NewStack(a.batt, container.NewCenter(a.dBattPct))
	battInner := container.NewPadded(container.NewVBox(battCap, battStack, a.dBatt))
	battTile := container.NewStack(tileBG(), battInner)

	// 2×2 grid of tiles that expands to fill the whole content area.
	tiles := container.NewGridWithColumns(2, profileTile, dpiTile, rateTile, battTile)

	content := container.NewBorder(
		container.NewVBox(header, layoutGap(6)), // top
		nil, nil, nil,
		tiles, // center — fills the rest of the window
	)

	// Faint artwork behind everything so the dashboard fills the window.
	art := canvas.NewImageFromResource(mouseRes)
	art.FillMode = canvas.ImageFillContain
	art.Translucency = 0.9

	return container.NewStack(art, container.NewPadded(content))
}

// layoutGap is a fixed-size transparent spacer.
func layoutGap(px float32) fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSize(px, px), canvas.NewRectangle(color.Transparent))
}

// ---- Profiles -------------------------------------------------------------

func (a *App) buildProfiles() fyne.CanvasObject {
	a.profSelect = widget.NewSelect(nil, func(string) {
		if a.suppress {
			return
		}
		a.renderProfileEditor()
	})
	a.profEditor = container.NewVBox(widget.NewLabel("Loading profiles…"))
	reload := widget.NewButtonWithIcon("Reload", theme.ViewRefreshIcon(), func() { a.loadProfiles() })
	top := container.NewBorder(nil, nil, caption("Profile"), reload, a.profSelect)
	return container.NewBorder(container.NewPadded(top), nil, nil, nil,
		container.NewVScroll(container.NewPadded(a.profEditor)))
}

func (a *App) loadProfiles() {
	go func() {
		a.opMu.Lock()
		dev := a.dev
		var profs []hidpp.Profile
		var cur int
		var err error
		if dev != nil {
			profs, err = dev.Profiles()
			cur, _ = dev.CurrentProfileSector()
		}
		a.opMu.Unlock()
		if dev == nil {
			return
		}
		fyne.Do(func() {
			if err != nil && len(profs) == 0 {
				a.status.SetText("profiles: " + err.Error())
				a.profEditor.Objects = []fyne.CanvasObject{widget.NewLabel("Could not read profiles: " + err.Error())}
				a.profEditor.Refresh()
				return
			}
			a.profiles = profs
			a.curSector = cur
			opts := make([]string, len(profs))
			sel := 0
			for i, p := range profs {
				tag := ""
				if p.Sector == cur {
					tag += "  ● ACTIVE"
					sel = i
				}
				if !p.Enabled {
					tag += "  (disabled)"
				}
				name := p.Name
				if name == "" {
					name = "(unnamed)"
				}
				opts[i] = fmt.Sprintf("Profile %d — %s%s", p.Index, name, tag)
			}
			a.suppress = true
			a.profSelect.Options = opts
			a.profSelect.Refresh()
			if len(opts) > 0 {
				a.profSelect.SetSelectedIndex(sel)
			}
			a.suppress = false
			a.renderProfileEditor()
		})
	}()
}

func (a *App) selectedProfile() (hidpp.Profile, bool) {
	i := a.profSelect.SelectedIndex()
	if i < 0 || i >= len(a.profiles) {
		return hidpp.Profile{}, false
	}
	return a.profiles[i], true
}

// renderProfileEditor rebuilds the editor for the selected profile (UI thread).
// DPI is the active resolution's X/Y, linked by default.
func (a *App) renderProfileEditor() {
	a.profEditor.Objects = nil
	defer a.profEditor.Refresh()

	p, ok := a.selectedProfile()
	if !ok {
		a.profEditor.Add(widget.NewLabel("No profile selected."))
		return
	}

	name := p.Name
	if name == "" {
		name = "(unnamed)"
	}
	state := "stored"
	if p.Sector == a.curSector {
		state = "ACTIVE"
	}
	if !p.Enabled {
		state += " · disabled"
	}
	header := widget.NewLabelWithStyle(
		fmt.Sprintf("Profile %d   ·   %s   ·   %s", p.Index, name, state),
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	xEntry := numEntry(p.DPIX)
	yEntry := numEntry(p.DPIY)
	dpiEntry := numEntry(p.DPIX)
	link := widget.NewCheck("Link X and Y", nil)
	link.SetChecked(p.DPIX == p.DPIY)

	fields := container.NewVBox()
	rebuild := func() {
		fields.Objects = nil
		if link.Checked {
			fields.Add(container.NewBorder(nil, nil, widget.NewLabel("DPI"), nil, dpiEntry))
		} else {
			fields.Add(container.NewBorder(nil, nil, widget.NewLabel("DPI X"), nil, xEntry))
			fields.Add(container.NewBorder(nil, nil, widget.NewLabel("DPI Y"), nil, yEntry))
		}
		fields.Refresh()
	}
	link.OnChanged = func(bool) { rebuild() }
	rebuild()

	sector := p.Sector
	save := widget.NewButtonWithIcon("Save DPI to this profile", theme.DocumentSaveIcon(), func() {
		var x, y int
		if link.Checked {
			x, _ = strconv.Atoi(strings.TrimSpace(dpiEntry.Text))
			y = x
		} else {
			x, _ = strconv.Atoi(strings.TrimSpace(xEntry.Text))
			y, _ = strconv.Atoi(strings.TrimSpace(yEntry.Text))
		}
		// No editor rebuild on success — that would reset the Link checkbox
		// (it's derived from X==Y) and clobber an intentional unlinked edit.
		a.runOp("save DPI", func(d *hidpp.Device) error { return d.WriteProfileResolution(sector, x, y) }, nil)
	})
	save.Importance = widget.HighImportance

	setActive := widget.NewButtonWithIcon("Set as active profile", theme.ConfirmIcon(), func() {
		a.runOp("activate profile", func(d *hidpp.Device) error { return d.SetCurrentProfileSector(sector) }, a.loadProfiles)
	})
	if !p.Enabled {
		setActive.Disable()
	}

	curName := p.Name
	rename := widget.NewButtonWithIcon("Rename", theme.DocumentCreateIcon(), func() {
		entry := widget.NewEntry()
		entry.SetText(curName)
		entry.Validator = nil
		dialog.ShowCustomConfirm("Rename profile", "Save", "Cancel", entry, func(ok bool) {
			if !ok {
				return
			}
			newName := strings.TrimSpace(entry.Text)
			a.runOp("rename profile", func(d *hidpp.Device) error { return d.SetProfileName(sector, newName) }, a.loadProfiles)
		}, a.win)
	})

	slot := p.Index
	var toggle *widget.Button
	if p.Enabled {
		toggle = widget.NewButtonWithIcon("Disable profile", theme.VisibilityOffIcon(), func() {
			a.runOp("disable profile", func(d *hidpp.Device) error { return d.SetProfileEnabled(slot, false) }, a.loadProfiles)
		})
	} else {
		toggle = widget.NewButtonWithIcon("Enable profile", theme.VisibilityIcon(), func() {
			a.runOp("enable profile", func(d *hidpp.Device) error { return d.SetProfileEnabled(slot, true) }, a.loadProfiles)
		})
	}

	hint := widget.NewLabel("DPI 100–44000. This mouse stores separate X/Y DPI for the active resolution; " +
		"keep them linked for normal use. Saving persists to onboard memory.")
	hint.Wrapping = fyne.TextWrapWord

	dpiCard := widget.NewCard("DPI", "", container.NewVBox(link, fields, hint, save))

	// Polling rate (stored in the profile, applied on switch).
	rateOpts := make([]string, len(hidpp.ReportRates))
	for i, hz := range hidpp.ReportRates {
		rateOpts[i] = fmt.Sprintf("%d Hz", hz)
	}
	rateSel := widget.NewSelect(rateOpts, nil)
	if p.ReportRateHz > 0 {
		rateSel.SetSelected(fmt.Sprintf("%d Hz", p.ReportRateHz))
	}
	rateSel.OnChanged = func(s string) {
		hz, err := strconv.Atoi(strings.TrimSuffix(s, " Hz"))
		if err != nil {
			return
		}
		a.runOp("set polling rate", func(d *hidpp.Device) error { return d.SetProfileReportRateHz(sector, hz) }, a.loadProfiles)
	}
	rateCard := widget.NewCard("Polling Rate", "applied when this profile is active", rateSel)

	info := widget.NewLabel(fmt.Sprintf("sector 0x%04X · rate byte 0x%02X (%d Hz) · RGB %d,%d,%d · raw res %v",
		p.Sector, p.ReportRate, p.ReportRateHz, p.Red, p.Green, p.Blue, p.DPI))
	info.TextStyle = fyne.TextStyle{Monospace: true}

	a.profEditor.Add(header)
	a.profEditor.Add(dpiCard)
	a.profEditor.Add(rateCard)
	a.profEditor.Add(container.NewGridWithColumns(3, setActive, toggle, rename))
	a.profEditor.Add(info)
}

// numEntry is a small entry pre-filled with an integer.
func numEntry(v int) *widget.Entry {
	e := widget.NewEntry()
	e.SetText(strconv.Itoa(v))
	return e
}

// ---- Buttons --------------------------------------------------------------

var physicalButtonNames = []string{
	"Left Click", "Right Click", "Middle / Wheel", "Back (side)", "Forward (side)",
}

func physicalButtonName(i int) string {
	if i < len(physicalButtonNames) {
		return physicalButtonNames[i]
	}
	return fmt.Sprintf("Button %d", i+1)
}

func (a *App) buildButtons() fyne.CanvasObject {
	a.btnBox = container.NewVBox(widget.NewLabel("Loading…"))
	return container.NewVScroll(container.NewPadded(a.btnBox))
}

func (a *App) loadButtons() {
	go func() {
		a.opMu.Lock()
		dev := a.dev
		var (
			info hidpp.ProfileInfo
			prof hidpp.Profile
			err  error
		)
		if dev != nil {
			info, _ = dev.ProfileInfo()
			prof, err = dev.ActiveProfile()
		}
		a.opMu.Unlock()
		if dev == nil {
			return
		}
		fyne.Do(func() { a.renderButtons(info, prof, err) })
	}()
}

func (a *App) renderButtons(info hidpp.ProfileInfo, prof hidpp.Profile, err error) {
	a.btnBox.Objects = nil
	defer a.btnBox.Refresh()
	if err != nil {
		a.btnBox.Add(widget.NewLabel("Couldn't read buttons: " + err.Error()))
		return
	}
	count := info.Buttons
	if count <= 0 || count > 16 {
		count = 5
	}
	pname := prof.Name
	if pname == "" {
		pname = fmt.Sprintf("sector 0x%04X", prof.Sector)
	}
	a.btnBox.Add(widget.NewLabelWithStyle("Editing profile:  "+pname, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	a.btnBox.Add(widget.NewLabelWithStyle("Click a button to reassign it. Changes save to the mouse and persist.",
		fyne.TextAlignLeading, fyne.TextStyle{Italic: true}))

	sector := prof.Sector
	for i := 0; i < count; i++ {
		i := i
		name := physicalButtonName(i)
		action := prof.Buttons[i]
		current := widget.NewLabelWithStyle(action.Describe(), fyne.TextAlignTrailing, fyne.TextStyle{})
		change := widget.NewButtonWithIcon("Change", theme.DocumentCreateIcon(), func() {
			a.assignDialog(sector, i, name, action)
		})
		row := container.NewBorder(nil, nil,
			widget.NewLabelWithStyle(name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			change, current)
		a.btnBox.Add(widget.NewCard("", "", container.NewPadded(row)))
	}
}

// assignDialog is the simple, guided "what should this button do?" picker.
func (a *App) assignDialog(sector, index int, name string, cur hidpp.ButtonAction) {
	const (
		catMouse = "Mouse Button"
		catKey   = "Keyboard Key"
		catMedia = "Media Key"
		catFunc  = "Mouse Function"
		catOff   = "Disabled (do nothing)"
	)
	cats := []string{catMouse, catKey, catMedia, catFunc, catOff}

	catSel := widget.NewSelect(cats, nil)
	valSel := widget.NewSelect(nil, nil)
	ctrl := widget.NewCheck("Ctrl", nil)
	shift := widget.NewCheck("Shift", nil)
	alt := widget.NewCheck("Alt", nil)
	super := widget.NewCheck("Super", nil)
	modRow := container.NewHBox(ctrl, shift, alt, super)

	var curList []hidpp.NamedCode // tracks the list backing valSel
	body := container.NewVBox()

	listFor := func(cat string) []hidpp.NamedCode {
		switch cat {
		case catMouse:
			return hidpp.MouseButtonChoices
		case catKey:
			return hidpp.KeyChoices
		case catMedia:
			return hidpp.MediaChoices
		case catFunc:
			return hidpp.FunctionChoices
		}
		return nil
	}
	rebuild := func() {
		curList = listFor(catSel.Selected)
		opts := make([]string, len(curList))
		for i, c := range curList {
			opts[i] = c.Name
		}
		valSel.Options = opts
		valSel.Refresh()
		body.Objects = nil
		if catSel.Selected == catOff {
			body.Add(widget.NewLabel("This button will do nothing when pressed."))
		} else {
			body.Add(widget.NewLabel("Action:"))
			body.Add(valSel)
			if catSel.Selected == catKey {
				body.Add(widget.NewLabel("Hold with (optional):"))
				body.Add(modRow)
			}
		}
		body.Refresh()
	}
	valByName := func(n string) {
		for i, c := range curList {
			if c.Name == n {
				valSel.SetSelectedIndex(i)
				return
			}
		}
	}

	catSel.OnChanged = func(string) {
		rebuild()
		if len(valSel.Options) > 0 {
			valSel.SetSelectedIndex(0)
		}
	}

	// Preselect from the current assignment.
	switch cur.Kind {
	case hidpp.ButtonMouse:
		catSel.Selected = catMouse
	case hidpp.ButtonKey:
		catSel.Selected = catKey
		ctrl.Checked = cur.Mods&0x01 != 0
		shift.Checked = cur.Mods&0x02 != 0
		alt.Checked = cur.Mods&0x04 != 0
		super.Checked = cur.Mods&0x08 != 0
	case hidpp.ButtonConsumer:
		catSel.Selected = catMedia
	case hidpp.ButtonFunction:
		catSel.Selected = catFunc
	default:
		catSel.Selected = catOff
	}
	rebuild()
	valByName(cur.Describe())

	content := container.NewVBox(widget.NewLabel("Make this button:"), catSel, body)
	dialog.ShowCustomConfirm("Assign — "+name, "Save", "Cancel", content, func(ok bool) {
		if !ok {
			return
		}
		act := hidpp.ButtonAction{Kind: hidpp.ButtonDisabled}
		i := valSel.SelectedIndex()
		switch catSel.Selected {
		case catMouse:
			if i >= 0 && i < len(curList) {
				act = hidpp.ButtonAction{Kind: hidpp.ButtonMouse, Code: curList[i].Code}
			}
		case catMedia:
			if i >= 0 && i < len(curList) {
				act = hidpp.ButtonAction{Kind: hidpp.ButtonConsumer, Code: curList[i].Code}
			}
		case catFunc:
			if i >= 0 && i < len(curList) {
				act = hidpp.ButtonAction{Kind: hidpp.ButtonFunction, Code: curList[i].Code}
			}
		case catKey:
			if i >= 0 && i < len(curList) {
				var mods byte
				if ctrl.Checked {
					mods |= 0x01
				}
				if shift.Checked {
					mods |= 0x02
				}
				if alt.Checked {
					mods |= 0x04
				}
				if super.Checked {
					mods |= 0x08
				}
				act = hidpp.ButtonAction{Kind: hidpp.ButtonKey, Code: curList[i].Code, Mods: mods}
			}
		}
		a.runOp("remap "+name, func(d *hidpp.Device) error { return d.SetProfileButton(sector, index, act) }, a.loadButtons)
	}, a.win)
}

// ---- Haptics --------------------------------------------------------------

func (a *App) buildHaptics() fyne.CanvasObject {
	a.hapBox = container.NewVBox(widget.NewLabel("Loading haptics…"))
	return container.NewVScroll(container.NewPadded(a.hapBox))
}

func (a *App) loadHaptics() {
	go func() {
		a.opMu.Lock()
		dev := a.dev
		var caps hidpp.AnalogCaps
		var cfgs []hidpp.AnalogConfig
		var err error
		if dev != nil {
			caps, err = dev.AnalogCaps()
			if err == nil {
				for i := 0; i < caps.Buttons; i++ {
					c, cerr := dev.AnalogConfig(i)
					if cerr != nil {
						err = cerr
						break
					}
					cfgs = append(cfgs, c)
				}
			}
		}
		a.opMu.Unlock()
		if dev == nil {
			return
		}
		fyne.Do(func() { a.renderHaptics(caps, cfgs, err) })
	}()
}

func (a *App) renderHaptics(caps hidpp.AnalogCaps, cfgs []hidpp.AnalogConfig, err error) {
	a.hapBox.Objects = nil
	defer a.hapBox.Refresh()
	if err != nil {
		a.hapBox.Add(widget.NewLabel("Haptics unavailable on this device:\n" + err.Error()))
		return
	}
	a.hapBox.Add(widget.NewLabelWithStyle(
		fmt.Sprintf("ANALOG_BUTTONS · 0x1B0C   —   actuation 1–%d · rapid trigger 1–%d · click haptics 0–%d",
			caps.MaxActuation, caps.MaxRapidTrigger, caps.MaxHaptics),
		fyne.TextAlignLeading, fyne.TextStyle{Italic: true}))

	for i := 0; i < caps.Buttons && i < len(cfgs); i++ {
		i := i
		cfg := cfgs[i]
		name := fmt.Sprintf("Button %d", i+1)
		if i < len(hidpp.AnalogButtonNames) {
			name = hidpp.AnalogButtonNames[i]
		}
		a.hapBox.Add(widget.NewCard(name, "", container.NewVBox(
			a.hapSlider("Click Haptics", 0, caps.MaxHaptics, cfg.Haptics, func(v int) {
				a.runOp(name+" haptics", func(d *hidpp.Device) error { return d.SetHaptics(i, v) }, nil)
			}),
			a.hapSlider("Actuation Point", 1, caps.MaxActuation, cfg.Actuation, func(v int) {
				a.runOp(name+" actuation", func(d *hidpp.Device) error { return d.SetActuation(i, v) }, nil)
			}),
			a.hapSlider("Rapid Trigger", 1, caps.MaxRapidTrigger, cfg.RapidTrigger, func(v int) {
				a.runOp(name+" rapid trigger", func(d *hidpp.Device) error { return d.SetRapidTrigger(i, v) }, nil)
			}),
		)))
	}
}

func (a *App) hapSlider(title string, min, max, val int, onEnd func(int)) fyne.CanvasObject {
	valLbl := widget.NewLabel(strconv.Itoa(val))
	valLbl.Alignment = fyne.TextAlignTrailing
	s := widget.NewSlider(float64(min), float64(max))
	s.Step = 1
	s.SetValue(float64(val))
	s.OnChanged = func(v float64) { valLbl.SetText(fmt.Sprintf("%.0f", v)) }
	s.OnChangeEnded = func(v float64) { onEnd(int(v)) }
	return container.NewBorder(nil, nil, widget.NewLabel(title), valLbl, s)
}
