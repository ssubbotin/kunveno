package signal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ssubbotin/kunveno/internal/media"
	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/room"
)

func newNotesEnv(t *testing.T, live bool) (*env, *notes.Store) {
	t.Helper()
	e := newEnv(t, 15)
	e.notesDir = t.TempDir()
	store, err := notes.Open(e.notesDir, 24*time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.conf.SetNotes(store, live)
	e.conf.NotesStopGrace = 300 * time.Millisecond
	return e, store
}

// notesOn switches notes and waits for the snapshot that shows it.
func notesOn(t *testing.T, c *websocket.Conn, on bool) {
	t.Helper()
	if on {
		send(t, c, `{"type":"notes","on":true}`)
	} else {
		send(t, c, `{"type":"notes","on":false}`)
	}
	for expect(t, c, "participants").Notes != on {
	}
}

// joinNotesBot plays the kunveno-notes bot: a loopback connection joining in the notes role.
func joinNotesBot(t *testing.T, e *env) (*websocket.Conn, string) {
	t.Helper()
	bot := dial(t, e)
	send(t, bot, `{"type":"join","room":"ops","name":"Заметки","role":"notes"}`)
	return bot, expect(t, bot, "joined").Self.ID
}

// phrase is the bot's phrase message for a two-second utterance that ended ago.
func phrase(speaker, text string, ago time.Duration) string {
	end := time.Now().Add(-ago)
	return rawPhrase(speaker, text, end.Add(-2*time.Second), end)
}

func rawPhrase(speaker, text string, start, end time.Time) string {
	b, _ := json.Marshal(map[string]any{"type": "phrase", "speaker": speaker, "start": start.UnixMilli(), "end": end.UnixMilli(), "text": text})
	return string(b)
}

// waitPhrases waits until the room's transcript file holds n phrases: the bot's phrases and a
// person's switch travel on different sockets, so a test orders them with this.
func waitPhrases(t *testing.T, e *env, n int) {
	t.Helper()
	waitFor(t, func() bool {
		data, _ := os.ReadFile(newestNotes(e))
		return strings.Count(string(data), `"type":"phrase"`) == n
	})
}

func waitReady(t *testing.T, store *notes.Store) notes.Transcript {
	t.Helper()
	waitFor(t, func() bool { _, ok := store.Until("ops"); return ok })
	meeting, _ := store.Latest("ops")
	tr, err := store.Load("ops", meeting)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// until reads frames until done(m) holds, failing on a frame of the banned type first.
func until(t *testing.T, c *websocket.Conn, banned string, done func(anyMsg) bool) {
	t.Helper()
	untilNone(t, c, []string{banned}, done)
}

// untilNone reads frames until done(m) holds, failing on a frame of any banned type first.
func untilNone(t *testing.T, c *websocket.Conn, banned []string, done func(anyMsg) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		_, data, err := c.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		var m anyMsg
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(banned, m.Type) {
			t.Fatalf("unexpected %q", m.Type)
		}
		if done(m) {
			return
		}
	}
	t.Fatal("condition not met in time")
}

func kinds(evs []notes.Event) string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return strings.Join(out, ",")
}

func TestNotesSwitchAndBotRole(t *testing.T) {
	e, _ := newNotesEnv(t, true)
	ca, ida := join(t, e, "Ann")
	early := dial(t, e)
	send(t, early, `{"type":"join","room":"ops","name":"Заметки","role":"notes"}`)
	if m := expect(t, early, "error"); m.Code != CodeBadRoom {
		t.Fatalf("notes role before anyone asked: %q", m.Code)
	}
	notesOn(t, ca, true)
	if got := e.reg.NotesRooms(); len(got) != 1 || got[0] != "ops" {
		t.Fatalf("NotesRooms = %v", got)
	}
	proxied := dialWith(t, e, http.Header{"X-Forwarded-For": {"10.8.0.5"}})
	send(t, proxied, `{"type":"join","room":"ops","name":"Заметки","role":"notes"}`)
	if m := expect(t, proxied, "error"); m.Code != CodeBadMessage {
		t.Fatalf("notes role through nginx: %q", m.Code)
	}
	bot, _ := joinNotesBot(t, e)
	for {
		m := expect(t, ca, "participants")
		if len(m.Participants) == 2 && m.Participants[1].BotKind == room.BotNotes && m.Notes {
			break
		}
	}
	// A helper cannot switch notes off; the phrase after it proves the switch was read.
	send(t, bot, `{"type":"notes","on":false}`)
	send(t, bot, phrase(ida, "Проверка.", 0))
	if m := expect(t, ca, "phrase"); m.Text != "Проверка." || m.Name != "Ann" {
		t.Fatalf("live phrase = %+v", m)
	}
	r, _ := e.reg.Get("ops")
	if !r.Notes() {
		t.Fatal("a helper switched notes off")
	}
}

func TestNotesDisabledIgnoresTheSwitchAndTheRole(t *testing.T) {
	e := newEnv(t, 15) // notes.enabled off: SetNotes never called
	ca, _ := join(t, e, "Ann")
	send(t, ca, `{"type":"notes","on":true}`)
	send(t, ca, `{"type":"mute","muted":true}`)
	for {
		m := expect(t, ca, "participants")
		if len(m.Participants) == 1 && m.Participants[0].Muted {
			if m.Notes {
				t.Fatal("notes switched on while disabled")
			}
			break
		}
	}
	if got := e.reg.NotesRooms(); len(got) != 0 {
		t.Fatalf("NotesRooms = %v", got)
	}
	bot := dial(t, e)
	send(t, bot, `{"type":"join","room":"ops","name":"Заметки","role":"notes"}`)
	if m := expect(t, bot, "error"); m.Code != CodeBadRoom {
		t.Fatalf("notes role while disabled: %q", m.Code)
	}
}

func TestPhraseOnlyFromTheNotesBot(t *testing.T) {
	e, _ := newNotesEnv(t, true)
	r, _ := e.reg.Get("ops")
	r.SetPlayer(true)
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	player := dial(t, e)
	send(t, player, `{"type":"join","room":"ops","name":"Видео","role":"player"}`)
	expect(t, player, "joined")
	send(t, ca, phrase(ida, "от человека", 0))
	send(t, player, phrase(ida, "от проигрывателя", 0))
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(ida, "от заметок", 0))
	if m := expect(t, ca, "phrase"); m.Text != "от заметок" {
		t.Fatalf("first phrase = %+v", m)
	}
}

func TestPhraseValidation(t *testing.T) {
	e, _ := newNotesEnv(t, true)
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	now := time.Now()
	for _, bad := range []string{
		rawPhrase(ida, "   ", now.Add(-2*time.Second), now),
		rawPhrase(ida, strings.Repeat("я", MaxPhraseLen+1), now.Add(-2*time.Second), now),
		rawPhrase(ida, "назад во времени", now, now.Add(-time.Second)),
		rawPhrase(ida, "из будущего", now.Add(2*time.Minute), now.Add(3*time.Minute)),
		rawPhrase("nobody00", "чужой", now.Add(-2*time.Second), now),
	} {
		send(t, bot, bad)
	}
	send(t, bot, rawPhrase(ida, strings.Repeat("я", MaxPhraseLen), now.Add(-2*time.Second), now))
	if m := expect(t, ca, "phrase"); len([]rune(m.Text)) != MaxPhraseLen {
		t.Fatalf("first accepted phrase has %d characters", len([]rune(m.Text)))
	}
}

func TestNotesStopFlushAndReady(t *testing.T) {
	e, store := newNotesEnv(t, false)
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(ida, "Первая фраза.", 3*time.Second))
	waitPhrases(t, e, 1)
	notesOn(t, ca, false)
	expect(t, bot, "stop")
	send(t, bot, phrase(ida, "Последняя фраза.", time.Second)) // finished after the stop
	send(t, bot, `{"type":"leave"}`)
	// notes.live is off: no phrase reaches the room.
	until(t, ca, "phrase", func(m anyMsg) bool { return m.Type == "notes_ready" })
	tr := waitReady(t, store)
	if tr.Room != "Operations" || len(tr.Phrases) != 2 || tr.Phrases[0].Name != "Ann" || tr.Phrases[1].Text != "Последняя фраза." {
		t.Fatalf("transcript = %+v", tr)
	}
	if kinds(tr.Events) != notes.KindPaused {
		t.Fatalf("events = %s", kinds(tr.Events))
	}
}

func TestNotesBotClosedAfterGrace(t *testing.T) {
	e, store := newNotesEnv(t, true)
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(ida, "Фраза.", time.Second))
	expect(t, ca, "phrase")
	notesOn(t, ca, false)
	expect(t, bot, "stop")
	stopped := time.Now()
	// The bot never leaves on its own and does not read: closing must not wait for its answer.
	expect(t, ca, "notes_ready")
	if took := time.Since(stopped); took < e.conf.NotesStopGrace-50*time.Millisecond || took > e.conf.NotesStopGrace+2*time.Second {
		t.Fatalf("notes ready %v after the stop; grace %v", took, e.conf.NotesStopGrace)
	}
	expectClosed(t, bot)
	waitReady(t, store)
}

// The last person leaves after the notes bot was told joined but before its join ended: the bot
// was in the room while the notes were on, so what it heard is kept.
func TestLastPersonLeavingWhileTheNotesBotFinishesItsJoin(t *testing.T) {
	e, store := newNotesEnv(t, false)
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	r, _ := e.reg.Get("ops")
	var once sync.Once
	e.media.setOnNegotiate(func(id string) {
		if id == ida {
			return
		}
		once.Do(func() { // inside the bot's join, after its joined went out
			_ = ca.CloseNow()
			for deadline := time.Now().Add(5 * time.Second); r.Notes() && time.Now().Before(deadline); {
				time.Sleep(time.Millisecond)
			}
		})
	})
	bot, _ := joinNotesBot(t, e)
	expect(t, bot, "stop")
	send(t, bot, phrase(ida, "До свидания.", 2*time.Second))
	send(t, bot, `{"type":"leave"}`)
	tr := waitReady(t, store)
	if len(tr.Phrases) != 1 || tr.Phrases[0].Name != "Ann" {
		t.Fatalf("transcript = %+v", tr)
	}
}

func TestLastPersonLeavingStopsNotes(t *testing.T) {
	e, store := newNotesEnv(t, false)
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(ida, "До свидания.", 2*time.Second))
	_ = ca.Close(websocket.StatusNormalClosure, "")
	expect(t, bot, "stop")
	send(t, bot, phrase(ida, "Это последнее.", time.Second)) // Ann has left; her name stays known
	send(t, bot, `{"type":"leave"}`)
	tr := waitReady(t, store)
	if len(tr.Phrases) != 2 || tr.Phrases[1].Name != "Ann" || len(tr.Events) != 0 {
		t.Fatalf("transcript = %+v", tr)
	}
}

func TestNotesPauseResumeKeepsOneTranscript(t *testing.T) {
	e, store := newNotesEnv(t, false)
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(ida, "До паузы.", 5*time.Second))
	waitPhrases(t, e, 1)
	notesOn(t, ca, false)
	expect(t, bot, "stop")
	send(t, bot, `{"type":"leave"}`)
	expect(t, ca, "notes_ready")
	notesOn(t, ca, true)
	bot2, _ := joinNotesBot(t, e)
	send(t, bot2, phrase(ida, "После паузы.", 0))
	waitPhrases(t, e, 2)
	notesOn(t, ca, false)
	expect(t, bot2, "stop")
	send(t, bot2, `{"type":"leave"}`)
	expect(t, ca, "notes_ready")
	tr := waitReady(t, store)
	if len(tr.Phrases) != 2 || kinds(tr.Events) != "paused,resumed,paused" {
		t.Fatalf("phrases = %d, events = %s", len(tr.Phrases), kinds(tr.Events))
	}
}

func TestNewMeetingBeginsItsOwnNotes(t *testing.T) {
	e, store := newNotesEnv(t, false)
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(ida, "Первая встреча.", time.Second))
	_ = ca.Close(websocket.StatusNormalClosure, "")
	expect(t, bot, "stop")
	send(t, bot, `{"type":"leave"}`)
	waitReady(t, store)
	cb, idb := join(t, e, "Bob")
	notesOn(t, cb, true)
	bot2, _ := joinNotesBot(t, e)
	send(t, bot2, phrase(idb, "Вторая встреча.", 0))
	waitFor(t, func() bool { // stored before the switch-off, as a bot's phrases are once it has joined
		data, _ := os.ReadFile(newestNotes(e))
		return strings.Contains(string(data), "Вторая встреча.")
	})
	notesOn(t, cb, false)
	expect(t, bot2, "stop")
	send(t, bot2, `{"type":"leave"}`)
	expect(t, cb, "notes_ready")
	tr := waitReady(t, store)
	if len(tr.Phrases) != 1 || tr.Phrases[0].Text != "Вторая встреча." || tr.Phrases[0].Name != "Bob" {
		t.Fatalf("transcript = %+v", tr)
	}
}

// Review focus: switched back on before the old bot has left.
func TestNotesBackOnWhileTheOldBotFlushes(t *testing.T) {
	e, store := newNotesEnv(t, false)
	e.conf.NotesStopGrace = 5 * time.Second
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(ida, "Раз.", 10*time.Second))
	waitPhrases(t, e, 1)
	notesOn(t, ca, false)
	expect(t, bot, "stop")
	notesOn(t, ca, true)
	send(t, bot, phrase(ida, "Два.", 8*time.Second))
	send(t, bot, `{"type":"leave"}`)
	expectClosed(t, bot)
	// The old bot's departure is broadcast after its notes are handled: no ready notice before it.
	until(t, ca, "notes_ready", func(m anyMsg) bool { return m.Type == "participants" && len(m.Participants) == 1 && m.Notes })
	if _, ok := store.Until("ops"); ok {
		t.Fatal("notes ready while switched on")
	}
	bot2, _ := joinNotesBot(t, e)
	send(t, bot2, phrase(ida, "Три.", 0))
	waitPhrases(t, e, 3)
	notesOn(t, ca, false)
	expect(t, bot2, "stop")
	send(t, bot2, `{"type":"leave"}`)
	expect(t, ca, "notes_ready")
	tr := waitReady(t, store)
	if len(tr.Phrases) != 3 || kinds(tr.Events) != "paused,resumed,paused" {
		t.Fatalf("phrases = %d, events = %s", len(tr.Phrases), kinds(tr.Events))
	}
}

// Review focus: a bot that drops while the room wants notes does not end the recording.
func TestNotesBotDropWhileWanted(t *testing.T) {
	e, store := newNotesEnv(t, true)
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(ida, "До обрыва.", 5*time.Second))
	expect(t, ca, "phrase")
	_ = bot.CloseNow()
	until(t, ca, "notes_ready", func(m anyMsg) bool { return m.Type == "participants" && len(m.Participants) == 1 && m.Notes })
	if _, ok := store.Until("ops"); ok {
		t.Fatal("a dropped bot made the notes ready")
	}
	bot2, _ := joinNotesBot(t, e)
	send(t, bot2, phrase(ida, "После обрыва.", 0))
	waitPhrases(t, e, 2)
	notesOn(t, ca, false)
	expect(t, bot2, "stop")
	send(t, bot2, `{"type":"leave"}`)
	expect(t, ca, "notes_ready")
	tr := waitReady(t, store)
	if len(tr.Phrases) != 2 || kinds(tr.Events) != notes.KindPaused {
		t.Fatalf("phrases = %d, events = %s", len(tr.Phrases), kinds(tr.Events))
	}
}

func TestNotesBotHearsPeopleOnly(t *testing.T) {
	e, _ := newNotesEnv(t, false)
	ca, ida := join(t, e, "Ann")
	e.media.peer(ida).setTrack(t, media.KindAudio)
	e.media.peer(ida).setTrack(t, media.KindCamera)
	r, _ := e.reg.Get("ops")
	r.SetPlayer(true)
	player := dial(t, e)
	send(t, player, `{"type":"join","room":"ops","name":"Видео","role":"player"}`)
	pid := expect(t, player, "joined").Self.ID
	e.media.peer(pid).setTrack(t, media.KindAudio)
	notesOn(t, ca, true)
	_, nid := joinNotesBot(t, e)
	np := e.media.peer(nid)
	if np.subCount(ida, media.KindAudio) != 1 || np.subCount(ida, media.KindCamera) != 0 || np.subCount(pid, media.KindAudio) != 0 {
		t.Fatalf("notes bot subscriptions: ann audio %d, ann camera %d, player audio %d",
			np.subCount(ida, media.KindAudio), np.subCount(ida, media.KindCamera), np.subCount(pid, media.KindAudio))
	}
	_, idb := join(t, e, "Bob")
	e.media.peer(idb).setTrack(t, media.KindCamera)
	e.media.peer(idb).publish(media.KindCamera)
	if np.subCount(idb, media.KindCamera) != 0 || e.media.peer(ida).subCount(idb, media.KindCamera) != 1 {
		t.Fatal("a camera published later reached the notes bot, or missed a person")
	}
}

// Ruling C3: notes switched back on after the meeting's transcript expired start a fresh one.
func TestNotesResumeAfterExpiryBeginsAgain(t *testing.T) {
	e, store := newNotesEnv(t, false)
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(ida, "До истечения.", 5*time.Second))
	waitPhrases(t, e, 1)
	notesOn(t, ca, false)
	expect(t, bot, "stop")
	send(t, bot, `{"type":"leave"}`)
	expect(t, ca, "notes_ready")
	if err := store.Delete("ops"); err != nil { // as the sweep does once they expire
		t.Fatal(err)
	}
	notesOn(t, ca, true)
	bot2, _ := joinNotesBot(t, e)
	send(t, bot2, phrase(ida, "После истечения.", 0))
	waitPhrases(t, e, 1)
	notesOn(t, ca, false)
	expect(t, bot2, "stop")
	send(t, bot2, `{"type":"leave"}`)
	expect(t, ca, "notes_ready")
	tr := waitReady(t, store)
	if len(tr.Phrases) != 1 || tr.Phrases[0].Text != "После истечения." || kinds(tr.Events) != notes.KindPaused {
		t.Fatalf("phrases = %+v, events = %s", tr.Phrases, kinds(tr.Events))
	}
}

// Notes deleted from the lobby during a meeting (DELETE /api/rooms/{slug}/notes): refused while
// recording; once they have ended and are deleted, switching notes on again starts a new
// transcript holding only what follows.
func TestNotesDeletedFromTheLobbyBeginAgain(t *testing.T) {
	e, store := newNotesEnv(t, false)
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(ida, "До удаления.", 5*time.Second))
	waitPhrases(t, e, 1)
	if err := store.DeleteEnded("ops", currentMeeting(store, "ops")); !errors.Is(err, notes.ErrRecording) {
		t.Fatalf("DeleteEnded while recording = %v", err)
	}
	notesOn(t, ca, false)
	expect(t, bot, "stop")
	send(t, bot, `{"type":"leave"}`)
	expect(t, ca, "notes_ready")
	if err := store.DeleteEnded("ops", currentMeeting(store, "ops")); err != nil {
		t.Fatal(err)
	}
	notesOn(t, ca, true)
	bot2, _ := joinNotesBot(t, e)
	send(t, bot2, phrase(ida, "После удаления.", 0))
	waitPhrases(t, e, 1)
	notesOn(t, ca, false)
	expect(t, bot2, "stop")
	send(t, bot2, `{"type":"leave"}`)
	expect(t, ca, "notes_ready")
	tr := waitReady(t, store)
	if len(tr.Phrases) != 1 || tr.Phrases[0].Text != "После удаления." || kinds(tr.Events) != notes.KindPaused {
		t.Fatalf("phrases = %+v, events = %s", tr.Phrases, kinds(tr.Events))
	}
}

// Task 3 carry: a new meeting begins its own transcript even while the previous meeting's bot is
// still finishing; it never resumes the previous one.
func TestNewMeetingWhileTheOldBotFlushesBegins(t *testing.T) {
	e, store := newNotesEnv(t, false)
	e.conf.NotesStopGrace = 5 * time.Second
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(ida, "Первая встреча.", time.Second))
	_ = ca.Close(websocket.StatusNormalClosure, "")
	expect(t, bot, "stop")
	cb, idb := join(t, e, "Bob") // a new meeting; the old bot has not left yet
	notesOn(t, cb, true)
	send(t, bot, `{"type":"leave"}`)
	expectClosed(t, bot)
	bot2, _ := joinNotesBot(t, e)
	send(t, bot2, phrase(idb, "Вторая встреча.", 0))
	waitFor(t, func() bool { // the old meeting's file also holds one phrase until this one begins anew
		data, _ := os.ReadFile(newestNotes(e))
		return strings.Contains(string(data), "Вторая встреча.")
	})
	notesOn(t, cb, false)
	expect(t, bot2, "stop")
	send(t, bot2, `{"type":"leave"}`)
	expect(t, cb, "notes_ready")
	tr := waitReady(t, store)
	if len(tr.Phrases) != 1 || tr.Phrases[0].Text != "Вторая встреча." || kinds(tr.Events) != notes.KindPaused {
		t.Fatalf("phrases = %+v, events = %s", tr.Phrases, kinds(tr.Events))
	}
}

// Ruling C5: people who leave a room that never took notes are not remembered.
func TestDepartedKeptOnlyWhileTakingNotes(t *testing.T) {
	e, _ := newNotesEnv(t, false)
	ca, ida := join(t, e, "Ann")
	cb, idb := join(t, e, "Bob")
	_ = cb.Close(websocket.StatusNormalClosure, "")
	// Wait for Bob's leave on the server. A one-person snapshot does not show it: Ann's own join
	// sent one, still unread, so notes could go on before Bob's leave and he would rightly be kept.
	waitFor(t, func() bool { return e.conf.Members("ops") == 1 })
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(idb, "Фраза Боба.", time.Second))
	send(t, bot, phrase(ida, "Фраза Анны.", 0))
	waitFor(t, func() bool { // phrases are taken in order: Bob's has been handled once Ann's is stored
		data, _ := os.ReadFile(newestNotes(e))
		return strings.Contains(string(data), "Фраза Анны.")
	})
	if data, _ := os.ReadFile(newestNotes(e)); strings.Contains(string(data), "Фраза Боба.") {
		t.Fatal("a person who left a room without notes was remembered")
	}
}

// Ruling C7: the notes bot cannot ask for cameras.
func TestNotesBotCannotAskForVideo(t *testing.T) {
	e, _ := newNotesEnv(t, true)
	ca, ida := join(t, e, "Ann")
	e.media.peer(ida).setTrack(t, media.KindCamera)
	notesOn(t, ca, true)
	bot, nid := joinNotesBot(t, e)
	send(t, bot, `{"type":"video","receive":true}`)
	send(t, bot, phrase(ida, "Видео не нужно.", 0)) // read after the video message
	expect(t, ca, "phrase")
	if n := e.media.peer(nid).subCount(ida, media.KindCamera); n != 0 {
		t.Fatalf("notes bot subscribed to a camera %d times", n)
	}
}

// Review fix 1: the old meeting's bot flushes after a new meeting began. Its phrases go to the old
// meeting's transcript, which ends when it leaves; the new meeting's people hear nothing of it.
func TestOldBotFlushAfterANewMeetingBegan(t *testing.T) {
	e, store := newNotesEnv(t, true)
	e.conf.NotesStopGrace = 5 * time.Second
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(ida, "Первая встреча.", 3*time.Second))
	waitPhrases(t, e, 1)
	_ = ca.Close(websocket.StatusNormalClosure, "")
	expect(t, bot, "stop")
	cb, _ := join(t, e, "Bob") // a new meeting while the old bot flushes
	send(t, bot, phrase(ida, "Поздняя фраза.", time.Second))
	send(t, bot, `{"type":"leave"}`)
	expectClosed(t, bot)
	untilNone(t, cb, []string{"notes_ready", "phrase"}, func(m anyMsg) bool {
		return m.Type == "participants" && len(m.Participants) == 1
	})
	tr := waitReady(t, store)
	if len(tr.Phrases) != 2 || tr.Phrases[1].Text != "Поздняя фраза." || tr.Phrases[1].Name != "Ann" {
		t.Fatalf("transcript = %+v", tr)
	}
}

// Review fix 1 (minor 3): the new meeting switching notes on and off while the old bot flushes
// does not make the old meeting's notes ready to the new meeting's people.
func TestNewMeetingIsNotToldOfTheOldNotes(t *testing.T) {
	e, store := newNotesEnv(t, false)
	e.conf.NotesStopGrace = 5 * time.Second
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, _ := joinNotesBot(t, e)
	send(t, bot, phrase(ida, "Первая встреча.", 3*time.Second))
	waitPhrases(t, e, 1)
	_ = ca.Close(websocket.StatusNormalClosure, "")
	expect(t, bot, "stop")
	cb, _ := join(t, e, "Bob")
	notesOn(t, cb, true)
	notesOn(t, cb, false)
	send(t, bot, `{"type":"leave"}`)
	expectClosed(t, bot)
	until(t, cb, "notes_ready", func(m anyMsg) bool {
		return m.Type == "participants" && len(m.Participants) == 1 && !m.Notes
	})
	if tr := waitReady(t, store); len(tr.Phrases) != 1 || tr.Phrases[0].Text != "Первая встреча." {
		t.Fatalf("transcript = %+v", tr)
	}
}

// Review fix 2: notes switched off while the bot's join is still setting up media; the bot must
// still get the stop and the grace.
func TestNotesOffWhileTheBotJoins(t *testing.T) {
	e, _ := newNotesEnv(t, false)
	ca, _ := join(t, e, "Ann")
	notesOn(t, ca, true)
	entered, release := make(chan struct{}), make(chan struct{})
	e.media.setHold(func() { close(entered); <-release })
	bot := dial(t, e)
	send(t, bot, `{"type":"join","room":"ops","name":"Заметки","role":"notes"}`)
	<-entered
	e.media.setHold(nil)
	notesOn(t, ca, false)
	close(release)
	expect(t, bot, "joined")
	expect(t, bot, "stop")
	expectClosed(t, bot)
}

// Final review: a notes bot that joined while the notes were being switched off is told to stop at
// once. Its phrases arrive after the transcript has ended; they must not reopen it (a resumed mark
// before the pause) nor cause a second notes_ready.
func TestStoppingBotCannotReopenEndedNotes(t *testing.T) {
	e, store := newNotesEnv(t, false)
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot1, _ := joinNotesBot(t, e)
	send(t, bot1, phrase(ida, "Первая фраза.", 3*time.Second))
	waitPhrases(t, e, 1)

	entered, release := make(chan struct{}), make(chan struct{})
	e.media.setHold(func() { close(entered); <-release })
	bot2 := dial(t, e)
	send(t, bot2, `{"type":"join","room":"ops","name":"Заметки","role":"notes"}`)
	<-entered
	e.media.setHold(nil)
	notesOn(t, ca, false)
	expect(t, bot1, "stop")
	send(t, bot1, `{"type":"leave"}`)
	expect(t, ca, "notes_ready")

	close(release)
	id2 := expect(t, bot2, "joined").Self.ID
	expect(t, bot2, "stop")
	has := func(m anyMsg, id string) bool {
		return slices.ContainsFunc(m.Participants, func(p ParticipantInfo) bool { return p.ID == id })
	}
	until(t, ca, "notes_ready", func(m anyMsg) bool { return m.Type == "participants" && has(m, id2) })
	send(t, bot2, phrase(ida, "Поздняя фраза.", time.Second))
	send(t, bot2, `{"type":"leave"}`)
	until(t, ca, "notes_ready", func(m anyMsg) bool { return m.Type == "participants" && !has(m, id2) })

	tr := waitReady(t, store)
	if len(tr.Phrases) != 1 || kinds(tr.Events) != notes.KindPaused {
		t.Fatalf("phrases = %+v, events = %s", tr.Phrases, kinds(tr.Events))
	}
}

// The bot that recorded the meeting may deliver its first phrase only after the stop (recognition
// lags the speech): that phrase still begins the transcript.
func TestFirstPhraseAfterTheStopIsKept(t *testing.T) {
	e, store := newNotesEnv(t, false)
	ca, ida := join(t, e, "Ann")
	notesOn(t, ca, true)
	bot, idb := joinNotesBot(t, e)
	// The bot's read loop answers only once its join is over: the switch-off below comes after it.
	send(t, bot, `{"type":"mute","muted":true}`)
	until(t, ca, "notes_ready", func(m anyMsg) bool {
		return m.Type == "participants" && slices.ContainsFunc(m.Participants, func(p ParticipantInfo) bool { return p.ID == idb && p.Muted })
	})
	notesOn(t, ca, false)
	expect(t, bot, "stop")
	send(t, bot, phrase(ida, "Единственная фраза.", time.Second))
	send(t, bot, `{"type":"leave"}`)
	expect(t, ca, "notes_ready")
	if tr := waitReady(t, store); len(tr.Phrases) != 1 {
		t.Fatalf("phrases = %+v", tr.Phrases)
	}
}

// currentMeeting is the meeting the room's notes were last begun for.
func currentMeeting(store *notes.Store, slug string) int64 {
	m, _ := store.Current(slug)
	return m
}

// newestNotes is the file of ops's newest meeting transcript: the names are the meetings' starts, of
// one length, so they sort by time.
func newestNotes(e *env) string {
	files, _ := filepath.Glob(filepath.Join(e.notesDir, "ops", "*.jsonl"))
	sort.Strings(files)
	if len(files) == 0 {
		return filepath.Join(e.notesDir, "ops", "none.jsonl")
	}
	return files[len(files)-1]
}
