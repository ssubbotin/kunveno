package room

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

var (
	ErrInvalid  = errors.New("invalid room")
	ErrNotFound = errors.New("no such room")
	ErrFixed    = errors.New("room comes from the config file")
	ErrNotEmpty = errors.New("room is not empty")
)

// DefaultCapacity is the capacity of rooms created through the API.
const DefaultCapacity = 15

// MinCreatedPIN is the shortest PIN accepted for a room created in the lobby.
const MinCreatedPIN = 6

// Spec is the persisted form of a created room.
type Spec struct {
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	PIN             string `json:"pin"`
	MaxParticipants int    `json:"max_participants"`
}

// Registry holds configured rooms and rooms created at runtime. Safe for concurrent use.
type Registry struct {
	mu    sync.Mutex
	rooms map[string]*Room
	order []*Room
}

func NewRegistry() *Registry { return &Registry{rooms: map[string]*Room{}} }

// Add registers a configured room.
func (g *Registry) Add(r *Room) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rooms[r.slug] = r
	g.order = append(g.order, r)
}

// Restore registers a room created in an earlier run.
func (g *Registry) Restore(s Spec) {
	r := New(s.Slug, s.Name, s.PIN, s.MaxParticipants)
	r.created = true
	g.Add(r)
}

func (g *Registry) Get(slug string) (*Room, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.rooms[slug]
	return r, ok
}

func (g *Registry) All() []*Room {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]*Room(nil), g.order...)
}

// Create adds a room with a slug derived from the name. Name 1..48 characters after
// trimming, PIN 6..16 characters: rooms made in the lobby get a PIN that resists guessing.
func (g *Registry) Create(name, pin string) (*Room, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 48 {
		return nil, fmt.Errorf("%w: name must be 1..48 characters", ErrInvalid)
	}
	if n := utf8.RuneCountInString(pin); n < MinCreatedPIN || n > 16 {
		return nil, fmt.Errorf("%w: pin must be %d..16 characters", ErrInvalid, MinCreatedPIN)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	base := slugify(name)
	slug := base
	for i := 2; g.rooms[slug] != nil; i++ {
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	r := New(slug, name, pin, DefaultCapacity)
	r.created = true
	g.rooms[slug] = r
	g.order = append(g.order, r)
	return r, nil
}

// Delete removes an empty created room after checking its PIN.
func (g *Registry) Delete(slug, pin string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.rooms[slug]
	switch {
	case !ok:
		return ErrNotFound
	case !r.created:
		return ErrFixed
	case !r.CheckPIN(pin):
		return ErrBadPIN
	case r.Count() > 0:
		return ErrNotEmpty
	}
	delete(g.rooms, slug)
	for i, o := range g.order {
		if o == r {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}
	return nil
}

// CreatedSpecs returns the rooms to persist, in creation order.
func (g *Registry) CreatedSpecs() []Spec {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Spec
	for _, r := range g.order {
		if r.created {
			out = append(out, Spec{Slug: r.slug, Name: r.name, PIN: r.pin, MaxParticipants: r.max})
		}
	}
	return out
}

// slugify keeps ASCII letters and digits, turns every other run into one hyphen, and
// caps the length so suffixes still fit the 32-character slug rule.
func slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, c := range strings.ToLower(name) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 28 {
		s = strings.TrimRight(s[:28], "-")
	}
	if s == "" {
		s = "room"
	}
	return s
}

// EchoRooms lists the slugs of rooms that want the echo helper.
func (g *Registry) EchoRooms() []string {
	out := []string{}
	for _, r := range g.All() {
		if r.Echo() {
			out = append(out, r.slug)
		}
	}
	return out
}

// NotesRooms lists the slugs of rooms that want meeting notes.
func (g *Registry) NotesRooms() []string {
	out := []string{}
	for _, r := range g.All() {
		if r.Notes() {
			out = append(out, r.slug)
		}
	}
	return out
}
