package httpapi

import (
	"encoding/json"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/ssubbotin/kunveno/internal/metrics"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ssubbotin/kunveno/internal/auth"
	"github.com/ssubbotin/kunveno/internal/room"
)

func TestRoomsListsWithoutPINs(t *testing.T) {
	reg := room.NewRegistry()
	r := room.New("ops", "Operations", "1234", 15)
	_ = r.Join("a", "Ann", "1234")
	reg.Add(r)
	reg.Add(room.New("dev", "Development", "5678", 20))

	rec := httptest.NewRecorder()
	Rooms(RoomsConfig{Registry: reg}).ServeHTTP(rec, httptest.NewRequest("GET", "/api/rooms", nil))
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
	Rooms(RoomsConfig{Registry: reg}).ServeHTTP(rec, httptest.NewRequest("PUT", "/api/rooms", nil))
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
	var deleted []string
	h := Rooms(RoomsConfig{Registry: reg, Persist: func() error { saves++; return nil },
		Deleted: func(slug string) error { deleted = append(deleted, slug); return nil }})
	createdBefore := testutil.ToFloat64(metrics.RoomsCreated)
	deletedBefore := testutil.ToFloat64(metrics.RoomsDeleted)

	rec := do(t, h, "POST", "/api/rooms", `{"name":"Retro","pin":"432100"}`)
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
		{"POST", "/api/rooms", `{"name":"","pin":"432100"}`, http.StatusBadRequest},
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
	_ = r.Join("a", "Ann", "432100")
	if rec := do(t, h, "DELETE", "/api/rooms/retro", `{"pin":"432100"}`); rec.Code != http.StatusConflict {
		t.Fatalf("delete non-empty: %d", rec.Code)
	}
	r.Leave("a")
	if rec := do(t, h, "DELETE", "/api/rooms/retro", `{"pin":"432100"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if saves != 2 {
		t.Fatalf("saves = %d after delete", saves)
	}
	if len(deleted) != 1 || deleted[0] != "retro" {
		t.Fatalf("the deletion hook ran for %v; only the deleted room must reach it", deleted)
	}
	if d := testutil.ToFloat64(metrics.RoomsCreated) - createdBefore; d != 1 {
		t.Errorf("rooms created counted %v, want 1", d)
	}
	if d := testutil.ToFloat64(metrics.RoomsDeleted) - deletedBefore; d != 1 {
		t.Errorf("rooms deleted counted %v, want 1", d)
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

func TestDeleteGuessingIsBlocked(t *testing.T) {
	reg := room.NewRegistry()
	if _, err := reg.Create("Retro", "432100"); err != nil {
		t.Fatal(err)
	}
	h := Rooms(RoomsConfig{Registry: reg, PINs: ratelimit.New(5, 10*time.Minute, time.Minute)})
	try := func(pin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("DELETE", "/api/rooms/retro", strings.NewReader(`{"pin":"`+pin+`"}`))
		req.RemoteAddr = "127.0.0.1:1"
		req.Header.Set("X-Forwarded-For", "10.0.0.66")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for i := 0; i < 5; i++ {
		if rec := try("000000"); rec.Code != http.StatusForbidden {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	rec := try("432100")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("blocked delete: %d retry-after=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if _, ok := reg.Get("retro"); !ok {
		t.Fatal("room deleted while blocked")
	}
}

func TestInfo(t *testing.T) {
	rec := httptest.NewRecorder()
	Info("Конференц связь", []string{"👍", "☕"}, true, true, false, false, 15, nil).ServeHTTP(rec, httptest.NewRequest("GET", "/api/info", nil))
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"auth":{"enabled":false,"providers":[]},"noise_suppression":true,"notes":true,"notes_live":false,"reactions":["👍","☕"],"reconnect_grace":15,"recording":false,"title":"Конференц связь"}` {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestInfoSignIn(t *testing.T) {
	rec := httptest.NewRecorder()
	Info("T", nil, false, false, false, false, 15, &SignInInfo{Enabled: true, Providers: []auth.ProviderInfo{{Type: "yandex", Label: "Яндекс"}}}).
		ServeHTTP(rec, httptest.NewRequest("GET", "/api/info", nil))
	if !strings.Contains(rec.Body.String(), `"auth":{"enabled":true,"providers":[{"type":"yandex","label":"Яндекс"}]}`) {
		t.Fatalf("%s", rec.Body)
	}
	rec = httptest.NewRecorder()
	Info("T", nil, false, false, false, false, 15, &SignInInfo{}).ServeHTTP(rec, httptest.NewRequest("GET", "/api/info", nil))
	if !strings.Contains(rec.Body.String(), `"auth":{"enabled":false,"providers":[]}`) {
		t.Fatalf("off: %s", rec.Body)
	}
}

func TestInfoRecording(t *testing.T) {
	rec := httptest.NewRecorder()
	Info("T", nil, false, false, false, true, 30, nil).ServeHTTP(rec, httptest.NewRequest("GET", "/api/info", nil))
	if !strings.Contains(rec.Body.String(), `"recording":true`) || !strings.Contains(rec.Body.String(), `"reconnect_grace":30`) {
		t.Fatalf("%s", rec.Body)
	}
}
