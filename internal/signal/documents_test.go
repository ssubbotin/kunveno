package signal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ssubbotin/kunveno/internal/documents"
)

func newDocumentsEnv(t *testing.T) (*env, *documents.Store) {
	t.Helper()
	e := newFacilitatorEnv(t)
	store, err := documents.Open(t.TempDir(), 24*time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.conf.SetDocuments(store)
	return e, store
}

type docMsg struct {
	Type string `json:"type"`
	List []struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Updated int64  `json:"updated"`
		Rev     int    `json:"rev"`
	} `json:"list"`
}

// expectDocuments reads frames until a documents message arrives and returns it.
func expectDocuments(t *testing.T, c *websocket.Conn) docMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for documents: %v", err)
		}
		var m docMsg
		if json.Unmarshal(data, &m) == nil && m.Type == "documents" {
			return m
		}
	}
}

// noDocuments reads frames for a short while and fails on a documents message.
func noDocuments(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if strings.Contains(string(data), `"documents"`) {
			t.Fatalf("unexpected documents message: %s", data)
		}
	}
}

// storeDocument stores a document for the room's live meeting, as the upload handler does.
func storeDocument(t *testing.T, e *env, store *documents.Store, id, title string) {
	t.Helper()
	meeting, ok := e.conf.DocumentMeeting("ops")
	if !ok {
		t.Fatal("no meeting with a facilitator")
	}
	if _, err := store.Put("ops", meeting.UnixMilli(), id, title, []byte("# "+title)); err != nil {
		t.Fatal(err)
	}
	e.conf.DocumentsStored("ops", meeting)
}

// A document is accepted only while a facilitator is in the room's live meeting.
func TestDocumentMeetingNeedsAFacilitator(t *testing.T) {
	e, _ := newDocumentsEnv(t)
	if _, ok := e.conf.DocumentMeeting("ops"); ok {
		t.Fatal("empty room accepts documents")
	}
	ca, _ := joinRoom(t, e, "ops", "Ann")
	if _, ok := e.conf.DocumentMeeting("ops"); ok {
		t.Fatal("a room without the facilitator accepts documents")
	}
	facilitatorOn(t, ca, true)
	bot, _ := joinFacilitator(t, e)
	start, ok := e.conf.DocumentMeeting("ops")
	if r, _ := e.reg.Get("ops"); !ok || func() bool { m, _ := r.Meeting(); return !m.Equal(start) }() {
		t.Fatalf("meeting %v %v", start, ok)
	}
	send(t, bot, `{"type":"leave"}`)
	waitFor(t, func() bool { _, ok := e.conf.DocumentMeeting("ops"); return !ok })
}

// People hear the list when a document is stored or replaced and on join; bots never do.
func TestDocumentsMessageToPeople(t *testing.T) {
	e, store := newDocumentsEnv(t)
	ca, _ := joinRoom(t, e, "ops", "Ann")
	facilitatorOn(t, ca, true)
	bot, _ := joinFacilitator(t, e)
	storeDocument(t, e, store, "minutes", "Протокол")
	m := expectDocuments(t, ca)
	if len(m.List) != 1 || m.List[0].ID != "minutes" || m.List[0].Title != "Протокол" || m.List[0].Rev != 1 || m.List[0].Updated <= 0 {
		t.Fatalf("list = %+v", m.List)
	}
	storeDocument(t, e, store, "minutes", "Протокол встречи")
	if m := expectDocuments(t, ca); len(m.List) != 1 || m.List[0].Rev != 2 || m.List[0].Title != "Протокол встречи" {
		t.Fatalf("replaced = %+v", m.List)
	}
	cb, _ := joinRoom(t, e, "ops", "Bob")
	if m := expectDocuments(t, cb); len(m.List) != 1 || m.List[0].Rev != 2 {
		t.Fatalf("on join = %+v", m.List)
	}
	noDocuments(t, bot)
}

// Without documents nothing is said: not on join, not after a facilitator came.
func TestNoDocumentsMessageWhenEmptyOrOff(t *testing.T) {
	e, _ := newDocumentsEnv(t)
	ca, _ := joinRoom(t, e, "ops", "Ann")
	noDocuments(t, ca)
	off := newFacilitatorEnv(t) // documents.enabled off: SetDocuments never called
	cb, _ := joinRoom(t, off, "ops", "Bob")
	facilitatorOn(t, cb, true)
	joinFacilitator(t, off)
	if _, ok := off.conf.DocumentMeeting("ops"); ok {
		t.Fatal("documents accepted while off")
	}
	noDocuments(t, cb)
}
