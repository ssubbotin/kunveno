// Package documents keeps the Markdown documents a bot (the facilitator of the kunveno-facilitator
// add-on) prepares during a meeting: per room and meeting, each under an id the bot chooses, with
// its title, revision and update time. A document is kept for keep after its last update.
package documents

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limits of the bot contract.
const (
	MaxBody       = 512 << 10 // bytes of Markdown
	MaxTitle      = 120       // characters after trim
	MaxPerMeeting = 20
)

var (
	ErrBadSlug    = errors.New("documents: bad room slug")
	ErrBadID      = errors.New("documents: bad document id")
	ErrBadTitle   = errors.New("documents: bad title")
	ErrTooMany    = errors.New("documents: the meeting already has the most documents")
	ErrNoDocument = errors.New("documents: no such document")
)

var (
	slugRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	idRe   = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
)

// ValidID reports whether id may name a document.
func ValidID(id string) bool { return idRe.MatchString(id) }

// CleanTitle collapses every whitespace run of s to one space and trims the ends; false when the
// result is not valid UTF-8, holds a control character or is not 1..MaxTitle characters long.
func CleanTitle(s string) (string, bool) {
	if !utf8.ValidString(s) {
		return "", false
	}
	t := strings.Join(strings.Fields(s), " ")
	if n := utf8.RuneCountInString(t); n < 1 || n > MaxTitle {
		return "", false
	}
	for _, r := range t {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return t, true
}

// Info describes one stored document.
type Info struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Rev     int       `json:"rev"`
	Created time.Time `json:"created"` // first stored; orders the list
	Updated time.Time `json:"updated"`
}

// Until is when the document expires under keep.
func (i Info) Until(keep time.Duration) time.Time { return i.Updated.Add(keep) }

// Gone names a meeting that lost a document in the sweep.
type Gone struct {
	Slug    string
	Meeting int64
}

type key struct {
	slug    string
	meeting int64 // the meeting's start, Unix ms
}

// Store keeps dir/<slug>/<meeting>/<id>.md and, per meeting, index.json with the documents' Info.
// Safe for concurrent use.
type Store struct {
	dir  string
	keep time.Duration
	now  func() time.Time

	mu   sync.Mutex
	docs map[key][]Info // in the order first stored
}

// Open reads the indexes in dir (a missing dir holds none). An index that cannot be read is logged
// and skipped, left on disk; only a failure to list dir is an error.
func Open(dir string, keep time.Duration, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	s := &Store{dir: dir, keep: keep, now: now, docs: map[key][]Info{}}
	rooms, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	for _, r := range rooms {
		if !r.IsDir() || !slugRe.MatchString(r.Name()) {
			continue
		}
		meetings, _ := os.ReadDir(filepath.Join(dir, r.Name()))
		for _, m := range meetings {
			ms, err := strconv.ParseInt(m.Name(), 10, 64)
			if !m.IsDir() || err != nil {
				continue
			}
			k := key{r.Name(), ms}
			path := filepath.Join(s.meetingDir(k), "index.json")
			data, err := os.ReadFile(path)
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					slog.Warn("cannot read a documents index; skipped", "file", path, "err", err)
				}
				continue
			}
			var list []Info
			if err := json.Unmarshal(data, &list); err != nil {
				slog.Warn("cannot parse a documents index; skipped", "file", path, "err", err)
				continue
			}
			kept := list[:0]
			for _, d := range list {
				if !idRe.MatchString(d.ID) {
					continue
				}
				if _, err := os.Stat(s.bodyPath(k, d.ID)); err == nil {
					kept = append(kept, d)
				}
			}
			if len(kept) > 0 {
				s.docs[k] = kept
			}
		}
	}
	return s, nil
}

// Keep is how long a document stays after its last update.
func (s *Store) Keep() time.Duration { return s.keep }

func (s *Store) meetingDir(k key) string {
	return filepath.Join(s.dir, k.slug, strconv.FormatInt(k.meeting, 10))
}

func (s *Store) bodyPath(k key, id string) string {
	return filepath.Join(s.meetingDir(k), id+".md")
}

func (s *Store) live(d Info) bool { return s.now().Before(d.Until(s.keep)) }

// Put stores body as the document id of the room's meeting. A new id is refused with ErrTooMany
// once the meeting has MaxPerMeeting documents; an id already stored is replaced, its revision
// grows by one and its title becomes title.
func (s *Store) Put(slug string, meeting int64, id, title string, body []byte) (Info, error) {
	switch {
	case !slugRe.MatchString(slug):
		return Info{}, ErrBadSlug
	case !idRe.MatchString(id):
		return Info{}, ErrBadID
	}
	title, ok := CleanTitle(title)
	if !ok {
		return Info{}, ErrBadTitle
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{slug, meeting}
	list := s.docs[k]
	at := -1
	live := 0
	for i, d := range list {
		if d.ID == id {
			at = i
		}
		if s.live(d) {
			live++
		}
	}
	now := s.now()
	info := Info{ID: id, Title: title, Rev: 1, Created: now, Updated: now}
	if at >= 0 {
		info.Rev, info.Created = list[at].Rev+1, list[at].Created
	} else if live >= MaxPerMeeting {
		return Info{}, ErrTooMany
	}
	if err := os.MkdirAll(s.meetingDir(k), 0o700); err != nil {
		return Info{}, err
	}
	if err := writeFile(s.bodyPath(k, id), body); err != nil {
		return Info{}, err
	}
	next := append([]Info(nil), list...)
	if at >= 0 {
		next[at] = info
	} else {
		next = append(next, info)
	}
	if err := s.saveLocked(k, next); err != nil {
		return Info{}, err
	}
	s.docs[k] = next
	return info, nil
}

// List returns the meeting's documents that have not expired, in the order first stored.
func (s *Store) List(slug string, meeting int64) []Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Info
	for _, d := range s.docs[key{slug, meeting}] {
		if s.live(d) {
			out = append(out, d)
		}
	}
	return out
}

// Load reads one document; ErrNoDocument when it is not there or has expired.
func (s *Store) Load(slug string, meeting int64, id string) (Info, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{slug, meeting}
	for _, d := range s.docs[k] {
		if d.ID != id || !s.live(d) {
			continue
		}
		body, err := os.ReadFile(s.bodyPath(k, id))
		if errors.Is(err, fs.ErrNotExist) {
			return Info{}, nil, ErrNoDocument
		}
		return d, body, err
	}
	return Info{}, nil, ErrNoDocument
}

// Delete removes one document at a person's request; ErrNoDocument when it is not there or has
// expired.
func (s *Store) Delete(slug string, meeting int64, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{slug, meeting}
	list := s.docs[k]
	for i, d := range list {
		if d.ID != id || !s.live(d) {
			continue
		}
		next := append(append([]Info(nil), list[:i]...), list[i+1:]...)
		if err := s.replaceLocked(k, next); err != nil {
			return err
		}
		if err := os.Remove(s.bodyPath(k, id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("cannot delete a document file", "file", s.bodyPath(k, id), "err", err)
		}
		s.cleanLocked(k)
		return nil
	}
	return ErrNoDocument
}

// Meetings lists the starts of the room's meetings that keep a document, newest first.
func (s *Store) Meetings(slug string) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int64
	for k, list := range s.docs {
		if k.slug != slug {
			continue
		}
		for _, d := range list {
			if s.live(d) {
				out = append(out, k.meeting)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] > out[j] })
	return out
}

// Latest is the newest meeting of the room that keeps the document id.
func (s *Store) Latest(slug, id string) (int64, bool) {
	for _, m := range s.Meetings(slug) {
		for _, d := range s.List(slug, m) {
			if d.ID == id {
				return m, true
			}
		}
	}
	return 0, false
}

// Has reports whether the meeting keeps a document.
func (s *Store) Has(slug string, meeting int64) bool { return len(s.List(slug, meeting)) > 0 }

// Remove deletes everything the room keeps, for a deleted room. The room is forgotten even when
// its directory cannot be removed (the error is returned).
func (s *Store) Remove(slug string) error {
	if !slugRe.MatchString(slug) {
		return ErrBadSlug
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.docs {
		if k.slug == slug {
			delete(s.docs, k)
		}
	}
	return os.RemoveAll(filepath.Join(s.dir, slug))
}

// Sweep deletes expired documents and returns the meetings that lost one, sorted.
func (s *Store) Sweep() []Gone {
	s.mu.Lock()
	defer s.mu.Unlock()
	var gone []Gone
	for k, list := range s.docs {
		var kept []Info
		for _, d := range list {
			if s.live(d) {
				kept = append(kept, d)
			}
		}
		if len(kept) == len(list) {
			continue
		}
		if err := s.replaceLocked(k, kept); err != nil {
			continue // tried again on the next sweep
		}
		for _, d := range list {
			if !s.live(d) {
				_ = os.Remove(s.bodyPath(k, d.ID))
			}
		}
		gone = append(gone, Gone{Slug: k.slug, Meeting: k.meeting})
		s.cleanLocked(k)
	}
	sort.Slice(gone, func(i, j int) bool {
		if gone[i].Slug != gone[j].Slug {
			return gone[i].Slug < gone[j].Slug
		}
		return gone[i].Meeting < gone[j].Meeting
	})
	return gone
}

// replaceLocked saves list as the meeting's index, or removes the index when list is empty.
func (s *Store) replaceLocked(k key, list []Info) error {
	if len(list) == 0 {
		if err := os.Remove(filepath.Join(s.meetingDir(k), "index.json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		delete(s.docs, k)
		return nil
	}
	if err := s.saveLocked(k, list); err != nil {
		return err
	}
	s.docs[k] = list
	return nil
}

// cleanLocked removes the directories of a meeting and a room left with nothing.
func (s *Store) cleanLocked(k key) {
	if _, ok := s.docs[k]; ok {
		return
	}
	_ = os.RemoveAll(s.meetingDir(k))           // only stray files of this store remain
	_ = os.Remove(filepath.Join(s.dir, k.slug)) // only when empty
}

func (s *Store) saveLocked(k key, list []Info) error {
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(s.meetingDir(k), "index.json"), append(data, '\n'))
}

// writeFile replaces path atomically (temporary file and rename), mode 0600.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".doc-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
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
	return os.Rename(tmp.Name(), path)
}

// Bytes is how much the documents take on disk.
func (s *Store) Bytes() int64 {
	var n int64
	_ = filepath.WalkDir(s.dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// FileName is a download's name: the room, the meeting's start and the title, letters and digits
// kept and every other run turned into one hyphen, at most 60 characters of it; the id when
// nothing of the title is left.
func FileName(slug string, start time.Time, title, id string, loc *time.Location) string {
	var b strings.Builder
	n, dash := 0, false
	for _, r := range title {
		if n >= 60 {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
				n++
			}
			b.WriteRune(r)
			n++
			dash = false
		} else {
			dash = true
		}
	}
	name := b.String()
	if name == "" {
		name = id
	}
	return slug + "-" + start.In(loc).Format("2006-01-02-1504") + "-" + name + ".md"
}
