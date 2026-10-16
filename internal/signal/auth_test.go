package signal

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ssubbotin/kunveno/internal/auth"
	"github.com/ssubbotin/kunveno/internal/metrics"
	"github.com/ssubbotin/kunveno/internal/notes"
)

// The real sign-in satisfies Access.
var _ Access = (*auth.Gate)(nil)

// fakeAccess: the cookie "ann" is a session of Ann, "ann-old" her session after it expired, "bob"
// a session of Bob; the invitation "tok-ops" belongs to room ops.
type fakeAccess struct {
	mu      sync.Mutex
	revoked bool
	expired bool
}

func (a *fakeAccess) revoke() {
	a.mu.Lock()
	a.revoked = true
	a.mu.Unlock()
}

func (a *fakeAccess) expire() {
	a.mu.Lock()
	a.expired = true
	a.mu.Unlock()
}

var ann = auth.Identity{Provider: "yandex", ID: "1", Login: "ann@example.com", Name: "Анна Иванова"}

func (a *fakeAccess) Session(r *http.Request) (auth.Identity, bool) {
	c, err := r.Cookie(auth.SessionCookie)
	switch {
	case err != nil:
	case c.Value == "ann":
		return ann, true
	case c.Value == "bob":
		return auth.Identity{Provider: "yandex", ID: "2", Login: "bob@example.com", Name: "Борис"}, true
	}
	return auth.Identity{}, false
}

func (a *fakeAccess) ExpiredSession(r *http.Request) (auth.Identity, bool) {
	if c, err := r.Cookie(auth.SessionCookie); err == nil && c.Value == "ann-old" {
		return ann, true
	}
	return auth.Identity{}, false
}

func (a *fakeAccess) CheckInvite(room, token string) (auth.Invite, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if token == "tok-ops" && room == "ops" && !a.revoked {
		inv := auth.Invite{ID: "inv1", Room: "ops"}
		if a.expired {
			return inv, auth.ErrExpiredInvite
		}
		return inv, nil
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

// counterReaches waits for c to reach want: joins are counted after "joined" has gone out.
func counterReaches(c prometheus.Collector, want float64) bool {
	for i := 0; i < 100; i++ {
		if testutil.ToFloat64(c) == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestOnlyAdmittedGuestsAreCounted(t *testing.T) {
	e, _ := newAuthEnv(t)
	before := testutil.ToFloat64(metrics.GuestJoins)
	bad := dialAs(t, e, "")
	send(t, bad, `{"type":"join","room":"ops","invite":"nope","name":"Гость"}`)
	expect(t, bad, "error")
	ann := dialAs(t, e, "ann")
	send(t, ann, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	expect(t, ann, "joined")
	g := dialAs(t, e, "")
	send(t, g, `{"type":"join","room":"ops","invite":"tok-ops","name":"Гость"}`)
	expect(t, g, "joined")
	if !counterReaches(metrics.GuestJoins, before+1) {
		t.Fatalf("guest joins %v, want %v", testutil.ToFloat64(metrics.GuestJoins), before+1)
	}
	time.Sleep(50 * time.Millisecond)
	if got := testutil.ToFloat64(metrics.GuestJoins); got != before+1 {
		t.Fatalf("guest joins %v, want %v", got, before+1)
	}
}

// leaveAndWait sends leave on c and waits until the room has want members.
func leaveAndWait(t *testing.T, e *env, c *websocket.Conn, want int) {
	t.Helper()
	send(t, c, `{"type":"leave"}`)
	waitFor(t, func() bool { return e.conf.Members("ops") == want })
}

// A guest whose link expired during the meeting comes back after a reconnect (every reconnect is
// a fresh join) while the meeting runs; a new guest with that link does not get in.
func TestGuestRejoinsAfterTheLinkExpiredWhileTheMeetingRuns(t *testing.T) {
	e, a := newAuthEnv(t)
	host := dialAs(t, e, "bob")
	send(t, host, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	expect(t, host, "joined")
	g := dialAs(t, e, "")
	send(t, g, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	expect(t, g, "joined")
	a.expire()
	leaveAndWait(t, e, g, 1)
	g2 := dialAs(t, e, "")
	send(t, g2, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	expect(t, g2, "joined")
}

func TestExpiredLinkAfterTheMeetingEndedIsBad(t *testing.T) {
	e, a := newAuthEnv(t)
	e.conf.RejoinGrace = 0
	host := dialAs(t, e, "bob")
	send(t, host, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	expect(t, host, "joined")
	g := dialAs(t, e, "")
	send(t, g, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	expect(t, g, "joined")
	a.expire()
	leaveAndWait(t, e, g, 1)
	leaveAndWait(t, e, host, 0) // the meeting ends
	time.Sleep(10 * time.Millisecond)
	g2 := dialAs(t, e, "")
	send(t, g2, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	if m := expect(t, g2, "error"); m.Code != CodeBadInvite {
		t.Fatalf("code %q", m.Code)
	}
}

// An expired link that was never used in this meeting admits nobody.
func TestExpiredLinkNeverUsedInTheMeetingIsBad(t *testing.T) {
	e, a := newAuthEnv(t)
	host := dialAs(t, e, "bob")
	send(t, host, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	expect(t, host, "joined")
	a.expire()
	g := dialAs(t, e, "")
	send(t, g, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	if m := expect(t, g, "error"); m.Code != CodeBadInvite {
		t.Fatalf("code %q", m.Code)
	}
}

// Everyone dropped at once (a network blip): the guests come back within the grace.
func TestGuestRejoinsWithinTheGraceAfterEveryoneDropped(t *testing.T) {
	e, a := newAuthEnv(t)
	g := dialAs(t, e, "")
	send(t, g, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	expect(t, g, "joined")
	a.expire()
	leaveAndWait(t, e, g, 0)
	g2 := dialAs(t, e, "")
	send(t, g2, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	expect(t, g2, "joined")
}

func TestRevokedLinkNeverComesBack(t *testing.T) {
	e, a := newAuthEnv(t)
	host := dialAs(t, e, "bob")
	send(t, host, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	expect(t, host, "joined")
	g := dialAs(t, e, "")
	send(t, g, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	expect(t, g, "joined")
	a.expire()
	a.revoke()
	e.conf.DropInvite("inv1")
	expectClosed(t, g)
	g2 := dialAs(t, e, "")
	send(t, g2, `{"type":"join","room":"ops","invite":"tok-ops","name":"G"}`)
	if m := expect(t, g2, "error"); m.Code != CodeBadInvite {
		t.Fatalf("code %q", m.Code)
	}
}

// An employee whose session expired during the meeting comes back to it; after it ended the
// sign-in screen follows.
func TestEmployeeRejoinsWithAnExpiredSessionWhileTheMeetingRuns(t *testing.T) {
	e, _ := newAuthEnv(t)
	e.conf.RejoinGrace = 0
	host := dialAs(t, e, "bob")
	send(t, host, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	expect(t, host, "joined")
	c := dialAs(t, e, "ann")
	send(t, c, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	expect(t, c, "joined")
	leaveAndWait(t, e, c, 1)
	c2 := dialAs(t, e, "ann-old")
	send(t, c2, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	if m := expect(t, c2, "joined"); m.Self.Name != "Анна Иванова" {
		t.Fatalf("name %q", m.Self.Name)
	}
	leaveAndWait(t, e, c2, 1)
	leaveAndWait(t, e, host, 0) // the meeting ends
	time.Sleep(10 * time.Millisecond)
	c3 := dialAs(t, e, "ann-old")
	send(t, c3, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	if m := expect(t, c3, "error"); m.Code != CodeAuthRequired {
		t.Fatalf("code %q", m.Code)
	}
}

// An expired session of someone who was not in this meeting is no way in.
func TestExpiredSessionNotInTheMeetingIsRefused(t *testing.T) {
	e, _ := newAuthEnv(t)
	host := dialAs(t, e, "bob")
	send(t, host, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	expect(t, host, "joined")
	c := dialAs(t, e, "ann-old")
	send(t, c, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	if m := expect(t, c, "error"); m.Code != CodeAuthRequired {
		t.Fatalf("code %q", m.Code)
	}
}
