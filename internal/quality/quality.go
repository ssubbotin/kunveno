// Package quality turns media statistics into a coarse connection grade for the participant list.
package quality

import "time"

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

func rank(g string) int {
	switch g {
	case Good:
		return 2
	case Fair:
		return 1
	}
	return 0
}

// Tracker smooths grades for display: a worse grade shows at once, a better one only after two
// samples in a row agree, so a single lucky sample does not make the indicator flicker.
type Tracker struct {
	shown   string
	pending string
}

// Update returns the grade to show and whether it differs from the previous one.
func (t *Tracker) Update(s Sample) (string, bool) {
	g := Grade(s)
	switch {
	case t.shown == "" || rank(g) < rank(t.shown):
		t.pending = ""
	case rank(g) > rank(t.shown):
		if t.pending != g {
			t.pending = g
			return t.shown, false
		}
		t.pending = ""
	default:
		t.pending = ""
		return t.shown, false
	}
	changed := g != t.shown
	t.shown = g
	return g, changed
}
