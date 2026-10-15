package signal

import (
	"time"

	"github.com/coder/websocket"

	"github.com/ssubbotin/kunveno/internal/recording"
)

// RecordingStopGrace is how long a stopped recorder may take to finish and upload its file and
// leave; encoding is live, so the file is finished seconds after the stop.
const RecordingStopGrace = 60 * time.Second

// SetRecording turns meeting recording on (recording.enabled): people may switch it on in a room,
// the recorder may join, and the store knows which rooms are recording. Call before serving.
func (h *Handler) SetRecording(store *recording.Store) { h.recording = store }

// switchRecording turns the room's recording on or off at a person's request.
func (h *Handler) switchRecording(s *session, on bool) {
	lr, _ := h.lookup(s.slug)
	h.recMu.Lock()
	was := lr.room.Recording()
	lr.room.SetRecording(on)
	if on {
		h.recording.SetActive(s.slug, true)
	}
	h.recMu.Unlock() // before any socket write
	if on != was {
		s.log.Info("recording", "on", on)
	}
	if was && !on {
		h.stopRecorders(lr)
	}
	h.broadcast(lr)
}

// hasActiveRecorder reports whether a recorder that was not told to stop is in the room.
func (h *Handler) hasActiveRecorder(lr *liveRoom) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, o := range lr.members {
		if o.botKind == RoleRecorder && !o.stopping {
			return true
		}
	}
	return false
}

// stopRecorders asks the room's recorders to finish; one still there after the grace is closed.
// Without a recorder the room stops counting as recording at once.
func (h *Handler) stopRecorders(lr *liveRoom) {
	h.mu.Lock()
	var stop []*session
	for _, o := range lr.members {
		if o.botKind != RoleRecorder || o.stopping {
			continue
		}
		o.stopping = true
		b := o
		b.stopTimer = time.AfterFunc(h.recStopGrace, func() {
			b.log.Warn("recorder did not leave in time; closing it")
			b.cancel()
			_ = b.conn.Close(websocket.StatusNormalClosure, "recording off")
		})
		stop = append(stop, b)
	}
	h.mu.Unlock()
	for _, b := range stop {
		b.log.Info("recording stop")
		if err := b.write(Stop()); err != nil {
			b.log.Warn("recording stop write", "err", err)
			b.cancel()
		}
	}
	h.settleRecording(lr)
}

// recorderLeft runs when a recorder's session ends.
func (h *Handler) recorderLeft(lr *liveRoom, s *session) {
	h.mu.Lock()
	if s.stopTimer != nil {
		s.stopTimer.Stop()
	}
	h.mu.Unlock()
	h.settleRecording(lr)
}

// settleRecording clears the room's recording mark once it no longer wants a recording and no
// recorder is left to upload one.
func (h *Handler) settleRecording(lr *liveRoom) {
	if h.recording == nil {
		return
	}
	h.recMu.Lock()
	defer h.recMu.Unlock()
	if lr.room.Recording() {
		return
	}
	h.mu.Lock()
	for _, o := range lr.members {
		if o.botKind == RoleRecorder {
			h.mu.Unlock()
			return
		}
	}
	h.mu.Unlock()
	h.recording.SetActive(lr.room.Slug(), false)
}

// RecordingStored tells the people of a room that its recording can be downloaded; the upload
// handler calls it after storing the file.
func (h *Handler) RecordingStored(slug string) {
	lr, ok := h.lookup(slug) // creates the live entry only for a registered room
	if !ok {
		return
	}
	h.log.Info("recording ready", "room", slug)
	h.sendPeople(lr, RecordingReady())
}
