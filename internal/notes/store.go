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

// Store keeps the transcript of each room's last meeting in dir/<slug>.jsonl, one JSON object per
// line, appended as the meeting goes; a restart loses nothing already written. Safe for
// concurrent use.
type Store struct {
	dir  string
	keep time.Duration
	now  func() time.Time

	mu    sync.Mutex
	ended map[string]time.Time // slug -> end of its transcript; zero while the transcript is open
	start map[string]time.Time // slug -> start of the meeting its transcript belongs to
	// summaries marks rooms whose notes have a summary file; failed holds the end (ms) of notes the
	// add-on gave up on (summary.go).
	summaries map[string]bool
	failed    map[string]int64
}

// Open reads the transcripts in dir (a missing dir holds none). A file without an end line was cut
// off by a restart: it gets one at the time of its last line. A file that cannot be read is logged
// and skipped, left on disk as it is; only a failure to list dir is an error.
func Open(dir string, keep time.Duration, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	s := &Store{dir: dir, keep: keep, now: now, ended: map[string]time.Time{}, start: map[string]time.Time{},
		summaries: map[string]bool{}, failed: map[string]int64{}}
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		slug := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if !slugRe.MatchString(slug) {
			continue
		}
		lines, err := readLines(path)
		if err != nil {
			slog.Warn("cannot read a notes file; skipped", "file", path, "err", err)
			continue
		}
		if len(lines) == 0 || lines[0].Type != KindHeader {
			continue // not a transcript; left alone
		}
		last := lines[len(lines)-1]
		if last.Type != KindEnd {
			last = line{Type: KindEnd, At: lineTime(last)}
			if err := appendLine(path, last); err != nil {
				slog.Warn("cannot end a notes file cut off by a restart; skipped", "file", path, "err", err)
				continue
			}
		}
		s.ended[slug] = time.UnixMilli(last.At)
		s.start[slug] = time.UnixMilli(lines[0].At)
	}
	s.openSummaries()
	return s, nil
}

func (s *Store) path(slug string) string { return filepath.Join(s.dir, slug+".jsonl") }

// Begin starts the transcript of a new meeting, replacing the room's previous one.
func (s *Store) Begin(slug, room string, start time.Time) error {
	if !slugRe.MatchString(slug) {
		return ErrBadSlug
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(line{Type: KindHeader, Room: room, At: start.UnixMilli()})
	if err != nil {
		return err
	}
	tmp := s.path(slug) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path(slug)); err != nil {
		return err
	}
	s.dropSummaryLocked(slug)
	s.ended[slug] = time.Time{}
	s.start[slug] = time.UnixMilli(start.UnixMilli())
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

// Resume records that the notes were switched on again; it reopens an ended transcript.
func (s *Store) Resume(slug string, at time.Time) error {
	return s.add(slug, line{Type: KindResumed, At: at.UnixMilli()}, false)
}

// End closes the transcript: it is ready for download until at plus keep.
func (s *Store) End(slug string, at time.Time) error {
	return s.add(slug, line{Type: KindEnd, At: at.UnixMilli()}, true)
}

func (s *Store) add(slug string, l line, needOpen bool) error {
	if !slugRe.MatchString(slug) {
		return ErrBadSlug
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ended, ok := s.ended[slug]
	if !ok || (needOpen && !ended.IsZero()) {
		return ErrNotOpen
	}
	if err := appendLine(s.path(slug), l); err != nil {
		return err
	}
	switch l.Type {
	case KindEnd:
		s.ended[slug] = time.UnixMilli(l.At)
	case KindResumed:
		s.ended[slug] = time.Time{}
		s.dropSummaryLocked(slug)
	}
	return nil
}

// Until reports until when the room's notes can be downloaded; false when there are none, they are
// still being written, or they have expired.
func (s *Store) Until(slug string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.untilLocked(slug)
}

func (s *Store) untilLocked(slug string) (time.Time, bool) {
	ended, ok := s.ended[slug]
	if !ok || ended.IsZero() {
		return time.Time{}, false
	}
	until := ended.Add(s.keep)
	return until, s.now().Before(until)
}

// Meeting returns the start of the meeting whose ready notes the room keeps (to the millisecond);
// false when Until is false.
func (s *Store) Meeting(slug string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.untilLocked(slug); !ok {
		return time.Time{}, false
	}
	return s.start[slug], true
}

// Load reads the room's ready notes; ErrNoNotes when there are none to download.
func (s *Store) Load(slug string) (Transcript, error) {
	if !slugRe.MatchString(slug) {
		return Transcript{}, ErrBadSlug
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.untilLocked(slug); !ok {
		return Transcript{}, ErrNoNotes
	}
	lines, err := readLines(s.path(slug))
	if errors.Is(err, fs.ErrNotExist) {
		return Transcript{}, ErrNoNotes
	}
	if err != nil {
		return Transcript{}, err
	}
	if len(lines) == 0 || lines[0].Type != KindHeader {
		return Transcript{}, ErrNoNotes
	}
	t := Transcript{Slug: slug, Room: lines[0].Room, Start: time.UnixMilli(lines[0].At), End: s.ended[slug]}
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

// Delete removes the room's notes, for a deleted room. The room is forgotten even when the file
// cannot be removed (the error is returned), so a room created again under the same slug never
// inherits the old notes while the server runs. Such a file stays on disk and is ignored, the
// expiry sweep included, until a restart: then Open reads it again and it expires by its own end
// time.
func (s *Store) Delete(slug string) error {
	if !slugRe.MatchString(slug) {
		return ErrBadSlug
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.ended, slug)
	delete(s.start, slug)
	s.dropSummaryLocked(slug)
	if err := os.Remove(s.path(slug)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// DeleteEnded removes the room's ready notes at a person's request: ErrRecording while its
// transcript is open, ErrNoNotes when there are none or they have expired. The check and the
// removal hold the store's lock, so a meeting cannot reopen the transcript in between. When the
// file cannot be removed the notes stay listed and the error is returned, so the request can be
// repeated.
func (s *Store) DeleteEnded(slug string) error {
	if !slugRe.MatchString(slug) {
		return ErrBadSlug
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ended, ok := s.ended[slug]
	switch {
	case ok && ended.IsZero():
		return ErrRecording
	case !ok:
		return ErrNoNotes
	}
	if _, ready := s.untilLocked(slug); !ready {
		return ErrNoNotes // expired; the sweep removes it
	}
	if err := os.Remove(s.path(slug)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	delete(s.ended, slug)
	delete(s.start, slug)
	s.dropSummaryLocked(slug)
	return nil
}

// Sweep deletes expired notes and returns their rooms, sorted.
func (s *Store) Sweep() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var gone []string
	for slug, ended := range s.ended {
		if ended.IsZero() || s.now().Before(ended.Add(s.keep)) {
			continue
		}
		if err := os.Remove(s.path(slug)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			continue // tried again on the next sweep
		}
		delete(s.ended, slug)
		delete(s.start, slug)
		s.dropSummaryLocked(slug)
		gone = append(gone, slug)
	}
	sort.Strings(gone)
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
