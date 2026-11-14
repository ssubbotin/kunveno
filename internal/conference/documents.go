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

// DocumentsStored tells the people of the room the documents of its meeting that started at
// meeting; the upload handler calls it after storing one. Nothing is sent once another meeting runs.
func (c *Conference) DocumentsStored(slug string, meeting time.Time) {
	if c.documents == nil {
		return
	}
	lr, ok := c.lookup(slug)
	if !ok {
		return
	}
	if current, _ := lr.room.Meeting(); !current.Equal(meeting) {
		return
	}
	list := c.documents.List(slug, meeting.UnixMilli())
	if len(list) == 0 {
		return
	}
	c.log.Info("documents stored", "room", slug, "count", len(list))
	c.sendPeople(lr, Documents{List: list})
}

// documentsFor is the documents event of the room's live meeting for a person who just joined;
// false when there are none or documents are off.
func (c *Conference) documentsFor(lr *liveRoom) (Documents, bool) {
	if c.documents == nil {
		return Documents{}, false
	}
	start, active := lr.room.Meeting()
	if !active {
		return Documents{}, false
	}
	list := c.documents.List(lr.room.Slug(), start.UnixMilli())
	return Documents{List: list}, len(list) > 0
}
