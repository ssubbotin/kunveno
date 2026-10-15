// Package recording keeps meeting recordings: the last meeting's video file of each room, uploaded
// by the recording bot of the kunveno-recorder add-on, and its expiry.
package recording

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNoRecording = errors.New("no recording")
	ErrRecording   = errors.New("the room is recording")
	ErrStale       = errors.New("a later meeting's recording is already stored")
	ErrTooLarge    = errors.New("recording too large")
)

// Meta describes a stored recording.
type Meta struct {
	Meeting time.Time `json:"meeting"` // start of the meeting it records
	Ended   time.Time `json:"ended"`   // when the upload finished
	Size    int64     `json:"size"`
}

// Store is safe for concurrent use. Files: <slug>.mp4 and <slug>.json (Meta) in dir, 0600; an
// upload in progress is <slug>.mp4.tmp.
type Store struct {
	dir  string
	keep time.Duration
	now  func() time.Time

	mu     sync.Mutex
	meta   map[string]Meta
	active map[string]bool
}

func Open(dir string, keep time.Duration, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, keep: keep, now: now, meta: map[string]Meta{}, active: map[string]bool{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".tmp"):
			_ = os.Remove(filepath.Join(dir, name)) // an upload cut off by a restart
		case strings.HasSuffix(name, ".json"):
			slug := strings.TrimSuffix(name, ".json")
			var m Meta
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || json.Unmarshal(data, &m) != nil {
				continue
			}
			if _, err := os.Stat(s.video(slug)); err == nil {
				s.meta[slug] = m
			}
		}
	}
	return s, nil
}

func (s *Store) video(slug string) string { return filepath.Join(s.dir, slug+".mp4") }
func (s *Store) info(slug string) string  { return filepath.Join(s.dir, slug+".json") }

// SetActive marks the room as recording (on) or not.
func (s *Store) SetActive(slug string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		s.active[slug] = true
	} else {
		delete(s.active, slug)
	}
}

// Put stores the recording of meeting from r, at most max bytes. A recording of an earlier meeting
// than the stored one is refused (ErrStale); one of the same meeting replaces it.
func (s *Store) Put(slug string, meeting time.Time, r io.Reader, max int64) (Meta, error) {
	s.mu.Lock()
	if old, ok := s.meta[slug]; ok && meeting.Before(old.Meeting) {
		s.mu.Unlock()
		return Meta{}, ErrStale
	}
	s.mu.Unlock()
	tmp := s.video(slug) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Meta{}, err
	}
	n, err := io.Copy(f, io.LimitReader(r, max+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = ErrTooLarge
	}
	if err != nil {
		_ = os.Remove(tmp)
		return Meta{}, err
	}
	m := Meta{Meeting: meeting, Ended: s.now(), Size: n}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.meta[slug]; ok && meeting.Before(old.Meeting) {
		_ = os.Remove(tmp) // a later meeting's upload finished meanwhile
		return Meta{}, ErrStale
	}
	data, _ := json.Marshal(m)
	if err := os.WriteFile(s.info(slug)+".tmp", data, 0o600); err != nil {
		_ = os.Remove(tmp)
		return Meta{}, err
	}
	if err := os.Rename(tmp, s.video(slug)); err != nil {
		return Meta{}, err
	}
	if err := os.Rename(s.info(slug)+".tmp", s.info(slug)); err != nil {
		return Meta{}, err
	}
	s.meta[slug] = m
	return m, nil
}

func (s *Store) expired(m Meta) bool { return !s.now().Before(m.Ended.Add(s.keep)) }

// Until reports until when the room's recording can be downloaded.
func (s *Store) Until(slug string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.meta[slug]
	if !ok || s.expired(m) {
		return time.Time{}, false
	}
	return m.Ended.Add(s.keep), true
}

// Open opens the room's recording for reading; the caller closes the file.
func (s *Store) Open(slug string) (*os.File, Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.meta[slug]
	if !ok || s.expired(m) {
		return nil, Meta{}, ErrNoRecording
	}
	f, err := os.Open(s.video(slug))
	if err != nil {
		return nil, Meta{}, ErrNoRecording
	}
	return f, m, nil
}

// Delete removes the room's recording at a person's request: ErrRecording while the room records.
func (s *Store) Delete(slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[slug] {
		return ErrRecording
	}
	m, ok := s.meta[slug]
	if !ok || s.expired(m) {
		return ErrNoRecording
	}
	return s.removeLocked(slug)
}

// Remove deletes the recording of a deleted room, recording or not.
func (s *Store) Remove(slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, slug)
	if _, ok := s.meta[slug]; !ok {
		return nil
	}
	return s.removeLocked(slug)
}

func (s *Store) removeLocked(slug string) error {
	delete(s.meta, slug)
	err := os.Remove(s.video(slug))
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if e := os.Remove(s.info(slug)); e != nil && !errors.Is(e, os.ErrNotExist) && err == nil {
		err = e
	}
	return err
}

// Sweep deletes expired recordings and returns their rooms, sorted.
func (s *Store) Sweep() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var gone []string
	for slug, m := range s.meta {
		if s.expired(m) && !s.active[slug] {
			if err := s.removeLocked(slug); err == nil {
				gone = append(gone, slug)
			}
		}
	}
	sort.Strings(gone)
	return gone
}

// FileName is the download name: <slug>-YYYY-MM-DD-HHMM.mp4 with the meeting start in loc.
func FileName(slug string, meeting time.Time, loc *time.Location) string {
	return fmt.Sprintf("%s-%s.mp4", slug, meeting.In(loc).Format("2006-01-02-1504"))
}
