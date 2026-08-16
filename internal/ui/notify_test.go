package ui

import "testing"

type fired struct {
	title   string
	urgency byte
}

// newTestAlerts returns an alerts object that records instead of posting to
// D-Bus, plus a pointer to the recording slice.
func newTestAlerts() (*batteryAlerts, *[]fired) {
	var log []fired
	n := &batteryAlerts{
		fired:  map[int]bool{},
		levels: makeLevels(defaultAlertPercents),
	}
	n.send = func(title, body string, urgency byte) {
		log = append(log, fired{title, urgency})
	}
	return n, &log
}

// feed pushes a discharging sequence of readings through the watcher.
func feed(n *batteryAlerts, pcts ...int) {
	for _, p := range pcts {
		n.check(p, false, true)
	}
}

func TestFiresOncePerThreshold(t *testing.T) {
	n, log := newTestAlerts()
	feed(n, 50, 30, 21, 20, 19, 15, 11, 10, 9, 6, 5, 4, 2)

	if len(*log) != 3 {
		t.Fatalf("want 3 alerts (20/10/5), got %d: %+v", len(*log), *log)
	}
	wantUrgency := []byte{1, 2, 2}
	for i, f := range *log {
		if f.urgency != wantUrgency[i] {
			t.Errorf("alert %d: urgency %d, want %d", i, f.urgency, wantUrgency[i])
		}
	}
}

// A reading hovering on a boundary must not re-notify on every poll.
func TestNoRepeatWhileHovering(t *testing.T) {
	n, log := newTestAlerts()
	feed(n, 21, 20, 21, 20, 20, 21, 20)

	if len(*log) != 1 {
		t.Fatalf("want 1 alert, got %d: %+v", len(*log), *log)
	}
}

// Climbing clear of a threshold re-arms it; rearmMargin decides how far.
func TestRearmsAfterRecovery(t *testing.T) {
	n, log := newTestAlerts()
	feed(n, 20)             // fires
	feed(n, 20+rearmMargin) // still inside the margin: no re-arm
	feed(n, 20)
	if len(*log) != 1 {
		t.Fatalf("re-armed too eagerly: %+v", *log)
	}
	feed(n, 20+rearmMargin+1, 20) // now clear of the margin, must fire again
	if len(*log) != 2 {
		t.Fatalf("want 2 alerts after recovery, got %d: %+v", len(*log), *log)
	}
}

// A single steep drop past several thresholds warns once, at the lowest level.
func TestSteepDropWarnsOnce(t *testing.T) {
	n, log := newTestAlerts()
	feed(n, 40, 4)

	if len(*log) != 1 {
		t.Fatalf("want 1 alert, got %d: %+v", len(*log), *log)
	}
	if (*log)[0].urgency != 2 {
		t.Errorf("want critical urgency, got %d", (*log)[0].urgency)
	}
}

// Charging disarms everything so the next discharge cycle warns again.
func TestChargingResets(t *testing.T) {
	n, log := newTestAlerts()
	feed(n, 8) // fires 10 and 20
	n.check(30, true, true)
	n.check(60, true, true)
	feed(n, 19) // discharging again: 20% must fire once more

	if len(*log) != 2 {
		t.Fatalf("want 2 alerts across the cycle, got %d: %+v", len(*log), *log)
	}
}

// No reading at all must not be treated as 0%.
func TestUnavailableNeverFires(t *testing.T) {
	n, log := newTestAlerts()
	for i := 0; i < 5; i++ {
		n.check(0, false, false)
	}
	if len(*log) != 0 {
		t.Fatalf("alerts fired with no battery reading: %+v", *log)
	}
}

func TestMutedSuppressesDelivery(t *testing.T) {
	n, log := newTestAlerts()
	n.setMuted(true)
	feed(n, 19, 9, 4)
	if len(*log) != 0 {
		t.Fatalf("muted watcher still delivered: %+v", *log)
	}
}
