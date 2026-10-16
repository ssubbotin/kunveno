package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ssubbotin/kunveno/internal/auth"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"github.com/ssubbotin/kunveno/internal/room"
)

func invitesEnv(t *testing.T) (http.Handler, *auth.Invites, *[]string) {
	h, inv, revoked, _ := invitesEnvLog(t)
	return h, inv, revoked
}

func invitesEnvLog(t *testing.T) (http.Handler, *auth.Invites, *[]string, *bytes.Buffer) {
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Ops", "1234", 15))
	inv, _ := auth.OpenInvites(t.TempDir() + "/invites.json")
	var revoked []string
	var logs bytes.Buffer
	h := Invites(reg, inv, ratelimit.New(5, 10*time.Minute, time.Minute), 168*time.Hour, "https://meet.example.com",
		func(id string) { revoked = append(revoked, id) }, slog.New(slog.NewTextHandler(&logs, nil)))
	return h, inv, &revoked, &logs
}

func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func TestInviteCreateListRevoke(t *testing.T) {
	h, inv, revoked := invitesEnv(t)
	w := call(h, "POST", "/api/rooms/ops/invites", `{"pin":"1234","hours":24}`)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var made struct{ ID, Link string }
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	token, ok := strings.CutPrefix(made.Link, "https://meet.example.com/join/ops#invite=")
	if !ok || token == "" {
		t.Fatalf("link %q", made.Link)
	}
	if _, err := inv.Check("ops", token); err != nil {
		t.Fatal("the link's token does not check")
	}
	w = call(h, "POST", "/api/rooms/ops/invites/list", `{"pin":"1234"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), made.ID) || strings.Contains(w.Body.String(), token) {
		t.Fatalf("list %d %s", w.Code, w.Body)
	}
	if w := call(h, "DELETE", "/api/rooms/ops/invites/"+made.ID, `{"pin":"1234"}`); w.Code != 204 {
		t.Fatalf("revoke %d", w.Code)
	}
	if len(*revoked) != 1 || (*revoked)[0] != made.ID {
		t.Fatalf("revoked callback %v", *revoked)
	}
}

func TestInviteRefusals(t *testing.T) {
	h, _, _ := invitesEnv(t)
	for name, c := range map[string]struct {
		method, path, body string
		want               int
	}{
		"wrong pin":    {"POST", "/api/rooms/ops/invites", `{"pin":"0000","hours":24}`, 403},
		"no room":      {"POST", "/api/rooms/zzz/invites", `{"pin":"1234","hours":24}`, 404},
		"too long":     {"POST", "/api/rooms/ops/invites", `{"pin":"1234","hours":200}`, 400},
		"not a choice": {"POST", "/api/rooms/ops/invites", `{"pin":"1234","hours":5}`, 400},
		"unknown id":   {"DELETE", "/api/rooms/ops/invites/nope", `{"pin":"1234"}`, 404},
		"bad body":     {"POST", "/api/rooms/ops/invites", `{`, 400},
		"wrong method": {"GET", "/api/rooms/ops/invites", ``, 405},
		"list get":     {"GET", "/api/rooms/ops/invites/list", ``, 405},
		"other path":   {"POST", "/api/rooms/ops/invitesx", `{"pin":"1234"}`, 404},
	} {
		if w := call(h, c.method, c.path, c.body); w.Code != c.want {
			t.Errorf("%s: %d, want %d", name, w.Code, c.want)
		}
	}
}

func TestInviteChoicesStopAtTheLongest(t *testing.T) {
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Ops", "1234", 15))
	inv, _ := auth.OpenInvites(t.TempDir() + "/invites.json")
	h := Invites(reg, inv, nil, 24*time.Hour, "https://meet.example.com", func(string) {}, nil)
	for hours, want := range map[int]int{1: 201, 24: 201, 168: 400} {
		if w := call(h, "POST", "/api/rooms/ops/invites", `{"pin":"1234","hours":`+strconv.Itoa(hours)+`}`); w.Code != want {
			t.Errorf("%d hours: %d, want %d", hours, w.Code, want)
		}
	}
}

func TestInviteRecordsItsCreatorAndNeverLogsTheToken(t *testing.T) {
	h, _, _, logs := invitesEnvLog(t)
	r := httptest.NewRequest("POST", "/api/rooms/ops/invites", strings.NewReader(`{"pin":"1234","hours":1}`))
	r = r.WithContext(auth.WithIdentity(r.Context(), auth.Identity{Provider: "yandex", Login: "ann@example.com", Name: "Анна Иванова"}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var made struct {
		ID, Link, Expires string
	}
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	if _, err := time.Parse(time.RFC3339, made.Expires); err != nil {
		t.Fatalf("expires %q", made.Expires)
	}
	_, token, _ := strings.Cut(made.Link, "#invite=")
	w = call(h, "POST", "/api/rooms/ops/invites/list", `{"pin":"1234"}`)
	var list []struct {
		ID        string `json:"id"`
		CreatedBy string `json:"created_by"`
		Expires   string `json:"expires"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].ID != made.ID || list[0].CreatedBy != "Анна Иванова" {
		t.Fatalf("list %s", w.Body)
	}
	if strings.Contains(w.Body.String(), `"hash"`) {
		t.Fatalf("list shows hashes: %s", w.Body)
	}
	call(h, "DELETE", "/api/rooms/ops/invites/"+made.ID, `{"pin":"1234"}`)
	out := logs.String()
	if strings.Contains(out, token) {
		t.Fatalf("the token is in the log: %s", out)
	}
	for _, want := range []string{"invitation created", "invitation revoked", "ann@example.com", made.ID, "room=ops"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q: %s", want, out)
		}
	}
}

func TestInviteListOfNoneIsEmpty(t *testing.T) {
	h, _, _ := invitesEnv(t)
	if w := call(h, "POST", "/api/rooms/ops/invites/list", `{"pin":"1234"}`); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
