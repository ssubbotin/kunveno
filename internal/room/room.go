// Package room holds the state of a meeting room: who is in it, who is muted,
// who holds the single screen-share lock. It performs no I/O.
package room

import (
	"crypto/subtle"
	"errors"
	"sync"
)

var (
	ErrBadPIN    = errors.New("wrong pin")
	ErrFull      = errors.New("room is full")
	ErrShareBusy = errors.New("someone else is sharing")
	ErrUnknown   = errors.New("unknown participant")
)

// Participant is the public state of one member.
type Participant struct {
	ID    string
	Name  string
	Muted bool
	Bot   bool // a server-side helper such as the echo, not a person
	// BotKind says which helper: BotEcho, BotPlayer or BotCamera.
	BotKind string
	// Quality is the connection grade ("good", "fair", "poor"), empty until measured.
	Quality string
	Camera  bool // the participant's camera is on
	// DeclinesVideo: the participant declined other cameras; for metrics, not sent to clients.
	DeclinesVideo bool
}

// Snapshot is the full public state of a room.
type Snapshot struct {
	Participants []Participant
	Sharer       string
	Echo         bool // someone asked for the echo helper in this room
}

// Room is safe for concurrent use.
type Room struct {
	slug, name, pin string
	max             int
	created         bool // made through the API, not from the config file

	mu      sync.Mutex
	members map[string]*Participant
	order   []string
	sharer  string
	echo    bool
	player  bool
}

func New(slug, name, pin string, max int) *Room {
	return &Room{slug: slug, name: name, pin: pin, max: max, members: map[string]*Participant{}}
}

func (r *Room) Slug() string  { return r.slug }
func (r *Room) Created() bool { return r.created }

// CheckPIN compares in constant time.
func (r *Room) CheckPIN(pin string) bool {
	return subtle.ConstantTimeCompare([]byte(pin), []byte(r.pin)) == 1
}
func (r *Room) Name() string  { return r.name }
func (r *Room) Capacity() int { return r.max }

func (r *Room) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.members)
}

func (r *Room) Sharer() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sharer
}

// Join admits id with the given display name when the pin matches and there is room.
func (r *Room) Join(id, name, pin string) error {
	if !r.CheckPIN(pin) {
		return ErrBadPIN
	}
	return r.add(&Participant{ID: id, Name: name})
}

// Helper kinds that join rooms as bots.
const (
	BotEcho   = "echo"
	BotPlayer = "player"
	BotCamera = "camera"
)

// JoinBot admits a trusted server-side helper without a PIN; capacity still applies.
func (r *Room) JoinBot(id, name, kind string) error {
	return r.add(&Participant{ID: id, Name: name, Bot: true, BotKind: kind})
}

// SetPlayer marks the room as one with a looped video player (from the configuration).
func (r *Room) SetPlayer(on bool) {
	r.mu.Lock()
	r.player = on
	r.mu.Unlock()
}

func (r *Room) HasPlayer() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.player
}

func (r *Room) add(p *Participant) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.members[p.ID]; exists {
		return nil
	}
	if len(r.members) >= r.max {
		return ErrFull
	}
	r.members[p.ID] = p
	r.order = append(r.order, p.ID)
	return nil
}

// SetEcho records whether the room wants the echo helper.
func (r *Room) SetEcho(on bool) {
	r.mu.Lock()
	r.echo = on
	r.mu.Unlock()
}

func (r *Room) Echo() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.echo
}

// Leave removes id. It reports whether id held the share lock, which is now released.
func (r *Room) Leave(id string) (releasedShare bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.members[id]; !ok {
		return false
	}
	delete(r.members, id)
	for i, o := range r.order {
		if o == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	if !r.hasPeopleLocked() {
		r.echo = false // nobody left to listen
	}
	if r.sharer == id {
		r.sharer = ""
		return true
	}
	return false
}

func (r *Room) hasPeopleLocked() bool {
	for _, p := range r.members {
		if !p.Bot {
			return true
		}
	}
	return false
}

// SetMuted records the microphone state; false when id is unknown.
func (r *Room) SetMuted(id string, muted bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.members[id]
	if !ok {
		return false
	}
	p.Muted = muted
	return true
}

// StartShare grants the lock to id when free or already held by id.
func (r *Room) StartShare(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.members[id]; !ok {
		return ErrUnknown
	}
	if r.sharer != "" && r.sharer != id {
		return ErrShareBusy
	}
	r.sharer = id
	return nil
}

// StopShare releases the lock when id holds it; false otherwise.
func (r *Room) StopShare(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sharer != id || id == "" {
		return false
	}
	r.sharer = ""
	return true
}

// Snapshot copies the state in join order.
func (r *Room) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Snapshot{Sharer: r.sharer, Echo: r.echo, Participants: make([]Participant, 0, len(r.order))}
	for _, id := range r.order {
		s.Participants = append(s.Participants, *r.members[id])
	}
	return s
}

// SetQuality records a participant's connection grade and reports whether it changed.
func (r *Room) SetQuality(id, q string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.members[id]
	if !ok || p.Quality == q {
		return false
	}
	p.Quality = q
	return true
}

// SetCamera records whether a participant's camera is on and reports whether it changed.
func (r *Room) SetCamera(id string, on bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.members[id]
	if !ok || p.Camera == on {
		return false
	}
	p.Camera = on
	return true
}

// SetDeclinesVideo records whether a participant declined other cameras and reports a change.
func (r *Room) SetDeclinesVideo(id string, on bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.members[id]
	if !ok || p.DeclinesVideo == on {
		return false
	}
	p.DeclinesVideo = on
	return true
}
