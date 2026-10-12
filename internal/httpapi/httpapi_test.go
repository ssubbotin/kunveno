package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ssubbotin/kunveno/internal/room"
)

func TestRoomsListsWithoutPINs(t *testing.T) {
	reg := room.NewRegistry()
	r := room.New("ops", "Operations", "1234", 15)
	_ = r.Join("a", "Ann", "1234")
	reg.Add(r)
	reg.Add(room.New("dev", "Development", "5678", 20))

	rec := httptest.NewRecorder()
	Rooms(reg, nil).ServeHTTP(rec, httptest.NewRequest("GET", "/api/rooms", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0]["slug"] != "ops" || got[0]["participants"].(float64) != 1 || got[1]["capacity"].(float64) != 20 {
		t.Fatalf("body = %s", rec.Body.String())
	}
	for _, item := range got {
		if _, leaked := item["pin"]; leaked {
			t.Fatal("pin leaked into /api/rooms")
		}
	}
	rec = httptest.NewRecorder()
	Rooms(reg, nil).ServeHTTP(rec, httptest.NewRequest("PUT", "/api/rooms", nil))
	if rec.Code != 405 {
		t.Fatalf("PUT status %d, want 405", rec.Code)
	}
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestCreateAndDeleteRoom(t *testing.T) {
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Operations", "1234", 15))
	saves := 0
	h := Rooms(reg, func() error { saves++; return nil })

	rec := do(t, h, "POST", "/api/rooms", `{"name":"Retro","pin":"4321"}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"slug":"retro"`) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if saves != 1 {
		t.Fatalf("saves = %d after create", saves)
	}
	rec = do(t, h, "GET", "/api/rooms", "")
	var list []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 2 || list[0]["created"] != false || list[1]["created"] != true {
		t.Fatalf("list: %s", rec.Body)
	}

	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/api/rooms", `{"name":"","pin":"4321"}`, http.StatusBadRequest},
		{"POST", "/api/rooms", `not json`, http.StatusBadRequest},
		{"DELETE", "/api/rooms/retro", `{"pin":"0000"}`, http.StatusForbidden},
		{"DELETE", "/api/rooms/ops", `{"pin":"1234"}`, http.StatusForbidden},
		{"DELETE", "/api/rooms/nope", `{"pin":"1234"}`, http.StatusNotFound},
	} {
		if rec := do(t, h, c.method, c.path, c.body); rec.Code != c.want {
			t.Errorf("%s %s %s: %d, want %d (%s)", c.method, c.path, c.body, rec.Code, c.want, rec.Body)
		}
	}

	r, _ := reg.Get("retro")
	_ = r.Join("a", "Ann", "4321")
	if rec := do(t, h, "DELETE", "/api/rooms/retro", `{"pin":"4321"}`); rec.Code != http.StatusConflict {
		t.Fatalf("delete non-empty: %d", rec.Code)
	}
	r.Leave("a")
	if rec := do(t, h, "DELETE", "/api/rooms/retro", `{"pin":"4321"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if saves != 2 {
		t.Fatalf("saves = %d after delete", saves)
	}
}

func TestEchoRoomsIsLocalOnly(t *testing.T) {
	reg := room.NewRegistry()
	r := room.New("testing", "Testing", "1234", 15)
	reg.Add(r)
	r.SetEcho(true)
	h := EchoRooms(reg)

	req := httptest.NewRequest("GET", "/internal/echo", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `["testing"]` {
		t.Fatalf("local: %d %s", rec.Code, rec.Body)
	}

	for _, c := range []struct {
		addr, xff string
	}{{"10.8.0.5:5555", ""}, {"127.0.0.1:5555", "10.8.0.5"}} {
		req := httptest.NewRequest("GET", "/internal/echo", nil)
		req.RemoteAddr = c.addr
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("addr=%s xff=%q: %d, want 403", c.addr, c.xff, rec.Code)
		}
	}
}
