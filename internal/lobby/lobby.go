// Package lobby decides what the lobby shows of the material kept for each room after its
// meetings: the notes, the recording and the meeting they belong to; and removes that material
// when a room is deleted. It has no HTTP; httpapi serves the result.
package lobby

import (
	"log/slog"
	"sort"
	"time"

	"github.com/ssubbotin/kunveno/internal/meetings"
	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/recording"
)

// Kept holds the stores of meeting material. Notes and Recordings are set even while their feature
// is off, so that deleting a room always deletes them; NotesEnabled and RecordingEnabled alone
// decide what is shown. Any store may be nil.
type Kept struct {
	Notes            *notes.Store
	NotesEnabled     bool
	Recordings       *recording.Store
	RecordingEnabled bool
	Meetings         *meetings.Store // summaries of ended meetings
	// SummaryEnabled shows the state of the meeting summary kept with the notes (notes.summary).
	SummaryEnabled bool
}

// Material is what a room keeps, of the enabled features. A zero time means none.
type Material struct {
	NotesUntil     time.Time         // until when the notes of its last meeting can be downloaded
	RecordingUntil time.Time         // until when its stored recording can be downloaded
	Meeting        *meetings.Summary // the meeting whose recording or notes are kept, if summarised
	// Summary is the state of the meeting's summary, kept apart from the notes, and SummaryUntil
	// until when the room shows it; only while summaries are on.
	Summary      notes.SummaryState
	SummaryUntil time.Time
	// Meetings is how many meetings the room lists (Kept.History).
	Meetings int
}

// Part is one part of a meeting's recording.
type Part struct {
	Start, Until time.Time
	Size         int64
}

// Meeting is one meeting of a room with what it keeps, of the enabled features: no names.
type Meeting struct {
	Start, End   time.Time // End is zero when the index has no entry (material of a migrated meeting)
	People       int
	NotesUntil   time.Time // zero: no notes
	Summary      notes.SummaryState
	SummaryUntil time.Time
	Parts        []Part
}

// Of returns what the room keeps.
func (k Kept) Of(slug string) Material {
	var m Material
	if k.NotesEnabled && k.Notes != nil {
		m.NotesUntil, _ = k.Notes.Until(slug)
		if latest, ok := k.Notes.LatestSummary(slug); ok && k.SummaryEnabled {
			m.Summary = k.Notes.SummaryState(slug, latest)
			m.SummaryUntil, _ = k.Notes.SummaryUntil(slug, latest)
		}
	}
	if k.RecordingEnabled && k.Recordings != nil {
		m.RecordingUntil, _ = k.Recordings.Until(slug)
	}
	m.Meeting = k.lastMeeting(slug)
	m.Meetings = len(k.History(slug))
	return m
}

// History lists the room's meetings, newest first: those of the index and any meeting a store still
// keeps material of, with that material.
func (k Kept) History(slug string) []Meeting {
	byStart := map[int64]*Meeting{}
	get := func(ms int64) *Meeting {
		if m, ok := byStart[ms]; ok {
			return m
		}
		m := &Meeting{Start: time.UnixMilli(ms)}
		byStart[ms] = m
		return m
	}
	for _, s := range k.Meetings.List(slug) {
		m := get(s.Start.UnixMilli())
		m.End, m.People = s.End, s.People
	}
	if k.NotesEnabled && k.Notes != nil {
		for _, n := range k.Notes.List(slug) {
			m := get(n.Meeting)
			m.NotesUntil = n.Until
			if m.End.IsZero() {
				m.End = n.Ended
			}
		}
		if k.SummaryEnabled {
			for _, ms := range k.Notes.SummaryMeetings(slug) {
				m := get(ms)
				m.Summary = k.Notes.SummaryState(slug, ms)
				m.SummaryUntil, _ = k.Notes.SummaryUntil(slug, ms)
			}
		}
	}
	if k.RecordingEnabled && k.Recordings != nil {
		for _, ms := range k.Recordings.Meetings(slug) {
			m := get(ms)
			for _, p := range k.Recordings.Parts(slug, ms) {
				m.Parts = append(m.Parts, Part{Start: p.Part, Until: p.Ended.Add(k.Recordings.Keep()), Size: p.Size})
			}
		}
	}
	out := make([]Meeting, 0, len(byStart))
	for _, m := range byStart {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	return out
}

// lastMeeting finds the summary of the newest meeting whose recording, notes or meeting summary the
// room keeps; nil when there is none.
func (k Kept) lastMeeting(slug string) *meetings.Summary {
	if k.Meetings == nil {
		return nil
	}
	var starts []time.Time
	if k.RecordingEnabled && k.Recordings != nil {
		if m, ok := k.Recordings.Meeting(slug); ok {
			starts = append(starts, m)
		}
	}
	if k.NotesEnabled && k.Notes != nil {
		if m, ok := k.Notes.Meeting(slug); ok {
			starts = append(starts, m)
		}
		if m, ok := k.Notes.LatestSummary(slug); ok && k.SummaryEnabled {
			starts = append(starts, time.UnixMilli(m))
		}
	}
	var newest *meetings.Summary
	for _, start := range starts {
		if m, ok := k.Meetings.Find(slug, start); ok && (newest == nil || m.Start.After(newest.Start)) {
			newest = &m
		}
	}
	return newest
}

// Forget deletes everything kept for a deleted room. Failures are logged; the room stays deleted.
func (k Kept) Forget(slug string) {
	if k.Notes != nil {
		// The store forgets the notes even when the file stays. A file left behind is ignored
		// until a restart, when the store reads it again and it expires by its own end time.
		if err := k.Notes.Delete(slug); err != nil {
			slog.Warn("cannot delete the notes file of a deleted room", "room", slug, "err", err)
		}
	}
	if k.Recordings != nil {
		if err := k.Recordings.Remove(slug); err != nil {
			slog.Warn("cannot delete the recording of a deleted room", "room", slug, "err", err)
		}
	}
	if err := k.Meetings.Delete(slug); err != nil {
		slog.Warn("cannot delete the meetings of a deleted room", "room", slug, "err", err)
	}
}
