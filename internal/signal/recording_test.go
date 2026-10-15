package signal

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ssubbotin/kunveno/internal/media"
	"github.com/ssubbotin/kunveno/internal/recording"
)

func newRecordingEnv(t *testing.T) (*env, *recording.Store) {
	t.Helper()
	e := newEnv(t, 15)
	store, err := recording.Open(t.TempDir(), 24*time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.conf.SetRecording(store)
	e.conf.RecordingStopGrace = 300 * time.Millisecond
	return e, store
}

func recordingOn(t *testing.T, c *websocket.Conn, on bool) {
	t.Helper()
	if on {
		send(t, c, `{"type":"recording","on":true}`)
	} else {
		send(t, c, `{"type":"recording","on":false}`)
	}
	for expect(t, c, "participants").Recording != on {
	}
}

func joinRecorder(t *testing.T, e *env) (*websocket.Conn, anyMsg) {
	t.Helper()
	bot := dial(t, e)
	send(t, bot, `{"type":"join","room":"ops","name":"Запись","role":"recorder"}`)
	return bot, expect(t, bot, "joined")
}

func TestRecordingSwitchAndRecorderRole(t *testing.T) {
	e, store := newRecordingEnv(t)
	ca, _ := join(t, e, "Ann")
	refused := dial(t, e)
	send(t, refused, `{"type":"join","room":"ops","name":"Запись","role":"recorder"}`)
	if m := expect(t, refused, "error"); m.Code != CodeBadRoom {
		t.Fatalf("code %q", m.Code)
	}
	recordingOn(t, ca, true)
	if got := e.reg.RecordingRooms(); len(got) != 1 {
		t.Fatalf("rooms %v", got)
	}
	_, joined := joinRecorder(t, e)
	if joined.Meeting == "" {
		t.Fatal("recorder joined without the meeting start")
	}
	second := dial(t, e)
	send(t, second, `{"type":"join","room":"ops","name":"Запись","role":"recorder"}`)
	if m := expect(t, second, "error"); m.Code != CodeBadRoom {
		t.Fatalf("second recorder: code %q", m.Code)
	}
	if err := store.Delete("ops"); err == nil {
		t.Fatal("delete allowed while recording")
	}
}

func TestRecordingDisabledIgnoresTheSwitchAndTheRole(t *testing.T) {
	e := newEnv(t, 15)
	ca, _ := join(t, e, "Ann")
	send(t, ca, `{"type":"recording","on":true}`)
	send(t, ca, `{"type":"mute","muted":true}`)
	if m := expect(t, ca, "participants"); m.Recording {
		t.Fatal("recording switched on while disabled")
	}
	bot := dial(t, e)
	send(t, bot, `{"type":"join","room":"ops","name":"Запись","role":"recorder"}`)
	if m := expect(t, bot, "error"); m.Code != CodeBadRoom {
		t.Fatalf("code %q", m.Code)
	}
}

// The recorder gets people's audio, screen and cameras, nothing from helpers.
func TestRecorderReceivesPeoplesMediaOnly(t *testing.T) {
	e, _ := newRecordingEnv(t)
	ca, ida := join(t, e, "Ann")
	e.media.peer(ida).setTrack(t, media.KindAudio)
	e.media.peer(ida).setTrack(t, media.KindCamera)
	r, _ := e.reg.Get("ops")
	r.SetPlayer(true)
	player := dial(t, e)
	send(t, player, `{"type":"join","room":"ops","name":"Видео","role":"player"}`)
	pid := expect(t, player, "joined").Self.ID
	e.media.peer(pid).setTrack(t, media.KindAudio)
	e.media.peer(pid).setTrack(t, media.KindCamera)
	recordingOn(t, ca, true)
	_, joined := joinRecorder(t, e)
	rp := e.media.peer(joined.Self.ID)
	if rp.subCount(ida, media.KindAudio) != 1 || rp.subCount(ida, media.KindCamera) != 1 ||
		rp.subCount(pid, media.KindAudio) != 0 || rp.subCount(pid, media.KindCamera) != 0 {
		t.Fatalf("recorder subscriptions: ann audio %d, ann camera %d, player audio %d, player camera %d",
			rp.subCount(ida, media.KindAudio), rp.subCount(ida, media.KindCamera), rp.subCount(pid, media.KindAudio), rp.subCount(pid, media.KindCamera))
	}
	// A camera switched on after the recorder joined reaches it too.
	_, idb := join(t, e, "Bob")
	e.media.peer(idb).setTrack(t, media.KindCamera)
	e.media.peer(idb).publish(media.KindCamera)
	waitFor(t, func() bool { return rp.subCount(idb, media.KindCamera) == 1 })
	send(t, ca, `{"type":"share","action":"start"}`)
	for expect(t, ca, "participants").Sharer != ida {
	}
	e.media.peer(ida).setTrack(t, media.KindScreen)
	e.media.peer(ida).publish(media.KindScreen)
	// At least once: the share's own subscribe can race the first RTP's, and the SFU keeps one.
	waitFor(t, func() bool { return rp.subCount(ida, media.KindScreen) >= 1 })
}

func TestRecordingStopGraceAndActiveMark(t *testing.T) {
	e, store := newRecordingEnv(t)
	ca, _ := join(t, e, "Ann")
	recordingOn(t, ca, true)
	bot, _ := joinRecorder(t, e)
	recordingOn(t, ca, false)
	expect(t, bot, "stop")
	expectClosed(t, bot) // never leaves: closed after the grace
	waitFor(t, func() bool { return store.Delete("ops") != recording.ErrRecording })
}

func TestLastPersonLeavingStopsTheRecorder(t *testing.T) {
	e, _ := newRecordingEnv(t)
	ca, _ := join(t, e, "Ann")
	recordingOn(t, ca, true)
	bot, _ := joinRecorder(t, e)
	send(t, ca, `{"type":"leave"}`)
	expect(t, bot, "stop")
}

func TestRecordingStoredTellsThePeople(t *testing.T) {
	e, _ := newRecordingEnv(t)
	ca, _ := join(t, e, "Ann")
	e.conf.RecordingStored("ops")
	expect(t, ca, "recording_ready")
}

func TestRecordingBackOnWhileRecorderStoppingKeepsTheMark(t *testing.T) {
	e, store := newRecordingEnv(t)
	e.conf.RecordingStopGrace = time.Minute
	ca, _ := join(t, e, "Ann")
	recordingOn(t, ca, true)
	first, _ := joinRecorder(t, e)
	recordingOn(t, ca, false)
	expect(t, first, "stop")
	recordingOn(t, ca, true)
	joinRecorder(t, e) // a second recorder is admitted: the first is stopping
	if err := store.Delete("ops"); err != recording.ErrRecording {
		t.Fatalf("delete: %v", err)
	}
}

func TestRecorderLeavingAfterLastPersonClearsTheMark(t *testing.T) {
	e, store := newRecordingEnv(t)
	e.conf.RecordingStopGrace = time.Minute
	ca, _ := join(t, e, "Ann")
	recordingOn(t, ca, true)
	bot, _ := joinRecorder(t, e)
	send(t, ca, `{"type":"leave"}`)
	expect(t, bot, "stop")
	send(t, bot, `{"type":"leave"}`)
	waitFor(t, func() bool { return store.Delete("ops") == recording.ErrNoRecording })
}

func TestRecordingToggleRace(t *testing.T) {
	e, store := newRecordingEnv(t)
	e.conf.RecordingStopGrace = time.Minute
	ca, _ := join(t, e, "Ann")
	cb, _ := join(t, e, "Bob")
	recordingOn(t, ca, true)
	var wg sync.WaitGroup
	for _, c := range []*websocket.Conn{ca, cb} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				send(t, c, fmt.Sprintf(`{"type":"recording","on":%v}`, i%2 == 0))
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			bot := dial(t, e)
			send(t, bot, `{"type":"join","room":"ops","name":"Запись","role":"recorder"}`)
			if readType(t, bot) == "joined" {
				send(t, bot, `{"type":"leave"}`)
			}
			_ = bot.Close(websocket.StatusNormalClosure, "")
		}
	}()
	wg.Wait()
	send(t, ca, `{"type":"recording","on":false}`)
	for expect(t, ca, "participants").Recording {
	}
	waitFor(t, func() bool { return store.Delete("ops") == recording.ErrNoRecording })
}

// readType reads the next frame and returns its type, or "" when the read fails.
func readType(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		return ""
	}
	var m anyMsg
	_ = json.Unmarshal(data, &m)
	return m.Type
}
