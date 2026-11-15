package conference

import (
	"time"

	"github.com/ssubbotin/kunveno/internal/room"
)

// BotStateRate is how many state changes of one facilitator reach the room in any second; the
// rest are dropped.
const BotStateRate = 5

// stateWindow remembers when the last BotStateRate changes were relayed.
type stateWindow struct {
	sent [BotStateRate]time.Time
	next int // the slot of the oldest relay, overwritten by the next one
}

// wait is how long after now the next change may be relayed; zero when it may be now.
func (w *stateWindow) wait(now time.Time) time.Duration {
	if oldest := w.sent[w.next]; !oldest.IsZero() {
		return max(0, oldest.Add(time.Second).Sub(now))
	}
	return 0
}

// allow reports whether a change at now may be relayed, and counts it when it may.
func (w *stateWindow) allow(now time.Time) bool {
	if w.wait(now) > 0 {
		return false
	}
	w.sent[w.next] = now
	w.next = (w.next + 1) % BotStateRate
	return true
}

// SetBotState tells the people of the room what the facilitator s is doing now (listening,
// thinking, speaking or waiting; signal validates the word). A state equal to the one relayed last
// is not relayed. Beyond BotStateRate changes a second the people hear nothing more until the
// window allows again; then they hear the state the bot reported last, if it differs from the one
// they have, so a burst never leaves them with a stale state. Members other than a facilitator are
// ignored.
func (c *Conference) SetBotState(s *Member, state string) {
	if s.botKind != room.BotFacilitator {
		return
	}
	lr, ok := c.lookup(s.slug)
	if !ok {
		return
	}
	lr.botMu.Lock()
	defer lr.botMu.Unlock()
	s.botWanted = state
	c.relayBotStateLocked(lr, s)
}

// relayBotStateLocked relays s.botWanted when it differs from the state relayed last and the rate
// allows it, or arms a timer for when the rate will. Called with lr.botMu held.
func (c *Conference) relayBotStateLocked(lr *liveRoom, s *Member) {
	if s.botWanted == s.botState || s.stateTimer != nil || s.botGone {
		return
	}
	now := time.Now()
	if wait := s.stateWindow.wait(now); wait > 0 {
		s.stateTimer = time.AfterFunc(wait, func() {
			lr.botMu.Lock()
			defer lr.botMu.Unlock()
			s.stateTimer = nil
			c.relayBotStateLocked(lr, s)
		})
		return
	}
	s.stateWindow.allow(now)
	s.botState = s.botWanted
	c.sendPeople(lr, BotState{ID: s.id, State: s.botState})
}

// forgetBotState stops a facilitator's waiting state change once it has left the room.
func (c *Conference) forgetBotState(lr *liveRoom, s *Member) {
	lr.botMu.Lock()
	defer lr.botMu.Unlock()
	s.botGone = true
	if s.stateTimer != nil {
		s.stateTimer.Stop()
		s.stateTimer = nil
	}
}

// SetBotHelp tells the people of the room how to talk to the facilitator s, replacing what it said
// before; the same text again is not relayed. Members other than a facilitator are ignored.
func (c *Conference) SetBotHelp(s *Member, text string) {
	if s.botKind != room.BotFacilitator {
		return
	}
	lr, ok := c.lookup(s.slug)
	if !ok {
		return
	}
	lr.botMu.Lock()
	defer lr.botMu.Unlock()
	if text == s.botHelp {
		return
	}
	s.botHelp = text
	c.sendPeople(lr, BotHelp{ID: s.id, Text: text})
}

// sendBotStatusOnJoin tells a person who just joined the state and the help of each facilitator in
// the room, when it has set them. A facilitator's state and help live on its member, so they are
// gone once it has left.
func (c *Conference) sendBotStatusOnJoin(lr *liveRoom, s *Member) {
	lr.botMu.Lock()
	defer lr.botMu.Unlock()
	var out []Event
	c.mu.Lock()
	for _, o := range lr.members {
		if o.botKind != room.BotFacilitator {
			continue
		}
		if o.botState != "" {
			out = append(out, BotState{ID: o.id, State: o.botState})
		}
		if o.botHelp != "" {
			out = append(out, BotHelp{ID: o.id, Text: o.botHelp})
		}
	}
	c.mu.Unlock() // before any socket write
	for _, e := range out {
		if err := s.conn.Send(e); err != nil {
			s.log.Warn("bot status write", "err", err)
			return
		}
	}
}
