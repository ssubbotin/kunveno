package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ssubbotin/kunveno/internal/lobby"
	"github.com/ssubbotin/kunveno/internal/meetings"
	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"github.com/ssubbotin/kunveno/internal/room"
)

// historyRig: ops with two meetings. m1 (9:37–10:02) keeps notes and a summary; m2 (11:00–11:30)
// keeps notes and two recording parts.
type historyRig struct {
	mux            *http.ServeMux
	notes          *notes.Store
	m1, m2         time.Time
	part1, part2   time.Time
	pins           *ratelimit.Limiter
	reg            *room.Registry
	meets          *meetings.Store
	cfg            MeetingsConfig
	gateRefusesAll bool
}

func newHistoryRig(t *testing.T) *historyRig {
	t.Helper()
	now := meetingAt(12, 0)
	h := &historyRig{m1: meetingAt(9, 37), m2: meetingAt(11, 0), reg: room.NewRegistry(), meets: meetingsStore(t),
		pins: ratelimit.New(5, 10*time.Minute, time.Minute)}
	h.part1, h.part2 = h.m2.Add(time.Minute), h.m2.Add(15*time.Minute)
	h.reg.Add(room.New("ops", "Операции", "1234", 15))
	h.notes = notesStore(t, &now)
	rec := recordingStore(t, &now)
	recordMeeting(t, h.notes, "ops") // m1
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(h.notes.PutSummary("ops", h.m1.UnixMilli(), meetingAt(10, 2).UnixMilli(), []byte("## Кратко\nпервая")))
	must(h.notes.Begin("ops", "Операции", h.m2))
	must(h.notes.Phrase("ops", notes.Phrase{Name: "Борис", Start: h.m2.Add(time.Minute), End: h.m2.Add(time.Minute + 3*time.Second), Text: "Вторая."}))
	must(h.notes.End("ops", meetingAt(11, 30)))
	_, err := rec.Put("ops", h.m2, h.part1, strings.NewReader("part one"), 1<<20)
	must(err)
	_, err = rec.Put("ops", h.m2, h.part2, strings.NewReader("part two"), 1<<20)
	must(err)
	must(h.meets.Add("ops", meetings.Summary{Start: h.m1, End: meetingAt(10, 2), People: 7}))
	must(h.meets.Add("ops", meetings.Summary{Start: h.m2, End: meetingAt(11, 30), People: 3}))
	h.cfg = MeetingsConfig{Registry: h.reg, Kept: lobby.Kept{Notes: h.notes, NotesEnabled: true, SummaryEnabled: true,
		Recordings: rec, RecordingEnabled: true, Meetings: h.meets}, PINs: h.pins, Links: NewLinks(time.Minute), Loc: msk}
	h.mux = http.NewServeMux()
	RegisterMeetings(h.mux, h.cfg, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if h.gateRefusesAll {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	h.mux.Handle("GET /api/rooms/{slug}/recording/{token}", RecordingFile(rec, h.cfg.Links, msk))
	return h
}

func (h *historyRig) req(method, path, body, ip string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("X-Forwarded-For", ip)
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, r)
	return w
}

type listed struct {
	Start, End   string
	People       int
	NotesUntil   string `json:"notes_until"`
	Summary      string
	SummaryUntil string `json:"summary_until"`
	Parts        []struct {
		Start, Until string
		Size         int64
	}
}

func (h *historyRig) list(t *testing.T) []listed {
	t.Helper()
	w := h.req("GET", "/api/rooms/ops/meetings", "", "10.0.0.1")
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "Борис") || strings.Contains(w.Body.String(), "1234") {
		t.Fatalf("names or the PIN in the list: %s", w.Body)
	}
	var out []listed
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMeetingsList(t *testing.T) {
	h := newHistoryRig(t)
	got := h.list(t)
	if len(got) != 2 {
		t.Fatalf("meetings %+v", got)
	}
	newest, older := got[0], got[1]
	if newest.Start != h.m2.Format(time.RFC3339) || newest.People != 3 || newest.NotesUntil == "" || len(newest.Parts) != 2 ||
		newest.Parts[0].Start != h.part1.Format(time.RFC3339) || newest.Parts[0].Size != 8 || newest.Summary != "pending" {
		t.Fatalf("newest %+v", newest)
	}
	if older.Start != h.m1.Format(time.RFC3339) || older.People != 7 || older.Summary != "ready" || older.SummaryUntil == "" || len(older.Parts) != 0 {
		t.Fatalf("older %+v", older)
	}
	if w := h.req("GET", "/api/rooms/nope/meetings", "", "10.0.0.1"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown room: %d", w.Code)
	}
}

func TestPerMeetingDownloadAndDelete(t *testing.T) {
	h := newHistoryRig(t)
	m1 := fmt.Sprint(h.m1.UnixMilli())
	m2 := fmt.Sprint(h.m2.UnixMilli())
	pin := `{"pin":"1234","lang":"ru"}`
	if w := h.req("POST", "/api/rooms/ops/meetings/"+m1+"/notes", pin, "10.0.0.1"); w.Code != 200 || !strings.Contains(w.Body.String(), "Начнём.") {
		t.Fatalf("m1 notes: %d %s", w.Code, w.Body)
	}
	if w := h.req("POST", "/api/rooms/ops/meetings/"+m2+"/notes", pin, "10.0.0.1"); w.Code != 200 || !strings.Contains(w.Body.String(), "Вторая.") {
		t.Fatalf("m2 notes: %d %s", w.Code, w.Body)
	}
	if w := h.req("POST", "/api/rooms/ops/meetings/"+m1+"/summary", pin, "10.0.0.1"); w.Code != 200 || !strings.Contains(w.Body.String(), "первая") {
		t.Fatalf("m1 summary: %d %s", w.Code, w.Body)
	}
	part2 := fmt.Sprint(h.part2.UnixMilli())
	w := h.req("POST", "/api/rooms/ops/meetings/"+m2+"/recording/"+part2, pin, "10.0.0.1")
	var link struct{ URL string }
	_ = json.Unmarshal(w.Body.Bytes(), &link)
	if w.Code != 200 || link.URL == "" {
		t.Fatalf("part link: %d %s", w.Code, w.Body)
	}
	if f := h.req("GET", link.URL, "", "10.0.0.1"); f.Code != 200 || f.Body.String() != "part two" {
		t.Fatalf("part file: %d %q", f.Code, f.Body.String())
	}

	if w := h.req("DELETE", "/api/rooms/ops/meetings/"+m1+"/notes", pin, "10.0.0.1"); w.Code != http.StatusNoContent {
		t.Fatalf("delete m1 notes: %d %s", w.Code, w.Body)
	}
	if w := h.req("DELETE", "/api/rooms/ops/meetings/"+m2+"/recording/"+part2, pin, "10.0.0.1"); w.Code != http.StatusNoContent {
		t.Fatalf("delete part: %d %s", w.Code, w.Body)
	}
	got := h.list(t)
	if got[1].NotesUntil != "" || got[1].Summary != "ready" {
		t.Fatalf("m1 after deleting its notes %+v", got[1])
	}
	if got[0].NotesUntil == "" || len(got[0].Parts) != 1 {
		t.Fatalf("m2 touched %+v", got[0])
	}
	if w := h.req("POST", "/api/rooms/ops/meetings/"+m1+"/notes", `{"pin":"0000"}`, "10.0.0.2"); w.Code != http.StatusForbidden {
		t.Fatalf("wrong PIN: %d", w.Code)
	}
	if w := h.req("POST", "/api/rooms/ops/meetings/soon/notes", pin, "10.0.0.1"); w.Code != http.StatusNotFound {
		t.Fatalf("bad meeting: %d", w.Code)
	}
}

// The routes without a meeting serve the latest meeting that has the item.
func TestOldRoutesMeanLatest(t *testing.T) {
	h := newHistoryRig(t)
	notesDL := NotesDownload(h.reg, h.notes, true, nil, msk)
	if w := post(notesDL, "/api/rooms/ops/notes", `{"pin":"1234"}`, "10.0.0.1"); w.Code != 200 || !strings.Contains(w.Body.String(), "Вторая.") {
		t.Fatalf("old notes route: %d %s", w.Code, w.Body)
	}
	sumDL := SummaryDownload(h.reg, h.notes, true, nil, msk)
	if w := post(sumDL, "/api/rooms/ops/summary", `{"pin":"1234"}`, "10.0.0.1"); w.Code != http.StatusNotFound {
		t.Fatalf("old summary route: the latest meeting's summary is pending, want 404: %d", w.Code)
	}
}

func TestMeetingsListWithSignIn(t *testing.T) {
	h := newHistoryRig(t)
	h.gateRefusesAll = true
	if w := h.req("GET", "/api/rooms/ops/meetings", "", "10.0.0.1"); w.Code != http.StatusUnauthorized {
		t.Fatalf("list without a session: %d", w.Code)
	}
	m1 := fmt.Sprint(h.m1.UnixMilli())
	if w := h.req("POST", "/api/rooms/ops/meetings/"+m1+"/notes", `{"pin":"1234"}`, "10.0.0.1"); w.Code != http.StatusUnauthorized {
		t.Fatalf("download without a session: %d", w.Code)
	}
}

// The room list counts the meetings.
func TestRoomListCountsMeetings(t *testing.T) {
	h := newHistoryRig(t)
	rec := do(t, Rooms(RoomsConfig{Registry: h.reg, Kept: h.cfg.Kept}), "GET", "/api/rooms", "")
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list[0]["meetings"] != float64(2) {
		t.Fatalf("meetings = %v", list[0]["meetings"])
	}
}

// last_meeting is the newest meeting that keeps something, whichever kind of material it keeps.
func TestLastMeetingIsTheNewest(t *testing.T) {
	h := newHistoryRig(t)
	// The older meeting gets a video too; once the newer one's video goes, the latest video is the
	// older meeting's, while the newer meeting still keeps its notes.
	if _, err := h.cfg.Kept.Recordings.Put("ops", h.m1, h.m1, strings.NewReader("old video"), 1<<20); err != nil {
		t.Fatal(err)
	}
	rec := do(t, Rooms(RoomsConfig{Registry: h.reg, Kept: h.cfg.Kept}), "GET", "/api/rooms", "")
	if got := lastMeetings(t, rec.Body.String())["ops"]; got == nil || got.Start != h.m2.Format(time.RFC3339) {
		t.Fatalf("last_meeting %+v, want the 11:00 meeting", got)
	}
	m2 := fmt.Sprint(h.m2.UnixMilli())
	for _, p := range []string{fmt.Sprint(h.part1.UnixMilli()), fmt.Sprint(h.part2.UnixMilli())} {
		if w := h.req("DELETE", "/api/rooms/ops/meetings/"+m2+"/recording/"+p, `{"pin":"1234"}`, "10.0.0.1"); w.Code != http.StatusNoContent {
			t.Fatalf("delete part: %d", w.Code)
		}
	}
	rec = do(t, Rooms(RoomsConfig{Registry: h.reg, Kept: h.cfg.Kept}), "GET", "/api/rooms", "")
	if got := lastMeetings(t, rec.Body.String())["ops"]; got == nil || got.Start != h.m2.Format(time.RFC3339) {
		t.Fatalf("last_meeting after its video went %+v, want the 11:00 meeting (its notes stay)", got)
	}
}

// The list names each meeting and part exactly (Unix ms), so a client builds the item routes from it
// even for starts that are not on a whole second.
func TestListNamesMeetingsExactly(t *testing.T) {
	h := newHistoryRig(t)
	odd := meetingAt(12, 0).Add(5*time.Second + 123*time.Millisecond)
	if err := h.notes.Begin("ops", "Операции", odd); err != nil {
		t.Fatal(err)
	}
	if err := h.notes.End("ops", odd.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.cfg.Kept.Recordings.Put("ops", odd, odd.Add(1500*time.Millisecond), strings.NewReader("p"), 1<<20); err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Meeting int64
		Parts   []struct{ Part int64 }
	}
	w := h.req("GET", "/api/rooms/ops/meetings", "", "10.0.0.1")
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if rows[0].Meeting != odd.UnixMilli() || len(rows[0].Parts) != 1 || rows[0].Parts[0].Part != odd.Add(1500*time.Millisecond).UnixMilli() {
		t.Fatalf("rows %+v", rows[0])
	}
	base := fmt.Sprintf("/api/rooms/ops/meetings/%d", rows[0].Meeting)
	if w := h.req("POST", base+"/notes", `{"pin":"1234"}`, "10.0.0.1"); w.Code != 200 {
		t.Fatalf("notes by the listed meeting: %d", w.Code)
	}
	if w := h.req("POST", fmt.Sprintf("%s/recording/%d", base, rows[0].Parts[0].Part), `{"pin":"1234"}`, "10.0.0.1"); w.Code != 200 {
		t.Fatalf("part by the listed part: %d", w.Code)
	}
}
