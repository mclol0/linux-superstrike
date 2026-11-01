package hidpp

import "testing"

func TestSideButtonEncoding(t *testing.T) {
	back := (ButtonAction{Kind: ButtonMouse, Code: 0x0008}).encode()
	forward := (ButtonAction{Kind: ButtonMouse, Code: 0x0010}).encode()
	if back != [4]byte{0x80, 0x01, 0x00, 0x08} {
		t.Fatalf("Back encoded as % x", back)
	}
	if forward != [4]byte{0x80, 0x01, 0x00, 0x10} {
		t.Fatalf("Forward encoded as % x", forward)
	}
}
