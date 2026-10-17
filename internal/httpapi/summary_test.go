package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ssubbotin/kunveno/internal/lobby"
	"github.com/ssubbotin/kunveno/internal/meetings"
	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"github.com/ssubbotin/kunveno/internal/room"
)

// internal sends a request to the summary endpoints as the add-on does: from loopback, not through
// nginx.
func internal(h http.Handler, method, target string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	req.RemoteAddr = "127.0.0.1:40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func summaryRig(t *testing.T) (*room.Registry, *notes.Store, *time.Time, int64) {
	t.Helper()
	now := meetingAt(10, 5)
	store := notesStore(t, &now)
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Операции", "1234", 15))
	recordMeeting(t, store, "ops")
	return reg, store, &now, meetingAt(10, 2).UnixMilli()
}

func TestSummaryQueueAndUpload(t *testing.T) {
	reg, store, _, end := summaryRig(t)
	h := SummaryInternal(store, true, msk)

	rec := internal(h, "GET", "/internal/notes/summaries", nil)
	if want := fmt.Sprintf(`[{"slug":"ops","end":%d}]`, end); rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("queue: %d %s, want %s", rec.Code, rec.Body, want)
	}
	rec = internal(h, "GET", fmt.Sprintf("/internal/notes/summaries/ops?end=%d", end), nil)
	var got struct {
		Room, Start, Notes string
		End                int64
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != 200 || err != nil {
		t.Fatalf("notes: %d %s (%v)", rec.Code, rec.Body, err)
	}
	if got.Room != "Операции" || got.End != end || !strings.Contains(got.Notes, "Начнём.") || got.Start != meetingAt(9, 37).Format(time.RFC3339) {
		t.Fatalf("notes = %+v", got)
	}
	rec = internal(h, "PUT", fmt.Sprintf("/internal/notes/summaries/ops?end=%d", end), strings.NewReader("## Кратко\nok\n"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	if rec := internal(h, "GET", "/internal/notes/summaries", nil); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("queue after upload: %s", rec.Body)
	}

	dl := SummaryDownload(reg, store, true, ratelimit.New(5, 10*time.Minute, time.Minute), msk)
	rec = post(dl, "/api/rooms/ops/summary", `{"pin":"1234","lang":"ru"}`, "10.0.0.1")
	if rec.Code != 200 {
		t.Fatalf("download: %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="ops-2026-10-14-0937-summary.md"` {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable")
	}
	if body := rec.Body.String(); !strings.HasPrefix(body, "# Итоги встречи «Операции»\n") || !strings.Contains(body, "## Кратко\nok\n") {
		t.Fatalf("body:\n%s", body)
	}
}

func TestSummaryStale(t *testing.T) {
	_, store, _, end := summaryRig(t)
	h := SummaryInternal(store, true, msk)
	for _, target := range []string{
		fmt.Sprintf("/internal/notes/summaries/ops?end=%d", end+1),
		fmt.Sprintf("/internal/notes/summaries/nope?end=%d", end),
	} {
		if rec := internal(h, "PUT", target, strings.NewReader("x")); rec.Code != http.StatusConflict {
			t.Errorf("PUT %s: %d", target, rec.Code)
		}
		if rec := internal(h, "GET", target, nil); rec.Code != http.StatusConflict {
			t.Errorf("GET %s: %d", target, rec.Code)
		}
		if rec := internal(h, "POST", strings.Replace(target, "?", "/failed?", 1), nil); rec.Code != http.StatusConflict {
			t.Errorf("failed %s: %d", target, rec.Code)
		}
	}
	// Notes switched on again after the meeting: the old end no longer names the room's notes.
	if err := store.Resume("ops", meetingAt(10, 3)); err != nil {
		t.Fatal(err)
	}
	if rec := internal(h, "PUT", fmt.Sprintf("/internal/notes/summaries/ops?end=%d", end), strings.NewReader("x")); rec.Code != http.StatusConflict {
		t.Fatalf("PUT after Resume: %d", rec.Code)
	}
	if rec := internal(h, "PUT", "/internal/notes/summaries/ops?end=soon", strings.NewReader("x")); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad end: %d", rec.Code)
	}
}

func TestSummaryBadBody(t *testing.T) {
	_, store, _, end := summaryRig(t)
	h := SummaryInternal(store, true, msk)
	target := fmt.Sprintf("/internal/notes/summaries/ops?end=%d", end)
	for name, body := range map[string]string{
		"empty":       "",
		"blank":       " \n\t ",
		"not utf-8":   "ok \xff\xfe",
		"over 256KiB": strings.Repeat("a", 256<<10+1),
	} {
		if rec := internal(h, "PUT", target, strings.NewReader(body)); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if store.SummaryState("ops") != notes.SummaryPending {
		t.Fatal("a bad body was stored")
	}
}

func TestSummaryOutsideRefused(t *testing.T) {
	_, store, _, end := summaryRig(t)
	h := SummaryInternal(store, true, msk)
	for _, c := range []struct{ method, target string }{
		{"GET", "/internal/notes/summaries"},
		{"GET", fmt.Sprintf("/internal/notes/summaries/ops?end=%d", end)},
		{"PUT", fmt.Sprintf("/internal/notes/summaries/ops?end=%d", end)},
		{"POST", fmt.Sprintf("/internal/notes/summaries/ops/failed?end=%d", end)},
	} {
		for _, viaProxy := range []bool{true, false} {
			req := httptest.NewRequest(c.method, c.target, strings.NewReader("x"))
			req.RemoteAddr = "127.0.0.1:40000"
			if viaProxy {
				req.Header.Set("X-Forwarded-For", "10.0.0.9")
			} else {
				req.RemoteAddr = "10.0.0.9:40000"
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s proxy=%v: %d", c.method, c.target, viaProxy, rec.Code)
			}
		}
	}
	if store.SummaryState("ops") != notes.SummaryPending {
		t.Fatal("an outside request changed the state")
	}
}

func TestSummaryFailed(t *testing.T) {
	_, store, _, end := summaryRig(t)
	h := SummaryInternal(store, true, msk)
	if rec := internal(h, "POST", fmt.Sprintf("/internal/notes/summaries/ops/failed?end=%d", end), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("failed: %d %s", rec.Code, rec.Body)
	}
	if store.SummaryState("ops") != notes.SummaryFailed {
		t.Fatal("not marked failed")
	}
}

func TestSummaryOff(t *testing.T) {
	reg, store, _, end := summaryRig(t)
	h := SummaryInternal(store, false, msk)
	if rec := internal(h, "GET", "/internal/notes/summaries", nil); rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("queue while off: %d %s", rec.Code, rec.Body)
	}
	if rec := internal(h, "PUT", fmt.Sprintf("/internal/notes/summaries/ops?end=%d", end), strings.NewReader("x")); rec.Code != http.StatusNotFound {
		t.Fatalf("PUT while off: %d", rec.Code)
	}
	if rec := post(SummaryDownload(reg, store, false, nil, msk), "/api/rooms/ops/summary", `{"pin":"1234"}`, "10.0.0.1"); rec.Code != http.StatusNotFound {
		t.Fatalf("download while off: %d", rec.Code)
	}
}

func TestSummaryDownloadPIN(t *testing.T) {
	reg, store, _, _ := summaryRig(t)
	pins := ratelimit.New(3, 10*time.Minute, time.Minute)
	dl := SummaryDownload(reg, store, true, pins, msk)
	if rec := post(dl, "/api/rooms/ops/summary", `{"pin":"1234"}`, "10.0.0.1"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no summary") {
		t.Fatalf("none yet: %d %s", rec.Code, rec.Body)
	}
	for i := 0; i < 3; i++ {
		if rec := post(dl, "/api/rooms/ops/summary", `{"pin":"0000"}`, "10.0.0.2"); rec.Code != http.StatusForbidden {
			t.Fatalf("wrong PIN %d: %d", i, rec.Code)
		}
	}
	if rec := post(dl, "/api/rooms/ops/summary", `{"pin":"1234"}`, "10.0.0.2"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after the limit: %d", rec.Code)
	}
}

func TestRoomListSummaryState(t *testing.T) {
	reg, store, now, end := summaryRig(t)
	reg.Add(room.New("dev", "Разработка", "5678", 15))
	state := func(kept lobby.Kept) (ops, dev any) {
		t.Helper()
		var list []map[string]any
		rec := do(t, Rooms(RoomsConfig{Registry: reg, Kept: kept}), "GET", "/api/rooms", "")
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		for _, it := range list {
			if it["slug"] == "ops" {
				ops = it["summary"]
			} else {
				dev = it["summary"]
			}
		}
		return ops, dev
	}
	on := lobby.Kept{Notes: store, NotesEnabled: true, SummaryEnabled: true}
	if ops, dev := state(on); ops != "pending" || dev != nil {
		t.Fatalf("pending: ops=%v dev=%v", ops, dev)
	}
	if err := store.FailSummary("ops", end); err != nil {
		t.Fatal(err)
	}
	if ops, _ := state(on); ops != "failed" {
		t.Fatalf("failed: %v", ops)
	}
	if err := store.PutSummary("ops", end, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if ops, _ := state(on); ops != "ready" {
		t.Fatalf("ready: %v", ops)
	}
	if ops, _ := state(lobby.Kept{Notes: store, NotesEnabled: true}); ops != nil {
		t.Fatalf("summaries off: %v", ops)
	}
	*now = meetingAt(10, 2).Add(25 * time.Hour) // the notes expired
	if ops, _ := state(on); ops != nil {
		t.Fatalf("expired notes: %v", ops)
	}
}

func TestSummaryDelete(t *testing.T) {
	reg, store, _, end := summaryRig(t)
	if err := store.PutSummary("ops", end, []byte("x")); err != nil {
		t.Fatal(err)
	}
	h := SummaryDelete(reg, store, true, ratelimit.New(5, 10*time.Minute, time.Minute))
	if rec := del(h, "/api/rooms/ops/summary", `{"pin":"0000"}`, "10.0.0.1"); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong PIN: %d", rec.Code)
	}
	if rec := del(h, "/api/rooms/ops/summary", `{"pin":"1234"}`, "10.0.0.2"); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if store.SummaryState("ops") != notes.SummaryNone {
		t.Fatal("still there")
	}
	if _, ok := store.Until("ops"); !ok {
		t.Fatal("the notes went too")
	}
	if rec := del(h, "/api/rooms/ops/summary", `{"pin":"1234"}`, "10.0.0.2"); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", rec.Code)
	}
	if rec := internal(SummaryInternal(store, true, msk), "GET", "/internal/notes/summaries", nil); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("listed again for the add-on: %s", rec.Body)
	}
	if rec := del(SummaryDelete(reg, store, false, nil), "/api/rooms/ops/summary", `{"pin":"1234"}`, "10.0.0.3"); rec.Code != http.StatusNotFound {
		t.Fatalf("while off: %d", rec.Code)
	}
}

// Deleting the notes keeps the summary: the room still lists it, with its own expiry and meeting.
func TestRoomListSummaryWithoutNotes(t *testing.T) {
	reg, store, _, end := summaryRig(t)
	meets := meetings.Open(filepath.Join(t.TempDir(), "meetings.json"), nil)
	if err := meets.Add("ops", meetings.Summary{Start: meetingAt(9, 37), End: meetingAt(10, 2), People: 3}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutSummary("ops", end, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteEnded("ops"); err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	rec := do(t, Rooms(RoomsConfig{Registry: reg, Kept: lobby.Kept{Notes: store, NotesEnabled: true, SummaryEnabled: true, Meetings: meets}}), "GET", "/api/rooms", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	it := list[0]
	if _, has := it["notes_until"]; has {
		t.Fatalf("notes listed after their deletion: %v", it)
	}
	until, err := time.Parse(time.RFC3339, fmt.Sprint(it["summary_until"]))
	if it["summary"] != "ready" || err != nil || !until.Equal(meetingAt(10, 2).Add(24*time.Hour)) {
		t.Fatalf("summary %v until %v (%v)", it["summary"], it["summary_until"], err)
	}
	if it["last_meeting"] == nil {
		t.Fatal("no last_meeting for a room that keeps only a summary")
	}
	dl := SummaryDownload(reg, store, true, nil, msk)
	if rec := post(dl, "/api/rooms/ops/summary", `{"pin":"1234"}`, "10.0.0.1"); rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "# Итоги встречи «Операции»") {
		t.Fatalf("download without notes: %d %s", rec.Code, rec.Body)
	}
}
