// Package hidpp implements just enough of the Logitech HID++ 2.0 protocol,
// spoken directly over a Linux /dev/hidraw node, to drive the PRO X 2
// Superstrike (and most modern Logitech mice) without any external C library.
//
// HID++ is a request/response protocol carried inside HID reports. A request
// names a *feature index* and a *function* within that feature; the device
// replies with a report echoing the same indices plus our software id so we
// can correlate it against asynchronous notifications on the same node.
package hidpp

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Report type IDs and their on-the-wire lengths (including the leading report
// id byte). We always *send* long reports — every modern device accepts them —
// but the device may answer with either short or long.
const (
	ReportShort    = 0x10
	ReportLong     = 0x11
	ReportVeryLong = 0x12

	shortLen    = 7
	longLen     = 20
	veryLongLen = 64
)

// softwareID tags our requests (valid range 1..15). The device copies the low
// nibble of byte 3 back into its response, letting us ignore notifications.
const softwareID = 0x0A

// Error feature-index sentinels that appear in byte 2 of a reply.
const (
	errMarkerV10 = 0xFF // HID++ 1.0 style error
	errMarkerV20 = 0x8F // HID++ 2.0 style error
)

var (
	ErrTimeout   = errors.New("hidpp: timed out waiting for device response")
	ErrShortRead = errors.New("hidpp: response shorter than expected")
)

// HIDPPError is a structured error returned by the device itself.
type HIDPPError struct {
	FeatureIndex byte
	Function     byte
	Code         byte
	Raw          []byte // full error report, for diagnostics
}

func (e *HIDPPError) Error() string {
	return fmt.Sprintf("hidpp: device error 0x%02x (%s) — feature idx 0x%02x fn 0x%02x [raw % x]",
		e.Code, errName(e.Code), e.FeatureIndex, e.Function, e.Raw)
}

func errName(code byte) string {
	switch code {
	case 0x00:
		return "no error"
	case 0x01:
		return "unknown"
	case 0x02:
		return "invalid argument"
	case 0x03:
		return "out of range"
	case 0x04:
		return "hardware error"
	case 0x05:
		return "logitech internal"
	case 0x06:
		return "invalid feature index"
	case 0x07:
		return "invalid function id"
	case 0x08:
		return "busy"
	case 0x09:
		return "unsupported"
	default:
		return "unrecognised"
	}
}

// Device is an open HID++ channel to one physical mouse, reachable at a given
// device index (0xFF for a directly-attached / dj child node, 1..6 for a slot
// on a receiver). All calls are serialised by mu so request/response pairs on
// the shared hidraw fd never interleave.
type Device struct {
	mu      sync.Mutex
	f       *os.File
	Path    string // /dev/hidrawN
	Index   byte   // HID++ device index
	Name    string // HID name from sysfs, if known
	Timeout time.Duration
}

// Open opens a hidraw node for HID++ traffic. The caller is responsible for
// having read/write permission (see the shipped udev rule).
func Open(path string, index byte) (*Device, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	// 4s matches Solaar's DEFAULT_TIMEOUT; some setters reply slowly.
	return &Device{f: f, Path: path, Index: index, Timeout: 4 * time.Second}, nil
}

func (d *Device) Close() error {
	if d.f == nil {
		return nil
	}
	return d.f.Close()
}

// Call issues featureIndex.function(params...) and returns the parameter bytes
// of the response (everything after the 4-byte HID++ header). It transparently
// retries reads past unrelated notifications until the matching reply arrives
// or the timeout elapses.
func (d *Device) Call(featureIndex, function byte, params ...byte) ([]byte, error) {
	return d.callWithTimeout(d.Timeout, featureIndex, function, params...)
}

// callWithTimeout is used by commands with known acknowledgement behaviour.
// Most HID++ calls keep the normal device timeout; profile-memory writes use a
// shorter wait because some firmware applies them without sending a reply.
func (d *Device) callWithTimeout(timeout time.Duration, featureIndex, function byte, params ...byte) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	req := make([]byte, longLen)
	req[0] = ReportLong
	req[1] = d.Index
	req[2] = featureIndex
	req[3] = (function << 4) | softwareID
	copy(req[4:], params)

	// Drain any reports left in the kernel buffer from earlier traffic
	// (notifications, late acks) so we don't mis-correlate a stale report to
	// this request.
	d.drain()

	if _, err := d.f.Write(req); err != nil {
		return nil, fmt.Errorf("hidpp: write: %w", err)
	}

	// Because we drained stale traffic just before writing, the next report
	// addressed to this feature with our software id is this request's reply.
	// We match on feature index + software id (low nibble) rather than the full
	// function byte, since some setters echo the function nibble inconsistently.
	deadline := time.Now().Add(timeout)
	for {
		resp, err := d.read(deadline)
		if err != nil {
			return nil, err
		}
		if len(resp) < 4 {
			continue
		}
		// Accept our device index, or 0xFF (some firmware answers on the wildcard).
		if resp[1] != d.Index && resp[1] != 0xFF {
			continue
		}
		// Device-reported error (HID++ 2.0) referencing our request.
		if resp[2] == errMarkerV20 && len(resp) >= 6 &&
			resp[3] == featureIndex && (resp[4]&0x0F) == softwareID {
			return nil, &HIDPPError{FeatureIndex: resp[3], Function: resp[4] >> 4, Code: resp[5], Raw: append([]byte(nil), resp...)}
		}
		if resp[2] == errMarkerV10 && len(resp) >= 5 && (resp[3]&0x0F) == softwareID {
			return nil, &HIDPPError{FeatureIndex: featureIndex, Function: function, Code: resp[4], Raw: append([]byte(nil), resp...)}
		}
		// Matching successful response: same feature idx, same software id.
		if resp[2] == featureIndex && (resp[3]&0x0F) == softwareID {
			return resp[4:], nil
		}
		// Otherwise it's an unrelated notification — keep waiting.
	}
}

// drain consumes any immediately-available reports without blocking.
func (d *Device) drain() {
	for {
		fds := []unix.PollFd{{Fd: int32(d.f.Fd()), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 0)
		if err != nil || n == 0 {
			return
		}
		buf := make([]byte, veryLongLen)
		if _, err := d.f.Read(buf); err != nil {
			return
		}
	}
}

// read blocks (with poll) for the next inbound report until deadline.
func (d *Device) read(deadline time.Time) ([]byte, error) {
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, ErrTimeout
		}
		fds := []unix.PollFd{{Fd: int32(d.f.Fd()), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, int(remaining.Milliseconds()))
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return nil, fmt.Errorf("hidpp: poll: %w", err)
		}
		if n == 0 {
			return nil, ErrTimeout
		}
		buf := make([]byte, veryLongLen)
		nr, err := d.f.Read(buf)
		if err != nil {
			return nil, fmt.Errorf("hidpp: read: %w", err)
		}
		if nr == 0 {
			continue
		}
		return buf[:nr], nil
	}
}
