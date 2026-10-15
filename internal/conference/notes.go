package conference

import (
	"errors"
	"time"

	"github.com/ssubbotin/kunveno/internal/media"
	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/room"
)

// DefaultNotesStopGrace is how long a stopped notes bot may take to send its last phrases and leave.
const DefaultNotesStopGrace = 10 * time.Second

// maxDeparted bounds the names kept for people who left during a meeting.
const maxDeparted = 1000

// notesRoom is the transcript state of one live room; guarded by Conference.notesMu.
type notesRoom struct {
	meeting  time.Time         // the meeting the stored transcript belongs to; zero before its first phrase
	open     bool              // that transcript takes phrases: no end line since its last phrase
	onAt     time.Time         // when the notes were last switched on
	departed map[string]string // people who left during the meeting, id -> name, for late phrases
}

// SetNotes turns meeting notes on (notes.enabled): people may switch them on in a room, the notes
// bot may join, and its phrases go to store; live also shows every phrase to the room. Call before
// serving. Without it notes messages are ignored and the notes role is refused.
func (c *Conference) SetNotes(store *notes.Store, live bool) {
	c.notes = store
	c.notesLive = live
}

// receives reports whether to is subscribed to from's track of kind: the notes bot hears people's
// audio only; the recorder gets people's audio and screen; everyone else receives everything.
func receives(to, from *Member, kind media.Kind) bool {
	switch to.botKind {
	case room.BotNotes:
		return kind == media.KindAudio && !from.bot
	case room.BotRecorder:
		return (kind == media.KindAudio || kind == media.KindScreen) && !from.bot
	}
	return true
}

// SwitchNotes turns the room's notes on or off at a person's request. Helpers cannot switch them,
// and with notes.enabled off the switch does not exist.
func (c *Conference) SwitchNotes(s *Member, on bool) {
	if s.bot || c.notes == nil {
		return
	}
	lr, _ := c.lookup(s.slug)
	now := time.Now()
	c.notesMu.Lock()
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
			err = c.notes.Resume(s.slug, now) // paused, and the old bot has not left yet
		}
	case !on && was && current:
		err = c.notes.Pause(s.slug, now)
	}
	if errors.Is(err, notes.ErrNotOpen) {
		n.open, err = false, nil // gone from the store (the room's notes were deleted); the next phrase begins anew
	}
	c.notesMu.Unlock()
	if err != nil {
		s.log.Error("notes", "on", on, "err", err)
	}
	if on != was {
		s.log.Info("notes", "on", on)
	}
	if was && !on {
		c.stopNotes(lr)
	}
	c.broadcast(lr)
}

// stopNotes asks the room's notes bots to finish: each sends its last phrases and leaves. A bot
// still there after stopGrace is closed. Without a bot the transcript ends at once.
func (c *Conference) stopNotes(lr *liveRoom) {
	c.mu.Lock()
	var stop []*Member
	for _, o := range lr.members {
		if o.botKind != room.BotNotes || o.stopping {
			continue
		}
		o.stopping = true
		b := o
		b.stopTimer = time.AfterFunc(c.NotesStopGrace, func() {
			b.log.Warn("notes bot did not leave in time; closing it")
			b.conn.Abort("notes off") // Close would wait for the bot's answer
		})
		stop = append(stop, b)
	}
	c.mu.Unlock()
	for _, b := range stop {
		b.log.Info("notes stop")
		if err := b.conn.Send(Stop{}); err != nil {
			b.log.Warn("notes stop write", "err", err)
			b.conn.End()
		}
	}
	c.finishNotes(lr) // does nothing while a bot of the transcript's meeting is still there
}

// notesBotLeft runs when a notes bot's session ends; finishNotes decides whether the transcript is
// done. A bot that dropped while notes are wanted rejoins.
func (c *Conference) notesBotLeft(lr *liveRoom, s *Member) {
	c.mu.Lock()
	if s.stopTimer != nil {
		s.stopTimer.Stop()
	}
	c.mu.Unlock()
	c.finishNotes(lr)
}

// finishNotes ends the open transcript once no notes bot of its meeting is left to write to it and
// that meeting no longer wants notes. The transcript of the current meeting is announced to the
// people in the room; one of an earlier meeting (its bot flushed while a new meeting began) is
// ended quietly, since its people are gone.
func (c *Conference) finishNotes(lr *liveRoom) {
	if c.notes == nil {
		return
	}
	c.notesMu.Lock()
	n := &lr.notes
	meeting, active := lr.room.Meeting()
	current := n.meeting.Equal(meeting)
	ready := false
	if n.open && !c.hasNotesBot(lr, n.meeting) && (!current || !lr.room.Notes()) {
		if err := c.notes.End(lr.room.Slug(), time.Now()); err != nil {
			c.log.Error("notes end", "room", lr.room.Slug(), "err", err)
		} else {
			ready = current
		}
		n.open = false
	}
	if !active && !n.open && !c.hasNotesBot(lr, meeting) {
		n.departed = nil // the meeting is over and no bot will send its late phrases
	}
	c.notesMu.Unlock()
	if ready {
		c.log.Info("notes ready", "room", lr.room.Slug())
		c.sendPeople(lr, NotesReady{})
	}
}

// hasNotesBot reports whether a notes bot that joined during meeting is in the room.
func (c *Conference) hasNotesBot(lr *liveRoom, meeting time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, o := range lr.members {
		if o.botKind == room.BotNotes && o.meeting.Equal(meeting) {
			return true
		}
	}
	return false
}

// Phrase takes one phrase from the notes bot, already validated by the caller: what speaker said
// between start and end. A phrase from anyone else, one whose speaker the server does not know, or
// one from a bot stopped in its own join once the transcript has ended is dropped; the bot's
// session stays open.
func (c *Conference) Phrase(s *Member, speaker, text string, start, end time.Time) {
	if s.botKind != room.BotNotes {
		return
	}
	lr, _ := c.lookup(s.slug)
	var name string
	c.mu.Lock()
	if o, here := lr.members[speaker]; here && !o.bot {
		name = o.name
	}
	late := s.stoppedAtJoin
	c.mu.Unlock()
	c.notesMu.Lock()
	n := &lr.notes
	if name == "" {
		name = n.departed[speaker]
	}
	meeting, _ := lr.room.Meeting()
	earlier := !s.meeting.Equal(meeting) // the bot flushes a meeting that has ended since
	p := notes.Phrase{Name: name, Start: start, End: end, Text: text}
	var err error
	stored := false
	switch {
	case name == "":
	case late && !n.open:
		// The bot joined while the notes were being switched off: the transcript has ended and the
		// room was told; what it heard afterwards must not reopen it.
	case !earlier:
		err = c.appendPhrase(lr, p)
		stored = err == nil
	case n.open && n.meeting.Equal(s.meeting):
		// Into its own meeting's transcript; never the start of one for the new meeting.
		err = c.notes.Phrase(lr.room.Slug(), p)
		stored = err == nil
	}
	c.notesMu.Unlock()
	switch {
	case name == "":
		s.log.Info("phrase of an unknown speaker dropped", "speaker", speaker)
		return
	case err != nil:
		s.log.Error("notes phrase", "err", err)
		return
	case !stored:
		s.log.Info("phrase without open notes dropped")
		return
	}
	if c.notesLive && !earlier {
		c.sendPeople(lr, LivePhrase{Name: name, Start: start, Text: text})
	}
}

// appendPhrase stores p in the transcript of the room's meeting: the first phrase of a meeting
// replaces the room's previous notes, and a phrase after the notes were finished reopens them with
// a resumed mark at the time they were switched on. Called with notesMu held.
func (c *Conference) appendPhrase(lr *liveRoom, p notes.Phrase) error {
	n := &lr.notes
	slug := lr.room.Slug()
	meeting, _ := lr.room.Meeting()
	if meeting.IsZero() {
		return errors.New("no meeting in this room")
	}
	switch {
	case !n.meeting.Equal(meeting):
		if err := c.notes.Begin(slug, lr.room.Name(), meeting); err != nil {
			return err
		}
		n.meeting, n.open = meeting, true
	case !n.open:
		err := c.notes.Resume(slug, n.onAt)
		if errors.Is(err, notes.ErrNotOpen) {
			// The meeting's notes expired or were swept meanwhile: what follows starts afresh.
			err = c.notes.Begin(slug, lr.room.Name(), meeting)
		}
		if err != nil {
			return err
		}
		n.open = true
	}
	return c.notes.Phrase(slug, p)
}

// rememberSpeaker keeps the name of a person who left, for the phrases the bot still sends. Only
// while the room takes notes (wasNotes: it did until this person left) or its transcript is open,
// so a room without notes keeps no names.
func (c *Conference) rememberSpeaker(lr *liveRoom, s *Member, wasNotes bool) {
	if c.notes == nil {
		return
	}
	c.notesMu.Lock()
	if !wasNotes && !lr.notes.open {
		c.notesMu.Unlock()
		return
	}
	if lr.notes.departed == nil {
		lr.notes.departed = map[string]string{}
	}
	if len(lr.notes.departed) < maxDeparted {
		lr.notes.departed[s.id] = s.name
	}
	c.notesMu.Unlock()
}

// sendPeople writes msg to the people in the room, not to the helpers.
func (c *Conference) sendPeople(lr *liveRoom, msg Event) {
	c.mu.Lock()
	members := lr.membersExcept("")
	c.mu.Unlock()
	for _, o := range members {
		if o.bot {
			continue
		}
		if err := o.conn.Send(msg); err != nil {
			o.log.Warn("notes write", "err", err)
		}
	}
}
