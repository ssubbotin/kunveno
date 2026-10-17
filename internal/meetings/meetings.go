// Package meetings keeps the index of each room's meetings (start, end, number of people; never
// names) in one JSON file: the list the lobby shows, across restarts. A meeting stays while any of
// its material (notes, summary, recording) is kept, and otherwise for keep after its end.
package meetings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// DefaultKeep is how long a meeting with nothing left stays listed after its end (meetings.keep).
const DefaultKeep = 30 * 24 * time.Hour

// Summary describes one ended meeting.
type Summary struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	People int       `json:"people"`
}

// Store is safe for concurrent use; a nil *Store keeps nothing and finds nothing.
type Store struct {
	path string
	now  func() time.Time
	keep time.Duration

	mu    sync.Mutex
	rooms map[string][]Summary // slug -> summaries, newest first
}

// Open reads the file at path. A missing file holds nothing; a file that cannot be read or parsed
// is logged and the store starts empty (it is replaced on the next save), so it never blocks
// startup.
func Open(path string, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	s := &Store{path: path, now: now, keep: DefaultKeep, rooms: map[string][]Summary{}}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		slog.Warn("cannot read the meetings file; starting empty", "file", path, "err", err)
	default:
		var rooms map[string][]Summary
		if err := json.Unmarshal(data, &rooms); err != nil {
			slog.Warn("cannot parse the meetings file; starting empty", "file", path, "err", err)
			break
		}
		for slug, list := range rooms {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Start.After(list[j].Start) })
			if len(list) > 0 {
				s.rooms[slug] = list
			}
		}
	}
	return s
}

// Add saves an ended meeting of the room. A meeting with the same start replaces the one stored.
func (s *Store) Add(slug string, m Summary) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := []Summary{m}
	for _, o := range s.rooms[slug] {
		if !sameStart(o.Start, m.Start) {
			list = append(list, o)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Start.After(list[j].Start) })
	s.rooms[slug] = list
	return s.saveLocked()
}

// List returns the room's kept meetings, newest first.
func (s *Store) List(slug string) []Summary {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Summary(nil), s.rooms[slug]...)
}

// Find returns the room's meeting that started at start, compared to the millisecond (the notes
// store keeps milliseconds).
func (s *Store) Find(slug string, start time.Time) (Summary, bool) {
	if s == nil {
		return Summary{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.rooms[slug] {
		if sameStart(m.Start, start) {
			return m, true
		}
	}
	return Summary{}, false
}

// Delete forgets the room's meetings, for a deleted room.
func (s *Store) Delete(slug string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rooms[slug]; !ok {
		return nil
	}
	delete(s.rooms, slug)
	return s.saveLocked()
}

// SetKeep sets how long a meeting with nothing left stays listed after its end.
func (s *Store) SetKeep(d time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keep = d
}

// Count returns how many meetings the room lists.
func (s *Store) Count(slug string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rooms[slug])
}

// Sweep drops the meetings that keep nothing (has reports false; nil means nothing anywhere) and
// ended more than keep ago. A room missing from the configuration is no reason to drop anything.
func (s *Store) Sweep(has func(slug string, start int64) bool) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := s.now().Add(-s.keep)
	changed := false
	for slug, list := range s.rooms {
		kept := list[:0:0]
		for _, m := range list {
			if m.End.After(cut) || (has != nil && has(slug, m.Start.UnixMilli())) {
				kept = append(kept, m)
			}
		}
		if len(kept) == len(list) {
			continue
		}
		changed = true
		if len(kept) == 0 {
			delete(s.rooms, slug)
		} else {
			s.rooms[slug] = kept
		}
	}
	if !changed {
		return nil
	}
	return s.saveLocked()
}

func sameStart(a, b time.Time) bool { return a.UnixMilli() == b.UnixMilli() }

// saveLocked replaces the file atomically (temporary file and rename), mode 0600.
func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.rooms, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".meetings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
