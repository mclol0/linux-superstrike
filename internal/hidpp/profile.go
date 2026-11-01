package hidpp

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf16"
)

// MaxDPI is a practical upper bound for this sensor; values above it in a
// profile slot mean the DPI stage is disabled.
const MaxDPI = 44000

// Profile-memory write functions commonly complete without acknowledging the
// request. A short wait preserves replies from devices that do send them while
// avoiding the general four-second HID++ timeout for every 16-byte chunk.
const profileWriteAckTimeout = 100 * time.Millisecond

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
// Profile sector layouts seen in the field:
//
//	Legacy/configured: [3..12] five uint16 LE words, active X/Y at 9..12.
//	Pristine/wired:    [4..28] five [Xlo,Xhi,Ylo,Yhi,0x02] stages.
//
// Factory profiles may live in ROM sectors 0x0101.. and have 0xFFFF instead
// of a trailing CRC. Before editing one we clone it into the corresponding RAM
// sector, add a valid CRC and a minimal RAM control table, then activate it.
//
// Common fields:
//
//	[0] report rate
//	[1] active resolution index
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
// Configured and pristine firmware have different DPI layouts. The decoder
// detects both and exposes their effective X/Y value through the same fields.
type Profile struct {
	Index        int    // 1-based slot number
	Sector       int    // memory sector holding this profile
	Enabled      bool   // whether the slot is enabled in the control sector
	Name         string // UTF-16LE profile name
	Raw          []byte // the full sector, including CRC — patch this then write
	ReportRate   byte   // raw byte 0 (power-of-two rate code)
	ReportRateHz int    // decoded polling rate in Hz
	ResIndex     int    // raw[1]: resolution index
	DPIX         int    // active resolution X DPI
	DPIY         int    // active resolution Y DPI
	DPI          [5]int // raw resolution words (diagnostic)
	DPIStages    [5]DPIStage
	HasDPIStages bool
	Buttons      [16]ButtonAction // decoded button slots (bytes 32..95)
}

// DPIStage is one of the five resolutions the mouse can cycle through.
type DPIStage struct {
	Index   int
	X       int
	Y       int
	Enabled bool
}

// In the legacy/configured layout the active X and Y values are the last two
// of five uint16 words at bytes 9..12.
const (
	dpiXSlot = 3
	dpiYSlot = 4
)

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
	if profileUsesFiveByteDPI(raw) {
		p.HasDPIStages = true
		for i := 0; i < 5; i++ {
			off := 4 + i*5
			x := int(raw[off]) | int(raw[off+1])<<8
			y := int(raw[off+2]) | int(raw[off+3])<<8
			p.DPI[i] = x
			p.DPIStages[i] = DPIStage{Index: i, X: x, Y: y, Enabled: raw[off+4]&0x02 != 0}
			if i == p.ResIndex && p.DPIStages[i].Enabled {
				p.DPIX, p.DPIY = x, y
			}
		}
		if p.DPIX == 0 {
			for _, stage := range p.DPIStages {
				if stage.Enabled {
					p.DPIX, p.DPIY = stage.X, stage.Y
					break
				}
			}
		}
	} else {
		for i := 0; i < 5; i++ {
			p.DPI[i] = int(raw[3+i*2]) | int(raw[4+i*2])<<8
		}
		p.DPIX, p.DPIY = p.DPI[dpiXSlot], p.DPI[dpiYSlot]
	}
	for i := 0; i < 16; i++ {
		off := profileButtonsOffset + i*4
		if off+4 <= len(raw) {
			p.Buttons[i] = decodeButton(raw[off : off+4])
		}
	}
	p.Name = decodeProfileName(raw)
	return p
}

func profileUsesFiveByteDPI(raw []byte) bool {
	if len(raw) < 29 {
		return false
	}
	valid := 0
	for i := 0; i < 5; i++ {
		off := 4 + i*5
		x := int(raw[off]) | int(raw[off+1])<<8
		y := int(raw[off+2]) | int(raw[off+3])<<8
		if x >= 100 && x <= MaxDPI && y >= 100 && y <= MaxDPI {
			valid++
		}
	}
	return valid >= 3
}

func patchResolution(raw []byte, x, y int) error {
	if profileUsesFiveByteDPI(raw) {
		stage := int(raw[1])
		if stage < 0 || stage > 4 {
			stage = 0
		}
		return patchDPIStage(raw, stage, x, y, true, true)
	}
	if len(raw) < 13 {
		return fmt.Errorf("profile sector too small for DPI fields")
	}
	raw[3+dpiXSlot*2], raw[4+dpiXSlot*2] = byte(x), byte(x>>8)
	raw[3+dpiYSlot*2], raw[4+dpiYSlot*2] = byte(y), byte(y>>8)
	return nil
}

func patchDPIStage(raw []byte, stage, x, y int, enabled, makeDefault bool) error {
	if !profileUsesFiveByteDPI(raw) {
		return fmt.Errorf("this profile does not expose five editable DPI stages")
	}
	if stage < 0 || stage > 4 {
		return fmt.Errorf("DPI stage %d is out of range", stage+1)
	}
	if !enabled {
		enabledCount := 0
		for i := 0; i < 5; i++ {
			off := 4 + i*5
			if i != stage && raw[off+4]&0x02 != 0 {
				enabledCount++
			}
		}
		if enabledCount == 0 {
			return fmt.Errorf("at least one DPI stage must remain enabled")
		}
	}
	off := 4 + stage*5
	raw[off], raw[off+1] = byte(x), byte(x>>8)
	raw[off+2], raw[off+3] = byte(y), byte(y>>8)
	if enabled {
		raw[off+4] |= 0x02
	} else {
		raw[off+4] &^= 0x02
		if int(raw[1]) == stage {
			for i := 0; i < 5; i++ {
				if raw[4+i*5+4]&0x02 != 0 {
					raw[1] = byte(i)
					break
				}
			}
		}
	}
	if makeDefault {
		if !enabled {
			return fmt.Errorf("a disabled DPI stage cannot be the default")
		}
		raw[1] = byte(stage)
	}
	return nil
}

// decodeProfileName reads the UTF-16LE name at bytes 160..208.
func decodeProfileName(raw []byte) string {
	if len(raw) < 208 {
		return ""
	}
	units := make([]uint16, 0, 24)
	for i := 160; i+1 < 208; i += 2 {
		c := uint16(raw[i]) | uint16(raw[i+1])<<8
		if c == 0x0000 || c == 0xFFFF {
			break
		}
		units = append(units, c)
	}
	return strings.TrimSpace(string(utf16.Decode(units)))
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
		minSector, maxSector := 1, count
		if page == 0x01 {
			// Some firmware stores page-relative 0x0001.. entries in the ROM
			// control table; normalize them to the absolute 0x0101.. sectors.
			if sector >= 1 && sector <= count {
				sector += 0x0100
			}
			minSector, maxSector = 0x0101, 0x0100+count
		}
		if sector < minSector || sector > maxSector {
			break
		}
		out = append(out, struct {
			Sector  int
			Enabled bool
		}{Sector: sector, Enabled: raw[i+2] == 0x01})
	}
	return out, nil
}

func validProfileSector(sector, count int) bool {
	return sector >= 1 && sector <= count || sector >= 0x0101 && sector <= 0x0100+count
}

func (d *Device) readProfileSector(idx byte, sector, size int) ([]byte, error) {
	if sector >= 0x0100 {
		raw, err := d.readSector(idx, sector, size)
		if err != nil {
			return nil, err
		}
		if allEq(raw, 0xFF) || allEq(raw, 0x00) {
			return nil, fmt.Errorf("profile sector 0x%04X is blank", sector)
		}
		return raw, nil
	}
	return d.readSectorChecked(idx, sector, size)
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
		raw, rerr := d.readProfileSector(idx, h.Sector, info.SectorSize)
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
	if cerr != nil || !validProfileSector(sector, info.Count) {
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
	raw, err := d.readProfileSector(idx, sector, info.SectorSize)
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
	if _, err := d.callWithTimeout(profileWriteAckTimeout, idx, 0x06, byte(sector>>8), byte(sector&0xFF), 0x00, 0x00, byte(n>>8), byte(n&0xFF)); err != nil && !isTimeout(err) {
		return err
	}
	// stream 16-byte chunks; the device knows the true length from begin, so a
	// short final chunk is fine.
	for off := 0; off < n; off += 16 {
		end := off + 16
		if end > n {
			end = n
		}
		if _, err := d.callWithTimeout(profileWriteAckTimeout, idx, 0x07, data[off:end]...); err != nil && !isTimeout(err) {
			return err
		}
	}
	// commit
	if _, err := d.callWithTimeout(profileWriteAckTimeout, idx, 0x08); err != nil && !isTimeout(err) {
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

// WriteProfileResolution sets the active resolution's X and Y DPI for a
// profile, touching only those two words (read from raw[1]) and leaving every
// other byte — including the disabled placeholder slots — intact. Pass equal x
// and y for a normal symmetric DPI.
func (d *Device) WriteProfileResolution(sector, x, y int) error {
	if x < 100 || x > MaxDPI || y < 100 || y > MaxDPI {
		return fmt.Errorf("DPI must be 100..%d", MaxDPI)
	}
	actual, err := d.patchProfileSectorResolved(sector, func(raw []byte) error {
		return patchResolution(raw, x, y)
	})
	if err != nil {
		return err
	}
	return d.ReloadProfile(actual)
}

// WriteProfileDPIStage edits one of the five onboard DPI stages. Enabled
// stages are included when a button assigned to Next/Previous/Cycle DPI is
// pressed. makeDefault also selects the stage loaded when the profile starts.
func (d *Device) WriteProfileDPIStage(sector, stage, x, y int, enabled, makeDefault bool) (int, error) {
	if x < 100 || x > MaxDPI || y < 100 || y > MaxDPI {
		return 0, fmt.Errorf("DPI must be 100..%d", MaxDPI)
	}
	actual, err := d.patchProfileSectorResolved(sector, func(raw []byte) error {
		return patchDPIStage(raw, stage, x, y, enabled, makeDefault)
	})
	if err != nil {
		return 0, err
	}
	if err := d.ReloadProfile(actual); err != nil {
		return 0, err
	}
	if makeDefault {
		if err := d.activateLiveDPI(x); err != nil {
			return 0, err
		}
	}
	return actual, nil
}

// activateLiveDPI changes the sensor's current value without replacing the
// five onboard stages. The extended DPI setter is ignored while an onboard
// profile owns the sensor, so hand control to the host and keep it there. The
// desktop app deliberately leaves that device state intact when its HID handle
// closes because returning to onboard mode selects a stale stage on this model.
func (d *Device) activateLiveDPI(dpi int) error {
	lod := byte(0)
	if info, err := d.DPI(); err == nil {
		lod = info.LOD
	}
	mode, _ := d.OnboardMode()
	if mode != OnboardModeHost {
		if err := d.SetOnboardMode(OnboardModeHost); err != nil {
			return fmt.Errorf("enter host control to select DPI: %w", err)
		}
		// Mode changes are acknowledged before the sensor has finished changing
		// owners. Writing immediately can be accepted but silently discarded.
		time.Sleep(180 * time.Millisecond)
	}
	if err := d.setDPIWithLOD(dpi, lod); err != nil {
		_ = d.SetOnboardMode(OnboardModeOnboard)
		return fmt.Errorf("select live DPI: %w", err)
	}
	time.Sleep(40 * time.Millisecond)
	return nil
}

// SelectLiveDPI changes only the mouse's current sensitivity. It does not
// rewrite, verify, or reload the onboard profile sector.
func (d *Device) SelectLiveDPI(dpi int) error {
	if dpi < 100 || dpi > MaxDPI {
		return fmt.Errorf("DPI must be 100..%d", MaxDPI)
	}
	return d.activateLiveDPI(dpi)
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
	if len(ctrl) >= 4 && (allEq(ctrl[:4], 0x00) || allEq(ctrl[:4], 0xFF)) {
		sector, serr := d.CurrentProfileSector()
		if serr != nil || sector < 0x0101 {
			return fmt.Errorf("no editable RAM profile is available")
		}
		if _, err := d.patchProfileSectorResolved(sector, func([]byte) error { return nil }); err != nil {
			return err
		}
		ctrl, err = d.readSector(idx, 0x0000, info.SectorSize)
		if err != nil {
			return err
		}
		index = 1
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
func (d *Device) SetProfileName(sector int, name string) (int, error) {
	return d.patchProfileSectorResolved(sector, func(raw []byte) error {
		return patchProfileName(raw, name)
	})
}

func patchProfileName(raw []byte, name string) error {
	if len(raw) < 208 {
		return fmt.Errorf("sector too small for name field")
	}
	units := utf16.Encode([]rune(name))
	if len(units) > 24 {
		return fmt.Errorf("profile name must be 24 characters or fewer")
	}
	region := raw[160:208] // 48 bytes = 24 UTF-16LE code units
	for i := range region {
		region[i] = 0x00
	}
	for i, unit := range units {
		region[i*2] = byte(unit)
		region[i*2+1] = byte(unit >> 8)
	}
	return nil
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
func (d *Device) SetProfileReportRateHz(sector, hz int) (int, error) {
	v, ok := rateByteForHz(hz)
	if !ok {
		return 0, fmt.Errorf("unsupported polling rate %d Hz", hz)
	}
	actual, err := d.patchProfileSectorResolved(sector, func(raw []byte) error {
		raw[0] = v
		return nil
	})
	if err != nil {
		return 0, err
	}
	if err := d.ReloadProfile(actual); err != nil {
		return 0, err
	}
	return actual, nil
}

// ReloadProfile forces the firmware to (re)apply a profile's settings by
// switching to another enabled profile and back. Requires Onboard mode.
func (d *Device) ReloadProfile(sector int) error {
	if err := d.SetOnboardMode(OnboardModeOnboard); err != nil {
		return err
	}
	other := 0
	// Only the lightweight control table is needed to find a bounce target.
	// Profiles() also reads and decodes every 255-byte data sector.
	if idx, err := d.onboardIndex(); err == nil {
		if info, err := d.ProfileInfo(); err == nil {
			if headers, err := d.profileHeaders(idx, info.Count); err == nil {
				for _, header := range headers {
					if header.Sector != sector && header.Enabled {
						other = header.Sector
						break
					}
				}
			}
		}
	}
	if other != 0 {
		_ = d.SetCurrentProfileSector(other)
		time.Sleep(120 * time.Millisecond)
	} else if sector > 0 && sector < 0x0100 {
		// A pristine mouse initially runs the matching factory profile in ROM.
		// Once it has been cloned to a single RAM slot, selecting that same RAM
		// sector again is a no-op on some firmware. Bounce through the read-only
		// factory counterpart so the edited sector is actually reloaded.
		rom := 0x0100 | sector
		if err := d.SetCurrentProfileSector(rom); err == nil {
			time.Sleep(120 * time.Millisecond)
		} else {
			// Firmware that hides the ROM sector after RAM has been initialized
			// still reloads the profile when onboard mode is re-entered.
			_ = d.SetOnboardMode(OnboardModeHost)
			time.Sleep(80 * time.Millisecond)
			_ = d.SetOnboardMode(OnboardModeOnboard)
		}
	}
	return d.SetCurrentProfileSector(sector)
}

// patchProfileSector reads a profile sector, applies mutate to a copy, and
// writes it back with a fresh CRC.
func (d *Device) patchProfileSector(sector int, mutate func(raw []byte) error) error {
	_, err := d.patchProfileSectorResolved(sector, mutate)
	return err
}

func (d *Device) patchProfileSectorResolved(sector int, mutate func(raw []byte) error) (int, error) {
	idx, err := d.onboardIndex()
	if err != nil {
		return 0, err
	}
	info, err := d.ProfileInfo()
	if err != nil {
		return 0, err
	}
	if info.SectorSize < 64 {
		return 0, fmt.Errorf("implausible sector size %d (device busy?) — retry", info.SectorSize)
	}
	raw, err := d.readProfileSector(idx, sector, info.SectorSize)
	if err != nil {
		return 0, err
	}
	actual := sector
	bootstrap := sector >= 0x0100
	if bootstrap {
		actual = sector & 0x00FF
		if actual < 1 || actual > info.Count {
			actual = 1
		}
	}
	cp := append([]byte(nil), raw...)
	if err := mutate(cp); err != nil {
		return 0, err
	}
	if err := d.writeSector(idx, actual, cp); err != nil {
		return 0, err
	}
	if bootstrap {
		if err := d.writeMinimalControlSector(idx, info.SectorSize, actual); err != nil {
			return 0, err
		}
		if err := d.SetCurrentProfileSector(actual); err != nil {
			return 0, err
		}
	}
	return actual, nil
}

func (d *Device) writeMinimalControlSector(idx byte, size, sector int) error {
	if size < 8 || sector < 1 || sector > 0xFF {
		return fmt.Errorf("cannot create RAM profile control sector")
	}
	ctrl := make([]byte, size)
	for i := range ctrl {
		ctrl[i] = 0xFF
	}
	ctrl[0], ctrl[1], ctrl[2], ctrl[3] = 0x00, byte(sector), 0x01, 0x00
	ctrl[4], ctrl[5] = 0xFF, 0xFF
	return d.writeSector(idx, 0x0000, ctrl)
}
