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
}

// Snapshot is the full public state of a room.
type Snapshot struct {
	Participants []Participant
	Sharer       string
}

// Room is safe for concurrent use.
type Room struct {
	slug, name, pin string
	max             int

	mu      sync.Mutex
	members map[string]*Participant
	order   []string
	sharer  string
}

func New(slug, name, pin string, max int) *Room {
	return &Room{slug: slug, name: name, pin: pin, max: max, members: map[string]*Participant{}}
}

func (r *Room) Slug() string  { return r.slug }
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
	if subtle.ConstantTimeCompare([]byte(pin), []byte(r.pin)) != 1 {
		return ErrBadPIN
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.members[id]; exists {
		return nil
	}
	if len(r.members) >= r.max {
		return ErrFull
	}
	r.members[id] = &Participant{ID: id, Name: name}
	r.order = append(r.order, id)
	return nil
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
	if r.sharer == id {
		r.sharer = ""
		return true
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
	s := Snapshot{Sharer: r.sharer, Participants: make([]Participant, 0, len(r.order))}
	for _, id := range r.order {
		s.Participants = append(s.Participants, *r.members[id])
	}
	return s
}

// Registry is the fixed set of configured rooms.
type Registry struct {
	rooms map[string]*Room
	order []*Room
}

func NewRegistry() *Registry { return &Registry{rooms: map[string]*Room{}} }

func (g *Registry) Add(r *Room) {
	g.rooms[r.slug] = r
	g.order = append(g.order, r)
}

func (g *Registry) Get(slug string) (*Room, bool) {
	r, ok := g.rooms[slug]
	return r, ok
}

func (g *Registry) All() []*Room { return append([]*Room(nil), g.order...) }
