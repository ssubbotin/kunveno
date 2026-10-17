package notes

import (
	"bufio"
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
)

// Line kinds of a transcript file.
const (
	KindHeader  = "header"
	KindPhrase  = "phrase"
	KindPaused  = "paused"
	KindResumed = "resumed"
	KindEnd     = "end"
)

var (
	ErrBadSlug = errors.New("notes: bad room slug")
	ErrNoNotes = errors.New("notes: none ready for this room")
	ErrNotOpen = errors.New("notes: the room has no open transcript")
	// ErrRecording: the room's notes are still being recorded and cannot be deleted.
	ErrRecording = errors.New("notes: still being recorded")
)

var slugRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// line is one JSON object of a transcript file. Times are Unix milliseconds.
type line struct {
	Type string `json:"type"`
	Room string `json:"room,omitempty"` // header: the room's name
	At   int64  `json:"at"`             // header: meeting start; phrase: its start; events: when
	End  int64  `json:"end,omitempty"`  // phrase
	Name string `json:"name,omitempty"` // phrase: the speaker's name when the phrase arrived
	Text string `json:"text,omitempty"` // phrase
}

// Phrase is one recognised utterance.
type Phrase struct {
	Name       string
	Start, End time.Time
	Text       string
}

// Event is a pause (KindPaused) or a resumption (KindResumed) of the recording.
type Event struct {
	Kind string
	At   time.Time
}

// Transcript is one meeting's notes as read back from the store.
type Transcript struct {
	Slug, Room string
	Start, End time.Time
	Phrases    []Phrase
	Events     []Event
}

// key names one meeting of a room: its start in Unix milliseconds.
type key struct {
	slug    string
	meeting int64
}

// Kept describes one meeting's ready notes.
type Kept struct {
	Meeting int64     // the meeting's start, Unix ms
	Ended   time.Time // when its notes ended
	Until   time.Time // until when they are kept
}

// Gone names notes the expiry sweep removed.
type Gone struct {
	Slug    string
	Meeting int64
}

// Store keeps the transcript of every meeting of every room as dir/<slug>/<meeting>.jsonl, one JSON
// object per line, appended as the meeting goes; a restart loses nothing already written. Each
// transcript is kept for keep after it ends. Safe for concurrent use.
type Store struct {
	dir  string
	keep time.Duration
	now  func() time.Time

	mu      sync.Mutex
	ended   map[key]time.Time // end of each transcript; zero while it is open
	current map[string]int64  // the meeting Phrase, Pause, Resume and End act on: the room's last Begin
	// The summaries kept apart from the notes, the end (ms) of notes the add-on gave up on, and the
	// end of notes whose summary a person deleted (summary.go).
	summaries map[key]summaryRec
	failed    map[key]int64
	dismissed map[key]int64
}

// Open reads the transcripts in dir (a missing dir holds none), after moving the files of the layout
// that kept only each room's last meeting (migrate.go). A file without an end line was cut off by a
// restart: it gets one at the time of its last line. A file that cannot be read is logged and
// skipped, left on disk as it is; only a failure to list dir is an error.
func Open(dir string, keep time.Duration, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	s := &Store{dir: dir, keep: keep, now: now, ended: map[key]time.Time{}, current: map[string]int64{},
		summaries: map[key]summaryRec{}, failed: map[key]int64{}, dismissed: map[key]int64{}}
	migrate(dir)
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
		slug := r.Name()
		paths, _ := filepath.Glob(filepath.Join(dir, slug, "*.jsonl"))
		for _, path := range paths {
			meeting, err := strconv.ParseInt(strings.TrimSuffix(filepath.Base(path), ".jsonl"), 10, 64)
			if err != nil {
				continue // not a transcript of this store; left alone
			}
			lines, err := readLines(path)
			if err != nil {
				slog.Warn("cannot read a notes file; skipped", "file", path, "err", err)
				continue
			}
			if len(lines) == 0 || lines[0].Type != KindHeader {
				continue
			}
			last := lines[len(lines)-1]
			if last.Type != KindEnd {
				last = line{Type: KindEnd, At: lineTime(last)}
				if err := appendLine(path, last); err != nil {
					slog.Warn("cannot end a notes file cut off by a restart; skipped", "file", path, "err", err)
					continue
				}
			}
			s.ended[key{slug, meeting}] = time.UnixMilli(last.At)
			if meeting > s.current[slug] {
				s.current[slug] = meeting
			}
		}
	}
	s.openSummaries()
	return s, nil
}

func (s *Store) roomDir(slug string) string { return filepath.Join(s.dir, slug) }

func (s *Store) path(k key) string {
	return filepath.Join(s.dir, k.slug, strconv.FormatInt(k.meeting, 10)+".jsonl")
}

// Begin starts the transcript of a meeting that starts at start. The room's earlier meetings stay;
// a transcript of the same meeting (gone or expired meanwhile) is started again.
func (s *Store) Begin(slug, room string, start time.Time) error {
	if !slugRe.MatchString(slug) {
		return ErrBadSlug
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.roomDir(slug), 0o700); err != nil {
		return err
	}
	k := key{slug, start.UnixMilli()}
	data, err := json.Marshal(line{Type: KindHeader, Room: room, At: k.meeting})
	if err != nil {
		return err
	}
	tmp := s.path(k) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path(k)); err != nil {
		return err
	}
	s.dropSummaryLocked(k)
	s.ended[k] = time.Time{}
	s.current[slug] = k.meeting
	return nil
}

// Phrase appends a phrase to the room's open transcript.
func (s *Store) Phrase(slug string, p Phrase) error {
	return s.add(slug, line{Type: KindPhrase, At: p.Start.UnixMilli(), End: p.End.UnixMilli(), Name: p.Name, Text: p.Text}, true)
}

// Pause records that the notes were switched off.
func (s *Store) Pause(slug string, at time.Time) error {
	return s.add(slug, line{Type: KindPaused, At: at.UnixMilli()}, true)
}

// Resume records that the notes were switched on again; it reopens the room's last transcript when
// it has ended.
func (s *Store) Resume(slug string, at time.Time) error {
	return s.add(slug, line{Type: KindResumed, At: at.UnixMilli()}, false)
}

// End closes the room's open transcript: it is ready for download until at plus keep.
func (s *Store) End(slug string, at time.Time) error {
	return s.add(slug, line{Type: KindEnd, At: at.UnixMilli()}, true)
}

func (s *Store) add(slug string, l line, needOpen bool) error {
	if !slugRe.MatchString(slug) {
		return ErrBadSlug
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{slug, s.current[slug]}
	ended, ok := s.ended[k]
	if !ok || (needOpen && !ended.IsZero()) {
		return ErrNotOpen
	}
	if err := appendLine(s.path(k), l); err != nil {
		return err
	}
	switch l.Type {
	case KindEnd:
		s.ended[k] = time.UnixMilli(l.At)
	case KindResumed:
		s.ended[k] = time.Time{}
		s.dropSummaryLocked(k)
	}
	return nil
}

// readyLocked reports until when the meeting's notes are kept; false when there are none, they are
// still being written, or they have expired.
func (s *Store) readyLocked(k key) (time.Time, bool) {
	ended, ok := s.ended[k]
	if !ok || ended.IsZero() {
		return time.Time{}, false
	}
	until := ended.Add(s.keep)
	return until, s.now().Before(until)
}

// List returns the room's ready notes, newest meeting first.
func (s *Store) List(slug string) []Kept {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Kept
	for k, ended := range s.ended {
		if k.slug != slug {
			continue
		}
		if until, ok := s.readyLocked(k); ok {
			out = append(out, Kept{Meeting: k.meeting, Ended: ended, Until: until})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meeting > out[j].Meeting })
	return out
}

// Latest returns the room's newest meeting with ready notes.
func (s *Store) Latest(slug string) (int64, bool) {
	if l := s.List(slug); len(l) > 0 {
		return l[0].Meeting, true
	}
	return 0, false
}

// Until reports until when the room's newest ready notes are kept; false when it has none.
func (s *Store) Until(slug string) (time.Time, bool) {
	if l := s.List(slug); len(l) > 0 {
		return l[0].Until, true
	}
	return time.Time{}, false
}

// Meeting returns the start of the meeting of the room's newest ready notes.
func (s *Store) Meeting(slug string) (time.Time, bool) {
	m, ok := s.Latest(slug)
	return time.UnixMilli(m), ok
}

// Load reads the meeting's ready notes; ErrNoNotes when there are none to download.
func (s *Store) Load(slug string, meeting int64) (Transcript, error) {
	if !slugRe.MatchString(slug) {
		return Transcript{}, ErrBadSlug
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{slug, meeting}
	if _, ok := s.readyLocked(k); !ok {
		return Transcript{}, ErrNoNotes
	}
	lines, err := readLines(s.path(k))
	if errors.Is(err, fs.ErrNotExist) {
		return Transcript{}, ErrNoNotes
	}
	if err != nil {
		return Transcript{}, err
	}
	if len(lines) == 0 || lines[0].Type != KindHeader {
		return Transcript{}, ErrNoNotes
	}
	t := Transcript{Slug: slug, Room: lines[0].Room, Start: time.UnixMilli(lines[0].At), End: s.ended[k]}
	for _, l := range lines[1:] {
		switch l.Type {
		case KindPhrase:
			t.Phrases = append(t.Phrases, Phrase{Name: l.Name, Start: time.UnixMilli(l.At), End: time.UnixMilli(l.End), Text: l.Text})
		case KindPaused, KindResumed:
			t.Events = append(t.Events, Event{Kind: l.Type, At: time.UnixMilli(l.At)})
		}
	}
	return t, nil
}

// Delete removes everything the room keeps, for a deleted room. The room is forgotten even when its
// directory cannot be removed (the error is returned), so a room created again under the same slug
// never inherits the old notes while the server runs; Open reads what stayed after a restart, and it
// expires by its own end times.
func (s *Store) Delete(slug string) error {
	if !slugRe.MatchString(slug) {
		return ErrBadSlug
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.ended {
		if k.slug == slug {
			delete(s.ended, k)
		}
	}
	for k := range s.summaries {
		if k.slug == slug {
			delete(s.summaries, k)
		}
	}
	for _, m := range []map[key]int64{s.failed, s.dismissed} {
		for k := range m {
			if k.slug == slug {
				delete(m, k)
			}
		}
	}
	delete(s.current, slug)
	return os.RemoveAll(s.roomDir(slug))
}

// DeleteEnded removes one meeting's ready notes at a person's request: ErrRecording while they are
// open, ErrNoNotes when there are none or they have expired. The meeting's summary stays. When the
// file cannot be removed the notes stay listed and the error is returned, so the request can be
// repeated.
func (s *Store) DeleteEnded(slug string, meeting int64) error {
	if !slugRe.MatchString(slug) {
		return ErrBadSlug
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{slug, meeting}
	ended, ok := s.ended[k]
	switch {
	case ok && ended.IsZero():
		return ErrRecording
	case !ok:
		return ErrNoNotes
	}
	if _, ready := s.readyLocked(k); !ready {
		return ErrNoNotes // expired; the sweep removes it
	}
	if err := os.Remove(s.path(k)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	delete(s.ended, k)
	s.dropMarksLocked(k)
	return nil
}

// Sweep deletes expired notes and returns them, sorted; it also deletes expired summaries and the
// directories of rooms left with nothing.
func (s *Store) Sweep() []Gone {
	s.mu.Lock()
	defer s.mu.Unlock()
	var gone []Gone
	for k, ended := range s.ended {
		if ended.IsZero() || s.now().Before(ended.Add(s.keep)) {
			continue
		}
		if err := os.Remove(s.path(k)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			continue // tried again on the next sweep
		}
		delete(s.ended, k)
		s.dropMarksLocked(k)
		gone = append(gone, Gone{Slug: k.slug, Meeting: k.meeting})
	}
	s.sweepSummariesLocked()
	for _, g := range gone {
		_ = os.Remove(s.roomDir(g.Slug)) // only when empty
	}
	sort.Slice(gone, func(i, j int) bool {
		if gone[i].Slug != gone[j].Slug {
			return gone[i].Slug < gone[j].Slug
		}
		return gone[i].Meeting < gone[j].Meeting
	})
	return gone
}

// readLines parses a transcript file; a line torn by a crash is skipped.
func readLines(path string) ([]line, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []line
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Type != "" {
			out = append(out, l)
		}
	}
	return out, sc.Err()
}

func appendLine(path string, l line) error {
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	// A line torn by a crash has no newline; start on a fresh line so this one stays readable.
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, st.Size()-1); err == nil && last[0] != '\n' {
			data = append([]byte{'\n'}, data...)
		}
	}
	_, werr := f.Write(append(data, '\n'))
	return errors.Join(werr, f.Close())
}

// lineTime is when a line's content ends: a phrase's end, any other line's time.
func lineTime(l line) int64 {
	if l.Type == KindPhrase {
		return l.End
	}
	return l.At
}

// Current returns the meeting the room's notes were last begun for; false when none.
func (s *Store) Current(slug string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.current[slug]
	return m, ok
}
