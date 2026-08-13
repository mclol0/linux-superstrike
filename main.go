// Command superstrike is a self-contained Linux control panel for the Logitech
// PRO X 2 Superstrike. It speaks HID++ 2.0 directly over /dev/hidraw and renders
// a Fyne GUI — no G HUB, no background daemon, one binary.
//
// Without flags it launches the GUI. A few headless flags are provided for
// diagnostics / support (see -h).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"superstrike/internal/hidpp"
	"superstrike/internal/ui"
)

func main() {
	probe := flag.Bool("probe", false, "headless: print device info + HID++ feature table")
	profile := flag.Bool("profile", false, "headless: dump the active onboard profile (read-only)")
	profiles := flag.Bool("profiles", false, "headless: list all profiles + control sectors (read-only)")
	measurerate := flag.Bool("measurerate", false, "measure the ACTUAL report rate from kernel input events (move the mouse)")
	scan := flag.Bool("scan", false, "headless: list Logitech hidraw nodes + HID++ ping results")
	tray := flag.Bool("tray", false, "start in the panel indicator only, without opening the window")
	testnotify := flag.Bool("testnotify", false, "post one sample low-battery notification per threshold and exit")
	flag.Parse()

	switch {
	case *probe:
		runProbe()
	case *profile:
		runProfile()
	case *profiles:
		runProfiles()
	case *measurerate:
		runMeasureRate()
	case *scan:
		runScan()
	case *testnotify:
		ui.TestAlert()
	default:
		ui.Run(*tray)
	}
}

func openMouse() *hidpp.Device {
	devs, _, err := hidpp.Discover()
	if err != nil || len(devs) == 0 {
		fmt.Fprintln(os.Stderr, "no device:", err)
		os.Exit(1)
	}
	return pickDevice(devs)
}

// pickDevice selects the configurable Superstrike among discovered Logitech
// devices, preferring the matching name and avoiding the bare receiver.
func pickDevice(devs []*hidpp.Device) *hidpp.Device {
	pick := devs[0]
	best := -1 << 30
	for _, d := range devs {
		up := strings.ToUpper(d.Name)
		score := 0
		if strings.Contains(up, "SUPERSTRIKE") {
			score += 6
		}
		if strings.Contains(up, "RECEIVER") {
			score -= 4
		}
		if d.Has(hidpp.FeatOnboardProfile) {
			score += 4
		}
		if score > best {
			best, pick = score, d
		}
	}
	return pick
}

// runProbe prints device info and the full HID++ feature table.
func runProbe() {
	devs, perm, err := hidpp.Discover()
	if err != nil {
		fmt.Fprintln(os.Stderr, "scan error:", err)
		os.Exit(1)
	}
	if len(devs) == 0 {
		if perm {
			fmt.Fprintln(os.Stderr, "permission denied on /dev/hidraw* — run with sudo or install the udev rule")
		} else {
			fmt.Fprintln(os.Stderr, "no HID++ Logitech device responded — is the mouse powered on?")
		}
		os.Exit(1)
	}
	for _, d := range devs {
		ver, _ := d.Ping()
		name, _ := d.DeviceName()
		fmt.Printf("\n=== %s (idx 0x%02X)  HID name: %q  HID++ %s ===\n", d.Path, d.Index, d.Name, ver)
		if name != "" {
			fmt.Printf("  marketing name : %s\n", name)
		}
		if b, err := d.Battery(); err == nil && b.Available {
			fmt.Printf("  battery        : %d%% (charging=%v) via %s\n", b.Percent, b.Charging, b.Source)
		}
		if dpi, err := d.DPI(); err == nil {
			fmt.Printf("  dpi            : current=%d range=%d-%d step=%d\n", dpi.Current, dpi.Min, dpi.Max, dpi.Step)
		}
		if cur, sup, err := d.ReportRate(); err == nil {
			fmt.Printf("  report rate    : current=%dHz supported=%v\n", cur, sup)
		}
		if feats, err := d.EnumerateFeatures(); err == nil {
			fmt.Printf("  features (%d):\n", len(feats))
			for _, f := range feats {
				fmt.Printf("    idx 0x%02X  id 0x%04X  %s\n", f.Index, f.ID, f.Name())
			}
		}
		d.Close()
	}
}

// runProfile dumps the active onboard profile (read-only).
func runProfile() {
	d := openMouse()
	defer d.Close()
	info, err := d.ProfileInfo()
	if err != nil {
		fmt.Fprintln(os.Stderr, "profile info:", err)
		os.Exit(1)
	}
	fmt.Printf("memory: sectorSize=%d count=%d buttons=%d\n", info.SectorSize, info.Count, info.Buttons)
	p, err := d.ActiveProfile()
	if err != nil {
		fmt.Fprintln(os.Stderr, "read profile:", err)
		os.Exit(1)
	}
	fmt.Printf("active profile sector 0x%04X  name=%q\n", p.Sector, p.Name)
	fmt.Printf("  polling rate : %d Hz\n", p.ReportRateHz)
	fmt.Printf("  DPI (X,Y)    : %d, %d\n", p.DPIX, p.DPIY)
	fmt.Printf("  RGB          : %d,%d,%d\n", p.Red, p.Green, p.Blue)
	for i := 0; i < info.Buttons && i < 16; i++ {
		fmt.Printf("  button %d     : %s\n", i+1, p.Buttons[i].Describe())
	}
}

// runProfiles lists all profile slots.
func runProfiles() {
	d := openMouse()
	defer d.Close()
	cur, _ := d.CurrentProfileSector()
	fmt.Printf("current profile sector: 0x%04X\n", cur)
	profs, err := d.Profiles()
	if err != nil {
		fmt.Fprintln(os.Stderr, "profiles:", err)
	}
	for _, p := range profs {
		active := ""
		if p.Sector == cur {
			active = " (active)"
		}
		fmt.Printf("  slot %d  sector 0x%04X  enabled=%v%s  name=%q  DPI=(%d,%d)  %dHz\n",
			p.Index, p.Sector, p.Enabled, active, p.Name, p.DPIX, p.DPIY, p.ReportRateHz)
	}
}

// findSuperstrikeEvent locates the mouse's /dev/input/eventN node via
// /proc/bus/input/devices, preferring the pointer ("Mouse") handler.
func findSuperstrikeEvent() string {
	data, err := os.ReadFile("/proc/bus/input/devices")
	if err != nil {
		return ""
	}
	best := ""
	for _, block := range strings.Split(string(data), "\n\n") {
		if !strings.Contains(strings.ToUpper(block), "SUPERSTRIKE") {
			continue
		}
		ev := ""
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "H: Handlers=") {
				for _, h := range strings.Fields(strings.TrimPrefix(line, "H: Handlers=")) {
					if strings.HasPrefix(h, "event") {
						ev = "/dev/input/" + h
					}
				}
			}
		}
		if ev == "" {
			continue
		}
		if strings.Contains(strings.ToUpper(block), "MOUSE") {
			return ev
		}
		if best == "" {
			best = ev
		}
	}
	return best
}

// runMeasureRate counts SYN_REPORT events while the user moves the mouse,
// giving the true report rate independent of any HID++ register.
func runMeasureRate() {
	ev := findSuperstrikeEvent()
	if ev == "" {
		fmt.Fprintln(os.Stderr, "could not find the Superstrike event device")
		os.Exit(1)
	}
	f, err := os.Open(ev)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v (try with sudo)\n", ev, err)
		os.Exit(1)
	}
	defer f.Close()

	const evSize = 24 // struct input_event on amd64
	fmt.Printf("measuring %s — MOVE THE MOUSE CONTINUOUSLY for 4 seconds...\n", ev)
	buf := make([]byte, evSize*128)
	syn := 0
	deadline := time.Now().Add(4 * time.Second)
	start := time.Now()
	for time.Now().Before(deadline) {
		remain := time.Until(deadline)
		fds := []unix.PollFd{{Fd: int32(f.Fd()), Events: unix.POLLIN}}
		n, perr := unix.Poll(fds, int(remain.Milliseconds()))
		if perr == unix.EINTR {
			continue
		}
		if n <= 0 {
			break
		}
		nr, rerr := f.Read(buf)
		if rerr != nil {
			break
		}
		for off := 0; off+evSize <= nr; off += evSize {
			typ := uint16(buf[off+16]) | uint16(buf[off+17])<<8
			code := uint16(buf[off+18]) | uint16(buf[off+19])<<8
			if typ == 0x00 && code == 0x00 { // EV_SYN / SYN_REPORT
				syn++
			}
		}
	}
	elapsed := time.Since(start).Seconds()
	if syn == 0 {
		fmt.Println("no events seen — did you move the mouse? (also try sudo)")
		return
	}
	fmt.Printf("counted %d reports in %.2fs  ≈  %.0f Hz (actual report rate)\n", syn, elapsed, float64(syn)/elapsed)
}

// runScan lists every Logitech hidraw node and whether it answers HID++.
func runScan() {
	nodes, _ := filepath.Glob("/dev/hidraw*")
	sort.Strings(nodes)
	fmt.Println("Logitech (046d) hidraw nodes:")
	for _, node := range nodes {
		base := filepath.Base(node)
		data, _ := os.ReadFile(fmt.Sprintf("/sys/class/hidraw/%s/device/uevent", base))
		var id, name string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "HID_ID=") {
				id = strings.TrimPrefix(line, "HID_ID=")
			} else if strings.HasPrefix(line, "HID_NAME=") {
				name = strings.TrimPrefix(line, "HID_NAME=")
			}
		}
		if !strings.Contains(strings.ToUpper(id), "046D") {
			continue
		}
		fmt.Printf("\n%s  id=%s  name=%q\n", node, id, name)
		f, oerr := os.OpenFile(node, os.O_RDWR, 0)
		if oerr != nil {
			fmt.Printf("  open: %v\n", oerr)
			continue
		}
		f.Close()
		for _, idx := range []byte{0x01, 0xFF, 0x02, 0x03} {
			d, err := hidpp.Open(node, idx)
			if err != nil {
				fmt.Printf("  idx 0x%02X: open err %v\n", idx, err)
				continue
			}
			d.Timeout = 400 * time.Millisecond
			ver, perr := d.Ping()
			if perr == nil {
				nm, _ := d.DeviceName()
				fmt.Printf("  idx 0x%02X: HID++ %s  name=%q  <-- responds\n", idx, ver, nm)
			} else {
				fmt.Printf("  idx 0x%02X: no response\n", idx)
			}
			d.Close()
		}
	}
}
