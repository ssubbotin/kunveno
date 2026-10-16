package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ErrBadInvite: the token is unknown, belongs to another room, has expired or was revoked.
var ErrBadInvite = errors.New("bad invite")

// Invite is one guest link. Hash is the SHA-256 of the token; the token itself is never kept.
type Invite struct {
	ID        string    `json:"id"`   // public: the dialog revokes by it
	Hash      string    `json:"hash"` // hex SHA-256 of the token
	Room      string    `json:"room"`
	CreatedBy string    `json:"created_by"`
	Created   time.Time `json:"created"`
	Expires   time.Time `json:"expires"`
	Revoked   bool      `json:"revoked"`
}

// Invites keeps the invitations in a JSON file (mode 0600), rewritten whole on every change.
type Invites struct {
	path string
	Now  func() time.Time

	mu  sync.Mutex
	all []Invite
}

func OpenInvites(path string) (*Invites, error) {
	s := &Invites{path: path, Now: time.Now}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.all); err != nil {
		return nil, err
	}
	return s, nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func random(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Create makes an invitation to room valid for ttl and returns its token (shown once).
func (s *Invites) Create(room, createdBy string, ttl time.Duration) (string, Invite, error) {
	tok, err := random(32)
	if err != nil {
		return "", Invite{}, err
	}
	id, err := random(9)
	if err != nil {
		return "", Invite{}, err
	}
	now := s.Now()
	inv := Invite{ID: id, Hash: hashToken(tok), Room: room, CreatedBy: createdBy, Created: now, Expires: now.Add(ttl)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.all = append(s.all, inv)
	if err := s.save(); err != nil {
		s.all = s.all[:len(s.all)-1]
		return "", Invite{}, err
	}
	return tok, inv, nil
}

func (s *Invites) usable(inv Invite) bool { return !inv.Revoked && s.Now().Before(inv.Expires) }

// Check finds the usable invitation of room with this token.
func (s *Invites) Check(room, token string) (Invite, error) {
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, inv := range s.all {
		if inv.Hash == h && inv.Room == room && s.usable(inv) {
			return inv, nil
		}
	}
	return Invite{}, ErrBadInvite
}

// List returns room's usable invitations, newest first, without their hashes.
func (s *Invites) List(room string) []Invite {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Invite
	for _, inv := range s.all {
		if inv.Room == room && s.usable(inv) {
			inv.Hash = ""
			out = append(out, inv)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Revoke ends room's invitation id at once.
func (s *Invites) Revoke(room, id string) (Invite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.all {
		if inv := s.all[i]; inv.ID == id && inv.Room == room && s.usable(inv) {
			s.all[i].Revoked = true
			if err := s.save(); err != nil {
				s.all[i].Revoked = false
				return Invite{}, err
			}
			inv.Hash = ""
			return inv, nil
		}
	}
	return Invite{}, ErrBadInvite
}

// Active counts the usable invitations of every room.
func (s *Invites) Active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, inv := range s.all {
		if s.usable(inv) {
			n++
		}
	}
	return n
}

// Sweep drops expired and revoked invitations from the file.
func (s *Invites) Sweep() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.all[:0]
	for _, inv := range s.all {
		if s.usable(inv) {
			kept = append(kept, inv)
		}
	}
	if len(kept) == len(s.all) {
		return nil
	}
	s.all = kept
	return s.save()
}

// save writes the file atomically (a temporary file renamed over it). Called with mu held.
func (s *Invites) save() error {
	b, err := json.MarshalIndent(s.all, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".invites-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
