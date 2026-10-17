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

// A meeting summary is written by the kunveno-notes add-on from a meeting's ended notes and kept as
// dir/<slug>/<meeting>.<end>.summary.md, where end is the notes' end in Unix milliseconds: an upload
// for any other notes is refused. The file's first line holds the room and the meeting's start, so
// the summary stands on its own: deleting the notes keeps it and deleting it keeps the notes. It
// stays as long as the notes would (keep after their end) and goes when the notes are switched on
// again in that meeting, and with the room.

// SummaryState is what a meeting has of a summary.
type SummaryState string

const (
	SummaryNone    SummaryState = ""        // no summary and no ready notes waiting for one
	SummaryPending SummaryState = "pending" // ready notes, the summary not written yet
	SummaryReady   SummaryState = "ready"
	SummaryFailed  SummaryState = "failed" // the add-on gave up on these notes
)

var (
	ErrNoSummary = errors.New("notes: no summary for this meeting")
	// ErrStale: the meeting has no ready notes that ended at the given time.
	ErrStale = errors.New("notes: these notes are no longer the meeting's")
)

// Pending names a meeting's ready notes that wait for a summary.
type Pending struct {
	Slug    string
	Meeting int64 // the meeting's start, Unix ms
	End     int64 // the notes' end, Unix ms
}

// Summary is a meeting's summary with what its file's header needs.
type Summary struct {
	Room       string
	Start, End time.Time
	Body       []byte
}

// summaryRec is a stored summary: the end of the notes it was written from, and the room's name.
type summaryRec struct {
	end  int64
	room string
}

// summaryMeta is the first line of a summary file.
type summaryMeta struct {
	Room  string `json:"room"`
	Start int64  `json:"start"`
}

const summarySuffix = ".summary.md"

// deletedSuffix marks a summary a person deleted while its notes are kept: it is not written again.
const deletedSuffix = ".summary.deleted"

func (s *Store) summaryName(k key, end int64, suffix string) string {
	return filepath.Join(s.dir, k.slug, strconv.FormatInt(k.meeting, 10)+"."+strconv.FormatInt(end, 10)+suffix)
}

// readyEndLocked is the end of the meeting's ready notes in ms; false when there are none.
func (s *Store) readyEndLocked(k key) (int64, bool) {
	if _, ok := s.readyLocked(k); !ok {
		return 0, false
	}
	return s.ended[k].UnixMilli(), true
}

// summaryLocked is the meeting's stored summary while it has not expired.
func (s *Store) summaryLocked(k key) (summaryRec, bool) {
	rec, ok := s.summaries[k]
	if !ok || !s.now().Before(time.UnixMilli(rec.end).Add(s.keep)) {
		return summaryRec{}, false
	}
	return rec, true
}

// EndedAt returns the end of the meeting's ready notes in Unix ms; false when there are none.
func (s *Store) EndedAt(slug string, meeting int64) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readyEndLocked(key{slug, meeting})
}

// MeetingEndedAt finds the room's meeting whose ready notes ended at end.
func (s *Store) MeetingEndedAt(slug string, end int64) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.ended {
		if got, ok := s.readyEndLocked(k); ok && k.slug == slug && got == end {
			return k.meeting, true
		}
	}
	return 0, false
}

// PendingSummaries lists the ready notes with no summary that nobody deleted and the add-on has not
// given up on, by room and meeting.
func (s *Store) PendingSummaries() []Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Pending
	for k := range s.ended {
		if end, ok := s.waitingLocked(k); ok {
			out = append(out, Pending{Slug: k.slug, Meeting: k.meeting, End: end})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Slug != out[j].Slug {
			return out[i].Slug < out[j].Slug
		}
		return out[i].Meeting < out[j].Meeting
	})
	return out
}

// waitingLocked: the meeting's ready notes wait for a summary (no summary, not deleted, not failed).
func (s *Store) waitingLocked(k key) (int64, bool) {
	end, ok := s.readyEndLocked(k)
	if !ok || s.dismissed[k] == end || s.failed[k] == end {
		return 0, false
	}
	if _, has := s.summaries[k]; has {
		return 0, false
	}
	return end, true
}

// PutSummary stores the summary of the meeting's ready notes that ended at end; ErrStale for any
// other notes or a summary a person deleted.
func (s *Store) PutSummary(slug string, meeting, end int64, body []byte) error {
	if !slugRe.MatchString(slug) {
		return ErrStale
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{slug, meeting}
	if got, ok := s.readyEndLocked(k); !ok || got != end || s.dismissed[k] == end {
		return ErrStale
	}
	lines, err := readLines(s.path(k))
	if err != nil || len(lines) == 0 || lines[0].Type != KindHeader {
		return ErrStale
	}
	head, err := json.Marshal(summaryMeta{Room: lines[0].Room, Start: meeting})
	if err != nil {
		return err
	}
	path := s.summaryName(k, end, summarySuffix)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(append(head, '\n'), body...), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.summaries[k] = summaryRec{end: end, room: lines[0].Room}
	delete(s.failed, k)
	return nil
}

// FailSummary records that the add-on gave up on the meeting's notes that ended at end, until a
// restart.
func (s *Store) FailSummary(slug string, meeting, end int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{slug, meeting}
	if got, ok := s.readyEndLocked(k); !ok || got != end {
		return ErrStale
	}
	s.failed[k] = end
	return nil
}

// SummaryState says what the meeting has of a summary.
func (s *Store) SummaryState(slug string, meeting int64) SummaryState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked(key{slug, meeting})
}

func (s *Store) stateLocked(k key) SummaryState {
	if _, ok := s.summaryLocked(k); ok {
		return SummaryReady
	}
	end, ok := s.readyEndLocked(k)
	switch {
	case !ok || s.dismissed[k] == end:
		return SummaryNone
	case s.failed[k] == end:
		return SummaryFailed
	}
	return SummaryPending
}

// SummaryUntil is until when the meeting shows its summary: a stored one until its expiry, a pending
// or failed one as long as its notes; false for SummaryNone.
func (s *Store) SummaryUntil(slug string, meeting int64) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{slug, meeting}
	if rec, ok := s.summaryLocked(k); ok {
		return time.UnixMilli(rec.end).Add(s.keep), true
	}
	if s.stateLocked(k) == SummaryNone {
		return time.Time{}, false
	}
	return s.readyLocked(k)
}

// LatestSummary returns the room's newest meeting that has a summary in any state.
func (s *Store) LatestSummary(slug string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best int64
	found := false
	consider := func(k key) {
		if k.slug == slug && (!found || k.meeting > best) && s.stateLocked(k) != SummaryNone {
			best, found = k.meeting, true
		}
	}
	for k := range s.ended {
		consider(k)
	}
	for k := range s.summaries {
		consider(k)
	}
	return best, found
}

// LoadSummary reads the meeting's stored summary; ErrNoSummary when there is none.
func (s *Store) LoadSummary(slug string, meeting int64) (Summary, error) {
	if !slugRe.MatchString(slug) {
		return Summary{}, ErrNoSummary
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{slug, meeting}
	rec, ok := s.summaryLocked(k)
	if !ok {
		return Summary{}, ErrNoSummary
	}
	data, err := os.ReadFile(s.summaryName(k, rec.end, summarySuffix))
	if errors.Is(err, fs.ErrNotExist) {
		return Summary{}, ErrNoSummary
	}
	if err != nil {
		return Summary{}, err
	}
	_, body, _ := bytes.Cut(data, []byte{'\n'})
	return Summary{Room: rec.room, Start: time.UnixMilli(meeting), End: time.UnixMilli(rec.end), Body: body}, nil
}

// DeleteSummary deletes the meeting's summary at a person's request, whether it is written, still
// pending or failed; the notes stay, and while they do no summary is written for them again.
// ErrNoSummary when there is nothing to delete.
func (s *Store) DeleteSummary(slug string, meeting int64) error {
	if !slugRe.MatchString(slug) {
		return ErrNoSummary
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{slug, meeting}
	if s.stateLocked(k) == SummaryNone {
		return ErrNoSummary
	}
	if end, ok := s.readyEndLocked(k); ok {
		if err := os.WriteFile(s.summaryName(k, end, deletedSuffix), nil, 0o600); err != nil {
			return err
		}
		s.dismissed[k] = end
		delete(s.failed, k)
	}
	if rec, ok := s.summaries[k]; ok {
		if err := os.Remove(s.summaryName(k, rec.end, summarySuffix)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		delete(s.summaries, k)
	}
	return nil
}

// dropSummaryLocked removes everything of the meeting's summary, for notes switched on again in that
// meeting or started anew. A file that cannot be removed is forgotten all the same; Open removes it.
func (s *Store) dropSummaryLocked(k key) {
	delete(s.summaries, k)
	s.dropMarksLocked(k)
	s.removeMatching(k, summarySuffix+"*") // the summary and a tmp
}

// dropMarksLocked forgets what was decided about the summary of the meeting's notes (failed,
// deleted) when those notes go and the summary may stay.
func (s *Store) dropMarksLocked(k key) {
	delete(s.failed, k)
	delete(s.dismissed, k)
	s.removeMatching(k, deletedSuffix)
}

func (s *Store) removeMatching(k key, suffix string) {
	paths, _ := filepath.Glob(filepath.Join(s.dir, k.slug, strconv.FormatInt(k.meeting, 10)+".*"+suffix))
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

// sweepSummariesLocked removes expired summaries.
func (s *Store) sweepSummariesLocked() {
	for k, rec := range s.summaries {
		if s.now().Before(time.UnixMilli(rec.end).Add(s.keep)) {
			continue
		}
		if err := os.Remove(s.summaryName(k, rec.end, summarySuffix)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			continue // tried again on the next sweep
		}
		delete(s.summaries, k)
		_ = os.Remove(s.roomDir(k.slug)) // only when empty
	}
}

// openSummaries reads the summaries and deletion marks Open finds. A summary is kept while it has
// not expired and its meeting has no other notes; a mark only next to the notes it names. Every
// other such file, and any temporary file a crash left, is removed.
func (s *Store) openSummaries() {
	paths, _ := filepath.Glob(filepath.Join(s.dir, "*", "*"+summarySuffix))
	for _, path := range paths {
		k, end, ok := splitName(path, summarySuffix)
		ended, hasNotes := s.ended[k]
		if ok && (!hasNotes || (!ended.IsZero() && ended.UnixMilli() == end)) && s.now().Before(time.UnixMilli(end).Add(s.keep)) {
			if meta, err := readMeta(path); err == nil && meta.Start == k.meeting {
				s.summaries[k] = summaryRec{end: end, room: meta.Room}
				continue
			}
		}
		_ = os.Remove(path)
	}
	marks, _ := filepath.Glob(filepath.Join(s.dir, "*", "*"+deletedSuffix))
	for _, path := range marks {
		k, end, ok := splitName(path, deletedSuffix)
		if ended, has := s.ended[k]; ok && has && !ended.IsZero() && ended.UnixMilli() == end {
			s.dismissed[k] = end
			continue
		}
		_ = os.Remove(path)
	}
	tmps, _ := filepath.Glob(filepath.Join(s.dir, "*", "*"+summarySuffix+".tmp"))
	for _, path := range tmps {
		_ = os.Remove(path)
	}
}

// splitName reads <dir>/<slug>/<meeting>.<end><suffix>.
func splitName(path, suffix string) (key, int64, bool) {
	slug := filepath.Base(filepath.Dir(path))
	meetingText, endText, _ := strings.Cut(strings.TrimSuffix(filepath.Base(path), suffix), ".")
	meeting, err1 := strconv.ParseInt(meetingText, 10, 64)
	end, err2 := strconv.ParseInt(endText, 10, 64)
	return key{slug, meeting}, end, err1 == nil && err2 == nil && slugRe.MatchString(slug)
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

// SummaryMeetings lists the room's meetings that have a summary in any state, newest first.
func (s *Store) SummaryMeetings(slug string) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[int64]bool{}
	var out []int64
	add := func(k key) {
		if k.slug == slug && !seen[k.meeting] && s.stateLocked(k) != SummaryNone {
			seen[k.meeting] = true
			out = append(out, k.meeting)
		}
	}
	for k := range s.ended {
		add(k)
	}
	for k := range s.summaries {
		add(k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] > out[j] })
	return out
}
