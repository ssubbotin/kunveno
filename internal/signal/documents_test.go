package signal

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
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
	Type    string `json:"type"`
	Meeting int64  `json:"meeting"`
	List    []struct {
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
	e.conf.DocumentsChanged("ops", meeting)
}

// liveMeeting is the start of the room's live meeting in Unix ms.
func liveMeeting(t *testing.T, e *env) int64 {
	t.Helper()
	r, _ := e.reg.Get("ops")
	m, _ := r.Meeting()
	return m.UnixMilli()
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
	if m.Meeting != liveMeeting(t, e) || m.Meeting == 0 {
		t.Fatalf("meeting %d, want %d", m.Meeting, liveMeeting(t, e))
	}
	if len(m.List) != 1 || m.List[0].ID != "minutes" || m.List[0].Title != "Протокол" || m.List[0].Rev != 1 || m.List[0].Updated <= 0 {
		t.Fatalf("list = %+v", m.List)
	}
	storeDocument(t, e, store, "minutes", "Протокол встречи")
	if m := expectDocuments(t, ca); len(m.List) != 1 || m.List[0].Rev != 2 || m.List[0].Title != "Протокол встречи" {
		t.Fatalf("replaced = %+v", m.List)
	}
	cb, _ := joinRoom(t, e, "ops", "Bob")
	if m := expectDocuments(t, cb); len(m.List) != 1 || m.List[0].Rev != 2 || m.Meeting != liveMeeting(t, e) {
		t.Fatalf("on join = %+v", m)
	}
	noDocuments(t, bot)
}

// A deleted document of the live meeting gives the room its new list, an empty one included; a
// change to another meeting says nothing.
func TestDocumentsListAfterDelete(t *testing.T) {
	e, store := newDocumentsEnv(t)
	ca, _ := joinRoom(t, e, "ops", "Ann")
	facilitatorOn(t, ca, true)
	joinFacilitator(t, e)
	storeDocument(t, e, store, "minutes", "Протокол")
	storeDocument(t, e, store, "plan", "План")
	for len(expectDocuments(t, ca).List) != 2 {
	}
	m := liveMeeting(t, e)
	if err := store.Delete("ops", m, "minutes"); err != nil {
		t.Fatal(err)
	}
	e.conf.DocumentsChanged("ops", time.UnixMilli(m))
	if got := expectDocuments(t, ca); len(got.List) != 1 || got.List[0].ID != "plan" {
		t.Fatalf("after delete %+v", got.List)
	}
	if err := store.Delete("ops", m, "plan"); err != nil {
		t.Fatal(err)
	}
	e.conf.DocumentsChanged("ops", time.UnixMilli(m))
	if got := expectDocuments(t, ca); len(got.List) != 0 || got.Meeting != m {
		t.Fatalf("after the last delete %+v", got)
	}
	e.conf.DocumentsChanged("ops", time.UnixMilli(m-60000)) // an earlier meeting
	noDocuments(t, ca)
}

// Two uploads at once: the list of the first is built before the second document is stored but
// is slow to go out; the last list the room hears still holds both documents.
func TestConcurrentUploadsEndWithTheFullList(t *testing.T) {
	e, store := newDocumentsEnv(t)
	ca, _ := joinRoom(t, e, "ops", "Ann")
	facilitatorOn(t, ca, true)
	joinFacilitator(t, e)
	meeting, _ := e.conf.DocumentMeeting("ops")
	firstIn := make(chan struct{})
	var calls atomic.Int32
	e.conf.BeforeDocumentsSend = func() {
		if calls.Add(1) == 1 {
			close(firstIn)
			time.Sleep(300 * time.Millisecond) // the second upload runs meanwhile
		}
	}
	upload := func(id string) {
		if _, err := store.Put("ops", meeting.UnixMilli(), id, id, []byte("x")); err != nil {
			t.Error(err)
		}
		e.conf.DocumentsChanged("ops", meeting)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); upload("minutes") }()
	<-firstIn
	go func() { defer wg.Done(); upload("plan") }()
	wg.Wait()
	var last docMsg
	for i := 0; i < 2; i++ {
		last = expectDocuments(t, ca)
	}
	if len(last.List) != 2 {
		t.Fatalf("the last list has %d documents", len(last.List))
	}
	noDocuments(t, ca)
}
