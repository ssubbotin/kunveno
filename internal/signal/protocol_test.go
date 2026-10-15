package signal

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseClientAccepts(t *testing.T) {
	cases := map[string]string{
		"join":      `{"type":"join","room":"ops","pin":"1234","name":"  Ann  "}`,
		"answer":    `{"type":"answer","sdp":"v=0\r\n"}`,
		"candidate": `{"type":"candidate","candidate":{"candidate":"candidate:1 1 udp 1 10.8.0.5 5000 typ host","sdpMid":"0","sdpMLineIndex":0}}`,
		"mute":      `{"type":"mute","muted":true}`,
		"share":     `{"type":"share","action":"start"}`,
		"leave":     `{"type":"leave"}`,
		"echo":      `{"type":"echo","on":true}`,
		"camera":    `{"type":"camera","on":true}`,
		"react":     `{"type":"react","emoji":"👍"}`,
		"video":     `{"type":"video","receive":false}`,
		"notes":     `{"type":"notes","on":true}`,
		"phrase":    `{"type":"phrase","speaker":"a1b2c3d4","start":1760421600000,"end":1760421602000,"text":"Начнём."}`,
	}
	for name, raw := range cases {
		m, err := ParseClient([]byte(raw))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if m.Type != name {
			t.Errorf("%s: type = %q", name, m.Type)
		}
		if name == "join" && m.Name != "Ann" {
			t.Errorf("join: name not trimmed: %q", m.Name)
		}
	}
}

func TestParseClientRejects(t *testing.T) {
	cases := map[string]string{
		"not json":          `hello`,
		"unknown type":      `{"type":"dance"}`,
		"join no pin":       `{"type":"join","room":"ops","name":"Ann"}`,
		"join bad slug":     `{"type":"join","room":"Ops Room","pin":"1234","name":"Ann"}`,
		"join empty name":   `{"type":"join","room":"ops","pin":"1234","name":"   "}`,
		"join long name":    `{"type":"join","room":"ops","pin":"1234","name":"` + strings.Repeat("x", 33) + `"}`,
		"answer no sdp":     `{"type":"answer"}`,
		"candidate nil":     `{"type":"candidate"}`,
		"mute no flag":      `{"type":"mute"}`,
		"share bad":         `{"type":"share","action":"maybe"}`,
		"echo no flag":      `{"type":"echo"}`,
		"notes no flag":     `{"type":"notes"}`,
		"phrase no speaker": `{"type":"phrase","start":1,"end":2,"text":"x"}`,
		"phrase no times":   `{"type":"phrase","speaker":"a","text":"x"}`,
		"camera no flag":    `{"type":"camera"}`,
		"react empty":       `{"type":"react","emoji":""}`,
		"react long":        `{"type":"react","emoji":"👍👍👍👍👍"}`,
		"video no flag":     `{"type":"video"}`,
		"join bad media":    `{"type":"join","room":"ops","pin":"1234","name":"Ann","media":"carrier-pigeon"}`,
		"join bad client":   `{"type":"join","room":"ops","pin":"1234","name":"Ann","client":"no spaces allowed"}`,
		"join bad role":     `{"type":"join","room":"ops","pin":"1234","name":"Ann","role":"admin"}`,
	}
	for name, raw := range cases {
		if _, err := ParseClient([]byte(raw)); !errors.Is(err, ErrBadMessage) {
			t.Errorf("%s: err = %v, want ErrBadMessage", name, err)
		}
	}
}

func TestParseClientEchoRoleNeedsNoPIN(t *testing.T) {
	m, err := ParseClient([]byte(`{"type":"join","room":"ops","name":"Echo","role":"echo"}`))
	if err != nil || m.Role != "echo" {
		t.Fatalf("m = %+v, err = %v", m, err)
	}
}

func TestServerMessagesEncode(t *testing.T) {
	b, _ := json.Marshal(Participants([]ParticipantInfo{{ID: "a", Name: "Ann", Muted: true}, {ID: "e", Name: "Echo", Bot: true}}, "", true, false, false))
	want := `{"type":"participants","participants":[{"id":"a","name":"Ann","muted":true},{"id":"e","name":"Echo","muted":false,"bot":true}],"sharer":"","echo":true,"notes":false,"recording":false}`
	if string(b) != want {
		t.Errorf("participants:\n got %s\nwant %s", b, want)
	}
	b, _ = json.Marshal(ErrorMsg(CodeBadPIN, "wrong pin"))
	if string(b) != `{"type":"error","code":"bad_pin","message":"wrong pin"}` {
		t.Errorf("error: %s", b)
	}
	b, _ = json.Marshal(Joined(ParticipantInfo{ID: "a", Name: "Ann"}, "ops", "Operations"))
	if string(b) != `{"type":"joined","self":{"id":"a","name":"Ann","muted":false},"room":{"slug":"ops","name":"Operations"}}` {
		t.Errorf("joined: %s", b)
	}
	b, _ = json.Marshal(Offer("v=0"))
	if string(b) != `{"type":"offer","sdp":"v=0"}` {
		t.Errorf("offer: %s", b)
	}
}

func TestParseClientJoinMediaTCP(t *testing.T) {
	m, err := ParseClient([]byte(`{"type":"join","room":"ops","pin":"1234","name":"Ann","media":"tcp"}`))
	if err != nil || m.Media != "tcp" {
		t.Fatalf("m = %+v, err = %v", m, err)
	}
}

func TestParseClientNotesRoleNeedsNoPIN(t *testing.T) {
	m, err := ParseClient([]byte(`{"type":"join","room":"ops","name":"Заметки","role":"notes"}`))
	if err != nil || m.Role != RoleNotes {
		t.Fatalf("m = %+v, err = %v", m, err)
	}
}

func TestValidPhrase(t *testing.T) {
	now := time.UnixMilli(1_760_421_600_000)
	ms := func(d time.Duration) *int64 { v := now.Add(d).UnixMilli(); return &v }
	zero := int64(0)
	text, start, end, ok := validPhrase(ClientMessage{Speaker: "a", Start: ms(-3 * time.Second), End: ms(-time.Second), Text: "  Начнём.  "}, now)
	if !ok || text != "Начнём." || !start.Equal(now.Add(-3*time.Second)) || !end.Equal(now.Add(-time.Second)) {
		t.Fatalf("valid phrase: %q %v %v %v", text, start, end, ok)
	}
	if text, _, _, ok := validPhrase(ClientMessage{Speaker: "a", Start: ms(-2 * time.Second), End: ms(0), Text: "a\n# b\tc"}, now); !ok || text != "a # b c" {
		t.Errorf("whitespace runs: %q %v", text, ok)
	}
	for name, m := range map[string]ClientMessage{
		"empty text":       {Speaker: "a", Start: ms(-2 * time.Second), End: ms(0), Text: "   "},
		"long text":        {Speaker: "a", Start: ms(-2 * time.Second), End: ms(0), Text: strings.Repeat("я", MaxPhraseLen+1)},
		"end before start": {Speaker: "a", Start: ms(0), End: ms(-time.Second), Text: "x"},
		"far future":       {Speaker: "a", Start: ms(time.Minute), End: ms(61 * time.Second), Text: "x"},
		"zero start":       {Speaker: "a", Start: &zero, End: ms(0), Text: "x"},
	} {
		if _, _, _, ok := validPhrase(m, now); ok {
			t.Errorf("%s: accepted", name)
		}
	}
	edge := ClientMessage{Speaker: "a", Start: ms(0), End: ms(MaxPhraseAhead), Text: strings.Repeat("я", MaxPhraseLen)}
	if _, _, _, ok := validPhrase(edge, now); !ok {
		t.Error("the longest text ending a minute ahead was refused")
	}
}

func TestNotesServerMessagesEncode(t *testing.T) {
	for _, c := range []struct {
		msg  any
		want string
	}{
		{Stop(), `{"type":"stop"}`},
		{NotesReady(), `{"type":"notes_ready"}`},
		{LivePhrase("Ann", 1760421600000, "Привет"), `{"type":"phrase","name":"Ann","start":1760421600000,"text":"Привет"}`},
	} {
		if b, _ := json.Marshal(c.msg); string(b) != c.want {
			t.Errorf("got %s, want %s", b, c.want)
		}
	}
}

func TestParseRecordingMessages(t *testing.T) {
	if _, err := ParseClient([]byte(`{"type":"recording","on":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseClient([]byte(`{"type":"recording"}`)); !errors.Is(err, ErrBadMessage) {
		t.Fatalf("recording without on: %v", err)
	}
	if _, err := ParseClient([]byte(`{"type":"join","room":"ops","name":"Запись","role":"recorder"}`)); err != nil {
		t.Fatalf("recorder join: %v", err)
	}
}

func TestParticipantsCarriesRecording(t *testing.T) {
	b, _ := json.Marshal(Participants(nil, "", false, false, true))
	if !strings.Contains(string(b), `"recording":true`) {
		t.Fatal(string(b))
	}
	b, _ = json.Marshal(RecordingReady())
	if string(b) != `{"type":"recording_ready"}` {
		t.Fatal(string(b))
	}
}

func TestJoinedRecorderCarriesTheMeeting(t *testing.T) {
	m := time.Date(2026, 10, 15, 8, 0, 0, 123e6, time.UTC)
	b, _ := json.Marshal(JoinedRecorder(ParticipantInfo{ID: "x"}, "ops", "Ops", m))
	if !strings.Contains(string(b), `"meeting":"2026-10-15T08:00:00.123Z"`) {
		t.Fatal(string(b))
	}
}

func TestParseClientJoinRestart(t *testing.T) {
	join := func(restart string) ClientMessage {
		t.Helper()
		m, err := ParseClient([]byte(`{"type":"join","room":"ops","pin":"1234","name":"Ann"` + restart + `}`))
		if err != nil {
			t.Fatalf("%s: %v", restart, err)
		}
		return m
	}
	m := join(`,"restart":{"reason":"media_disconnected","after":27,"transport":"udp"}`)
	if m.Restart == nil || *m.Restart != (Restart{Reason: "media_disconnected", After: 27, Transport: "udp"}) {
		t.Fatalf("restart = %+v", m.Restart)
	}
	if m := join(`,"restart":{"reason":"rtc_error","after":0,"transport":""}`); m.Restart == nil || m.Restart.Transport != "" {
		t.Errorf("empty transport: %+v", m.Restart)
	}
	if m := join(`,"restart":{"reason":"ws_closed","after":86400}`); m.Restart == nil {
		t.Error("missing transport is allowed")
	}
	if m := join(``); m.Restart != nil {
		t.Errorf("no restart: %+v", m.Restart)
	}
	for _, bad := range []string{
		`,"restart":{"reason":"boredom","after":1,"transport":"udp"}`,
		`,"restart":{"after":1,"transport":"udp"}`,
		`,"restart":{"reason":"ws_closed","after":-1}`,
		`,"restart":{"reason":"ws_closed","after":86401}`,
		`,"restart":{"reason":"ws_closed","after":1.5}`,
		`,"restart":{"reason":"ws_closed","after":"1"}`,
		`,"restart":{"reason":"ws_closed","after":1,"transport":"quic"}`,
		`,"restart":{"reason":"ws_closed","after":1,"transport":5}`,
		`,"restart":{"reason":7,"after":1}`,
		`,"restart":"ws_closed"`,
		`,"restart":[1]`,
		`,"restart":null`,
	} {
		if m := join(bad); m.Restart != nil {
			t.Errorf("%s: restart kept: %+v", bad, m.Restart)
		}
	}
}
