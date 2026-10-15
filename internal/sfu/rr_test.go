package sfu

import (
	"github.com/ssubbotin/kunveno/internal/media"

	"github.com/pion/rtcp"
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

// Downlink loss is measured over the whole sampling window from the cumulative counters in the
// receiver reports, so one lost packet out of a few seconds of audio stays below the fair line.
func TestDownLossFromCumulativeCounters(t *testing.T) {
	p := &Peer{}
	rr := func(seq, lost uint32) *rtcp.ReceiverReport {
		return &rtcp.ReceiverReport{Reports: []rtcp.ReceptionReport{{SSRC: 7, LastSequenceNumber: seq, TotalLost: lost, FractionLost: 5}}}
	}
	p.noteReceiverReport(rr(1000, 0))
	p.noteReceiverReport(rr(1050, 1)) // the 1 s report alone says ~2% (fraction 5/256)
	p.noteReceiverReport(rr(1150, 1))
	if got := p.takeDownLoss(); got < 0.0065 || got > 0.0068 {
		t.Fatalf("down loss = %v, want 1/150", got)
	}
	if got := p.takeDownLoss(); got != 0 {
		t.Fatalf("window not reset: %v", got)
	}
}

// Video loss is measured like audio loss but kept apart: it must not move the connection grade.
func TestVideoLossIsSeparate(t *testing.T) {
	p := &Peer{}
	rr := func(ssrc, seq, lost uint32) *rtcp.ReceiverReport {
		return &rtcp.ReceiverReport{Reports: []rtcp.ReceptionReport{{SSRC: ssrc, LastSequenceNumber: seq, TotalLost: lost}}}
	}
	if _, ok := p.takeVideoLoss(); ok {
		t.Fatal("a window without video reported a loss figure")
	}
	p.noteReport(media.KindCamera, rr(9, 100, 0))
	p.noteReport(media.KindCamera, rr(9, 200, 10)) // 10 of 100 video packets lost
	p.noteReport(media.KindAudio, rr(7, 1000, 0))
	p.noteReport(media.KindAudio, rr(7, 1150, 0))
	if got, ok := p.takeVideoLoss(); !ok || got < 0.099 || got > 0.101 {
		t.Fatalf("video loss = %v, want 0.1", got)
	}
	if got := p.takeDownLoss(); got != 0 {
		t.Fatalf("audio loss = %v, want 0: video loss leaked into it", got)
	}
}

func TestCountNACKedPackets(t *testing.T) {
	n := &rtcp.TransportLayerNack{Nacks: []rtcp.NackPair{{PacketID: 10, LostPackets: 0b101}, {PacketID: 40}}}
	// pair 1: packet 10 plus bitmask bits 0 and 2 (11 and 13); pair 2: packet 40.
	if got := nackedPackets(n); got != 4 {
		t.Fatalf("nacked = %d, want 4", got)
	}
}
