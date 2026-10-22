package conference

import (
	"time"

	"github.com/ssubbotin/kunveno/internal/room"
)

// Event is something a member's client is told; signal turns each into a protocol message.
type Event interface{ event() }

// Joined confirms a join to the member who made it.
type Joined struct {
	ID, Name       string // the member
	Slug, RoomName string
	Recorder       bool      // the member is the recorder, which is told the meeting's start
	Meeting        time.Time // the start of the meeting, for the recorder
	Offered        bool      // the room offers the facilitator switch
}

// Roster is the room's participants and switches, sent to everyone on every change.
type Roster struct{ room.Snapshot }

// ShareBusy refuses a share while someone else holds the lock.
type ShareBusy struct{}

// Reaction is a participant's emoji, shown to everyone in the room.
type Reaction struct{ From, Emoji string }

// Stop asks the notes bot, the recorder or the facilitator to finish and leave.
type Stop struct{}

// NotesReady tells the people in a room that its notes can be downloaded.
type NotesReady struct{}

// RecordingReady tells the people in a room that its recording can be downloaded.
type RecordingReady struct{}

// LivePhrase shows a recognised phrase in the room (notes.live).
type LivePhrase struct {
	Name  string
	Start time.Time
	Text  string
}

func (Joined) event()         {}
func (Roster) event()         {}
func (ShareBusy) event()      {}
func (Reaction) event()       {}
func (Stop) event()           {}
func (NotesReady) event()     {}
func (RecordingReady) event() {}
func (LivePhrase) event()     {}
