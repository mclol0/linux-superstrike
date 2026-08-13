package ui

import (
	"fmt"
	"sort"
	"sync"

	"github.com/godbus/dbus/v5"
)

// Low-battery alerts.
//
// Notifications go out over org.freedesktop.Notifications directly rather than
// through fyne.App.SendNotification, because the desktop spec's urgency hint
// and replaces_id are what make this behave: a 5% warning should be critical
// (it stays on screen until dismissed) and each new warning should replace the
// previous bubble instead of stacking several of them.

type alertLevel struct {
	pct     int
	urgency byte // 0 low, 1 normal, 2 critical
	title   string
}

// defaultAlertPercents are the out-of-the-box thresholds; the Settings tab can
// replace them.
var defaultAlertPercents = []int{20, 10, 5}

// rearmMargin is how far the level must climb back above a threshold before
// that threshold can fire again. Without it, a reading hovering on the
// boundary would notify on every poll.
const rearmMargin = 3

// makeLevels builds the level table from bare percentages, sorted high to low.
// Anything at or below 10% is treated as critical so it stays on screen.
func makeLevels(pcts []int) []alertLevel {
	cleaned := make([]int, 0, len(pcts))
	seen := map[int]bool{}
	for _, p := range pcts {
		if p <= 0 || p > 100 || seen[p] {
			continue
		}
		seen[p] = true
		cleaned = append(cleaned, p)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(cleaned)))

	out := make([]alertLevel, 0, len(cleaned))
	for _, p := range cleaned {
		lv := alertLevel{pct: p, urgency: 1, title: "Mouse battery low"}
		if p <= 10 {
			lv.urgency = 2
			lv.title = "Mouse battery very low"
		}
		if p <= 5 {
			lv.title = "Mouse battery critical"
		}
		out = append(out, lv)
	}
	return out
}

type batteryAlerts struct {
	mu      sync.Mutex
	levels  []alertLevel
	fired   map[int]bool
	lastID  uint32
	muted   bool
	conn    *dbus.Conn
	connErr error

	// send is the delivery hook; swapped out in tests. Called with n.mu held.
	send func(title, body string, urgency byte)
}

func newBatteryAlerts() *batteryAlerts {
	n := &batteryAlerts{
		fired:  map[int]bool{},
		levels: makeLevels(defaultAlertPercents),
	}
	n.send = n.sendLocked
	return n
}

// setLevels replaces the thresholds and disarms everything, so a settings
// change cannot fire a stale warning on the next poll.
func (n *batteryAlerts) setLevels(pcts []int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.levels = makeLevels(pcts)
	n.fired = map[int]bool{}
}

func (n *batteryAlerts) setMuted(v bool) {
	n.mu.Lock()
	n.muted = v
	n.mu.Unlock()
}

func (n *batteryAlerts) isMuted() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.muted
}

// TestAlert posts one sample notification per default threshold, for checking
// that the desktop actually shows them without waiting for a flat battery.
func TestAlert() {
	n := newBatteryAlerts()
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, lv := range n.levels {
		n.lastID = 0 // don't replace: show them all at once
		n.send(lv.title,
			fmt.Sprintf("PRO X2 Superstrike is at %d%%. Connect the cable to charge.", lv.pct),
			lv.urgency)
	}
}

// check evaluates one battery reading and fires at most one notification.
// Safe to call from any goroutine.
func (n *batteryAlerts) check(pct int, charging, available bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Charging (or no reading) disarms everything: the next discharge cycle
	// should warn again from scratch.
	if !available || charging {
		clear(n.fired)
		return
	}

	// Re-arm thresholds the battery has climbed clear of.
	for _, lv := range n.levels {
		if n.fired[lv.pct] && pct > lv.pct+rearmMargin {
			delete(n.fired, lv.pct)
		}
	}

	// Fire the lowest crossed threshold that has not fired yet.
	var hit *alertLevel
	for i := range n.levels {
		if pct <= n.levels[i].pct && !n.fired[n.levels[i].pct] {
			hit = &n.levels[i]
		}
	}
	if hit == nil {
		return
	}
	// Mark every threshold at or above the one that fired, so a drop from 25%
	// straight to 4% warns once (critical) rather than three times.
	for _, lv := range n.levels {
		if lv.pct >= hit.pct {
			n.fired[lv.pct] = true
		}
	}
	if n.muted {
		return
	}
	n.send(hit.title,
		fmt.Sprintf("PRO X2 Superstrike is at %d%%. Connect the cable to charge.", pct),
		hit.urgency)
}

// sendLocked posts the notification. Caller holds n.mu.
func (n *batteryAlerts) sendLocked(title, body string, urgency byte) {
	if n.conn == nil {
		if n.connErr != nil {
			return // already failed once; don't retry on every poll
		}
		conn, err := dbus.SessionBus()
		if err != nil {
			n.connErr = err
			return
		}
		n.conn = conn
	}
	obj := n.conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	hints := map[string]dbus.Variant{"urgency": dbus.MakeVariant(urgency)}
	// -1 = let the server pick the timeout; critical notifications stay until
	// dismissed regardless.
	call := obj.Call("org.freedesktop.Notifications.Notify", 0,
		"Superstrike", n.lastID, "superstrike", title, body,
		[]string{}, hints, int32(-1))
	if call.Err != nil {
		return
	}
	var id uint32
	if err := call.Store(&id); err == nil {
		n.lastID = id // replace this bubble next time instead of stacking
	}
}
