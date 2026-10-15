package conference

import (
	"time"

	"github.com/ssubbotin/kunveno/internal/recording"
	"github.com/ssubbotin/kunveno/internal/room"
)

// DefaultRecordingStopGrace is how long a stopped recorder may take to finish and upload its file and
// leave; encoding is live, so the file is finished seconds after the stop.
const DefaultRecordingStopGrace = 60 * time.Second

// SetRecording turns meeting recording on (recording.enabled): people may switch it on in a room,
// the recorder may join, and the store knows which rooms are recording. Call before serving.
func (c *Conference) SetRecording(store *recording.Store) { c.recording = store }

// SwitchRecording turns the room's recording on or off at a person's request. Helpers cannot switch
// it, and with recording.enabled off the switch does not exist.
func (c *Conference) SwitchRecording(s *Member, on bool) {
	if s.bot || c.recording == nil {
		return
	}
	lr, _ := c.lookup(s.slug)
	c.recMu.Lock()
	was := lr.room.Recording()
	lr.room.SetRecording(on)
	if on {
		c.recording.SetActive(s.slug, true)
	}
	c.recMu.Unlock() // before any socket write
	if on != was {
		s.log.Info("recording", "on", on)
	}
	if was && !on {
		c.stopRecorders(lr)
	}
	c.broadcast(lr)
}

// hasActiveRecorder reports whether a recorder that was not told to stop is in the room.
func (c *Conference) hasActiveRecorder(lr *liveRoom) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, o := range lr.members {
		if o.botKind == room.BotRecorder && !o.stopping {
			return true
		}
	}
	return false
}

// stopRecorders asks the room's recorders to finish; one still there after the grace is closed.
// Without a recorder the room stops counting as recording at once.
func (c *Conference) stopRecorders(lr *liveRoom) {
	c.mu.Lock()
	var stop []*Member
	for _, o := range lr.members {
		if o.botKind != room.BotRecorder || o.stopping {
			continue
		}
		o.stopping = true
		b := o
		b.stopTimer = time.AfterFunc(c.RecordingStopGrace, func() {
			b.log.Warn("recorder did not leave in time; closing it")
			b.conn.Abort("recording off")
		})
		stop = append(stop, b)
	}
	c.mu.Unlock()
	for _, b := range stop {
		b.log.Info("recording stop")
		if err := b.conn.Send(Stop{}); err != nil {
			b.log.Warn("recording stop write", "err", err)
			b.conn.End()
		}
	}
	c.settleRecording(lr)
}

// recorderLeft runs when a recorder's session ends.
func (c *Conference) recorderLeft(lr *liveRoom, s *Member) {
	c.mu.Lock()
	if s.stopTimer != nil {
		s.stopTimer.Stop()
	}
	c.mu.Unlock()
	c.settleRecording(lr)
}

// settleRecording clears the room's recording mark once it no longer wants a recording and no
// recorder is left to upload one.
func (c *Conference) settleRecording(lr *liveRoom) {
	if c.recording == nil {
		return
	}
	c.recMu.Lock()
	defer c.recMu.Unlock()
	if lr.room.Recording() {
		return
	}
	c.mu.Lock()
	for _, o := range lr.members {
		if o.botKind == room.BotRecorder {
			c.mu.Unlock()
			return
		}
	}
	c.mu.Unlock()
	c.recording.SetActive(lr.room.Slug(), false)
}

// RecordingStored tells the people of a room that its recording can be downloaded; the upload
// handler calls it after storing the file.
func (c *Conference) RecordingStored(slug string) {
	lr, ok := c.lookup(slug) // creates the live entry only for a registered room
	if !ok {
		return
	}
	c.log.Info("recording ready", "room", slug)
	c.sendPeople(lr, RecordingReady{})
}
