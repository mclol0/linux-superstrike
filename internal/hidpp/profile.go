package hidpp

import (
	"fmt"
	"strings"
	"time"
)

// MaxDPI is a practical upper bound for this sensor; values above it in a
// profile slot mean the DPI stage is disabled.
const MaxDPI = 44000

// Onboard profile editing. On the Superstrike the live DPI/report-rate setters
// (0x2202 / 0x8061) are no-ops — the device runs entirely from its onboard
// profile sector. To actually change (and persist) DPI and report rate we read
// the active profile sector, patch the relevant bytes in place, recompute the
// trailing CRC-16 and write the sector back. Patching in place (rather than
// re-serialising) preserves every byte we don't understand.
//
// Memory protocol (feature 0x8100):
//
//	fn0 getInfo (0x00) -> memory, profile, macro, count, oob, buttons, sectors, size(BE16), shift
//	fn5 read   (0x50) : [secHi, secLo, offHi, offLo] -> 16 data bytes
//	fn6 begin  (0x60) : [secHi, secLo, 0,0, lenHi, lenLo]
//	fn7 write  (0x70) : 16 data bytes
//	fn8 commit (0x80)
//
// Profile sector layout (little-endian DPI words):
//
//	[0]      report rate
//	[1]      active resolution (DPI stage) index
//	[2]      shift resolution index
//	[3..12]  five DPI stages, uint16 LE each
//	[13..15] red, green, blue
//	[size-2..] CRC-16/CCITT

var crc16Table [256]uint16

func init() {
	for i := 0; i < 256; i++ {
		c := uint16(i) << 8
		for j := 0; j < 8; j++ {
			if c&0x8000 != 0 {
				c = (c << 1) ^ 0x1021
			} else {
				c <<= 1
			}
		}
		crc16Table[i] = c
	}
}

// crc16 is CRC-16/CCITT-FALSE (init 0xFFFF), matching the device's checksum.
func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc = (crc << 8) ^ crc16Table[byte(crc>>8)^b]
	}
	return crc
}

// ProfileInfo summarises the onboard-profile memory model.
type ProfileInfo struct {
	SectorSize int
	Count      int
	Buttons    int
}

// Profile is the decoded, editable view of one profile sector.
//
// DPI is stored as five 5-byte resolution entries — [Xlo,Xhi,Ylo,Yhi,flag] —
// starting at sector byte 4, giving a fixed 800/1200/1600/2400/3200 DPI
// ladder on an unmodified profile. (An earlier version of this code assumed
// 2-byte words on a fixed stride from byte 3, which happens to alias into
// entry 1's X/Y by coincidence — explaining why those edits "succeeded" and
// read back correctly, but never reached the real sensor.) Confirmed by
// diffing a sector against a known-good DPI change made through the real
// G HUB, and independently corroborated by a from-scratch reverse-engineering
// of a different physical unit in
// github.com/mclol0/linux-superstrike/issues/1.
//
// Which entry the firmware treats as "active" is still not understood —
// raw[1] (ResIndex) does not appear to directly index the five entries, and
// the one flag-byte difference observed between an edited and unmodified
// sector (0x03 vs the usual 0x02) does not correlate with the entry actually
// in use. So WriteProfileResolution writes the requested DPI into all five
// entries rather than guessing which one is live — confirmed via blind
// (eyes-closed swipe, to rule out visual feedback biasing hand motion) raw
// kernel evdev pointer-count measurement to reliably change the real sensor
// DPI regardless of which entry is selected.
type Profile struct {
	Index      int    // 1-based slot number
	Sector     int    // memory sector holding this profile
	Enabled    bool   // whether the slot is enabled in the control sector
	Name       string // UTF-16LE profile name
	Raw          []byte // the full sector, including CRC — patch this then write
	ReportRate   byte   // raw byte 0 (power-of-two rate code)
	ReportRateHz int    // decoded polling rate in Hz
	ResIndex     int    // raw[1]: meaning not fully understood, see above
	DPIX       int    // entry 0's X DPI (display only — see above)
	DPIY       int    // entry 0's Y DPI (display only — see above)
	DPI        [5]int // each entry's X DPI (diagnostic)
	Red        byte
	Green      byte
	Blue       byte
	Buttons    [16]ButtonAction // decoded button slots (bytes 32..95)
}

// Resolution entries are 5 bytes each — [Xlo,Xhi,Ylo,Yhi,flag] — starting at
// sector byte 4. See the Profile doc comment above.
const (
	resEntryBase  = 4
	resEntrySize  = 5
	resEntryCount = 5
)

func resEntryOffset(i int) int { return resEntryBase + i*resEntrySize }

// ReportRates lists the polling rates this scheme supports, high to low.
var ReportRates = []int{8000, 4000, 2000, 1000, 500, 250, 125}

// hzForRateByte decodes the profile rate byte: Hz = 125 << byte
// (byte 0=125Hz, 1=250, 2=500, 3=1000, 4=2000, 5=4000, 6=8000). Verified by
// measuring the actual report rate after writing each byte.
func hzForRateByte(b byte) int {
	if b > 9 {
		return 0
	}
	return 125 << b
}

// rateByteForHz encodes a polling rate (Hz) to its profile byte, if supported.
func rateByteForHz(hz int) (byte, bool) {
	for v := byte(0); v <= 9; v++ {
		if 125<<v == hz {
			return v, true
		}
	}
	return 0, false
}

// decodeProfile parses a sector into a Profile (without Index/Enabled, which
// come from the control sector).
func decodeProfile(sector int, raw []byte) Profile {
	p := Profile{Sector: sector, Raw: raw, ReportRate: raw[0], ReportRateHz: hzForRateByte(raw[0]), ResIndex: int(raw[1])}
	for i := 0; i < resEntryCount; i++ {
		off := resEntryOffset(i)
		p.DPI[i] = int(raw[off]) | int(raw[off+1])<<8 // entry i's X DPI, little-endian
	}
	off0 := resEntryOffset(0)
	p.DPIX = int(raw[off0]) | int(raw[off0+1])<<8
	p.DPIY = int(raw[off0+2]) | int(raw[off0+3])<<8
	p.Red, p.Green, p.Blue = raw[13], raw[14], raw[15]
	for i := 0; i < 16; i++ {
		off := profileButtonsOffset + i*4
		if off+4 <= len(raw) {
			p.Buttons[i] = decodeButton(raw[off : off+4])
		}
	}
	p.Name = decodeProfileName(raw)
	return p
}

// decodeProfileName reads the UTF-16LE name at bytes 160..208.
func decodeProfileName(raw []byte) string {
	if len(raw) < 208 {
		return ""
	}
	var sb strings.Builder
	for i := 160; i+1 < 208; i += 2 {
		c := uint16(raw[i]) | uint16(raw[i+1])<<8
		if c == 0x0000 || c == 0xFFFF {
			break
		}
		sb.WriteRune(rune(c))
	}
	return strings.TrimSpace(sb.String())
}

// ProfileInfo reads the onboard-profile memory model (sector size etc.).
func (d *Device) ProfileInfo() (ProfileInfo, error) {
	idx, err := d.onboardIndex()
	if err != nil {
		return ProfileInfo{}, err
	}
	p, err := d.Call(idx, 0x00)
	if err != nil {
		return ProfileInfo{}, err
	}
	if len(p) < 10 {
		return ProfileInfo{}, ErrShortRead
	}
	if p[0] != 0x01 {
		return ProfileInfo{}, fmt.Errorf("unsupported onboard memory model 0x%02x", p[0])
	}
	return ProfileInfo{
		SectorSize: int(p[7])<<8 | int(p[8]),
		Count:      int(p[3]),
		Buttons:    int(p[5]),
	}, nil
}

// profileHeaders reads the control sector and returns each profile's data
// sector and enabled flag. Falls back from RAM (page 0) to ROM (page 1) when
// the RAM control page is blank.
func (d *Device) profileHeaders(idx byte, count int) ([]struct {
	Sector  int
	Enabled bool
}, error) {
	if count < 1 || count > 8 {
		count = 5 // guard against a transient bad ProfileInfo read
	}
	page := byte(0x00)
	first, err := d.Call(idx, 0x05, 0x00, 0x00, 0x00, 0x00)
	if err != nil {
		return nil, err
	}
	if len(first) >= 4 && (allEq(first[:4], 0x00) || allEq(first[:4], 0xFF)) {
		page = 0x01
	}

	var raw []byte
	for off := 0; off < 64; off += 16 {
		chunk, err := d.Call(idx, 0x05, page, 0x00, byte(off>>8), byte(off&0xFF))
		if err != nil {
			break
		}
		raw = append(raw, chunk...)
	}

	var out []struct {
		Sector  int
		Enabled bool
	}
	for i := 0; i+2 < len(raw) && len(out) < count; i += 4 {
		if raw[i] == 0xFF && raw[i+1] == 0xFF {
			break
		}
		sector := int(raw[i])<<8 | int(raw[i+1])
		// On this device profile sectors are 1..count; anything else means we've
		// walked into garbage from a partial read — stop.
		if sector < 1 || sector > count {
			break
		}
		out = append(out, struct {
			Sector  int
			Enabled bool
		}{Sector: sector, Enabled: raw[i+2] == 0x01})
	}
	return out, nil
}

// readSectorChecked reads a sector and verifies its trailing CRC-16, retrying a
// few times — so a transient partial/garbage read is rejected rather than
// decoded into bogus values.
func (d *Device) readSectorChecked(idx byte, sector, size int) ([]byte, error) {
	var raw []byte
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		raw, err = d.readSector(idx, sector, size)
		if err == nil && len(raw) == size && size >= 2 {
			stored := uint16(raw[size-2])<<8 | uint16(raw[size-1])
			if crc16(raw[:size-2]) == stored {
				return raw, nil
			}
			err = fmt.Errorf("sector 0x%04X CRC mismatch (bad read)", sector)
		}
	}
	return nil, err
}

func allEq(b []byte, v byte) bool {
	for _, x := range b {
		if x != v {
			return false
		}
	}
	return true
}

// readSector reads `size` bytes from a sector via 16-byte reads. The device
// rejects reads that run past the sector, so for a size that isn't a multiple
// of 16 the final read is aligned to (size-16) and its tail kept.
func (d *Device) readSector(idx byte, sector, size int) ([]byte, error) {
	read16 := func(off int) ([]byte, error) {
		c, err := d.Call(idx, 0x05, byte(sector>>8), byte(sector&0xFF), byte(off>>8), byte(off&0xFF))
		if err != nil {
			return nil, err
		}
		if len(c) < 16 {
			return nil, ErrShortRead
		}
		return c[:16], nil
	}
	out := make([]byte, 0, size+16)
	off := 0
	for ; off+16 <= size; off += 16 {
		c, err := read16(off)
		if err != nil {
			return nil, err
		}
		out = append(out, c...)
	}
	if off < size { // partial final chunk, read end-aligned
		c, err := read16(size - 16)
		if err != nil {
			return nil, err
		}
		out = append(out, c[16-(size-off):]...)
	}
	return out[:size], nil
}

// ReadSector reads a full sector by number (sector-size from ProfileInfo). The
// RAM control sector is 0x0000 and the ROM control sector is 0x0100.
func (d *Device) ReadSector(sector int) ([]byte, error) {
	idx, err := d.onboardIndex()
	if err != nil {
		return nil, err
	}
	info, err := d.ProfileInfo()
	if err != nil {
		return nil, err
	}
	return d.readSector(idx, sector, info.SectorSize)
}

// CurrentProfileSector returns the sector of the profile the device is running
// (fn4 getCurrentProfile).
func (d *Device) CurrentProfileSector() (int, error) {
	idx, err := d.onboardIndex()
	if err != nil {
		return 0, err
	}
	r, err := d.Call(idx, 0x04)
	if err != nil {
		return 0, err
	}
	if len(r) < 2 {
		return 0, ErrShortRead
	}
	return int(r[0])<<8 | int(r[1]), nil
}

// SetCurrentProfileSector makes the given profile active (fn3 setCurrentProfile).
// Switching only takes effect in Onboard mode, so we ensure that first.
func (d *Device) SetCurrentProfileSector(sector int) error {
	idx, err := d.onboardIndex()
	if err != nil {
		return err
	}
	_ = d.SetOnboardMode(OnboardModeOnboard)
	_, err = d.Call(idx, 0x03, byte(sector>>8), byte(sector&0xFF))
	return err
}

// Profiles enumerates every onboard profile slot, reading and decoding each.
func (d *Device) Profiles() ([]Profile, error) {
	idx, err := d.onboardIndex()
	if err != nil {
		return nil, err
	}
	info, err := d.ProfileInfo()
	if err != nil {
		return nil, err
	}
	headers, err := d.profileHeaders(idx, info.Count)
	if err != nil {
		return nil, err
	}
	out := make([]Profile, 0, len(headers))
	for i, h := range headers {
		raw, rerr := d.readSectorChecked(idx, h.Sector, info.SectorSize)
		if rerr != nil {
			continue // skip a transiently-bad slot rather than show garbage
		}
		p := decodeProfile(h.Sector, raw)
		p.Index = i + 1
		p.Enabled = h.Enabled
		out = append(out, p)
	}
	return out, nil
}

// ActiveProfile reads and decodes the profile the device is currently running,
// falling back to the first enabled slot if the current sector can't be read.
func (d *Device) ActiveProfile() (Profile, error) {
	idx, err := d.onboardIndex()
	if err != nil {
		return Profile{}, err
	}
	info, err := d.ProfileInfo()
	if err != nil {
		return Profile{}, err
	}
	sector, cerr := d.CurrentProfileSector()
	if cerr != nil || sector < 1 || sector > info.Count {
		headers, herr := d.profileHeaders(idx, info.Count)
		if herr != nil || len(headers) == 0 {
			return Profile{}, fmt.Errorf("no onboard profiles: %v", herr)
		}
		sector = headers[0].Sector
		for _, h := range headers {
			if h.Enabled {
				sector = h.Sector
				break
			}
		}
	}
	raw, err := d.readSectorChecked(idx, sector, info.SectorSize)
	if err != nil {
		return Profile{}, err
	}
	return decodeProfile(sector, raw), nil
}

// writeSector recomputes the CRC over a sector buffer and writes it back, then
// verifies by reading it again. The write step functions may not ack, so we
// tolerate read timeouts there and rely on the read-back for confirmation.
func (d *Device) writeSector(idx byte, sector int, data []byte) error {
	n := len(data)
	crc := crc16(data[:n-2])
	data[n-2] = byte(crc >> 8)
	data[n-1] = byte(crc & 0xFF)

	// begin write
	if _, err := d.Call(idx, 0x06, byte(sector>>8), byte(sector&0xFF), 0x00, 0x00, byte(n>>8), byte(n&0xFF)); err != nil && !isTimeout(err) {
		return err
	}
	// stream 16-byte chunks; the device knows the true length from begin, so a
	// short final chunk is fine.
	for off := 0; off < n; off += 16 {
		end := off + 16
		if end > n {
			end = n
		}
		if _, err := d.Call(idx, 0x07, data[off:end]...); err != nil && !isTimeout(err) {
			return err
		}
	}
	// commit
	if _, err := d.Call(idx, 0x08); err != nil && !isTimeout(err) {
		return err
	}

	// verify
	got, err := d.readSector(idx, sector, n)
	if err != nil {
		return err
	}
	if string(got) != string(data) {
		return fmt.Errorf("onboard profile write verification failed")
	}
	return nil
}

func isTimeout(err error) bool { return err == ErrTimeout }

// WriteRawProfileAndReload writes a full, already-mutated sector buffer
// (recomputing its CRC) and reloads it live via ReloadProfile. Exposed for
// diagnostics that need to patch more than patchProfileSector's callback
// covers (e.g. testing multiple resolution-stage slots at once).
func (d *Device) WriteRawProfileAndReload(sector int, raw []byte) error {
	idx, err := d.onboardIndex()
	if err != nil {
		return err
	}
	if err := d.writeSector(idx, sector, raw); err != nil {
		return err
	}
	return d.ReloadProfile(sector)
}

// WriteProfileResolution sets a profile's DPI. Since it isn't yet known which
// of the profile's 5 resolution entries the firmware treats as "active" (see
// the Profile doc comment), this writes the same X/Y into all 5 entries —
// confirmed via blind raw-evdev measurement to reliably change the real
// sensor DPI regardless of which entry is selected. Each entry's flag byte
// (and every other byte in the sector) is left untouched. Pass equal x and y
// for a normal symmetric DPI.
func (d *Device) WriteProfileResolution(sector, x, y int) error {
	if x < 100 || x > MaxDPI || y < 100 || y > MaxDPI {
		return fmt.Errorf("DPI must be 100..%d", MaxDPI)
	}
	if err := d.patchProfileSector(sector, func(raw []byte) error {
		for i := 0; i < resEntryCount; i++ {
			off := resEntryOffset(i)
			raw[off+0] = byte(x & 0xFF)
			raw[off+1] = byte(x >> 8)
			raw[off+2] = byte(y & 0xFF)
			raw[off+3] = byte(y >> 8)
			// raw[off+4] (flag byte) intentionally left untouched
		}
		return nil
	}); err != nil {
		return err
	}
	// The write above only lands in flash — like report rate, the firmware only
	// reloads a profile's live sensor state on a profile *switch*, so bounce
	// through another enabled profile and back (see SetProfileReportRateHz).
	return d.ReloadProfile(sector)
}

// SetProfileEnabled flips the enabled flag for the profile at the given 1-based
// slot index in the RAM control sector (0x0000). The flag lives at byte
// slot*4+2 of the 4-byte-per-slot header table; everything else is preserved
// and the CRC recomputed.
func (d *Device) SetProfileEnabled(index int, enabled bool) error {
	idx, err := d.onboardIndex()
	if err != nil {
		return err
	}
	info, err := d.ProfileInfo()
	if err != nil {
		return err
	}
	ctrl, err := d.readSector(idx, 0x0000, info.SectorSize)
	if err != nil {
		return err
	}
	pos := (index-1)*4 + 2
	if pos >= len(ctrl)-2 {
		return fmt.Errorf("profile slot %d out of range", index)
	}
	cp := append([]byte(nil), ctrl...)
	if enabled {
		cp[pos] = 0x01
	} else {
		cp[pos] = 0x00
	}
	return d.writeSector(idx, 0x0000, cp)
}

// SetProfileName writes the profile's name (UTF-16LE, max 24 chars) into bytes
// 160..207 of the sector, zero-padded, then re-CRCs and writes.
func (d *Device) SetProfileName(sector int, name string) error {
	return d.patchProfileSector(sector, func(raw []byte) error {
		if len(raw) < 208 {
			return fmt.Errorf("sector too small for name field")
		}
		region := raw[160:208] // 48 bytes = 24 UTF-16LE code units
		for i := range region {
			region[i] = 0x00
		}
		runes := []rune(name)
		for i, r := range runes {
			if i >= 24 {
				break
			}
			region[i*2] = byte(r)
			region[i*2+1] = byte(r >> 8)
		}
		return nil
	})
}

// SetProfileReportRate writes the raw report-rate byte of a profile sector.
func (d *Device) SetProfileReportRate(sector int, rateByte byte) error {
	return d.patchProfileSector(sector, func(raw []byte) error {
		raw[0] = rateByte
		return nil
	})
}

// SetProfileReportRateHz sets a profile's polling rate (Hz) and makes it take
// effect. The firmware only loads a profile's rate on a profile *switch*, so we
// bounce through another enabled profile and back.
func (d *Device) SetProfileReportRateHz(sector, hz int) error {
	v, ok := rateByteForHz(hz)
	if !ok {
		return fmt.Errorf("unsupported polling rate %d Hz", hz)
	}
	if err := d.SetProfileReportRate(sector, v); err != nil {
		return err
	}
	return d.ReloadProfile(sector)
}

// ReloadProfile forces the firmware to (re)apply a profile's settings by
// switching to another enabled profile and back. Requires Onboard mode.
func (d *Device) ReloadProfile(sector int) error {
	if err := d.SetOnboardMode(OnboardModeOnboard); err != nil {
		return err
	}
	other := 0
	if profs, err := d.Profiles(); err == nil {
		for _, p := range profs {
			if p.Sector != sector && p.Enabled {
				other = p.Sector
				break
			}
		}
	}
	if other != 0 {
		_ = d.SetCurrentProfileSector(other)
		time.Sleep(120 * time.Millisecond)
	}
	return d.SetCurrentProfileSector(sector)
}

// patchProfileSector reads a profile sector, applies mutate to a copy, and
// writes it back with a fresh CRC.
func (d *Device) patchProfileSector(sector int, mutate func(raw []byte) error) error {
	idx, err := d.onboardIndex()
	if err != nil {
		return err
	}
	info, err := d.ProfileInfo()
	if err != nil {
		return err
	}
	if info.SectorSize < 64 {
		return fmt.Errorf("implausible sector size %d (device busy?) — retry", info.SectorSize)
	}
	raw, err := d.readSectorChecked(idx, sector, info.SectorSize)
	if err != nil {
		return err
	}
	cp := append([]byte(nil), raw...)
	if err := mutate(cp); err != nil {
		return err
	}
	return d.writeSector(idx, sector, cp)
}
