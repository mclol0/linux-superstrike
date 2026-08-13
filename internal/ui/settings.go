package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Preference keys. Everything the panel indicator and the alert watcher do is
// opt-out-able from here, and defaults keep the pre-existing behaviour of a
// plain windowed app for anyone who does not want an indicator.
const (
	prefTrayEnabled   = "tray.enabled"
	prefTrayStyle     = "tray.style"
	prefAlertsEnabled = "alerts.enabled"
	prefAlertLevels   = "alerts.levels"
	prefAutostart     = "autostart.enabled"
)

// autostartFile is the XDG autostart entry this app manages.
func autostartFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "autostart", "superstrike-tray.desktop")
}

// autostartEnabled reports whether the entry exists.
func autostartEnabled() bool {
	p := autostartFile()
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// setAutostart writes or removes the autostart entry. The Exec line points at
// the running binary, so it keeps working from a build directory as well as
// from an installed copy.
func setAutostart(on bool) error {
	p := autostartFile()
	if p == "" {
		return fmt.Errorf("no user config dir")
	}
	if !on {
		err := os.Remove(p)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	body := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Superstrike Battery Indicator\n" +
		"Comment=Shows the PRO X 2 Superstrike battery level in the panel\n" +
		"Exec=" + exe + " -tray\n" +
		"Icon=superstrike\n" +
		"Terminal=false\n" +
		"X-GNOME-Autostart-enabled=true\n" +
		"Categories=Utility;HardwareSettings;\n"
	return os.WriteFile(p, []byte(body), 0o644)
}

// parseLevels reads a comma-separated threshold list. Invalid entries are
// dropped rather than rejected wholesale, so a typo cannot leave the user with
// no warnings at all.
func parseLevels(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(part), "%"))
		if part == "" {
			continue
		}
		if v, err := strconv.Atoi(part); err == nil && v > 0 && v <= 100 {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return defaultAlertPercents
	}
	return out
}

// formatLevels renders thresholds back into the entry field's format.
func formatLevels(pcts []int) string {
	parts := make([]string, len(pcts))
	for i, p := range pcts {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ", ")
}

// loadSettings applies stored preferences to the running app. Called once
// before the tray is built.
func (a *App) loadSettings() {
	p := a.fyne.Preferences()
	a.trayWanted = p.BoolWithFallback(prefTrayEnabled, true)
	a.trayStyle = ParseTrayStyle(p.StringWithFallback(prefTrayStyle, string(TrayBarAndPercent)))

	levels := parseLevels(p.StringWithFallback(prefAlertLevels, formatLevels(defaultAlertPercents)))
	a.alerts.setLevels(levels)
	a.alerts.setMuted(!p.BoolWithFallback(prefAlertsEnabled, true))
}

// hint renders the small explanatory line under a control. Each control gets
// its own, because the settings here differ in when they take effect and a
// single shared footnote was misleading.
func hint(text string) *widget.Label {
	l := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	l.Wrapping = fyne.TextWrapWord
	return l
}

// buildSettings renders the Settings tab.
func (a *App) buildSettings() fyne.CanvasObject {
	p := a.fyne.Preferences()

	// ---- panel indicator ----
	//
	// Enabling and disabling are not symmetric, and the UI has to say so.
	// Fyne starts the tray on the first SetSystemTrayMenu call, so switching
	// this on takes effect at once; but systrayRunning is a package-level
	// latch with no teardown path, so switching it off cannot remove an icon
	// that is already showing.
	trayState := widget.NewLabel("")
	trayState.Wrapping = fyne.TextWrapWord

	setTrayState := func(s string) {
		trayState.SetText(s)
	}
	if a.desk != nil {
		setTrayState("Active in the panel now.")
	} else if a.trayWanted {
		setTrayState("Enabled, but this desktop offers no system tray.")
	} else {
		setTrayState("Off. Turning it on takes effect immediately.")
	}

	// Hidden until the indicator setting is actually changed, then offered for
	// either direction: turning it off cannot remove a running icon, and
	// turning it on may need a clean start, so both cases warrant the button.
	restartBtn := widget.NewButtonWithIcon("Restart the app", theme.ViewRefreshIcon(), func() {
		if err := restartApp(); err != nil {
			dialog.ShowError(err, a.win)
		}
	})
	restartBtn.Importance = widget.HighImportance
	restartBtn.Hide()

	trayToggle := widget.NewCheck("Show a panel indicator", nil)
	trayToggle.SetChecked(a.trayWanted)
	trayToggle.OnChanged = func(v bool) {
		p.SetBool(prefTrayEnabled, v)
		a.trayWanted = v
		restartBtn.Show() // the state changed, so offer a clean restart
		switch {
		case v && a.desk != nil:
			setTrayState("Active in the panel now.")
		case v:
			if a.setupTray() {
				go a.tick() // populate the icon without waiting for the next poll
				setTrayState("Active in the panel now.")
			} else {
				setTrayState("Could not start the indicator. A restart usually fixes " +
					"this; if it does not, this desktop has no system tray.")
			}
		default:
			if a.desk != nil {
				setTrayState("Turned off — the icon stays until the app restarts, " +
					"because Fyne cannot remove a running tray icon.")
			} else {
				setTrayState("Off. Turning it on takes effect immediately.")
			}
		}
	}

	styleNames := map[TrayStyle]string{
		TrayBarAndPercent: "Battery bar and percentage",
		TrayBarOnly:       "Battery bar only",
		TrayPercentOnly:   "Percentage only",
	}
	styleSelect := widget.NewSelect([]string{
		styleNames[TrayBarAndPercent],
		styleNames[TrayBarOnly],
		styleNames[TrayPercentOnly],
	}, func(s string) {
		for st, label := range styleNames {
			if label == s {
				a.trayStyle = st
				p.SetString(prefTrayStyle, string(st))
				a.trayKey = "" // force a redraw with the new layout
				a.refreshTrayIcon()
			}
		}
	})
	styleSelect.SetSelected(styleNames[a.trayStyle])

	autoToggle := widget.NewCheck("Start automatically at login", nil)
	autoToggle.SetChecked(autostartEnabled())
	autoToggle.OnChanged = func(v bool) {
		if err := setAutostart(v); err != nil {
			a.status.SetText("autostart: " + err.Error())
			return
		}
		p.SetBool(prefAutostart, v)
	}

	trayCard := card(container.NewVBox(
		sectionTitle("PANEL INDICATOR"),
		trayToggle,
		trayState,
		restartBtn,
		container.NewBorder(nil, nil, widget.NewLabel("Style"), nil, styleSelect),
		hint("Redraws the icon straight away, once the indicator is running."),
		autoToggle,
		hint("Applies from your next login."),
	))

	// ---- low battery alerts ----
	alertToggle := widget.NewCheck("Notify me when the battery gets low", func(v bool) {
		p.SetBool(prefAlertsEnabled, v)
		a.alerts.setMuted(!v)
		if a.trayMute != nil {
			a.trayMute.Checked = v
			a.trayMenu.Refresh()
		}
	})
	alertToggle.SetChecked(!a.alerts.isMuted())

	levelEntry := widget.NewEntry()
	levelEntry.SetText(p.StringWithFallback(prefAlertLevels, formatLevels(defaultAlertPercents)))
	levelEntry.OnSubmitted = func(s string) {
		levels := parseLevels(s)
		a.alerts.setLevels(levels)
		p.SetString(prefAlertLevels, formatLevels(levels))
		levelEntry.SetText(formatLevels(levels))
		a.status.SetText("battery alert thresholds: " + formatLevels(levels) + " %")
	}
	levelEntry.OnChanged = func(string) {} // edits apply on Enter

	testBtn := widget.NewButton("Send a test notification", func() {
		go TestAlert()
	})

	alertCard := card(container.NewVBox(
		sectionTitle("LOW BATTERY ALERTS"),
		alertToggle,
		container.NewBorder(nil, nil, widget.NewLabel("Thresholds (%)"), nil, levelEntry),
		hint("Comma separated, press Enter to apply — takes effect at once. Each "+
			"threshold warns once per discharge and re-arms after charging."),
		testBtn,
	))

	// ---- backdrop ----
	logoState := widget.NewLabel("")
	logoState.Wrapping = fyne.TextWrapWord
	refreshLogoState := func() {
		if p := currentLogoPath(); p != "" {
			logoState.SetText("Using " + tildePath(p))
		} else {
			logoState.SetText("Using the built-in mark.")
		}
	}
	refreshLogoState()

	chooseBtn := widget.NewButtonWithIcon("Choose image…", theme.FolderOpenIcon(), func() {
		fd := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
			if err != nil || rc == nil {
				return
			}
			defer rc.Close()
			data, rerr := io.ReadAll(rc)
			if rerr != nil {
				dialog.ShowError(rerr, a.win)
				return
			}
			if serr := saveLogo(strings.ToLower(rc.URI().Extension()), data); serr != nil {
				dialog.ShowError(serr, a.win)
				return
			}
			a.reloadBackdrop()
			refreshLogoState()
		}, a.win)
		fd.SetFilter(storage.NewExtensionFileFilter([]string{".svg", ".png", ".jpg", ".jpeg"}))
		fd.Show()
	})

	resetBtn := widget.NewButtonWithIcon("Use built-in", theme.ViewRefreshIcon(), func() {
		if err := clearLogo(); err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		a.reloadBackdrop()
		refreshLogoState()
	})

	logoCard := card(container.NewVBox(
		sectionTitle("BACKDROP"),
		widget.NewLabel("Image shown behind the window."),
		logoState,
		container.NewHBox(chooseBtn, resetBtn),
		hint("svg, png or jpg — applied straight away. No vendor logo is bundled; "+
			"anything you pick stays on this machine."),
	))

	return container.NewPadded(container.NewVScroll(
		container.NewVBox(trayCard, alertCard, logoCard),
	))
}
