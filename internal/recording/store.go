// Package recording keeps meeting recordings: the last meeting's video file of each room, uploaded
// by the recording bot of the kunveno-recorder add-on, and its expiry.
package recording

import (
	"bytes"
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

// Store is safe for concurrent use. Files: <slug>.mp4 and <slug>.json (Meta) in dir, 0600. An
// upload writes unique temporary files (ending in .tmp) that are renamed into place or removed.
type Store struct {
	dir  string
	keep time.Duration
	now  func() time.Time

	mu     sync.Mutex
	meta   map[string]Meta
	active map[string]bool
}

// Open loads the stored recordings of dir. Leftover temporary files and files without a partner
// (an .mp4 without a valid .json, or a .json without its .mp4) are removed.
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
		if strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(filepath.Join(dir, name)) // an upload cut off by a restart
			continue
		}
		if slug, ok := strings.CutSuffix(name, ".json"); ok {
			var m Meta
			data, err := os.ReadFile(filepath.Join(dir, name))
			_, serr := os.Stat(s.video(slug))
			if err == nil && serr == nil && json.Unmarshal(data, &m) == nil {
				s.meta[slug] = m
			} else {
				_ = os.Remove(filepath.Join(dir, name)) // best effort: an orphan
			}
		}
	}
	for _, e := range entries {
		if slug, ok := strings.CutSuffix(e.Name(), ".mp4"); ok {
			if _, kept := s.meta[slug]; !kept {
				_ = os.Remove(filepath.Join(s.dir, e.Name())) // best effort: an orphan
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
// than the stored one is refused (ErrStale) unless the stored one has expired; one of the same
// meeting replaces it. On failure the stored recording is left as it was, or removed when the
// failure came after the new files were put in place.
func (s *Store) Put(slug string, meeting time.Time, r io.Reader, max int64) (Meta, error) {
	s.mu.Lock()
	stale := s.staleLocked(slug, meeting)
	s.mu.Unlock()
	if stale {
		return Meta{}, ErrStale
	}
	tmpVideo, n, err := s.writeTemp(slug+".mp4.*.tmp", r, max)
	if err != nil {
		return Meta{}, err
	}
	m := Meta{Meeting: meeting, Ended: s.now(), Size: n}
	data, _ := json.Marshal(m)
	tmpInfo, _, err := s.writeTemp(slug+".json.*.tmp", bytes.NewReader(data), int64(len(data)))
	if err != nil {
		_ = os.Remove(tmpVideo)
		return Meta{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.staleLocked(slug, meeting) { // a later meeting's upload finished meanwhile
		_ = os.Remove(tmpVideo)
		_ = os.Remove(tmpInfo)
		return Meta{}, ErrStale
	}
	if err := os.Rename(tmpVideo, s.video(slug)); err != nil {
		_ = os.Remove(tmpVideo)
		_ = os.Remove(tmpInfo)
		return Meta{}, err
	}
	if err := os.Rename(tmpInfo, s.info(slug)); err != nil {
		// The new video is in place without its meta: drop the pair so the store is consistent.
		_ = os.Remove(tmpInfo)
		_ = os.Remove(s.video(slug))
		_ = os.Remove(s.info(slug))
		delete(s.meta, slug)
		return Meta{}, err
	}
	s.meta[slug] = m
	return m, nil
}

// writeTemp copies at most max bytes from r into a new file in dir named by pattern. It returns
// the file's name and size; on failure the file is removed.
func (s *Store) writeTemp(pattern string, r io.Reader, max int64) (string, int64, error) {
	f, err := os.CreateTemp(s.dir, pattern)
	if err != nil {
		return "", 0, err
	}
	name := f.Name()
	n, err := io.Copy(f, io.LimitReader(r, max+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err == nil && n > max {
		err = ErrTooLarge
	}
	if err != nil {
		_ = os.Remove(name)
		return "", 0, err
	}
	return name, n, nil
}

// staleLocked reports whether a stored, unexpired recording is of a later meeting than meeting.
// The caller holds s.mu.
func (s *Store) staleLocked(slug string, meeting time.Time) bool {
	old, ok := s.meta[slug]
	return ok && !s.expired(old) && meeting.Before(old.Meeting)
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

// removeLocked deletes both files of the room and then its meta entry. If a file cannot be
// removed, the entry stays so that a later Sweep or Remove retries. The caller holds s.mu.
func (s *Store) removeLocked(slug string) error {
	var first error
	for _, p := range []string{s.video(slug), s.info(slug)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	if first != nil {
		return first
	}
	delete(s.meta, slug)
	return nil
}

// Sweep deletes expired recordings and returns their rooms, sorted. A room whose files cannot be
// removed is left for a later sweep and not returned.
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
