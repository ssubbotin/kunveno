// Package echo turns the audio of a room into a delayed echo stream. Opus cannot be mixed
// without decoding, so it follows one speaker at a time: whoever talks first holds the echo
// until they have been silent for the hold time, then the next talker takes over. Output
// sequence numbers and timestamps are rewritten so the stream stays continuous across switches.
package echo

import (
	"sync"
	"time"

	"github.com/pion/rtp"
)

// opusFrame is one 20 ms Opus frame in 48 kHz timestamp units.
const opusFrame = 960

type queued struct {
	due time.Time
	pkt *rtp.Packet
}

// Delayer is safe for concurrent use: Push is called from one goroutine per source track,
// Due from the sender loop.
type Delayer struct {
	delay     time.Duration
	minSpeech int // payload bytes at or above which a packet counts as speech
	hold      time.Duration

	mu         sync.Mutex
	active     string
	lastSpeech time.Time
	offset     uint32
	started    bool
	marker     bool
	seq        uint16
	lastTS     uint32
	queue      []queued
}

func New(delay time.Duration, minSpeechBytes int, hold time.Duration) *Delayer {
	return &Delayer{delay: delay, minSpeech: minSpeechBytes, hold: hold}
}

// Push offers one received packet from source src.
func (d *Delayer) Push(src string, p *rtp.Packet, now time.Time) {
	speech := len(p.Payload) >= d.minSpeech
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case d.active == "":
		if !speech {
			return
		}
		d.switchTo(src, p, now)
	case src != d.active:
		if !speech || now.Sub(d.lastSpeech) <= d.hold {
			return
		}
		d.switchTo(src, p, now)
	}
	if speech {
		d.lastSpeech = now
	}
	out := p.Clone()
	// Extensions (MID above all) belong to the connection the packet came in on.
	out.Extension = false
	out.Extensions = nil
	out.ExtensionProfile = 0
	out.CSRC = nil
	out.SequenceNumber = d.seq
	d.seq++
	out.Timestamp = p.Timestamp + d.offset
	out.Marker = d.marker
	d.marker = false
	d.lastTS = out.Timestamp
	d.queue = append(d.queue, queued{due: now.Add(d.delay), pkt: out})
}

func (d *Delayer) switchTo(src string, p *rtp.Packet, now time.Time) {
	d.active = src
	d.lastSpeech = now
	if d.started {
		d.offset = d.lastTS + opusFrame - p.Timestamp
	} else {
		d.offset = 0
		d.started = true
	}
	d.marker = true
}

// Due removes and returns the packets whose delay has passed.
func (d *Delayer) Due(now time.Time) []*rtp.Packet {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for n < len(d.queue) && !d.queue[n].due.After(now) {
		n++
	}
	if n == 0 {
		return nil
	}
	out := make([]*rtp.Packet, n)
	for i := range out {
		out[i] = d.queue[i].pkt
	}
	d.queue = append(d.queue[:0], d.queue[n:]...)
	return out
}
