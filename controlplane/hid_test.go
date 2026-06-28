package main

import (
	"bytes"
	"os"
	"testing"
)

// mouseRelMove must emit a relative report for a plain click (no movement) and
// must split a large delta into multiple <=127 chunks that sum to the original.
func TestMouseRelChunking(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "hidg2")
	if err != nil {
		t.Fatal(err)
	}
	h := &HID{mouseRel: tmp}

	// pure click, no movement -> exactly one report, button byte set, no delta
	h.mouseRelMove(0, 0, 0, 0x01)
	b, _ := os.ReadFile(tmp.Name())
	if len(b) != 4 || b[0] != 0x01 || b[1] != 0 || b[2] != 0 {
		t.Fatalf("click report = %v, want one [01 00 00 00]", b)
	}

	// large delta -> several reports; X deltas sum to 300, Y to -200, range ok
	tmp.Truncate(0)
	tmp.Seek(0, 0)
	h.mouseRelMove(300, -200, 0, 0)
	b, _ = os.ReadFile(tmp.Name())
	if len(b)%4 != 0 || len(b) < 8 {
		t.Fatalf("expected multiple 4-byte reports, got %d bytes", len(b))
	}
	var sx, sy int
	for i := 0; i < len(b); i += 4 {
		if b[i] != 0 {
			t.Errorf("buttons should stay 0, report %d had %02x", i/4, b[i])
		}
		sx += int(int8(b[i+1]))
		sy += int(int8(b[i+2]))
		if int8(b[i+1]) > 127 || int8(b[i+1]) < -127 || int8(b[i+2]) > 127 || int8(b[i+2]) < -127 {
			t.Errorf("delta out of [-127,127] in report %d", i/4)
		}
	}
	if sx != 300 || sy != -200 {
		t.Errorf("chunk sums = (%d,%d), want (300,-200)", sx, sy)
	}

	// nil relative device must be a no-op, never panic
	(&HID{}).mouseRelMove(10, 10, 0, 1)
	_ = bytes.TrimSpace
}
