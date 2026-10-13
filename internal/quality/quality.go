// Package quality turns media statistics into a coarse connection grade for the participant list.
package quality

import (
	"sort"
	"time"
)

const (
	Good = "good"
	Fair = "fair"
	Poor = "poor"
)

// Sample is one measurement of a participant's connection.
type Sample struct {
	UpLoss   float64       // fraction of the participant's packets lost on the way to the server
	DownLoss float64       // fraction of the server's packets the participant reported lost
	RTT      time.Duration // round trip between server and participant
	Jitter   time.Duration // jitter of the participant's audio as received by the server
	// VideoLoss: fraction of our screen and camera packets the participant lost. Reported in
	// metrics only; the grade follows audio, which matters most in a call.
	VideoLoss float64
	HasVideo  bool // the participant received video in this window
}

// Thresholds follow common VoIP guidance: under 1-2% loss and 150-200 ms of delay a call feels
// fine; past 5% loss or 400 ms speech breaks up or people talk over each other. Jitter up to
// ~40 ms is absorbed by the browser's jitter buffer; ICE-TCP alone runs at ~25 ms.
func Grade(s Sample) string {
	loss := max(s.UpLoss, s.DownLoss)
	switch {
	case loss >= 0.05 || s.RTT >= 400*time.Millisecond || s.Jitter >= 80*time.Millisecond:
		return Poor
	case loss >= 0.015 || s.RTT >= 200*time.Millisecond || s.Jitter >= 40*time.Millisecond:
		return Fair
	}
	return Good
}

// Tracker smooths grades for display: it grades the median of the last three samples, taken per
// measure. A problem shows once it holds for two samples of three (~6 s at the server's 3 s
// sampling); a single spike in one window, common on a VPN, does not move the indicator. With two
// samples so far the better one counts, so a bad first moment does not flash.
type Tracker struct {
	last  []Sample // up to three, oldest first
	shown string
}

// Update adds a sample and returns the grade to show and whether it differs from the previous one.
func (t *Tracker) Update(s Sample) (string, bool) {
	t.last = append(t.last, s)
	if len(t.last) > 3 {
		t.last = t.last[1:]
	}
	g := Grade(median(t.last))
	changed := g != t.shown
	t.shown = g
	return g, changed
}

// median takes each measure's middle value; of two, the smaller (better) one.
func median(ss []Sample) Sample {
	pick := func(get func(Sample) float64) float64 {
		v := make([]float64, len(ss))
		for i, s := range ss {
			v[i] = get(s)
		}
		sort.Float64s(v)
		return v[(len(v)-1)/2]
	}
	return Sample{
		UpLoss:   pick(func(s Sample) float64 { return s.UpLoss }),
		DownLoss: pick(func(s Sample) float64 { return s.DownLoss }),
		RTT:      time.Duration(pick(func(s Sample) float64 { return float64(s.RTT) })),
		Jitter:   time.Duration(pick(func(s Sample) float64 { return float64(s.Jitter) })),
	}
}
