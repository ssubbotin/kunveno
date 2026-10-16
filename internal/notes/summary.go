package notes

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A meeting summary is written by the kunveno-notes add-on from a room's ended notes and kept next
// to them as dir/<slug>.<end>.summary.md. It belongs to one ended transcript, named by the
// transcript's end in Unix milliseconds, which the file name carries: an upload for any other end is refused, and every way the notes go
// (a new meeting, reopening, deletion, expiry, a deleted room) takes the summary with them.

// SummaryState is what the room's ready notes have of a summary.
type SummaryState string

const (
	SummaryNone    SummaryState = ""        // no ready notes
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

const summarySuffix = ".summary.md"

// deletedSuffix marks a summary a person deleted: the notes stay, the summary is not written again.
const deletedSuffix = ".summary.deleted"

func (s *Store) summaryPath(slug string, end int64) string {
	return filepath.Join(s.dir, slug+"."+strconv.FormatInt(end, 10)+summarySuffix)
}

// readyEndLocked is the end of the room's ready notes in ms; false when there are none.
func (s *Store) readyEndLocked(slug string) (int64, bool) {
	if _, ok := s.untilLocked(slug); !ok {
		return 0, false
	}
	return s.ended[slug].UnixMilli(), true
}

// EndedAt returns the end of the room's ready notes in Unix ms; false when there are none.
func (s *Store) EndedAt(slug string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readyEndLocked(slug)
}

// PendingSummaries lists the ready notes with no summary that the add-on has not given up on.
func (s *Store) PendingSummaries() []Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Pending
	for slug := range s.ended {
		end, ok := s.readyEndLocked(slug)
		if _, has := s.summaries[slug]; !ok || has || s.dismissed[slug] == end {
			continue
		}
		if f, failed := s.failed[slug]; failed && f == end {
			continue
		}
		out = append(out, Pending{Slug: slug, End: end})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// PutSummary stores the summary of the room's ready notes that ended at end; ErrStale for any other
// notes.
func (s *Store) PutSummary(slug string, end int64, body []byte) error {
	if !slugRe.MatchString(slug) {
		return ErrStale
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if got, ok := s.readyEndLocked(slug); !ok || got != end || s.dismissed[slug] == end {
		return ErrStale
	}
	path := s.summaryPath(slug, end)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.summaries[slug] = end
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

// SummaryState says what the room's ready notes have of a summary.
func (s *Store) SummaryState(slug string) SummaryState {
	s.mu.Lock()
	defer s.mu.Unlock()
	end, ok := s.readyEndLocked(slug)
	if !ok || s.dismissed[slug] == end {
		return SummaryNone
	}
	if _, has := s.summaries[slug]; has {
		return SummaryReady
	}
	if f, failed := s.failed[slug]; failed && f == end {
		return SummaryFailed
	}
	return SummaryPending
}

// LoadSummary reads the summary of the room's ready notes; ErrNoSummary when there is none.
func (s *Store) LoadSummary(slug string) (Summary, error) {
	if !slugRe.MatchString(slug) {
		return Summary{}, ErrNoSummary
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	end, ok := s.readyEndLocked(slug)
	if have, has := s.summaries[slug]; !ok || !has || have != end {
		return Summary{}, ErrNoSummary
	}
	body, err := os.ReadFile(s.summaryPath(slug, end))
	if errors.Is(err, fs.ErrNotExist) {
		return Summary{}, ErrNoSummary
	}
	if err != nil {
		return Summary{}, err
	}
	lines, err := readLines(s.path(slug))
	if err != nil || len(lines) == 0 || lines[0].Type != KindHeader {
		return Summary{}, ErrNoSummary
	}
	return Summary{Room: lines[0].Room, Start: s.start[slug], End: s.ended[slug], Body: body}, nil
}

// dropSummaryLocked removes the room's summary with its notes. A file that cannot be removed is
// forgotten all the same; a restart removes it, since its notes are gone or new.
func (s *Store) dropSummaryLocked(slug string) {
	delete(s.summaries, slug)
	delete(s.failed, slug)
	delete(s.dismissed, slug)
	paths, _ := filepath.Glob(filepath.Join(s.dir, slug+".*.summary.*")) // the summary, its tmp, a deletion mark
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

// openSummaries marks the summaries and deletion marks of the transcripts Open loaded, each only
// when its file names the end of its room's notes, and removes every other such file and any
// temporary file a crash left.
func (s *Store) openSummaries() {
	for suffix, into := range map[string]map[string]int64{summarySuffix: s.summaries, deletedSuffix: s.dismissed} {
		paths, _ := filepath.Glob(filepath.Join(s.dir, "*"+suffix))
		for _, path := range paths {
			slug, endText, _ := strings.Cut(strings.TrimSuffix(filepath.Base(path), suffix), ".")
			end, err := strconv.ParseInt(endText, 10, 64)
			if ended, ok := s.ended[slug]; ok && err == nil && slugRe.MatchString(slug) && !ended.IsZero() && ended.UnixMilli() == end {
				into[slug] = end
				continue
			}
			_ = os.Remove(path)
		}
	}
	tmps, _ := filepath.Glob(filepath.Join(s.dir, "*"+summarySuffix+".tmp"))
	for _, path := range tmps {
		_ = os.Remove(path)
	}
}

// DeleteSummary deletes the summary of the room's ready notes at a person's request, whether it
// is written, still pending or failed: the notes stay and the summary is not written again for
// them. ErrNoSummary when the room has no ready notes or their summary is already deleted.
func (s *Store) DeleteSummary(slug string) error {
	if !slugRe.MatchString(slug) {
		return ErrNoSummary
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	end, ok := s.readyEndLocked(slug)
	if !ok || s.dismissed[slug] == end {
		return ErrNoSummary
	}
	mark := filepath.Join(s.dir, slug+"."+strconv.FormatInt(end, 10)+deletedSuffix)
	if err := os.WriteFile(mark, nil, 0o600); err != nil {
		return err
	}
	if err := os.Remove(s.summaryPath(slug, end)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = os.Remove(mark)
		return err
	}
	delete(s.summaries, slug)
	delete(s.failed, slug)
	s.dismissed[slug] = end
	return nil
}
