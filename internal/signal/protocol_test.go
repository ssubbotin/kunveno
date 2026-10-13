package signal

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
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
		"not json":        `hello`,
		"unknown type":    `{"type":"dance"}`,
		"join no pin":     `{"type":"join","room":"ops","name":"Ann"}`,
		"join bad slug":   `{"type":"join","room":"Ops Room","pin":"1234","name":"Ann"}`,
		"join empty name": `{"type":"join","room":"ops","pin":"1234","name":"   "}`,
		"join long name":  `{"type":"join","room":"ops","pin":"1234","name":"` + strings.Repeat("x", 33) + `"}`,
		"answer no sdp":   `{"type":"answer"}`,
		"candidate nil":   `{"type":"candidate"}`,
		"mute no flag":    `{"type":"mute"}`,
		"share bad":       `{"type":"share","action":"maybe"}`,
		"echo no flag":    `{"type":"echo"}`,
		"camera no flag":  `{"type":"camera"}`,
		"react unknown":   `{"type":"react","emoji":"💩"}`,
		"react text":      `{"type":"react","emoji":"hello"}`,
		"video no flag":   `{"type":"video"}`,
		"join bad media":  `{"type":"join","room":"ops","pin":"1234","name":"Ann","media":"carrier-pigeon"}`,
		"join bad client": `{"type":"join","room":"ops","pin":"1234","name":"Ann","client":"no spaces allowed"}`,
		"join bad role":   `{"type":"join","room":"ops","pin":"1234","name":"Ann","role":"admin"}`,
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
	b, _ := json.Marshal(Participants([]ParticipantInfo{{ID: "a", Name: "Ann", Muted: true}, {ID: "e", Name: "Echo", Bot: true}}, "", true))
	want := `{"type":"participants","participants":[{"id":"a","name":"Ann","muted":true},{"id":"e","name":"Echo","muted":false,"bot":true}],"sharer":"","echo":true}`
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
