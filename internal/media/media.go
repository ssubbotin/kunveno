// Package media is the vocabulary shared by the media engine (sfu) and the code that coordinates
// participants (conference): what a participant publishes, how a subscription names it, and the
// callbacks and offers that cross between them. It has no dependencies, so neither side depends
// on the other's implementation.
package media

import "errors"

// Kind is the media a Source publishes.
type Kind int

const (
	KindAudio Kind = iota
	KindScreen
	KindCamera
)

func (k Kind) String() string {
	switch k {
	case KindAudio:
		return "audio"
	case KindCamera:
		return "camera"
	}
	return "screen"
}

// ErrNoTrack is returned when subscribing to a source that has not published yet.
var ErrNoTrack = errors.New("source has no track of that kind")

// Signaler delivers server offers to one client.
type Signaler interface {
	SendOffer(sdp string) error
}

// Source is a publisher another peer can subscribe to.
type Source interface {
	ID() string
	Publishes(Kind) bool // its track of that kind has arrived
}

// Handlers are callbacks into the coordinator (the conference package).
type Handlers struct {
	OnPublisher func(Source, Kind) // first RTP of that kind arrived; the track is ready
	OnFailed    func(Source)       // the connection failed or closed unexpectedly
	// TCPOnly strips UDP candidates from this peer's offers, for a participant whose UDP path
	// failed: the browser then has only the ICE-TCP candidate to connect to.
	TCPOnly bool
}

// Candidate is an ICE candidate a client sends, in the browser's RTCIceCandidateInit form.
type Candidate struct {
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex"`
	UsernameFragment *string `json:"usernameFragment"`
}
