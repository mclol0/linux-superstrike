package ui

import (
	"fmt"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"superstrike/internal/hidpp"
)

// setupTray installs the panel indicator. On GNOME/Zorin this surfaces through
// the AppIndicator extension, landing in the bottom-right tray next to the
// network and drive icons.
//
// Returns false on drivers with no system tray (e.g. mobile), where the caller
// must keep the window as the only entry point.
func (a *App) setupTray() bool {
	desk, ok := a.fyne.(desktop.App)
	if !ok {
		return false
	}
	a.desk = desk

	a.trayBatt = fyne.NewMenuItem("Battery: —", nil)
	a.trayBatt.Disabled = true
	a.trayName = fyne.NewMenuItem("No device", nil)
	a.trayName.Disabled = true

	a.trayMute = fyne.NewMenuItem("Battery alerts", nil)
	a.trayMute.Checked = true
	a.trayMute.Action = func() {
		a.trayMute.Checked = !a.trayMute.Checked
		a.alerts.setMuted(!a.trayMute.Checked)
		a.trayMenu.Refresh()
	}

	menu := fyne.NewMenu("Superstrike",
		a.trayName,
		a.trayBatt,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Open control panel", func() {
			a.win.Show()
			a.win.RequestFocus()
		}),
		fyne.NewMenuItem("Refresh now", func() {
			go a.tick()
		}),
		a.trayMute,
		fyne.NewMenuItemSeparator(),
		// Deliberately not a.fyne.Quit(): that tears the tray down through
		// systray.nativeEnd(), which calls instance.conn.Close() without a nil
		// check while instance.conn is only assigned at the very end of
		// nativeStart(). If the two race, quitting panics with a nil deref
		// (fyne.io/systray v1.12.1). Nothing here needs flushing on the way
		// out — settings are written as they change and the device is left in
		// onboard mode — so exiting directly is safe and avoids the crash.
		fyne.NewMenuItem("Quit", func() { os.Exit(0) }),
	)
	desk.SetSystemTrayMenu(menu)
	desk.SetSystemTrayWindow(a.win)
	a.trayMenu = menu
	a.setTrayIcon(0, false, false)

	// With a tray present, closing the window parks the app in the panel
	// instead of exiting — otherwise the indicator would vanish on close.
	a.win.SetCloseIntercept(func() { a.win.Hide() })
	return true
}

// setTrayIcon re-renders the indicator, skipping the work when nothing about
// the displayed state changed (tick runs every few seconds).
func (a *App) setTrayIcon(pct int, charging, available bool) {
	if a.desk == nil {
		return
	}
	key := fmt.Sprintf("%d/%v/%v", pct, charging, available)
	if key == a.trayKey {
		return
	}
	a.trayKey = key
	// Must stay a .png resource: Fyne squashes SVG tray icons into a square.
	a.desk.SetSystemTrayIcon(fyne.NewStaticResource("superstrike-tray.png",
		trayIconPNG(pct, charging, available, a.trayStyle)))
}

// updateTray refreshes the indicator icon and its menu labels. Must be called
// on the Fyne goroutine.
func (a *App) updateTray(name string, batt hidpp.Battery, connected bool) {
	if a.desk == nil {
		return
	}
	switch {
	case !connected:
		a.trayName.Label = "Disconnected"
		a.trayBatt.Label = "Battery: —"
		a.setTrayIcon(0, false, false)
	case !batt.Available:
		a.trayName.Label = name
		a.trayBatt.Label = "Battery: unavailable"
		a.setTrayIcon(0, false, false)
	case batt.Charging:
		a.trayName.Label = name
		a.trayBatt.Label = fmt.Sprintf("Battery: %d%% (charging)", batt.Percent)
		a.setTrayIcon(batt.Percent, true, true)
	default:
		a.trayName.Label = name
		a.trayBatt.Label = fmt.Sprintf("Battery: %d%% (on battery)", batt.Percent)
		a.setTrayIcon(batt.Percent, false, true)
	}
	a.trayMenu.Refresh()
}

// refreshTrayIcon redraws the indicator from the last known state, used when a
// setting changes the icon layout rather than the battery reading.
func (a *App) refreshTrayIcon() {
	if a.desk == nil {
		return
	}
	pct, charging, available := a.lastBatt.Percent, a.lastBatt.Charging, a.lastBattOK
	a.trayKey = ""
	a.setTrayIcon(pct, charging, available)
}
