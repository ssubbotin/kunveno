package signal

import (
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ssubbotin/kunveno/internal/recording"
	"github.com/ssubbotin/kunveno/internal/sfu"
)

func newRecordingEnv(t *testing.T) (*env, *recording.Store) {
	t.Helper()
	e := newEnv(t, 15)
	store, err := recording.Open(t.TempDir(), 24*time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.h.SetRecording(store)
	e.h.recStopGrace = 300 * time.Millisecond
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
	if got := e.h.reg.RecordingRooms(); len(got) != 1 {
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

func TestRecorderReceivesPeoplesAudioAndScreenOnly(t *testing.T) {
	e, _ := newRecordingEnv(t)
	ca, ida := join(t, e, "Ann")
	e.media.peer(ida).setTrack(t, sfu.KindAudio)
	e.media.peer(ida).setTrack(t, sfu.KindCamera)
	r, _ := e.h.reg.Get("ops")
	r.SetPlayer(true)
	player := dial(t, e)
	send(t, player, `{"type":"join","room":"ops","name":"Видео","role":"player"}`)
	pid := expect(t, player, "joined").Self.ID
	e.media.peer(pid).setTrack(t, sfu.KindAudio)
	recordingOn(t, ca, true)
	_, joined := joinRecorder(t, e)
	rp := e.media.peer(joined.Self.ID)
	if rp.subCount(ida, sfu.KindAudio) != 1 || rp.subCount(ida, sfu.KindCamera) != 0 || rp.subCount(pid, sfu.KindAudio) != 0 {
		t.Fatalf("recorder subscriptions: ann audio %d, ann camera %d, player audio %d",
			rp.subCount(ida, sfu.KindAudio), rp.subCount(ida, sfu.KindCamera), rp.subCount(pid, sfu.KindAudio))
	}
	send(t, ca, `{"type":"share","action":"start"}`)
	for expect(t, ca, "participants").Sharer != ida {
	}
	e.media.peer(ida).setTrack(t, sfu.KindScreen)
	e.h.onPublisher(e.media.peer(ida), sfu.KindScreen)
	waitFor(t, func() bool { return rp.subCount(ida, sfu.KindScreen) == 1 })
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
	e.h.RecordingStored("ops")
	expect(t, ca, "recording_ready")
}
