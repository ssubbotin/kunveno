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

// allow reports whether a change at now may be relayed, and counts it when it may.
func (w *stateWindow) allow(now time.Time) bool {
	if oldest := w.sent[w.next]; !oldest.IsZero() && now.Sub(oldest) < time.Second {
		return false
	}
	w.sent[w.next] = now
	w.next = (w.next + 1) % BotStateRate
	return true
}

// SetBotState tells the people of the room what the facilitator s is doing now (listening,
// thinking, speaking or waiting; signal validates the word). A state equal to the current one and
// a change beyond BotStateRate a second are dropped. Members other than a facilitator are ignored.
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
	if state == s.botState || !s.stateWindow.allow(time.Now()) {
		return
	}
	s.botState = state
	c.sendPeople(lr, BotState{ID: s.id, State: state})
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
