package hidpp

import "fmt"

// Button remapping. The Superstrike has no live remap feature (0x1B04), so a
// button's action lives in the onboard profile sector: 16 slots of 4 bytes at
// profileButtonsOffset (slot i at offset+i*4). We decode/encode those 4 bytes
// and patch them in place — same mechanism as DPI/rate. Encoding per Solaar's
// hidpp20.Button.
//
// NOTE: this device's profile layout puts the primary button table at offset
// 48 (not Solaar's generic 32 — verified by locating the factory
// 80 01 00 0X entries). The G-Shift layer is at 112.
const profileButtonsOffset = 48

// ButtonKind is the category of a button assignment.
type ButtonKind int

const (
	ButtonDisabled ButtonKind = iota
	ButtonMouse               // a mouse button
	ButtonKey                 // keyboard key (+modifiers)
	ButtonConsumer            // media / consumer key
	ButtonFunction            // built-in mouse function (DPI/profile/…)
	ButtonRaw                 // macro or unrecognised — preserved verbatim
)

// ButtonAction is a decoded, editable button assignment.
type ButtonAction struct {
	Kind ButtonKind
	Code uint16  // mouse mask / consumer code / function id / HID keycode
	Mods byte    // keyboard modifier mask (for ButtonKey)
	Raw  [4]byte // original bytes (for ButtonRaw / round-trip)
}

// NamedCode pairs a friendly label with its wire code.
type NamedCode struct {
	Name string
	Code uint16
}

// Curated, user-facing choice lists.
var (
	MouseButtonChoices = []NamedCode{
		{"Left Click", 0x0001}, {"Right Click", 0x0002}, {"Middle Click", 0x0004},
		{"Back", 0x0008}, {"Forward", 0x0010}, {"DPI Button", 0x2000},
	}
	MediaChoices = []NamedCode{
		{"Play / Pause", 0x00CD}, {"Next Track", 0x00B5}, {"Previous Track", 0x00B6},
		{"Stop", 0x00B7}, {"Mute", 0x00E2}, {"Volume Up", 0x00E9}, {"Volume Down", 0x00EA},
	}
	FunctionChoices = []NamedCode{
		{"Next DPI", 0x03}, {"Previous DPI", 0x04}, {"Cycle DPI", 0x05},
		{"Default DPI", 0x06}, {"DPI Shift (hold)", 0x07},
		{"Next Profile", 0x08}, {"Previous Profile", 0x09}, {"Cycle Profile", 0x0A},
		{"Battery Status", 0x0C},
	}
	ModifierChoices = []NamedCode{
		{"Ctrl", 0x01}, {"Shift", 0x02}, {"Alt", 0x04}, {"Super", 0x08},
	}
	KeyChoices []NamedCode // built in init()
)

func init() {
	for i := 0; i < 26; i++ {
		KeyChoices = append(KeyChoices, NamedCode{string(rune('A' + i)), uint16(0x04 + i)})
	}
	for i := 1; i <= 9; i++ {
		KeyChoices = append(KeyChoices, NamedCode{fmt.Sprintf("%d", i), uint16(0x1E + i - 1)})
	}
	KeyChoices = append(KeyChoices, NamedCode{"0", 0x27})
	for i := 1; i <= 12; i++ {
		KeyChoices = append(KeyChoices, NamedCode{fmt.Sprintf("F%d", i), uint16(0x3A + i - 1)})
	}
	KeyChoices = append(KeyChoices,
		NamedCode{"Space", 0x2C}, NamedCode{"Enter", 0x28}, NamedCode{"Escape", 0x29},
		NamedCode{"Tab", 0x2B}, NamedCode{"Backspace", 0x2A}, NamedCode{"Delete", 0x4C},
		NamedCode{"Up Arrow", 0x52}, NamedCode{"Down Arrow", 0x51},
		NamedCode{"Left Arrow", 0x50}, NamedCode{"Right Arrow", 0x4F},
		NamedCode{"Home", 0x4A}, NamedCode{"End", 0x4D},
		NamedCode{"Page Up", 0x4B}, NamedCode{"Page Down", 0x4E},
	)
}

func codeName(list []NamedCode, code uint16, fallback string) string {
	for _, c := range list {
		if c.Code == code {
			return c.Name
		}
	}
	return fallback
}

func modString(mods byte) string {
	out := ""
	for _, m := range ModifierChoices {
		if mods&byte(m.Code) != 0 {
			if out != "" {
				out += "+"
			}
			out += m.Name
		}
	}
	return out
}

// Describe returns a human-readable summary of the assignment.
func (a ButtonAction) Describe() string {
	switch a.Kind {
	case ButtonMouse:
		return codeName(MouseButtonChoices, a.Code, fmt.Sprintf("Mouse 0x%04X", a.Code))
	case ButtonKey:
		k := codeName(KeyChoices, a.Code, fmt.Sprintf("Key 0x%02X", a.Code))
		if m := modString(a.Mods); m != "" {
			return m + " + " + k
		}
		return "Key: " + k
	case ButtonConsumer:
		return codeName(MediaChoices, a.Code, fmt.Sprintf("Media 0x%04X", a.Code))
	case ButtonFunction:
		return codeName(FunctionChoices, a.Code, fmt.Sprintf("Function 0x%02X", a.Code))
	case ButtonRaw:
		return fmt.Sprintf("Custom (% x)", a.Raw[:])
	default:
		return "Disabled"
	}
}

func decodeButton(b []byte) ButtonAction {
	var a ButtonAction
	copy(a.Raw[:], b)
	switch b[0] >> 4 {
	case 0x8: // SEND
		switch b[1] {
		case 0x01:
			a.Kind, a.Code = ButtonMouse, uint16(b[2])<<8|uint16(b[3])
		case 0x02:
			a.Kind, a.Mods, a.Code = ButtonKey, b[2], uint16(b[3])
		case 0x03:
			a.Kind, a.Code = ButtonConsumer, uint16(b[2])<<8|uint16(b[3])
		default:
			a.Kind = ButtonDisabled
		}
	case 0x9: // FUNCTION
		a.Kind, a.Code = ButtonFunction, uint16(b[1])
	default:
		if b[0] == 0xFF {
			a.Kind = ButtonDisabled
		} else {
			a.Kind = ButtonRaw
		}
	}
	return a
}

func (a ButtonAction) encode() [4]byte {
	switch a.Kind {
	case ButtonMouse:
		return [4]byte{0x80, 0x01, byte(a.Code >> 8), byte(a.Code)}
	case ButtonKey:
		return [4]byte{0x80, 0x02, a.Mods, byte(a.Code)}
	case ButtonConsumer:
		return [4]byte{0x80, 0x03, byte(a.Code >> 8), byte(a.Code)}
	case ButtonFunction:
		return [4]byte{0x90, byte(a.Code), 0xFF, 0x00}
	case ButtonRaw:
		return a.Raw
	default: // Disabled = SEND / NO_ACTION
		return [4]byte{0x80, 0x00, 0xFF, 0xFF}
	}
}

// SetProfileButton writes one button slot (0..15) of a profile, leaving every
// other byte intact, then re-CRCs, writes, and reloads the resolved RAM sector
// so the mouse stops using its cached assignment table.
func (d *Device) SetProfileButton(sector, index int, a ButtonAction) (int, error) {
	if index < 0 || index >= 16 {
		return 0, fmt.Errorf("button index %d out of range", index)
	}
	enc := a.encode()
	actual, err := d.patchProfileSectorResolved(sector, func(raw []byte) error {
		off := profileButtonsOffset + index*4
		copy(raw[off:off+4], enc[:])
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
