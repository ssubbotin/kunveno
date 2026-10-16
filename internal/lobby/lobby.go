// Package lobby decides what the lobby shows of the material kept for each room after its
// meetings: the notes, the recording and the meeting they belong to; and removes that material
// when a room is deleted. It has no HTTP; httpapi serves the result.
package lobby

import (
	"log/slog"
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
	// Summary is the state of the notes' summary; only while NotesUntil is set and summaries are on.
	Summary notes.SummaryState
}

// Of returns what the room keeps.
func (k Kept) Of(slug string) Material {
	var m Material
	if k.NotesEnabled && k.Notes != nil {
		m.NotesUntil, _ = k.Notes.Until(slug)
		if k.SummaryEnabled && !m.NotesUntil.IsZero() {
			m.Summary = k.Notes.SummaryState(slug)
		}
	}
	if k.RecordingEnabled && k.Recordings != nil {
		m.RecordingUntil, _ = k.Recordings.Until(slug)
	}
	m.Meeting = k.lastMeeting(slug)
	return m
}

// lastMeeting finds the summary of the meeting whose recording or notes the room keeps, the
// recording's meeting first; nil when there is none.
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
	}
	for _, start := range starts {
		if m, ok := k.Meetings.Find(slug, start); ok {
			return &m
		}
	}
	return nil
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
