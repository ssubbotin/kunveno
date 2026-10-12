package echo

import (
	"testing"
	"time"

	"github.com/pion/rtp"
)

var t0 = time.Unix(1000, 0)

func pkt(seq uint16, ts uint32, size int) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{SequenceNumber: seq, Timestamp: ts}, Payload: make([]byte, size)}
}

func at(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

func TestPacketsComeOutAfterTheDelay(t *testing.T) {
	d := New(2*time.Second, 30, time.Second)
	d.Push("a", pkt(10, 5000, 80), at(0))
	if out := d.Due(at(1999)); len(out) != 0 {
		t.Fatalf("released %d packets early", len(out))
	}
	out := d.Due(at(2000))
	if len(out) != 1 || len(out[0].Payload) != 80 {
		t.Fatalf("got %d packets", len(out))
	}
	if len(d.Due(at(5000))) != 0 {
		t.Fatal("packet released twice")
	}
}

func TestSilenceBeforeAnySpeakerIsDropped(t *testing.T) {
	d := New(0, 30, time.Second)
	d.Push("a", pkt(1, 0, 5), at(0))
	if out := d.Due(at(0)); len(out) != 0 {
		t.Fatalf("silence forwarded: %d", len(out))
	}
}

func TestOneSpeakerAtATimeAndSwitchAfterHold(t *testing.T) {
	d := New(0, 30, time.Second)
	d.Push("a", pkt(100, 1000, 80), at(0))   // a starts talking: active
	d.Push("b", pkt(500, 90000, 80), at(10)) // b talks over a: ignored
	d.Push("a", pkt(101, 1960, 5), at(20))   // a silent, still active (hold)
	if out := d.Due(at(20)); len(out) != 2 {
		t.Fatalf("want a's two packets, got %d", len(out))
	}
	d.Push("b", pkt(501, 90960, 80), at(1100)) // a silent for > 1 s: b takes over
	out := d.Due(at(1100))
	if len(out) != 1 {
		t.Fatalf("want b's packet after hold, got %d", len(out))
	}
	if !out[0].Marker {
		t.Fatal("first packet of a new source must carry the marker bit")
	}
}

func TestOutputSequenceAndTimestampAreContinuous(t *testing.T) {
	d := New(0, 30, time.Second)
	d.Push("a", pkt(100, 1000, 80), at(0))
	d.Push("a", pkt(101, 1960, 80), at(20))
	d.Push("b", pkt(7, 777777, 80), at(1100)) // a went quiet (no packets): switch
	out := d.Due(at(1100))
	if len(out) != 3 {
		t.Fatalf("got %d packets", len(out))
	}
	for i := 1; i < len(out); i++ {
		if out[i].SequenceNumber != out[i-1].SequenceNumber+1 {
			t.Fatalf("seq gap: %d -> %d", out[i-1].SequenceNumber, out[i].SequenceNumber)
		}
		if out[i].Timestamp <= out[i-1].Timestamp {
			t.Fatalf("timestamp not increasing: %d -> %d", out[i-1].Timestamp, out[i].Timestamp)
		}
	}
	if out[2].Timestamp != out[1].Timestamp+960 {
		t.Fatalf("switch timestamp = %d, want previous + 960", out[2].Timestamp)
	}
}

func TestPushDoesNotAliasTheCallerPacket(t *testing.T) {
	d := New(0, 30, time.Second)
	p := pkt(1, 0, 80)
	d.Push("a", p, at(0))
	p.Payload[0] = 0xff
	if out := d.Due(at(0)); out[0].Payload[0] != 0 {
		t.Fatal("queued packet shares the caller's buffer")
	}
}

// Header extensions belong to the connection the packet arrived on (the MID extension in
// particular routes it there); re-sent packets must carry none.
func TestOutputDropsHeaderExtensions(t *testing.T) {
	d := New(0, 30, time.Second)
	p := pkt(1, 0, 80)
	if err := p.Header.SetExtension(1, []byte("2")); err != nil {
		t.Fatal(err)
	}
	p.CSRC = []uint32{7}
	d.Push("a", p, at(0))
	out := d.Due(at(0))[0]
	if out.Extension || len(out.Extensions) != 0 || len(out.CSRC) != 0 {
		t.Fatalf("extension=%v extensions=%d csrc=%d", out.Extension, len(out.Extensions), len(out.CSRC))
	}
}
