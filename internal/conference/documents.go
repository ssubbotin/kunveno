package conference

import (
	"time"

	"github.com/ssubbotin/kunveno/internal/documents"
	"github.com/ssubbotin/kunveno/internal/room"
)

// SetDocuments turns meeting documents on (documents.enabled): the facilitator may store them for
// the room's live meeting and people hear the list. Call before serving. Without it no documents
// message is ever sent.
func (c *Conference) SetDocuments(store *documents.Store) { c.documents = store }

// DocumentMeeting returns the start of the room's live meeting while a facilitator is in it: the
// meeting a document uploaded now belongs to. False with documents off, in a room without people
// or without a facilitator.
func (c *Conference) DocumentMeeting(slug string) (time.Time, bool) {
	if c.documents == nil {
		return time.Time{}, false
	}
	lr, ok := c.lookup(slug)
	if !ok {
		return time.Time{}, false
	}
	start, active := lr.room.Meeting()
	if !active || start.IsZero() {
		return time.Time{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, o := range lr.members {
		if o.botKind == room.BotFacilitator {
			return start, true
		}
	}
	return time.Time{}, false
}

// DocumentsChanged tells the people of the room the documents of its meeting that started at
// meeting, an empty list included; the upload, the delete and the expiry sweep call it after a
// change. Nothing is sent when that meeting is not the one the room runs now.
func (c *Conference) DocumentsChanged(slug string, meeting time.Time) {
	if c.documents == nil {
		return
	}
	lr, ok := c.lookup(slug)
	if !ok {
		return
	}
	lr.docsMu.Lock()
	defer lr.docsMu.Unlock()
	current, active := lr.room.Meeting()
	if !active || current.UnixMilli() != meeting.UnixMilli() {
		return
	}
	list := c.documents.List(slug, current.UnixMilli())
	if c.BeforeDocumentsSend != nil {
		c.BeforeDocumentsSend()
	}
	c.log.Info("documents changed", "room", slug, "count", len(list))
	c.sendPeople(lr, Documents{Meeting: current, List: list})
}

// sendDocumentsOnJoin tells a person who just joined the documents of the room's live meeting,
// when there are any.
func (c *Conference) sendDocumentsOnJoin(lr *liveRoom, s *Member) {
	if c.documents == nil {
		return
	}
	lr.docsMu.Lock()
	defer lr.docsMu.Unlock()
	start, active := lr.room.Meeting()
	if !active {
		return
	}
	list := c.documents.List(lr.room.Slug(), start.UnixMilli())
	if len(list) == 0 {
		return
	}
	if err := s.conn.Send(Documents{Meeting: start, List: list}); err != nil {
		s.log.Warn("documents write", "err", err)
	}
}
