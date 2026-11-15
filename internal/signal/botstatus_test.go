package signal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ssubbotin/kunveno/internal/conference"
)

// botMsg is a bot_state or bot_help message, or any other frame read on the way.
type botMsg struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	State   string `json:"state"`
	Text    string `json:"text"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// expectBot reads frames until one of type typ arrives and returns it.
func expectBot(t *testing.T, c *websocket.Conn, typ string) botMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q: %v", typ, err)
		}
		var m botMsg
		if json.Unmarshal(data, &m) == nil && m.Type == typ {
			return m
		}
	}
}

// noBotMessages reads frames for a short while and fails on a bot_state or bot_help message.
func noBotMessages(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m botMsg
		if json.Unmarshal(data, &m) == nil && (m.Type == "bot_state" || m.Type == "bot_help") {
			t.Fatalf("unexpected %s", data)
		}
	}
}

// joinFacilitatorRaw joins the facilitator bot and returns its socket, id and raw joined frame.
func joinFacilitatorRaw(t *testing.T, e *env) (*websocket.Conn, string, string) {
	t.Helper()
	bot := dial(t, e)
	send(t, bot, facilitatorJoin)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := bot.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for joined: %v", err)
		}
		var m anyMsg
		_ = json.Unmarshal(data, &m)
		if m.Type == "joined" {
			return bot, m.Self.ID, string(data)
		}
	}
}

// newBotStatusEnv has the facilitator offered in ops and the notes bot available, with Ann in the
// room and the facilitator switched on.
func newBotStatusEnv(t *testing.T) (*env, *websocket.Conn) {
	t.Helper()
	e, _ := newNotesEnv(t, false)
	e.conf.SetFacilitator(func(slug string) bool { return slug == "ops" })
	e.conf.FacilitatorStopGrace = 300 * time.Millisecond
	ca, _ := joinRoom(t, e, "ops", "Ann")
	facilitatorOn(t, ca, true)
	return e, ca
}

func TestFacilitatorJoinedAnnouncesStatus(t *testing.T) {
	e, ca := newBotStatusEnv(t)
	_, _, joined := joinFacilitatorRaw(t, e)
	if !strings.Contains(joined, `"status":true`) {
		t.Fatalf("facilitator joined without status: %s", joined)
	}
	if _, person := joinRoom(t, e, "ops", "Bob"); strings.Contains(person, `"status"`) {
		t.Fatalf("person joined with status: %s", person)
	}
	notesOn(t, ca, true)
	nb := dial(t, e)
	send(t, nb, `{"type":"join","room":"ops","name":"Заметки","role":"notes"}`)
	_, data, err := nb.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"joined"`) || strings.Contains(string(data), `"status"`) {
		t.Fatalf("notes bot joined: %s", data)
	}
}

func TestBotStateReachesPeople(t *testing.T) {
	e, ca := newBotStatusEnv(t)
	notesOn(t, ca, true)
	notesBot, _ := joinNotesBot(t, e)
	bot, fid := joinFacilitator(t, e)
	send(t, bot, `{"type":"status","state":"thinking"}`)
	if m := expectBot(t, ca, "bot_state"); m.ID != fid || m.State != "thinking" {
		t.Fatalf("bot_state = %+v", m)
	}
	noBotMessages(t, bot)
	noBotMessages(t, notesBot)
}

func TestBotStateSameIsNotRelayed(t *testing.T) {
	e, ca := newBotStatusEnv(t)
	bot, _ := joinFacilitator(t, e)
	send(t, bot, `{"type":"status","state":"thinking"}`)
	send(t, bot, `{"type":"status","state":"thinking"}`)
	send(t, bot, `{"type":"status","state":"speaking"}`)
	if m := expectBot(t, ca, "bot_state"); m.State != "thinking" {
		t.Fatalf("first = %+v", m)
	}
	if m := expectBot(t, ca, "bot_state"); m.State != "speaking" {
		t.Fatalf("second = %+v, want speaking (the repeated thinking is not relayed)", m)
	}
}

func TestBotStateRateBound(t *testing.T) {
	e, ca := newBotStatusEnv(t)
	bot, _ := joinFacilitator(t, e)
	// Read Ann's socket in the background: a read deadline would close it.
	type relayed struct {
		at    time.Time
		state string
	}
	got := make(chan relayed, 64)
	go func() {
		for {
			_, data, err := ca.Read(context.Background())
			if err != nil {
				return
			}
			var m botMsg
			if json.Unmarshal(data, &m) == nil && m.Type == "bot_state" {
				got <- relayed{time.Now(), m.State}
			}
		}
	}()
	start := time.Now()
	for i := 0; i < 20; i++ {
		if i%2 == 0 {
			send(t, bot, `{"type":"status","state":"listening"}`)
		} else {
			send(t, bot, `{"type":"status","state":"thinking"}`)
		}
	}
	n := 0
	for wait := time.After(time.Until(start.Add(900 * time.Millisecond))); ; {
		select {
		case <-got:
			n++
			continue
		case <-wait:
		}
		break
	}
	if n < 1 || n > conference.BotStateRate {
		t.Fatalf("%d state changes relayed within the first second, want 1..%d", n, conference.BotStateRate)
	}
	time.Sleep(time.Until(start.Add(1100 * time.Millisecond)))
	// The bot is still connected and its next change gets through.
	send(t, bot, `{"type":"status","state":"speaking"}`)
	for timeout := time.After(5 * time.Second); ; {
		select {
		case r := <-got:
			if r.state != "speaking" {
				continue
			}
		case <-timeout:
			t.Fatal("no speaking after the second")
		}
		break
	}
}

func TestBotHelpReachesPeopleAndJoiners(t *testing.T) {
	e, ca := newBotStatusEnv(t)
	bot, fid := joinFacilitator(t, e)
	const help = `Скажите «дальше», чтобы перейти к следующему.\nСкажите «стоп», чтобы закончить.`
	send(t, bot, `{"type":"status","state":"waiting"}`)
	send(t, bot, `{"type":"help","text":"`+help+`"}`)
	want := "Скажите «дальше», чтобы перейти к следующему.\nСкажите «стоп», чтобы закончить."
	if m := expectBot(t, ca, "bot_help"); m.ID != fid || m.Text != want {
		t.Fatalf("bot_help = %+v", m)
	}
	// The same text again is no replacement; a new one is.
	send(t, bot, `{"type":"help","text":"`+help+`"}`)
	send(t, bot, `{"type":"help","text":"Скажите «стоп»."}`)
	if m := expectBot(t, ca, "bot_help"); m.Text != "Скажите «стоп»." {
		t.Fatalf("after a repeat = %+v", m)
	}
	send(t, bot, `{"type":"help","text":"`+help+`"}`)
	if m := expectBot(t, ca, "bot_help"); m.Text != want {
		t.Fatalf("replaced = %+v", m)
	}
	// Bob joins later: participants first, then the state, then the help.
	cb := dial(t, e)
	send(t, cb, `{"type":"join","room":"ops","pin":"1234","name":"Bob"}`)
	var order []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for len(order) == 0 || order[len(order)-1] != "bot_help" {
		_, data, err := cb.Read(ctx)
		if err != nil {
			t.Fatalf("order so far %v: %v", order, err)
		}
		var m botMsg
		_ = json.Unmarshal(data, &m)
		switch m.Type {
		case "participants", "bot_state", "bot_help":
			if m.Type == "bot_state" && (m.ID != fid || m.State != "waiting") {
				t.Fatalf("replayed state %+v", m)
			}
			if m.Type == "bot_help" && (m.ID != fid || m.Text != want) {
				t.Fatalf("replayed help %+v", m)
			}
			if len(order) == 0 || order[len(order)-1] != m.Type {
				order = append(order, m.Type)
			}
		}
	}
	if strings.Join(order, ",") != "participants,bot_state,bot_help" {
		t.Fatalf("order %v", order)
	}
	noBotMessages(t, bot)
}

func TestBotHelpValidation(t *testing.T) {
	e, ca := newBotStatusEnv(t)
	for _, bad := range []string{
		`{"type":"help","text":""}`,
		`{"type":"help","text":"   \n  "}`,
		`{"type":"help"}`,
		`{"type":"help","text":"` + strings.Repeat("я", MaxHelpLen+1) + `"}`,
		`{"type":"help","text":"нуль\u0000внутри"}`,
		`{"type":"help","text":"табуляция\tвнутри"}`,
		`{"type":"help","text":"справа\u202eналево"}`,
		`{"type":"help","text":"метка\u200eнаправления"}`,
		`{"type":"help","text":"нулевая\u200bширина"}`,
		`{"type":"help","text":"строка\u2028разделитель"}`,
		`{"type":"help","text":"абзац\u2029разделитель"}`,
		`{"type":"status","state":"dancing"}`,
		`{"type":"status"}`,
		`{"type":"status","state":5}`,
	} {
		bot, _ := joinFacilitator(t, e)
		send(t, bot, bad)
		if m := expectBot(t, bot, "error"); m.Code != CodeBadMessage {
			t.Fatalf("%s: code %q", bad, m.Code)
		}
		expectClosed(t, bot)
		waitFor(t, func() bool { return e.conf.Members("ops") == 1 })
	}
	bot, _ := joinFacilitator(t, e)
	text := strings.Repeat("я", MaxHelpLen/2-1) + `\n` + strings.Repeat("я", MaxHelpLen/2)
	send(t, bot, `{"type":"help","text":"  `+text+`  "}`)
	m := expectBot(t, ca, "bot_help")
	if n := len([]rune(m.Text)); n != MaxHelpLen || !strings.Contains(m.Text, "\n") {
		t.Fatalf("accepted help has %d characters: %q", n, m.Text)
	}
}

func TestStatusFromOthersIsUnknown(t *testing.T) {
	e, ca := newBotStatusEnv(t)
	// What an unknown frame gets today.
	cd, _ := joinRoom(t, e, "ops", "Dan")
	send(t, cd, `{"type":"dance"}`)
	unknown := expectBot(t, cd, "error")
	expectClosed(t, cd)
	want := func(typ string) botMsg {
		m := unknown
		m.Message = strings.Replace(m.Message, `"dance"`, `"`+typ+`"`, 1)
		return m
	}
	notesOn(t, ca, true)
	for _, frame := range []string{
		`{"type":"status","state":"thinking"}`,
		`{"type":"help","text":"Скажите «стоп»."}`,
		`{"type":"status","state":"dancing"}`,
	} {
		typ := "status"
		if strings.Contains(frame, `"help"`) {
			typ = "help"
		}
		cb, _ := joinRoom(t, e, "ops", "Bob")
		send(t, cb, frame)
		if m := expectBot(t, cb, "error"); m != want(typ) {
			t.Fatalf("person %s: %+v, want %+v", frame, m, want(typ))
		}
		expectClosed(t, cb)
		nb, _ := joinNotesBot(t, e)
		send(t, nb, frame)
		if m := expectBot(t, nb, "error"); m != want(typ) {
			t.Fatalf("notes bot %s: %+v, want %+v", frame, m, want(typ))
		}
		expectClosed(t, nb)
		waitFor(t, func() bool { return e.conf.Members("ops") == 1 })
	}
	noBotMessages(t, ca)
}

func TestBotStateForgottenOnLeave(t *testing.T) {
	e, ca := newBotStatusEnv(t)
	first, _ := joinFacilitator(t, e)
	send(t, first, `{"type":"status","state":"speaking"}`)
	send(t, first, `{"type":"help","text":"Скажите «стоп»."}`)
	expectBot(t, ca, "bot_help")
	send(t, first, `{"type":"leave"}`)
	waitFor(t, func() bool { return e.conf.Members("ops") == 1 })
	second, sid := joinFacilitator(t, e)
	cb, _ := joinRoom(t, e, "ops", "Bob")
	noBotMessages(t, cb) // its read deadline closes Bob's socket
	send(t, second, `{"type":"status","state":"listening"}`)
	if m := expectBot(t, ca, "bot_state"); m.ID != sid || m.State != "listening" {
		t.Fatalf("bot_state = %+v", m)
	}
	// Switched off: the stopped bot's state goes with it.
	facilitatorOn(t, ca, false)
	expect(t, second, "stop")
	send(t, second, `{"type":"leave"}`)
	waitFor(t, func() bool { return e.conf.Members("ops") == 1 })
	cc, _ := joinRoom(t, e, "ops", "Cid")
	noBotMessages(t, cc)
}

func TestFacilitatorOffNoStatusField(t *testing.T) {
	e := newEnv(t, 15)
	ca, joined := joinRoom(t, e, "ops", "Ann")
	if strings.Contains(joined, `"status"`) {
		t.Fatalf("joined with status while the facilitator is off: %s", joined)
	}
	expectRefused(t, e, nil, CodeBadRoom)
	noBotMessages(t, ca)
}

// A burst beyond the rate, then silence: the last state still reaches the room within about a
// second, and the rate holds.
func TestBotStateLastOfBurstArrives(t *testing.T) {
	e, ca := newBotStatusEnv(t)
	bot, _ := joinFacilitator(t, e)
	type relayed struct {
		at    time.Time
		state string
	}
	got := make(chan relayed, 64)
	go func() {
		for {
			_, data, err := ca.Read(context.Background())
			if err != nil {
				return
			}
			var m botMsg
			if json.Unmarshal(data, &m) == nil && m.Type == "bot_state" {
				got <- relayed{time.Now(), m.State}
			}
		}
	}()
	start := time.Now()
	for i := 0; i < 20; i++ {
		if i%2 == 0 {
			send(t, bot, `{"type":"status","state":"listening"}`)
		} else {
			send(t, bot, `{"type":"status","state":"thinking"}`) // the last one sent
		}
	}
	var all []relayed
	for deadline := time.After(time.Until(start.Add(2500 * time.Millisecond))); ; {
		select {
		case r := <-got:
			all = append(all, r)
			continue
		case <-deadline:
		}
		break
	}
	last := -1
	for i, r := range all {
		if r.state == "thinking" && r.at.Before(start.Add(1500*time.Millisecond)) {
			last = i
		}
	}
	if len(all) == 0 || all[len(all)-1].state != "thinking" || last != len(all)-1 {
		t.Fatalf("relayed %+v; the last state thinking must arrive within 1.5 s and stay last", all)
	}
	for i := range all {
		n := 0
		for _, r := range all[i:] {
			if r.at.Sub(all[i].at) < 950*time.Millisecond {
				n++
			}
		}
		if n > conference.BotStateRate {
			t.Fatalf("%d relays within a second from %d: %+v", n, i, all)
		}
	}
}
