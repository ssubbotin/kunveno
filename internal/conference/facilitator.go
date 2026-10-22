package conference

import (
	"time"

	"github.com/ssubbotin/kunveno/internal/room"
)

// DefaultFacilitatorStopGrace is how long a stopped facilitator may take to stop speaking and leave.
const DefaultFacilitatorStopGrace = 5 * time.Second

// SetFacilitator turns the meeting facilitator on (facilitator.enabled): in the rooms offered
// reports, people may switch it on and the facilitator bot may join. Call before serving. Without
// it nothing about the facilitator is offered and the facilitator role is refused.
func (c *Conference) SetFacilitator(offered func(slug string) bool) { c.facilitatorOffered = offered }

// offersFacilitator reports whether the facilitator switch exists in the room slug.
func (c *Conference) offersFacilitator(slug string) bool {
	return c.facilitatorOffered != nil && c.facilitatorOffered(slug)
}

// SwitchFacilitator turns the room's facilitator on or off at a person's request. Helpers and
// guests cannot switch it, and in a room that does not offer it the switch does not exist.
func (c *Conference) SwitchFacilitator(s *Member, on bool) {
	if s.bot || s.guest || !c.offersFacilitator(s.slug) {
		return
	}
	lr, _ := c.lookup(s.slug)
	// Under c.mu, so a facilitator's join sees the flag and its own membership change together.
	c.mu.Lock()
	was := lr.room.Facilitator()
	lr.room.SetFacilitator(on)
	c.mu.Unlock() // before any socket write
	if on != was {
		s.log.Info("facilitator", "on", on)
	}
	if was && !on {
		c.stopFacilitators(lr)
	}
	c.broadcast(lr)
}

// facilitatorWanted reports whether a facilitator may join the room: it is offered there, switched
// on, and no other facilitator that was not told to stop is in it.
func (c *Conference) facilitatorWanted(lr *liveRoom) bool {
	if !c.offersFacilitator(lr.room.Slug()) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return lr.room.Facilitator() && !lr.hasActiveFacilitatorLocked()
}

// hasActiveFacilitatorLocked reports whether a facilitator that was not told to stop is in the
// room. Called with c.mu held.
func (lr *liveRoom) hasActiveFacilitatorLocked() bool {
	for _, o := range lr.members {
		if o.botKind == room.BotFacilitator && !o.stopping {
			return true
		}
	}
	return false
}

// stopFacilitators asks the room's facilitators to stop speaking and leave; one still there after
// the grace is closed.
func (c *Conference) stopFacilitators(lr *liveRoom) {
	c.mu.Lock()
	var stop []*Member
	for _, o := range lr.members {
		if o.botKind != room.BotFacilitator || o.stopping {
			continue
		}
		o.stopping = true
		b := o
		b.stopTimer = time.AfterFunc(c.FacilitatorStopGrace, func() {
			b.log.Warn("facilitator did not leave in time; closing it")
			b.conn.Abort("facilitator off") // Close would wait for the bot's answer
		})
		stop = append(stop, b)
	}
	c.mu.Unlock()
	for _, b := range stop {
		b.log.Info("facilitator stop")
		if err := b.conn.Send(Stop{}); err != nil {
			b.log.Warn("facilitator stop write", "err", err)
			b.conn.End()
		}
	}
}

// facilitatorLeft runs when a facilitator's session ends.
func (c *Conference) facilitatorLeft(s *Member) {
	c.mu.Lock()
	if s.stopTimer != nil {
		s.stopTimer.Stop()
	}
	c.mu.Unlock()
}
