package signal

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ssubbotin/kunveno/internal/media"
	"github.com/ssubbotin/kunveno/internal/recording"
	"github.com/ssubbotin/kunveno/internal/room"
)

// newFacilitatorEnv offers the facilitator in ops only; the room other has the switch hidden.
func newFacilitatorEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, 15)
	e.reg.Add(room.New("other", "Other", "1234", 15))
	e.conf.SetFacilitator(func(slug string) bool { return slug == "ops" })
	e.conf.FacilitatorStopGrace = 300 * time.Millisecond
	return e
}

// joinRoom joins a person to slug and returns the socket and the raw joined frame.
func joinRoom(t *testing.T, e *env, slug, name string) (*websocket.Conn, string) {
	t.Helper()
	c := dial(t, e)
	send(t, c, `{"type":"join","room":"`+slug+`","pin":"1234","name":"`+name+`"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for joined: %v", err)
		}
		var m anyMsg
		_ = json.Unmarshal(data, &m)
		if m.Type == "joined" {
			return c, string(data)
		}
	}
}

// facilitatorOn switches the facilitator and waits for the snapshot that shows it.
func facilitatorOn(t *testing.T, c *websocket.Conn, on bool) {
	t.Helper()
	if on {
		send(t, c, `{"type":"facilitator","on":true}`)
	} else {
		send(t, c, `{"type":"facilitator","on":false}`)
	}
	for expect(t, c, "participants").Facilitator != on {
	}
}

const facilitatorJoin = `{"type":"join","room":"ops","name":"Ведущий","role":"facilitator"}`

func joinFacilitator(t *testing.T, e *env) (*websocket.Conn, string) {
	t.Helper()
	bot := dial(t, e)
	send(t, bot, facilitatorJoin)
	return bot, expect(t, bot, "joined").Self.ID
}

func expectRefused(t *testing.T, e *env, header http.Header, code string) {
	t.Helper()
	c := dialWith(t, e, header)
	send(t, c, facilitatorJoin)
	if m := expect(t, c, "error"); m.Code != code {
		t.Fatalf("code %q, want %q", m.Code, code)
	}
}

func TestFacilitatorSwitchOnlyInOfferedRooms(t *testing.T) {
	e := newFacilitatorEnv(t)
	ca, joined := joinRoom(t, e, "ops", "Ann")
	if !strings.Contains(joined, `"facilitator":true`) {
		t.Fatalf("ops joined without the switch: %s", joined)
	}
	expectRefused(t, e, nil, CodeBadRoom) // offered, but nobody switched it on
	facilitatorOn(t, ca, true)
	if got := e.reg.FacilitatorRooms(); len(got) != 1 || got[0] != "ops" {
		t.Fatalf("rooms %v", got)
	}
	co, joined := joinRoom(t, e, "other", "Bob")
	if strings.Contains(joined, `"facilitator"`) {
		t.Fatalf("other joined with the switch: %s", joined)
	}
	send(t, co, `{"type":"facilitator","on":true}`)
	send(t, co, `{"type":"mute","muted":true}`)
	if m := expect(t, co, "participants"); m.Facilitator {
		t.Fatal("facilitator switched on in a room that does not offer it")
	}
	if r, _ := e.reg.Get("other"); r.Facilitator() {
		t.Fatal("other room wants the facilitator")
	}
}

func TestFacilitatorDisabledIgnoresTheSwitchAndTheRole(t *testing.T) {
	e := newEnv(t, 15)
	ca, joined := joinRoom(t, e, "ops", "Ann")
	if strings.Contains(joined, `"facilitator"`) {
		t.Fatalf("joined offers the switch while disabled: %s", joined)
	}
	send(t, ca, `{"type":"facilitator","on":true}`)
	send(t, ca, `{"type":"mute","muted":true}`)
	if m := expect(t, ca, "participants"); m.Facilitator {
		t.Fatal("facilitator switched on while disabled")
	}
	expectRefused(t, e, nil, CodeBadRoom)
}

func TestFacilitatorRoleIsLocalOnly(t *testing.T) {
	e := newFacilitatorEnv(t)
	ca, _ := join(t, e, "Ann")
	facilitatorOn(t, ca, true)
	expectRefused(t, e, http.Header{"X-Forwarded-For": {"10.8.0.5"}}, CodeBadMessage)
}

// The facilitator hears people's audio only; people hear the facilitator.
func TestFacilitatorReceivesPeoplesAudioOnly(t *testing.T) {
	e := newFacilitatorEnv(t)
	ca, ida := join(t, e, "Ann")
	pa := e.media.peer(ida)
	pa.setTrack(t, media.KindAudio)
	pa.setTrack(t, media.KindCamera)
	send(t, ca, `{"type":"share","action":"start"}`)
	for expect(t, ca, "participants").Sharer != ida {
	}
	pa.setTrack(t, media.KindScreen)
	pa.publish(media.KindScreen)
	r, _ := e.reg.Get("ops")
	r.SetPlayer(true)
	player := dial(t, e)
	send(t, player, `{"type":"join","room":"ops","name":"Видео","role":"player"}`)
	pid := expect(t, player, "joined").Self.ID
	e.media.peer(pid).setTrack(t, media.KindAudio)
	e.media.peer(pid).setTrack(t, media.KindCamera)
	facilitatorOn(t, ca, true)
	bot, fid := joinFacilitator(t, e)
	fp := e.media.peer(fid)
	if fp.subCount(ida, media.KindAudio) != 1 || fp.subCount(ida, media.KindCamera) != 0 || fp.subCount(ida, media.KindScreen) != 0 ||
		fp.subCount(pid, media.KindAudio) != 0 || fp.subCount(pid, media.KindCamera) != 0 || fp.subCount(fid, media.KindAudio) != 0 {
		t.Fatalf("facilitator subscriptions: ann audio %d camera %d screen %d, player audio %d camera %d, self %d",
			fp.subCount(ida, media.KindAudio), fp.subCount(ida, media.KindCamera), fp.subCount(ida, media.KindScreen),
			fp.subCount(pid, media.KindAudio), fp.subCount(pid, media.KindCamera), fp.subCount(fid, media.KindAudio))
	}
	// Asking for cameras changes nothing.
	send(t, bot, `{"type":"video","receive":true}`)
	// A person joining later: audio reaches the facilitator, the camera does not.
	_, idb := join(t, e, "Bob")
	pb := e.media.peer(idb)
	pb.setTrack(t, media.KindAudio)
	pb.publish(media.KindAudio)
	pb.setTrack(t, media.KindCamera)
	pb.publish(media.KindCamera)
	waitFor(t, func() bool { return fp.subCount(idb, media.KindAudio) == 1 })
	// The facilitator speaks: people hear it.
	fp.setTrack(t, media.KindAudio)
	fp.publish(media.KindAudio)
	waitFor(t, func() bool { return pa.subCount(fid, media.KindAudio) == 1 && pb.subCount(fid, media.KindAudio) == 1 })
	if fp.subCount(idb, media.KindCamera) != 0 || fp.subCount(ida, media.KindCamera) != 0 {
		t.Fatal("facilitator subscribed to a camera")
	}
}

func TestNotesAndRecorderHearTheFacilitator(t *testing.T) {
	e, store := newNotesEnv(t, true)
	e.conf.SetFacilitator(func(slug string) bool { return slug == "ops" })
	e.conf.FacilitatorStopGrace = 300 * time.Millisecond
	rec, err := recording.Open(t.TempDir(), 24*time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.conf.SetRecording(rec)
	ca, _ := join(t, e, "Ann")
	notesOn(t, ca, true)
	recordingOn(t, ca, true)
	facilitatorOn(t, ca, true)
	notesBot, nid := joinNotesBot(t, e)
	_, fid := joinFacilitator(t, e)
	fp := e.media.peer(fid)
	fp.setTrack(t, media.KindAudio)
	fp.publish(media.KindAudio)
	np := e.media.peer(nid)
	waitFor(t, func() bool { return np.subCount(fid, media.KindAudio) == 1 })
	_, joined := joinRecorder(t, e)
	if n := e.media.peer(joined.Self.ID).subCount(fid, media.KindAudio); n != 1 {
		t.Fatalf("recorder subscribed to the facilitator %d times", n)
	}
	send(t, notesBot, phrase(fid, "Начнём с Анны.", 0))
	if m := expect(t, ca, "phrase"); m.Name != "Ведущий" || m.Text != "Начнём с Анны." {
		t.Fatalf("live phrase = %+v", m)
	}
	waitPhrases(t, e, 1)
	notesOn(t, ca, false)
	expect(t, notesBot, "stop")
	send(t, notesBot, `{"type":"leave"}`)
	tr := waitReady(t, store)
	if len(tr.Phrases) != 1 || tr.Phrases[0].Name != "Ведущий" || tr.Phrases[0].Text != "Начнём с Анны." {
		t.Fatalf("transcript = %+v", tr.Phrases)
	}
}

func TestSwitchingOffStopsTheFacilitator(t *testing.T) {
	e := newFacilitatorEnv(t)
	ca, _ := join(t, e, "Ann")
	facilitatorOn(t, ca, true)
	bot, _ := joinFacilitator(t, e)
	facilitatorOn(t, ca, false)
	expect(t, bot, "stop")
	expectClosed(t, bot) // never leaves: closed after the grace
	if r, _ := e.reg.Get("ops"); r.Facilitator() {
		t.Fatal("flag still set")
	}
	waitFor(t, func() bool { return e.conf.Members("ops") == 1 })
	facilitatorOn(t, ca, true)
	joinFacilitator(t, e) // switched on again: a new one is admitted
}

func TestLastPersonLeavingStopsTheFacilitator(t *testing.T) {
	e := newFacilitatorEnv(t)
	ca, _ := join(t, e, "Ann")
	facilitatorOn(t, ca, true)
	bot, _ := joinFacilitator(t, e)
	send(t, ca, `{"type":"leave"}`)
	expect(t, bot, "stop")
	if r, _ := e.reg.Get("ops"); r.Facilitator() {
		t.Fatal("flag still set")
	}
	send(t, bot, `{"type":"leave"}`)
	waitFor(t, func() bool { return e.conf.Members("ops") == 0 })
}

func TestSecondFacilitatorRefused(t *testing.T) {
	e := newFacilitatorEnv(t)
	e.conf.FacilitatorStopGrace = time.Minute
	ca, _ := join(t, e, "Ann")
	facilitatorOn(t, ca, true)
	first, _ := joinFacilitator(t, e)
	expectRefused(t, e, nil, CodeBadRoom)
	// One told to stop no longer counts.
	facilitatorOn(t, ca, false)
	expect(t, first, "stop")
	facilitatorOn(t, ca, true)
	joinFacilitator(t, e)
}

func TestGuestCannotSwitchTheFacilitator(t *testing.T) {
	e, _ := newAuthEnv(t)
	e.conf.SetFacilitator(func(slug string) bool { return slug == "ops" })
	ann := dialAs(t, e, "ann")
	send(t, ann, `{"type":"join","room":"ops","pin":"1234","name":"x"}`)
	expect(t, ann, "joined")
	g := dialAs(t, e, "")
	send(t, g, `{"type":"join","room":"ops","invite":"tok-ops","name":"Гость"}`)
	expect(t, g, "joined")
	send(t, g, `{"type":"facilitator","on":true}`)
	time.Sleep(100 * time.Millisecond)
	if r, _ := e.reg.Get("ops"); r.Facilitator() {
		t.Fatal("a guest switched the facilitator on")
	}
	facilitatorOn(t, ann, true) // the employee still can
}

// Switching on and off while facilitators join: every facilitator that got in is stopped in the
// end, none is left behind in a room that no longer wants it.
func TestFacilitatorToggleRace(t *testing.T) {
	e := newFacilitatorEnv(t)
	ca, _ := join(t, e, "Ann")
	cb, _ := join(t, e, "Bob")
	facilitatorOn(t, ca, true)
	var wg sync.WaitGroup
	for _, c := range []*websocket.Conn{ca, cb} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				if i%2 == 0 {
					send(t, c, `{"type":"facilitator","on":true}`)
				} else {
					send(t, c, `{"type":"facilitator","on":false}`)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			bot := dial(t, e)
			send(t, bot, facilitatorJoin)
			readType(t, bot) // joined or refused; a joined one stays until it is stopped
		}
	}()
	wg.Wait()
	send(t, ca, `{"type":"facilitator","on":false}`)
	for expect(t, ca, "participants").Facilitator {
	}
	waitFor(t, func() bool { return e.conf.Members("ops") == 2 })
}

// Switched off while the facilitator is inside media setup, before it is a member the switch can
// stop: its join stops it itself.
func TestSwitchedOffWhileTheFacilitatorJoins(t *testing.T) {
	e := newFacilitatorEnv(t)
	ca, _ := join(t, e, "Ann")
	facilitatorOn(t, ca, true)
	r, _ := e.reg.Get("ops")
	var once sync.Once
	e.media.setHold(func() {
		once.Do(func() {
			send(t, ca, `{"type":"facilitator","on":false}`)
			for deadline := time.Now().Add(5 * time.Second); r.Facilitator() && time.Now().Before(deadline); {
				time.Sleep(time.Millisecond)
			}
		})
	})
	bot, _ := joinFacilitator(t, e)
	expect(t, bot, "stop")
}

// Two facilitators pass the first check together: only one of them gets in.
func TestTwoFacilitatorsJoiningAtOnce(t *testing.T) {
	e := newFacilitatorEnv(t)
	ca, _ := join(t, e, "Ann")
	facilitatorOn(t, ca, true)
	var entered sync.WaitGroup
	entered.Add(2)
	e.media.setHold(func() { entered.Done(); entered.Wait() }) // both inside media setup at once
	results := make(chan string, 2)
	for i := 0; i < 2; i++ {
		go func() {
			bot := dial(t, e)
			send(t, bot, facilitatorJoin)
			results <- readType(t, bot)
		}()
	}
	got := map[string]int{}
	for i := 0; i < 2; i++ {
		got[<-results]++
	}
	if got["joined"] != 1 || got["error"] != 1 {
		t.Fatalf("results %v", got)
	}
}
