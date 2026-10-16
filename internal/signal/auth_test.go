package signal

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ssubbotin/kunveno/internal/auth"
	"github.com/ssubbotin/kunveno/internal/notes"
)

// The real sign-in satisfies Access.
var _ Access = (*auth.Gate)(nil)

// fakeAccess: the cookie "ann" is a session of Ann; the invitation "tok-ops" belongs to room ops.
type fakeAccess struct {
	mu      sync.Mutex
	revoked bool
}

func (a *fakeAccess) revoke() {
	a.mu.Lock()
	a.revoked = true
	a.mu.Unlock()
}

func (a *fakeAccess) Session(r *http.Request) (auth.Identity, bool) {
	if c, err := r.Cookie(auth.SessionCookie); err == nil && c.Value == "ann" {
		return auth.Identity{Provider: "yandex", ID: "1", Login: "ann@example.com", Name: "Анна Иванова"}, true
	}
	return auth.Identity{}, false
}

func (a *fakeAccess) CheckInvite(room, token string) (auth.Invite, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if token == "tok-ops" && room == "ops" && !a.revoked {
		return auth.Invite{ID: "inv1", Room: "ops"}, nil
	}
	return auth.Invite{}, auth.ErrBadInvite
}

func newAuthEnv(t *testing.T) (*env, *fakeAccess) {
	e := newEnv(t, 15)
	a := &fakeAccess{}
	e.h.SetAccess(a)
	return e, a
}

// dialAs opens a socket with the session cookie value (empty: none).
func dialAs(t *testing.T, e *env, cookie string) *websocket.Conn {
	t.Helper()
	h := http.Header{}
	if cookie != "" {
		h.Set("Cookie", auth.SessionCookie+"="+cookie)
	}
	return dialWith(t, e, h)
}

func TestAuthOnJoinWithoutSessionIsRefused(t *testing.T) {
	e, _ := newAuthEnv(t)
	c := dialAs(t, e, "")
	send(t, c, `{"type":"join","room":"ops","pin":"1234","name":"Ann"}`)
	if m := expect(t, c, "error"); m.Code != CodeAuthRequired {
		t.Fatalf("code %q", m.Code)
	}
}

func TestEmployeeNameComesFromTheAccountAndThePINIsStillChecked(t *testing.T) {
	e, _ := newAuthEnv(t)
	bad := dialAs(t, e, "ann")
	send(t, bad, `{"type":"join","room":"ops","pin":"0000","name":"Boss"}`)
	if m := expect(t, bad, "error"); m.Code != CodeBadPIN {
		t.Fatalf("wrong PIN with a session: %q", m.Code)
	}
	c := dialAs(t, e, "ann")
	send(t, c, `{"type":"join","room":"ops","pin":"1234","name":"Boss"}`)
	if m := expect(t, c, "joined"); m.Self.Name != "Анна Иванова" {
		t.Fatalf("name %q", m.Self.Name)
	}
}

func TestGuestJoinsByInvitationWithoutPINAndCannotSwitch(t *testing.T) {
	e, _ := newRecordingEnv(t) // recording and notes enabled, so a refused switch is the guest check
	store, err := notes.Open(t.TempDir(), 24*time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.conf.SetNotes(store, false)
	e.h.SetAccess(&fakeAccess{})
	ann := dialAs(t, e, "ann")
	send(t, ann, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	expect(t, ann, "joined")
	g := dialAs(t, e, "")
	send(t, g, `{"type":"join","room":"ops","invite":"tok-ops","name":"Гость Петров"}`)
	if m := expect(t, g, "joined"); m.Self.Name != "Гость Петров" {
		t.Fatalf("guest name %q", m.Self.Name)
	}
	for {
		m := expect(t, ann, "participants")
		if len(m.Participants) == 2 {
			guest := m.Participants[0]
			if guest.Name != "Гость Петров" {
				guest = m.Participants[1]
			}
			if !guest.Guest {
				t.Fatalf("guest not marked: %+v", guest)
			}
			break
		}
	}
	send(t, g, `{"type":"recording","on":true}`)
	send(t, g, `{"type":"notes","on":true}`)
	send(t, g, `{"type":"echo","on":true}`)
	time.Sleep(100 * time.Millisecond)
	r, _ := e.reg.Get("ops")
	if r.Recording() || r.Notes() || r.Echo() {
		t.Fatal("a guest switched recording, notes or the echo on")
	}
	send(t, ann, `{"type":"echo","on":true}`) // the employee still can
	for !expect(t, ann, "participants").Echo {
	}
}

func TestBadInvitationIsRefusedAndCounted(t *testing.T) {
	e, _ := newAuthEnv(t)
	for i := 0; i < 5; i++ {
		g := dialAs(t, e, "")
		send(t, g, `{"type":"join","room":"ops","invite":"guess`+string(rune('a'+i))+`","name":"G"}`)
		if m := expect(t, g, "error"); m.Code != CodeBadInvite {
			t.Fatalf("code %q", m.Code)
		}
	}
	g := dialAs(t, e, "")
	send(t, g, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	if m := expect(t, g, "error"); m.Code != CodeTooManyAttempts || m.RetryAfter < 1 {
		t.Fatalf("after five bad invitations: %+v", m)
	}
	if strings.Contains(e.logs.String(), "guessa") {
		t.Fatal("an invitation token reached the log")
	}
}

func TestRevokedInvitationDisconnectsItsGuests(t *testing.T) {
	e, a := newAuthEnv(t)
	g := dialAs(t, e, "")
	send(t, g, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	expect(t, g, "joined")
	a.revoke()
	if n := e.conf.DropInvite("inv1"); n != 1 {
		t.Fatalf("dropped %d", n)
	}
	expectClosed(t, g)
	g2 := dialAs(t, e, "")
	send(t, g2, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	if m := expect(t, g2, "error"); m.Code != CodeBadInvite {
		t.Fatalf("revoked link: %q", m.Code)
	}
}

func TestAuthOffIgnoresInvitesAndNeedsNoSession(t *testing.T) {
	e := newEnv(t, 15) // no SetAccess: sign-in off
	c := dial(t, e)
	send(t, c, `{"type":"join","room":"ops","pin":"1234","name":"Ann"}`)
	if m := expect(t, c, "joined"); m.Self.Name != "Ann" {
		t.Fatalf("name %q", m.Self.Name)
	}
	g := dial(t, e)
	send(t, g, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	if m := expect(t, g, "error"); m.Code != CodeBadMessage {
		t.Fatalf("an invitation without sign-in: %q", m.Code)
	}
}

// Helpers join over loopback with a role and no session, with sign-in on as without it.
func TestAuthOnHelpersJoinWithoutSession(t *testing.T) {
	e, _ := newAuthEnv(t)
	ann := dialAs(t, e, "ann")
	send(t, ann, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	expect(t, ann, "joined")
	send(t, ann, `{"type":"echo","on":true}`)
	for !expect(t, ann, "participants").Echo {
	}
	bot := dialAs(t, e, "")
	send(t, bot, `{"type":"join","room":"ops","name":"Echo","role":"echo"}`)
	expect(t, bot, "joined")
}

// A guest whose join is still in progress when its invitation is revoked never ends up in the
// room: the revocation found nobody to disconnect, so the join itself must refuse it.
func TestInvitationRevokedDuringTheGuestsJoinRefusesIt(t *testing.T) {
	e, a := newAuthEnv(t)
	entered, release := make(chan struct{}), make(chan struct{})
	e.media.setHold(func() {
		entered <- struct{}{}
		<-release
	})
	g := dialAs(t, e, "")
	send(t, g, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	select {
	case <-entered: // the invitation was checked; the guest is inside media setup
	case <-time.After(5 * time.Second):
		t.Fatal("the guest's join never reached media setup")
	}
	e.media.setHold(nil)
	a.revoke()
	if n := e.conf.DropInvite("inv1"); n != 0 {
		t.Fatalf("dropped %d before the guest was a member", n)
	}
	close(release)
	if m := expect(t, g, "error"); m.Code != CodeBadInvite {
		t.Fatalf("code %q", m.Code)
	}
	expectClosed(t, g)
	r, _ := e.reg.Get("ops")
	if n := len(r.Snapshot().Participants); n != 0 || e.conf.Members("ops") != 0 {
		t.Fatalf("left behind: %d participants, %d members", n, e.conf.Members("ops"))
	}
}
