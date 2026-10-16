package notes

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A meeting summary is written by the kunveno-notes add-on from a room's ended notes and kept in
// the notes directory as dir/<slug>.<end>.summary.md, where end is the notes' end in Unix
// milliseconds: an upload for any other notes is refused. The file's first line holds the room and
// the meeting's start, so the summary stands on its own: deleting the notes keeps it and deleting it
// keeps the notes. It stays as long as the notes would (keep after their end) and goes with a new
// meeting in the room, with notes switched on again, and with the room.

// SummaryState is what the room has of a summary.
type SummaryState string

const (
	SummaryNone    SummaryState = ""        // no summary and no ready notes waiting for one
	SummaryPending SummaryState = "pending" // ready notes, the summary not written yet
	SummaryReady   SummaryState = "ready"
	SummaryFailed  SummaryState = "failed" // the add-on gave up on these notes
)

var (
	ErrNoSummary = errors.New("notes: no summary for this room")
	// ErrStale: the room has no ready notes that ended at the given time.
	ErrStale = errors.New("notes: these notes are no longer the room's")
)

// Pending names ready notes that wait for a summary.
type Pending struct {
	Slug string
	End  int64 // the transcript's end, Unix ms
}

// Summary is a room's summary with what its file's header needs.
type Summary struct {
	Room       string
	Start, End time.Time
	Body       []byte
}

// summaryRec is a stored summary: the notes' end and the meeting it describes.
type summaryRec struct {
	end, start int64 // Unix ms
	room       string
}

// summaryMeta is the first line of a summary file.
type summaryMeta struct {
	Room  string `json:"room"`
	Start int64  `json:"start"`
}

const summarySuffix = ".summary.md"

// deletedSuffix marks a summary a person deleted while its notes are kept: it is not written again.
const deletedSuffix = ".summary.deleted"

func (s *Store) summaryPath(slug string, end int64) string {
	return filepath.Join(s.dir, slug+"."+strconv.FormatInt(end, 10)+summarySuffix)
}

func (s *Store) markPath(slug string, end int64) string {
	return filepath.Join(s.dir, slug+"."+strconv.FormatInt(end, 10)+deletedSuffix)
}

// readyEndLocked is the end of the room's ready notes in ms; false when there are none.
func (s *Store) readyEndLocked(slug string) (int64, bool) {
	if _, ok := s.untilLocked(slug); !ok {
		return 0, false
	}
	return s.ended[slug].UnixMilli(), true
}

// summaryLocked is the room's stored summary while it has not expired.
func (s *Store) summaryLocked(slug string) (summaryRec, bool) {
	rec, ok := s.summaries[slug]
	if !ok || !s.now().Before(time.UnixMilli(rec.end).Add(s.keep)) {
		return summaryRec{}, false
	}
	return rec, true
}

// EndedAt returns the end of the room's ready notes in Unix ms; false when there are none.
func (s *Store) EndedAt(slug string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readyEndLocked(slug)
}

// PendingSummaries lists the ready notes with no summary that nobody deleted and the add-on has not
// given up on.
func (s *Store) PendingSummaries() []Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Pending
	for slug := range s.ended {
		if end, ok := s.waitingLocked(slug); ok {
			out = append(out, Pending{Slug: slug, End: end})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// waitingLocked: the room's ready notes wait for a summary (no summary, not deleted, not failed).
func (s *Store) waitingLocked(slug string) (int64, bool) {
	end, ok := s.readyEndLocked(slug)
	if !ok || s.dismissed[slug] == end || s.failed[slug] == end {
		return 0, false
	}
	if _, has := s.summaries[slug]; has {
		return 0, false
	}
	return end, true
}

// PutSummary stores the summary of the room's ready notes that ended at end; ErrStale for any other
// notes or a summary a person deleted.
func (s *Store) PutSummary(slug string, end int64, body []byte) error {
	if !slugRe.MatchString(slug) {
		return ErrStale
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if got, ok := s.readyEndLocked(slug); !ok || got != end || s.dismissed[slug] == end {
		return ErrStale
	}
	lines, err := readLines(s.path(slug))
	if err != nil || len(lines) == 0 || lines[0].Type != KindHeader {
		return ErrStale
	}
	rec := summaryRec{end: end, start: s.start[slug].UnixMilli(), room: lines[0].Room}
	head, err := json.Marshal(summaryMeta{Room: rec.room, Start: rec.start})
	if err != nil {
		return err
	}
	path := s.summaryPath(slug, end)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(append(head, '\n'), body...), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.summaries[slug] = rec
	delete(s.failed, slug)
	return nil
}

// FailSummary records that the add-on gave up on the notes that ended at end, until a restart.
func (s *Store) FailSummary(slug string, end int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if got, ok := s.readyEndLocked(slug); !ok || got != end {
		return ErrStale
	}
	s.failed[slug] = end
	return nil
}

// SummaryState says what the room has of a summary.
func (s *Store) SummaryState(slug string) SummaryState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked(slug)
}

func (s *Store) stateLocked(slug string) SummaryState {
	if _, ok := s.summaryLocked(slug); ok {
		return SummaryReady
	}
	end, ok := s.readyEndLocked(slug)
	switch {
	case !ok || s.dismissed[slug] == end:
		return SummaryNone
	case s.failed[slug] == end:
		return SummaryFailed
	}
	return SummaryPending
}

// SummaryUntil is until when the room shows its summary: a stored one until its expiry, a pending
// or failed one as long as its notes; false for SummaryNone.
func (s *Store) SummaryUntil(slug string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.summaryLocked(slug); ok {
		return time.UnixMilli(rec.end).Add(s.keep), true
	}
	if s.stateLocked(slug) == SummaryNone {
		return time.Time{}, false
	}
	return s.untilLocked(slug)
}

// SummaryMeeting is the start of the meeting the room's stored summary describes (to the ms).
func (s *Store) SummaryMeeting(slug string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.summaryLocked(slug)
	if !ok {
		return time.Time{}, false
	}
	return time.UnixMilli(rec.start), true
}

// LoadSummary reads the room's stored summary; ErrNoSummary when there is none.
func (s *Store) LoadSummary(slug string) (Summary, error) {
	if !slugRe.MatchString(slug) {
		return Summary{}, ErrNoSummary
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.summaryLocked(slug)
	if !ok {
		return Summary{}, ErrNoSummary
	}
	data, err := os.ReadFile(s.summaryPath(slug, rec.end))
	if errors.Is(err, fs.ErrNotExist) {
		return Summary{}, ErrNoSummary
	}
	if err != nil {
		return Summary{}, err
	}
	_, body, _ := bytes.Cut(data, []byte{'\n'})
	return Summary{Room: rec.room, Start: time.UnixMilli(rec.start), End: time.UnixMilli(rec.end), Body: body}, nil
}

// DeleteSummary deletes the room's summary at a person's request, whether it is written, still
// pending or failed; the notes stay, and while they do no summary is written for them again.
// ErrNoSummary when there is nothing to delete.
func (s *Store) DeleteSummary(slug string) error {
	if !slugRe.MatchString(slug) {
		return ErrNoSummary
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stateLocked(slug) == SummaryNone {
		return ErrNoSummary
	}
	if end, ok := s.readyEndLocked(slug); ok {
		if err := os.WriteFile(s.markPath(slug, end), nil, 0o600); err != nil {
			return err
		}
		s.dismissed[slug] = end
		delete(s.failed, slug)
	}
	if rec, ok := s.summaries[slug]; ok {
		if err := os.Remove(s.summaryPath(slug, rec.end)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		delete(s.summaries, slug)
	}
	return nil
}

// dropSummaryLocked removes everything of the room's summary: for a new meeting, notes switched on
// again, or a deleted room. A file that cannot be removed is forgotten all the same; Open removes it.
func (s *Store) dropSummaryLocked(slug string) {
	delete(s.summaries, slug)
	s.dropMarksLocked(slug)
	paths, _ := filepath.Glob(filepath.Join(s.dir, slug+".*"+summarySuffix+"*")) // the summary and a tmp
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

// dropMarksLocked forgets what was decided about the summary of the room's notes (failed, deleted)
// when those notes go and the summary may stay.
func (s *Store) dropMarksLocked(slug string) {
	delete(s.failed, slug)
	delete(s.dismissed, slug)
	paths, _ := filepath.Glob(filepath.Join(s.dir, slug+".*"+deletedSuffix))
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

// sweepSummariesLocked removes expired summaries.
func (s *Store) sweepSummariesLocked() {
	for slug, rec := range s.summaries {
		if s.now().Before(time.UnixMilli(rec.end).Add(s.keep)) {
			continue
		}
		if err := os.Remove(s.summaryPath(slug, rec.end)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			continue // tried again on the next sweep
		}
		delete(s.summaries, slug)
	}
}

// openSummaries reads the summaries and deletion marks Open finds. A summary is kept while it has
// not expired and its room has no other notes; a mark only next to the notes it names. Every other
// such file, and any temporary file a crash left, is removed.
func (s *Store) openSummaries() {
	paths, _ := filepath.Glob(filepath.Join(s.dir, "*"+summarySuffix))
	for _, path := range paths {
		slug, end, ok := splitName(path, summarySuffix)
		ended, hasNotes := s.ended[slug]
		if ok && (!hasNotes || (!ended.IsZero() && ended.UnixMilli() == end)) && s.now().Before(time.UnixMilli(end).Add(s.keep)) {
			if meta, err := readMeta(path); err == nil {
				s.summaries[slug] = summaryRec{end: end, start: meta.Start, room: meta.Room}
				continue
			}
		}
		_ = os.Remove(path)
	}
	marks, _ := filepath.Glob(filepath.Join(s.dir, "*"+deletedSuffix))
	for _, path := range marks {
		slug, end, ok := splitName(path, deletedSuffix)
		if ended, has := s.ended[slug]; ok && has && !ended.IsZero() && ended.UnixMilli() == end {
			s.dismissed[slug] = end
			continue
		}
		_ = os.Remove(path)
	}
	tmps, _ := filepath.Glob(filepath.Join(s.dir, "*"+summarySuffix+".tmp"))
	for _, path := range tmps {
		_ = os.Remove(path)
	}
}

// splitName reads <slug>.<end><suffix>.
func splitName(path, suffix string) (string, int64, bool) {
	slug, endText, _ := strings.Cut(strings.TrimSuffix(filepath.Base(path), suffix), ".")
	end, err := strconv.ParseInt(endText, 10, 64)
	return slug, end, err == nil && slugRe.MatchString(slug)
}

// readMeta reads a summary file's first line.
func readMeta(path string) (summaryMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return summaryMeta{}, err
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil {
		return summaryMeta{}, err
	}
	var m summaryMeta
	if err := json.Unmarshal(line, &m); err != nil || m.Start == 0 {
		return summaryMeta{}, errors.New("notes: not a summary file")
	}
	return m, nil
}
