package sfu

import (
	"testing"
	"time"
)

func TestRTTFromReceiverReport(t *testing.T) {
	lsr := uint32(0x1234_8000)   // compact NTP of our sender report
	dlsr := uint32(65536 / 2)    // the client held it 0.5 s
	now := lsr + dlsr + 65536/10 // 0.1 s of network round trip on top
	if got := rttFromReport(now, lsr, dlsr); got < 99*time.Millisecond || got > 101*time.Millisecond {
		t.Fatalf("rtt = %v, want ~100ms", got)
	}
	if got := rttFromReport(now, 0, dlsr); got != 0 {
		t.Fatalf("no sender report yet: rtt = %v, want 0", got)
	}
	if got := rttFromReport(lsr, lsr, dlsr); got != 0 {
		t.Fatalf("negative round trip: rtt = %v, want 0", got)
	}
}
