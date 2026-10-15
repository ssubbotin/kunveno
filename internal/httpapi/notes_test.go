package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"github.com/ssubbotin/kunveno/internal/room"
)

var msk = time.FixedZone("MSK", 3*3600)

func meetingAt(h, m int) time.Time { return time.Date(2026, 10, 14, h, m, 0, 0, msk) }

func notesStore(t *testing.T, now *time.Time) *notes.Store {
	t.Helper()
	s, err := notes.Open(t.TempDir(), 24*time.Hour, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func recordMeeting(t *testing.T, s *notes.Store, slug string) {
	t.Helper()
	for _, err := range []error{
		s.Begin(slug, "Операции", meetingAt(9, 37)),
		s.Phrase(slug, notes.Phrase{Name: "Анна", Start: meetingAt(9, 38), End: meetingAt(9, 38).Add(3 * time.Second), Text: "Начнём."}),
		s.End(slug, meetingAt(10, 2)),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func post(h http.Handler, path, body, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("X-Forwarded-For", ip)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNotesDownload(t *testing.T) {
	now := meetingAt(10, 5)
	store := notesStore(t, &now)
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Операции", "1234", 15))
	reg.Add(room.New("dev", "Разработка", "5678", 15))
	recordMeeting(t, store, "ops")
	if err := store.Begin("dev", "Разработка", meetingAt(10, 0)); err != nil { // still being written
		t.Fatal(err)
	}
	h := NotesDownload(reg, store, true, ratelimit.New(5, 10*time.Minute, time.Minute), msk)

	rec := post(h, "/api/rooms/ops/notes", `{"pin":"1234","lang":"en"}`, "10.0.0.1")
	if rec.Code != 200 {
		t.Fatalf("download: %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="ops-2026-10-14-0937.md"` {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if body := rec.Body.String(); !strings.HasPrefix(body, "# Meeting notes “Операции”\n") || !strings.Contains(body, "**09:38 Анна:** Начнём.") {
		t.Fatalf("body:\n%s", body)
	}
	if rec := post(h, "/api/rooms/ops/notes", `{"pin":"1234"}`, "10.0.0.1"); !strings.HasPrefix(rec.Body.String(), "# Заметки встречи «Операции»") {
		t.Fatalf("default language: %s", rec.Body)
	}
	for _, c := range []struct {
		path, body string
		want       int
	}{
		{"/api/rooms/ops/notes", `{"pin":"0000"}`, http.StatusForbidden},
		{"/api/rooms/nope/notes", `{"pin":"1234"}`, http.StatusNotFound},
		{"/api/rooms/dev/notes", `{"pin":"5678"}`, http.StatusNotFound},
		{"/api/rooms/ops/notes", `not json`, http.StatusBadRequest},
		{"/api/rooms/ops/x/notes", `{"pin":"1234"}`, http.StatusNotFound},
	} {
		if rec := post(h, c.path, c.body, "10.0.0.2"); rec.Code != c.want {
			t.Errorf("%s %s: %d, want %d", c.path, c.body, rec.Code, c.want)
		}
	}
	now = meetingAt(10, 2).Add(24*time.Hour + time.Minute)
	if rec := post(h, "/api/rooms/ops/notes", `{"pin":"1234"}`, "10.0.0.3"); rec.Code != http.StatusNotFound {
		t.Fatalf("expired: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/rooms/ops/notes", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
	if rec := post(NotesDownload(reg, nil, false, nil, msk), "/api/rooms/ops/notes", `{"pin":"1234"}`, "10.0.0.4"); rec.Code != http.StatusNotFound {
		t.Fatalf("notes disabled: %d", rec.Code)
	}
}

// Notes off: 404 right after the method check, before body parsing, PIN check or rate limiting.
func TestNotesDownloadDisabledAnswers404First(t *testing.T) {
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Операции", "1234", 15))
	pins := ratelimit.New(1, 10*time.Minute, time.Minute)
	now := meetingAt(10, 5)
	store := notesStore(t, &now)
	recordMeeting(t, store, "ops")
	h := NotesDownload(reg, store, false, pins, msk)
	for i, body := range []string{`{"pin":"0000"}`, `not json`, `{"pin":"0000"}`} {
		if rec := post(h, "/api/rooms/ops/notes", body, "10.0.0.9"); rec.Code != http.StatusNotFound {
			t.Fatalf("attempt %d: %d, want 404", i, rec.Code)
		}
	}
	if ok, _ := pins.Allow("10.0.0.9"); !ok {
		t.Fatal("disabled notes counted wrong PINs")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/rooms/ops/notes", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
}

func TestNotesDownloadSharesThePINLimit(t *testing.T) {
	now := meetingAt(10, 5)
	store := notesStore(t, &now)
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Операции", "1234", 15))
	recordMeeting(t, store, "ops")
	pins := ratelimit.New(5, 10*time.Minute, time.Minute)
	h := NotesDownload(reg, store, true, pins, msk)
	for i := 0; i < 5; i++ {
		if rec := post(h, "/api/rooms/ops/notes", `{"pin":"0000"}`, "10.0.0.66"); rec.Code != http.StatusForbidden {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	rec := post(h, "/api/rooms/ops/notes", `{"pin":"1234"}`, "10.0.0.66")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("blocked: %d retry-after=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if ok, _ := pins.Allow("10.0.0.66"); ok {
		t.Fatal("the shared limiter does not block the address")
	}
}

func TestRoomsListShowsNotesUntil(t *testing.T) {
	now := meetingAt(10, 5)
	store := notesStore(t, &now)
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Операции", "1234", 15))
	reg.Add(room.New("dev", "Разработка", "5678", 15))
	recordMeeting(t, store, "ops")
	rec := do(t, Rooms(reg, nil, nil, store, true, nil, false, nil), "GET", "/api/rooms", "")
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	got, err := time.Parse(time.RFC3339, list[0]["notes_until"].(string))
	if err != nil || !got.Equal(meetingAt(10, 2).Add(24*time.Hour)) {
		t.Fatalf("notes_until = %v (%v)", list[0]["notes_until"], err)
	}
	if _, has := list[1]["notes_until"]; has {
		t.Fatal("notes_until on a room without notes")
	}
	if strings.Contains(rec.Body.String(), `"pin"`) {
		t.Fatal("pin in /api/rooms")
	}
	rec = do(t, Rooms(reg, nil, nil, store, false, nil, false, nil), "GET", "/api/rooms", "")
	if strings.Contains(rec.Body.String(), "notes_until") {
		t.Fatal("notes_until while notes are disabled")
	}
}

// Review focus: a room deleted and created again under the same slug does not inherit notes.
func TestDeletingARoomDeletesItsNotes(t *testing.T) {
	now := meetingAt(10, 5)
	store := notesStore(t, &now)
	reg := room.NewRegistry()
	if _, err := reg.Create("Retro", "432100"); err != nil {
		t.Fatal(err)
	}
	recordMeeting(t, store, "retro")
	h := Rooms(reg, nil, nil, store, true, nil, false, nil)
	if rec := do(t, h, "DELETE", "/api/rooms/retro", `{"pin":"432100"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if _, ok := store.Until("retro"); ok {
		t.Fatal("notes outlived their room")
	}
	if rec := do(t, h, "POST", "/api/rooms", `{"name":"Retro","pin":"654321"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create again: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/api/rooms", ""); strings.Contains(rec.Body.String(), "notes_until") {
		t.Fatalf("the new room shows the old notes: %s", rec.Body)
	}
}

// Notes disabled (flag off) but the store is still passed: deleting a room still deletes its notes.
func TestDeletingARoomDeletesItsNotesWhileDisabled(t *testing.T) {
	now := meetingAt(10, 5)
	store := notesStore(t, &now)
	reg := room.NewRegistry()
	if _, err := reg.Create("Retro", "432100"); err != nil {
		t.Fatal(err)
	}
	recordMeeting(t, store, "retro")
	h := Rooms(reg, nil, nil, store, false, nil, false, nil)
	if rec := do(t, h, "GET", "/api/rooms", ""); strings.Contains(rec.Body.String(), "notes_until") {
		t.Fatalf("notes_until while disabled: %s", rec.Body)
	}
	if rec := do(t, h, "DELETE", "/api/rooms/retro", `{"pin":"432100"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if _, ok := store.Until("retro"); ok {
		t.Fatal("notes outlived their room while disabled")
	}
}

func TestNotesRoomsIsLocalOnly(t *testing.T) {
	reg := room.NewRegistry()
	r := room.New("ops", "Ops", "1234", 15)
	reg.Add(r)
	r.SetNotes(true)
	for _, c := range []struct {
		addr, xff string
		enabled   bool
		code      int
		body      string
	}{
		{"127.0.0.1:5555", "", true, 200, `["ops"]`},
		{"127.0.0.1:5555", "", false, 200, `[]`},
		{"10.8.0.5:5555", "", true, http.StatusForbidden, ""},
		{"127.0.0.1:5555", "10.8.0.5", true, http.StatusForbidden, ""},
	} {
		req := httptest.NewRequest("GET", "/internal/notes", nil)
		req.RemoteAddr = c.addr
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		rec := httptest.NewRecorder()
		NotesRooms(reg, c.enabled).ServeHTTP(rec, req)
		if rec.Code != c.code || (c.body != "" && strings.TrimSpace(rec.Body.String()) != c.body) {
			t.Errorf("addr=%s xff=%q enabled=%v: %d %s", c.addr, c.xff, c.enabled, rec.Code, rec.Body)
		}
	}
}

func TestInfoNotesLiveNeedsNotes(t *testing.T) {
	rec := httptest.NewRecorder()
	Info("T", nil, false, false, true, false, 15).ServeHTTP(rec, httptest.NewRequest("GET", "/api/info", nil))
	if !strings.Contains(rec.Body.String(), `"notes":false`) || !strings.Contains(rec.Body.String(), `"notes_live":false`) {
		t.Fatalf("%s", rec.Body)
	}
}

func TestDeletingARoomForgetsNotesEvenIfTheFileStays(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	now := meetingAt(10, 5)
	store, err := notes.Open(dir, 24*time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	reg := room.NewRegistry()
	if _, err := reg.Create("Retro", "432100"); err != nil {
		t.Fatal(err)
	}
	recordMeeting(t, store, "retro")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	h := Rooms(reg, nil, nil, store, true, nil, false, nil)
	if rec := do(t, h, "DELETE", "/api/rooms/retro", `{"pin":"432100"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "POST", "/api/rooms", `{"name":"Retro","pin":"654321"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create again: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/api/rooms", ""); strings.Contains(rec.Body.String(), "notes_until") {
		t.Fatalf("the new room lists the old notes: %s", rec.Body)
	}
}

func del(h http.Handler, path, body, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("DELETE", path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("X-Forwarded-For", ip)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNotesDelete(t *testing.T) {
	now := meetingAt(10, 5)
	store := notesStore(t, &now)
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Операции", "1234", 15))
	reg.Add(room.New("dev", "Разработка", "5678", 15))
	reg.Add(room.New("qa", "Тесты", "9999", 15))
	recordMeeting(t, store, "ops")
	if err := store.Begin("dev", "Разработка", meetingAt(10, 0)); err != nil { // still being recorded
		t.Fatal(err)
	}
	h := NotesDelete(reg, store, true, ratelimit.New(5, 10*time.Minute, time.Minute))
	for _, c := range []struct {
		path, body string
		want       int
		error      string
	}{
		{"/api/rooms/ops/notes", `{"pin":"0000"}`, http.StatusForbidden, ""},
		{"/api/rooms/nope/notes", `{"pin":"1234"}`, http.StatusNotFound, ""},
		{"/api/rooms/dev/notes", `{"pin":"5678"}`, http.StatusConflict, "notes are being recorded"},
		{"/api/rooms/qa/notes", `{"pin":"9999"}`, http.StatusNotFound, "no notes"},
		{"/api/rooms/ops/notes", `not json`, http.StatusBadRequest, ""},
		{"/api/rooms/ops/x/notes", `{"pin":"1234"}`, http.StatusNotFound, ""},
	} {
		rec := del(h, c.path, c.body, "10.0.0.2")
		if rec.Code != c.want {
			t.Errorf("%s %s: %d, want %d", c.path, c.body, rec.Code, c.want)
		}
		if c.error != "" && !strings.Contains(rec.Body.String(), `"error":"`+c.error+`"`) {
			t.Errorf("%s: body %s, want error %q", c.path, rec.Body, c.error)
		}
		if strings.Contains(rec.Body.String(), "1234") || strings.Contains(rec.Body.String(), "5678") {
			t.Errorf("%s: a PIN in the answer: %s", c.path, rec.Body)
		}
	}
	if _, ok := store.Until("ops"); !ok {
		t.Fatal("refused requests deleted the notes")
	}
	rec := del(h, "/api/rooms/ops/notes", `{"pin":"1234"}`, "10.0.0.1")
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if _, ok := store.Until("ops"); ok {
		t.Fatal("notes still ready after delete")
	}
	if _, err := store.Load("ops"); err != notes.ErrNoNotes {
		t.Fatalf("Load after delete: %v", err)
	}
	if rec := do(t, Rooms(reg, nil, nil, store, true, nil, false, nil), "GET", "/api/rooms", ""); strings.Contains(rec.Body.String(), "notes_until") {
		t.Fatalf("the list still shows the deleted notes: %s", rec.Body)
	}
	if rec := del(h, "/api/rooms/ops/notes", `{"pin":"1234"}`, "10.0.0.1"); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/rooms/ops/notes", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
}

func TestNotesDeleteExpired(t *testing.T) {
	now := meetingAt(10, 5)
	store := notesStore(t, &now)
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Операции", "1234", 15))
	recordMeeting(t, store, "ops")
	now = meetingAt(10, 2).Add(24*time.Hour + time.Minute)
	if rec := del(NotesDelete(reg, store, true, nil), "/api/rooms/ops/notes", `{"pin":"1234"}`, "10.0.0.3"); rec.Code != http.StatusNotFound {
		t.Fatalf("expired: %d", rec.Code)
	}
}

// Notes off: 404 right after the method check, before body parsing, PIN check or rate limiting.
func TestNotesDeleteDisabledAnswers404First(t *testing.T) {
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Операции", "1234", 15))
	pins := ratelimit.New(1, 10*time.Minute, time.Minute)
	now := meetingAt(10, 5)
	store := notesStore(t, &now)
	recordMeeting(t, store, "ops")
	h := NotesDelete(reg, store, false, pins)
	for i, body := range []string{`{"pin":"0000"}`, `not json`, `{"pin":"1234"}`} {
		if rec := del(h, "/api/rooms/ops/notes", body, "10.0.0.9"); rec.Code != http.StatusNotFound {
			t.Fatalf("attempt %d: %d, want 404", i, rec.Code)
		}
	}
	if ok, _ := pins.Allow("10.0.0.9"); !ok {
		t.Fatal("disabled notes counted wrong PINs")
	}
	if _, ok := store.Until("ops"); !ok {
		t.Fatal("notes deleted while notes are disabled")
	}
	if rec := del(NotesDelete(reg, nil, true, nil), "/api/rooms/ops/notes", `{"pin":"1234"}`, "10.0.0.9"); rec.Code != http.StatusNotFound {
		t.Fatalf("no store: %d", rec.Code)
	}
}

func TestNotesDeleteSharesThePINLimit(t *testing.T) {
	now := meetingAt(10, 5)
	store := notesStore(t, &now)
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Операции", "1234", 15))
	recordMeeting(t, store, "ops")
	pins := ratelimit.New(5, 10*time.Minute, time.Minute)
	h := NotesDelete(reg, store, true, pins)
	for i := 0; i < 5; i++ {
		if rec := del(h, "/api/rooms/ops/notes", `{"pin":"0000"}`, "10.0.0.66"); rec.Code != http.StatusForbidden {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	rec := del(h, "/api/rooms/ops/notes", `{"pin":"1234"}`, "10.0.0.66")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("blocked: %d retry-after=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if _, ok := store.Until("ops"); !ok {
		t.Fatal("a blocked address deleted the notes")
	}
}
