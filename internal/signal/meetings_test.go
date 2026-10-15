package signal

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ssubbotin/kunveno/internal/meetings"
)

func TestEndedMeetingIsSaved(t *testing.T) {
	e := newEnv(t, 15)
	store := meetings.Open(filepath.Join(t.TempDir(), "meetings.json"), nil)
	e.conf.SetMeetings(store)
	ca, _ := join(t, e, "Ann")
	cb, _ := join(t, e, "Bob")
	_ = ca.Close(websocket.StatusNormalClosure, "")
	time.Sleep(50 * time.Millisecond)
	if got := store.List("ops"); len(got) != 0 {
		t.Fatalf("saved while Bob is still in the meeting: %+v", got)
	}
	_ = cb.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(5 * time.Second)
	for len(store.List("ops")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := store.List("ops")
	if len(got) != 1 || got[0].People != 2 || got[0].End.Before(got[0].Start) {
		t.Fatalf("saved = %+v; want one meeting of 2 people", got)
	}
}
