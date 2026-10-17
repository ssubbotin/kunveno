// Package recording keeps meeting recordings: every part of every meeting's recording of each room,
// uploaded by the recording bot of the kunveno-recorder add-on, each part with its expiry.
package recording

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrNoRecording = errors.New("no recording")
	ErrRecording   = errors.New("the room is recording")
	ErrTooLarge    = errors.New("recording too large")
)

var slugRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// Meta describes a stored part of a meeting's recording.
type Meta struct {
	Meeting time.Time `json:"meeting"` // start of the meeting it records
	Part    time.Time `json:"part"`    // start of this part; the meeting's start for a recorder that sends none
	Ended   time.Time `json:"ended"`   // when the upload finished
	Size    int64     `json:"size"`
}

// Gone names a part the expiry sweep removed.
type Gone struct {
	Slug          string
	Meeting, Part int64 // Unix ms
}

type pkey struct {
	slug          string
	meeting, part int64
}

// Store is safe for concurrent use. Files: <slug>/<meeting>.<part>.mp4 and .json (Meta) in dir,
// 0600, the meeting and the part named by their starts in Unix ms. An upload writes unique
// temporary files (ending in .tmp) that are renamed into place or removed.
type Store struct {
	dir  string
	keep time.Duration
	now  func() time.Time

	mu     sync.Mutex
	meta   map[pkey]Meta
	active map[string]bool
}

// Open loads the stored recordings of dir, after moving the files of the layout that kept only each
// room's last recording (<slug>.mp4 and <slug>.json) into place as that meeting's part. Leftover
// temporary files and files without a partner are removed.
func Open(dir string, keep time.Duration, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, keep: keep, now: now, meta: map[pkey]Meta{}, active: map[string]bool{}}
	s.migrate()
	rooms, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, r := range rooms {
		if !r.IsDir() || !slugRe.MatchString(r.Name()) {
			continue
		}
		s.loadRoom(r.Name())
	}
	return s, nil
}

// loadRoom reads one room's directory.
func (s *Store) loadRoom(slug string) {
	entries, err := os.ReadDir(filepath.Join(s.dir, slug))
	if err != nil {
		return
	}
	for _, e := range entries {
		name, path := e.Name(), filepath.Join(s.dir, slug, e.Name())
		if strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(path) // an upload cut off by a restart
			continue
		}
		stem, ok := strings.CutSuffix(name, ".json")
		if !ok {
			continue
		}
		var m Meta
		data, err := os.ReadFile(path)
		k, kok := parseStem(slug, stem)
		_, serr := os.Stat(s.video(k))
		if err == nil && kok && serr == nil && json.Unmarshal(data, &m) == nil && m.Meeting.UnixMilli() == k.meeting {
			if m.Part.IsZero() {
				m.Part = m.Meeting
			}
			s.meta[k] = m
		} else {
			_ = os.Remove(path) // best effort: an orphan
		}
	}
	for _, e := range entries {
		if stem, ok := strings.CutSuffix(e.Name(), ".mp4"); ok {
			if k, kok := parseStem(slug, stem); !kok || !s.has(k) {
				_ = os.Remove(filepath.Join(s.dir, slug, e.Name())) // best effort: an orphan
			}
		}
	}
}

func (s *Store) has(k pkey) bool { _, ok := s.meta[k]; return ok }

// parseStem reads <meeting>.<part>.
func parseStem(slug, stem string) (pkey, bool) {
	a, b, _ := strings.Cut(stem, ".")
	m, err1 := strconv.ParseInt(a, 10, 64)
	p, err2 := strconv.ParseInt(b, 10, 64)
	return pkey{slug, m, p}, err1 == nil && err2 == nil
}

// migrate moves <slug>.mp4 with its <slug>.json to <slug>/<meeting>.<meeting>.mp4 and .json; a pair
// that cannot be read is removed as an orphan was before, a move that fails leaves the pair.
func (s *Store) migrate() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name, path := e.Name(), filepath.Join(s.dir, e.Name())
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(path)
			continue
		}
		slug, ok := strings.CutSuffix(name, ".json")
		if !ok {
			continue
		}
		var m Meta
		data, rerr := os.ReadFile(path)
		video := filepath.Join(s.dir, slug+".mp4")
		_, serr := os.Stat(video)
		if rerr != nil || serr != nil || json.Unmarshal(data, &m) != nil || !slugRe.MatchString(slug) {
			_ = os.Remove(path)
			continue
		}
		m.Part = m.Meeting
		k := pkey{slug, m.Meeting.UnixMilli(), m.Meeting.UnixMilli()}
		if err := os.MkdirAll(filepath.Join(s.dir, slug), 0o700); err != nil {
			slog.Warn("cannot move a recording of the old layout", "file", video, "err", err)
			continue
		}
		info, _ := json.Marshal(m)
		if err := os.WriteFile(s.info(k), info, 0o600); err != nil {
			slog.Warn("cannot move a recording of the old layout", "file", video, "err", err)
			continue
		}
		if err := os.Rename(video, s.video(k)); err != nil {
			_ = os.Remove(s.info(k))
			slog.Warn("cannot move a recording of the old layout", "file", video, "err", err)
			continue
		}
		_ = os.Remove(path)
		slog.Info("moved to the meeting history layout", "file", video, "to", s.video(k))
	}
	for _, e := range entries { // a video left without its meta
		if slug, ok := strings.CutSuffix(e.Name(), ".mp4"); ok && !e.IsDir() {
			if _, err := os.Stat(filepath.Join(s.dir, slug+".json")); err != nil {
				_ = os.Remove(filepath.Join(s.dir, e.Name()))
			}
		}
	}
}

func (s *Store) stem(k pkey) string {
	return filepath.Join(s.dir, k.slug, strconv.FormatInt(k.meeting, 10)+"."+strconv.FormatInt(k.part, 10))
}
func (s *Store) video(k pkey) string { return s.stem(k) + ".mp4" }
func (s *Store) info(k pkey) string  { return s.stem(k) + ".json" }

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

// Put stores one part of a meeting's recording from r, at most max bytes: part is the part's start,
// the meeting's start for a recorder that sends none. An upload of a stored part replaces that part
// only. On failure the stored part is left as it was, or removed when the failure came after the
// new files were put in place.
func (s *Store) Put(slug string, meeting, part time.Time, r io.Reader, max int64) (Meta, error) {
	if !slugRe.MatchString(slug) {
		return Meta{}, ErrNoRecording
	}
	k := pkey{slug, meeting.UnixMilli(), part.UnixMilli()}
	if err := os.MkdirAll(filepath.Join(s.dir, slug), 0o700); err != nil {
		return Meta{}, err
	}
	tmpVideo, n, err := s.writeTemp(slug, filepath.Base(s.video(k))+".*.tmp", r, max)
	if err != nil {
		return Meta{}, err
	}
	m := Meta{Meeting: meeting, Part: part, Ended: s.now(), Size: n}
	data, _ := json.Marshal(m)
	tmpInfo, _, err := s.writeTemp(slug, filepath.Base(s.info(k))+".*.tmp", bytes.NewReader(data), int64(len(data)))
	if err != nil {
		_ = os.Remove(tmpVideo)
		return Meta{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Rename(tmpVideo, s.video(k)); err != nil {
		_ = os.Remove(tmpVideo)
		_ = os.Remove(tmpInfo)
		return Meta{}, err
	}
	if err := os.Rename(tmpInfo, s.info(k)); err != nil {
		// The new video is in place without its meta: drop the pair so the store is consistent.
		_ = os.Remove(tmpInfo)
		_ = os.Remove(s.video(k))
		_ = os.Remove(s.info(k))
		delete(s.meta, k)
		return Meta{}, err
	}
	s.meta[k] = m
	return m, nil
}

// writeTemp copies at most max bytes from r into a new file in the room's directory named by
// pattern. It returns the file's name and size; on failure the file is removed.
func (s *Store) writeTemp(slug, pattern string, r io.Reader, max int64) (string, int64, error) {
	f, err := os.CreateTemp(filepath.Join(s.dir, slug), pattern)
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

func (s *Store) expired(m Meta) bool { return !s.now().Before(m.Ended.Add(s.keep)) }

// Parts returns the meeting's unexpired parts, by start.
func (s *Store) Parts(slug string, meeting int64) []Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Meta
	for k, m := range s.meta {
		if k.slug == slug && k.meeting == meeting && !s.expired(m) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Part.Before(out[j].Part) })
	return out
}

// Meetings returns the room's meetings with an unexpired part, newest first.
func (s *Store) Meetings(slug string) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[int64]bool{}
	var out []int64
	for k, m := range s.meta {
		if k.slug == slug && !s.expired(m) && !seen[k.meeting] {
			seen[k.meeting] = true
			out = append(out, k.meeting)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] > out[j] })
	return out
}

// Latest returns the room's newest meeting with a part, and its last part.
func (s *Store) Latest(slug string) (meeting, part int64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, found := s.latestLocked(slug)
	return k.meeting, k.part, found
}

func (s *Store) latestLocked(slug string) (pkey, bool) {
	var best pkey
	found := false
	for k, m := range s.meta {
		if k.slug != slug || s.expired(m) {
			continue
		}
		if !found || k.meeting > best.meeting || (k.meeting == best.meeting && k.part > best.part) {
			best, found = k, true
		}
	}
	return best, found
}

// Until reports until when the room's newest part can be downloaded.
func (s *Store) Until(slug string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.latestLocked(slug)
	if !ok {
		return time.Time{}, false
	}
	return s.meta[k].Ended.Add(s.keep), true
}

// Meeting returns the start of the room's newest meeting with a part.
func (s *Store) Meeting(slug string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.latestLocked(slug)
	if !ok {
		return time.Time{}, false
	}
	return s.meta[k].Meeting, true
}

// OpenPart opens one part for reading; the caller closes the file.
func (s *Store) OpenPart(slug string, meeting, part int64) (*os.File, Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openLocked(pkey{slug, meeting, part})
}

func (s *Store) openLocked(k pkey) (*os.File, Meta, error) {
	m, ok := s.meta[k]
	if !ok || s.expired(m) {
		return nil, Meta{}, ErrNoRecording
	}
	f, err := os.Open(s.video(k))
	if err != nil {
		return nil, Meta{}, ErrNoRecording
	}
	return f, m, nil
}

// Open opens the room's newest part, for the routes that name no meeting.
func (s *Store) Open(slug string) (*os.File, Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.latestLocked(slug)
	if !ok {
		return nil, Meta{}, ErrNoRecording
	}
	return s.openLocked(k)
}

// DeletePart removes one part at a person's request, also while the room records another.
func (s *Store) DeletePart(slug string, meeting, part int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := pkey{slug, meeting, part}
	m, ok := s.meta[k]
	if !ok || s.expired(m) {
		return ErrNoRecording
	}
	return s.removeLocked(k)
}

// Delete removes the room's newest part, for the route that names no meeting: ErrRecording while
// the room records, as before meeting history.
func (s *Store) Delete(slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[slug] {
		return ErrRecording
	}
	k, ok := s.latestLocked(slug)
	if !ok {
		return ErrNoRecording
	}
	return s.removeLocked(k)
}

// Remove deletes every recording of a deleted room, recording or not.
func (s *Store) Remove(slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, slug)
	for k := range s.meta {
		if k.slug == slug {
			delete(s.meta, k)
		}
	}
	if !slugRe.MatchString(slug) {
		return nil
	}
	return os.RemoveAll(filepath.Join(s.dir, slug))
}

// removeLocked deletes both files of a part and then its meta entry. If a file cannot be removed,
// the entry stays so that a later Sweep or delete retries. The caller holds s.mu.
func (s *Store) removeLocked(k pkey) error {
	var first error
	for _, p := range []string{s.video(k), s.info(k)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	if first != nil {
		return first
	}
	delete(s.meta, k)
	_ = os.Remove(filepath.Join(s.dir, k.slug)) // only when empty
	return nil
}

// Sweep deletes expired parts and returns them, sorted. A part whose files cannot be removed is left
// for a later sweep and not returned.
func (s *Store) Sweep() []Gone {
	s.mu.Lock()
	defer s.mu.Unlock()
	var gone []Gone
	for k, m := range s.meta {
		if s.expired(m) {
			if err := s.removeLocked(k); err == nil {
				gone = append(gone, Gone{Slug: k.slug, Meeting: k.meeting, Part: k.part})
			}
		}
	}
	sort.Slice(gone, func(i, j int) bool {
		a, b := gone[i], gone[j]
		if a.Slug != b.Slug {
			return a.Slug < b.Slug
		}
		if a.Meeting != b.Meeting {
			return a.Meeting < b.Meeting
		}
		return a.Part < b.Part
	})
	return gone
}

// FileName is the download name: <slug>-YYYY-MM-DD-HHMM.mp4 with the part's start in loc.
func FileName(slug string, part time.Time, loc *time.Location) string {
	return fmt.Sprintf("%s-%s.mp4", slug, part.In(loc).Format("2006-01-02-1504"))
}

// Has reports whether the meeting still keeps a part.
func (s *Store) Has(slug string, meeting int64) bool {
	return len(s.Parts(slug, meeting)) > 0
}
