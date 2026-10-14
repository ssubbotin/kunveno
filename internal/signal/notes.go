package signal

import (
	"errors"
	"time"

	"github.com/coder/websocket"

	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/sfu"
)

// NotesStopGrace is how long a stopped notes bot may take to send its last phrases and leave.
const NotesStopGrace = 10 * time.Second

// maxDeparted bounds the names kept for people who left during a meeting.
const maxDeparted = 1000

// notesRoom is the transcript state of one live room; guarded by Handler.notesMu.
type notesRoom struct {
	meeting  time.Time         // the meeting the stored transcript belongs to; zero before its first phrase
	open     bool              // that transcript takes phrases: no end line since its last phrase
	onAt     time.Time         // when the notes were last switched on
	departed map[string]string // people who left during the meeting, id -> name, for late phrases
}

// SetNotes turns meeting notes on (notes.enabled): people may switch them on in a room, the notes
// bot may join, and its phrases go to store; live also shows every phrase to the room. Call before
// serving. Without it notes messages are ignored and the notes role is refused.
func (h *Handler) SetNotes(store *notes.Store, live bool) {
	h.notes = store
	h.notesLive = live
}

// receives reports whether to is subscribed to from's track of kind: the notes bot hears people's
// audio only (no video, no other helper); everyone else receives everything.
func receives(to, from *session, kind sfu.Kind) bool {
	return to.botKind != RoleNotes || (kind == sfu.KindAudio && !from.bot)
}

// switchNotes turns the room's notes on or off at a person's request.
func (h *Handler) switchNotes(s *session, on bool) {
	lr, _ := h.lookup(s.slug)
	now := time.Now()
	h.notesMu.Lock()
	was := lr.room.Notes()
	lr.room.SetNotes(on)
	n := &lr.notes
	meeting, active := lr.room.Meeting()
	current := n.open && active && n.meeting.Equal(meeting)
	var err error
	switch {
	case on && !was:
		n.onAt = now
		if current {
			err = h.notes.Resume(s.slug, now) // paused, and the old bot has not left yet
		}
	case !on && was && current:
		err = h.notes.Pause(s.slug, now)
	}
	if errors.Is(err, notes.ErrNotOpen) {
		n.open, err = false, nil // gone from the store (the room's notes were deleted); the next phrase begins anew
	}
	h.notesMu.Unlock()
	if err != nil {
		s.log.Error("notes", "on", on, "err", err)
	}
	if on != was {
		s.log.Info("notes", "on", on)
	}
	if was && !on {
		h.stopNotes(lr)
	}
	h.broadcast(lr)
}

// stopNotes asks the room's notes bot to finish: it sends its last phrases and leaves. A bot still
// there after stopGrace is closed. Without a bot the transcript ends at once.
func (h *Handler) stopNotes(lr *liveRoom) {
	h.mu.Lock()
	var stop []*session
	present := false
	for _, o := range lr.members {
		if o.botKind != RoleNotes {
			continue
		}
		present = true
		if o.stopping {
			continue
		}
		o.stopping = true
		b := o
		b.stopTimer = time.AfterFunc(h.stopGrace, func() {
			b.log.Warn("notes bot did not leave in time; closing it")
			_ = b.conn.Close(websocket.StatusNormalClosure, "notes off")
			b.cancel()
		})
		stop = append(stop, b)
	}
	h.mu.Unlock()
	if !present {
		h.finishNotes(lr)
		return
	}
	for _, b := range stop {
		b.log.Info("notes stop")
		if err := b.write(Stop()); err != nil {
			b.log.Warn("notes stop write", "err", err)
			b.cancel()
		}
	}
}

// notesBotLeft runs when a notes bot's session ends. Once no notes bot is left and the room does
// not want notes, the transcript is finished. A bot that dropped while notes are wanted rejoins.
func (h *Handler) notesBotLeft(lr *liveRoom, s *session) {
	h.mu.Lock()
	if s.stopTimer != nil {
		s.stopTimer.Stop()
	}
	others := false
	for _, o := range lr.members {
		if o.botKind == RoleNotes {
			others = true
		}
	}
	h.mu.Unlock()
	if !lr.room.Notes() && !others {
		h.finishNotes(lr)
	}
}

// finishNotes ends the stored transcript of the meeting and tells the people in the room that it
// is ready.
func (h *Handler) finishNotes(lr *liveRoom) {
	if h.notes == nil {
		return
	}
	h.notesMu.Lock()
	n := &lr.notes
	ready := false
	if n.open {
		if err := h.notes.End(lr.room.Slug(), time.Now()); err != nil {
			h.log.Error("notes end", "room", lr.room.Slug(), "err", err)
		} else {
			ready = true
		}
		n.open = false
	}
	if _, active := lr.room.Meeting(); !active {
		n.departed = nil
	}
	h.notesMu.Unlock()
	if ready {
		h.log.Info("notes ready", "room", lr.room.Slug())
		h.sendPeople(lr, NotesReady())
	}
}

// phrase takes one phrase from the notes bot. An invalid phrase, or one whose speaker the server
// does not know, is dropped; the bot's socket stays open.
func (h *Handler) phrase(s *session, m ClientMessage) {
	text, start, end, ok := validPhrase(m, time.Now())
	if !ok {
		s.log.Warn("phrase refused")
		return
	}
	lr, _ := h.lookup(s.slug)
	var name string
	h.mu.Lock()
	if o, here := lr.members[m.Speaker]; here && !o.bot {
		name = o.name
	}
	h.mu.Unlock()
	h.notesMu.Lock()
	if name == "" {
		name = lr.notes.departed[m.Speaker]
	}
	var err error
	if name != "" {
		err = h.appendPhrase(lr, notes.Phrase{Name: name, Start: start, End: end, Text: text})
	}
	h.notesMu.Unlock()
	switch {
	case name == "":
		s.log.Info("phrase of an unknown speaker dropped", "speaker", m.Speaker)
		return
	case err != nil:
		s.log.Error("notes phrase", "err", err)
		return
	}
	if h.notesLive {
		h.sendPeople(lr, LivePhrase(name, start.UnixMilli(), text))
	}
}

// appendPhrase stores p in the transcript of the room's meeting: the first phrase of a meeting
// replaces the room's previous notes, and a phrase after the notes were finished reopens them with
// a resumed mark at the time they were switched on. Called with notesMu held.
func (h *Handler) appendPhrase(lr *liveRoom, p notes.Phrase) error {
	n := &lr.notes
	slug := lr.room.Slug()
	meeting, _ := lr.room.Meeting()
	if meeting.IsZero() {
		return errors.New("no meeting in this room")
	}
	switch {
	case !n.meeting.Equal(meeting):
		if err := h.notes.Begin(slug, lr.room.Name(), meeting); err != nil {
			return err
		}
		n.meeting, n.open = meeting, true
	case !n.open:
		err := h.notes.Resume(slug, n.onAt)
		if errors.Is(err, notes.ErrNotOpen) {
			// The meeting's notes expired or were swept meanwhile: what follows starts afresh.
			err = h.notes.Begin(slug, lr.room.Name(), meeting)
		}
		if err != nil {
			return err
		}
		n.open = true
	}
	return h.notes.Phrase(slug, p)
}

// rememberSpeaker keeps the name of a person who left, for the phrases the bot still sends. Only
// while the room takes notes (wasNotes: it did until this person left) or its transcript is open,
// so a room without notes keeps no names.
func (h *Handler) rememberSpeaker(lr *liveRoom, s *session, wasNotes bool) {
	if h.notes == nil {
		return
	}
	h.notesMu.Lock()
	if !wasNotes && !lr.notes.open {
		h.notesMu.Unlock()
		return
	}
	if lr.notes.departed == nil {
		lr.notes.departed = map[string]string{}
	}
	if len(lr.notes.departed) < maxDeparted {
		lr.notes.departed[s.id] = s.name
	}
	h.notesMu.Unlock()
}

// sendPeople writes msg to the people in the room, not to the helpers.
func (h *Handler) sendPeople(lr *liveRoom, msg any) {
	h.mu.Lock()
	members := lr.membersExcept("")
	h.mu.Unlock()
	for _, o := range members {
		if o.bot {
			continue
		}
		if err := o.write(msg); err != nil {
			o.log.Warn("notes write", "err", err)
		}
	}
}
