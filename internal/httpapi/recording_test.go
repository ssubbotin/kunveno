package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ssubbotin/kunveno/internal/lobby"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"github.com/ssubbotin/kunveno/internal/recording"
	"github.com/ssubbotin/kunveno/internal/room"
)

var meeting = time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)

type recEnv struct {
	reg    *room.Registry
	store  *recording.Store
	links  *Links
	stored []string
	mux    *http.ServeMux
	// unknown makes the upload's meeting check refuse every meeting.
	unknown bool
}

func newRecEnv(t *testing.T) *recEnv {
	t.Helper()
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Ops", "1234", 15))
	store, err := recording.Open(t.TempDir(), 24*time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := &recEnv{reg: reg, store: store, links: NewLinks(5 * time.Minute), mux: http.NewServeMux()}
	pins := ratelimit.New(5, 10*time.Minute, time.Minute)
	e.mux.Handle("/internal/recording", RecordingRooms(reg, true))
	known := func(string, time.Time) bool { return !e.unknown }
	e.mux.Handle("PUT /internal/recording/{slug}", RecordingUpload(reg, store, 1<<20, func(s string) { e.stored = append(e.stored, s) }, known))
	e.mux.Handle("POST /api/rooms/{slug}/recording", RecordingLink(reg, store, pins, e.links))
	e.mux.Handle("GET /api/rooms/{slug}/recording/{token}", RecordingFile(store, e.links, time.UTC))
	e.mux.Handle("DELETE /api/rooms/{slug}/recording", RecordingDelete(reg, store, pins))
	return e
}

// do runs a request from loopback (RemoteAddr of httptest.NewRequest is 192.0.2.1, so set it).
func (e *recEnv) do(method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:5000"
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	return w
}

func (e *recEnv) upload(t *testing.T, body string, m time.Time) int {
	t.Helper()
	return e.do(http.MethodPut, "/internal/recording/ops", body, map[string]string{"X-Kunveno-Meeting": m.Format(time.RFC3339Nano)}).Code
}

func (e *recEnv) link(t *testing.T, pin string) (int, string) {
	t.Helper()
	w := e.do(http.MethodPost, "/api/rooms/ops/recording", `{"pin":"`+pin+`"}`, nil)
	var v struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return w.Code, v.URL
}

func TestUploadOnlyFromLoopback(t *testing.T) {
	e := newRecEnv(t)
	r := httptest.NewRequest(http.MethodPut, "/internal/recording/ops", strings.NewReader("v"))
	r.Header.Set("X-Kunveno-Meeting", meeting.Format(time.RFC3339))
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r) // RemoteAddr 192.0.2.1
	if w.Code != http.StatusForbidden {
		t.Fatalf("remote upload: %d", w.Code)
	}
	if code := e.do(http.MethodPut, "/internal/recording/ops", "v", map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Kunveno-Meeting": meeting.Format(time.RFC3339)}).Code; code != http.StatusForbidden {
		t.Fatalf("proxied upload: %d", code)
	}
}

func TestUploadStoresAndAnnounces(t *testing.T) {
	e := newRecEnv(t)
	if code := e.upload(t, "video", meeting); code != http.StatusNoContent {
		t.Fatalf("upload %d", code)
	}
	if len(e.stored) != 1 || e.stored[0] != "ops" {
		t.Fatalf("stored %v", e.stored)
	}
	if code := e.do(http.MethodPut, "/internal/recording/ops", "v", nil).Code; code != http.StatusBadRequest {
		t.Fatalf("no meeting header: %d", code)
	}
	if code := e.do(http.MethodPut, "/internal/recording/nope", "v", map[string]string{"X-Kunveno-Meeting": meeting.Format(time.RFC3339)}).Code; code != http.StatusNotFound {
		t.Fatalf("unknown room: %d", code)
	}
	if code := e.upload(t, strings.Repeat("x", 1<<20+1), meeting); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large: %d", code)
	}
}

// The recorder of this release names the part it uploads; each part is kept.
func TestUploadPartHeader(t *testing.T) {
	e := newRecEnv(t)
	p2 := meeting.Add(20 * time.Minute)
	for _, part := range []time.Time{meeting, p2} {
		code := e.do(http.MethodPut, "/internal/recording/ops", "part", map[string]string{
			"X-Kunveno-Meeting": meeting.Format(time.RFC3339Nano), "X-Kunveno-Part": part.Format(time.RFC3339Nano)}).Code
		if code != http.StatusNoContent {
			t.Fatalf("upload %v: %d", part, code)
		}
	}
	if parts := e.store.Parts("ops", meeting.UnixMilli()); len(parts) != 2 || !parts[1].Part.Equal(p2) {
		t.Fatalf("parts %+v", parts)
	}
	bad := e.do(http.MethodPut, "/internal/recording/ops", "x", map[string]string{
		"X-Kunveno-Meeting": meeting.Format(time.RFC3339Nano), "X-Kunveno-Part": "soon"}).Code
	if bad != http.StatusBadRequest {
		t.Fatalf("bad part: %d", bad)
	}
}

// A recording of an earlier meeting the room lists is stored next to the later one's.
func TestUploadEarlierListedMeeting(t *testing.T) {
	e := newRecEnv(t)
	if code := e.upload(t, "new", meeting.Add(time.Hour)); code != http.StatusNoContent {
		t.Fatalf("new: %d", code)
	}
	if code := e.upload(t, "old", meeting); code != http.StatusNoContent {
		t.Fatalf("earlier: %d", code)
	}
	if got := e.store.Meetings("ops"); len(got) != 2 {
		t.Fatalf("meetings %v", got)
	}
}

// A meeting the room neither runs nor lists is refused.
func TestUploadUnknownMeeting(t *testing.T) {
	e := newRecEnv(t)
	e.unknown = true
	if code := e.upload(t, "v", meeting); code != http.StatusConflict {
		t.Fatalf("unknown meeting: %d", code)
	}
	if got := e.store.Meetings("ops"); len(got) != 0 {
		t.Fatalf("stored %v", got)
	}
}

func TestLinkAndFile(t *testing.T) {
	e := newRecEnv(t)
	if code, _ := e.link(t, "1234"); code != http.StatusNotFound {
		t.Fatalf("no recording: %d", code)
	}
	e.upload(t, "video", meeting)
	if code, _ := e.link(t, "0000"); code != http.StatusForbidden {
		t.Fatalf("wrong pin: %d", code)
	}
	code, url := e.link(t, "1234")
	if code != http.StatusOK || !strings.HasPrefix(url, "/api/rooms/ops/recording/") {
		t.Fatalf("link %d %q", code, url)
	}
	w := e.do(http.MethodGet, url, "", nil)
	if w.Code != http.StatusOK || w.Body.String() != "video" ||
		w.Header().Get("Content-Disposition") != `attachment; filename="ops-2026-10-15-0800.mp4"` || w.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("file %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	if code := e.do(http.MethodGet, "/api/rooms/ops/recording/0123456789abcdef0123456789abcdef", "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("unknown token: %d", code)
	}
}

func TestFileRange(t *testing.T) {
	e := newRecEnv(t)
	e.upload(t, "0123456789", meeting)
	_, url := e.link(t, "1234")
	w := e.do(http.MethodGet, url, "", map[string]string{"Range": "bytes=2-4"})
	if w.Code != http.StatusPartialContent || w.Body.String() != "234" {
		t.Fatalf("range %d %q", w.Code, w.Body.String())
	}
}

func TestLinkAfterDeleteAndReplace(t *testing.T) {
	e := newRecEnv(t)
	e.upload(t, "first", meeting)
	_, url := e.link(t, "1234")
	if code := e.do(http.MethodDelete, "/api/rooms/ops/recording", `{"pin":"1234"}`, nil).Code; code != http.StatusNoContent {
		t.Fatalf("delete %d", code)
	}
	if code := e.do(http.MethodGet, url, "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("link after delete: %d", code)
	}
	e.upload(t, "first", meeting)
	_, url = e.link(t, "1234")
	e.upload(t, "second", meeting.Add(time.Hour)) // another meeting: the linked part stays
	if code := e.do(http.MethodGet, url, "", nil).Code; code != http.StatusOK {
		t.Fatalf("link after another meeting's upload: %d", code)
	}
	e.upload(t, "again", meeting) // the same part replaced
	if code := e.do(http.MethodGet, url, "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("link to a replaced part: %d", code)
	}
}

func TestLinkExpires(t *testing.T) {
	e := newRecEnv(t)
	now := time.Now()
	e.links.now = func() time.Time { return now }
	e.upload(t, "v", meeting)
	_, url := e.link(t, "1234")
	now = now.Add(5*time.Minute + time.Second)
	if code := e.do(http.MethodGet, url, "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("expired link: %d", code)
	}
}

func TestDeleteWhileRecording(t *testing.T) {
	e := newRecEnv(t)
	e.upload(t, "v", meeting)
	e.store.SetActive("ops", true)
	if code := e.do(http.MethodDelete, "/api/rooms/ops/recording", `{"pin":"1234"}`, nil).Code; code != http.StatusConflict {
		t.Fatalf("delete while recording: %d", code)
	}
}

func TestRecordingRoomsList(t *testing.T) {
	e := newRecEnv(t)
	r, _ := e.reg.Get("ops")
	r.SetRecording(true)
	w := e.do(http.MethodGet, "/internal/recording", "", nil)
	b, _ := io.ReadAll(w.Body)
	if strings.TrimSpace(string(b)) != `["ops"]` {
		t.Fatalf("list %s", b)
	}
	off := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/internal/recording", nil)
	req.RemoteAddr = "127.0.0.1:1"
	RecordingRooms(e.reg, false).ServeHTTP(off, req)
	if strings.TrimSpace(off.Body.String()) != `[]` {
		t.Fatalf("disabled list %s", off.Body.String())
	}
}

func TestRecordingDisabledAnswers404(t *testing.T) {
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Ops", "1234", 15))
	mux := http.NewServeMux()
	mux.Handle("POST /api/rooms/{slug}/recording", RecordingLink(reg, nil, nil, NewLinks(time.Minute)))
	mux.Handle("PUT /internal/recording/{slug}", RecordingUpload(reg, nil, 1<<20, nil, nil))
	for _, m := range []string{http.MethodPost + " /api/rooms/ops/recording", http.MethodPut + " /internal/recording/ops"} {
		method, path, _ := strings.Cut(m, " ")
		r := httptest.NewRequest(method, path, strings.NewReader(`{"pin":"1234"}`))
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("X-Kunveno-Meeting", meeting.Format(time.RFC3339))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", m, w.Code)
		}
	}
}

func TestRoomsListRecordingUntilAndDelete(t *testing.T) {
	e := newRecEnv(t)
	e.upload(t, "v", time.Now().Add(-time.Hour))
	reg := e.reg
	reg.Add(room.New("dev", "Dev", "5678", 15))
	list := func(rec *recording.Store) []map[string]any {
		w := httptest.NewRecorder()
		Rooms(RoomsConfig{Registry: reg, Kept: lobby.Kept{Recordings: rec, RecordingEnabled: rec != nil}}).ServeHTTP(w, httptest.NewRequest("GET", "/api/rooms", nil))
		var out []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	with := 0
	for _, it := range list(e.store) {
		if _, ok := it["recording_until"]; ok {
			with++
			if it["slug"] != "ops" {
				t.Fatalf("recording_until on %v", it["slug"])
			}
		}
	}
	if with != 1 {
		t.Fatalf("rooms with recording_until: %d", with)
	}
	for _, it := range list(nil) {
		if _, ok := it["recording_until"]; ok {
			t.Fatal("recording_until without the store")
		}
	}
}

func TestDeletingARoomDeletesItsRecording(t *testing.T) {
	e := newRecEnv(t)
	rm, err := e.reg.Create("Retro", "432100")
	if err != nil {
		t.Fatal(err)
	}
	slug := rm.Slug()
	if _, err := e.store.Put(slug, meeting, meeting, strings.NewReader("v"), 1<<20); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	Rooms(RoomsConfig{Registry: e.reg, Kept: lobby.Kept{Recordings: e.store, RecordingEnabled: true}}).ServeHTTP(w, httptest.NewRequest("DELETE", "/api/rooms/"+slug, strings.NewReader(`{"pin":"432100"}`)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete room %d", w.Code)
	}
	if _, ok := e.store.Until(slug); ok {
		t.Fatal("recording stays after the room is deleted")
	}
}

func TestDeletingARoomDeletesItsRecordingWhileRecordingIsOff(t *testing.T) {
	e := newRecEnv(t)
	rm, err := e.reg.Create("Retro", "432100")
	if err != nil {
		t.Fatal(err)
	}
	slug := rm.Slug()
	if _, err := e.store.Put(slug, meeting, meeting, strings.NewReader("v"), 1<<20); err != nil {
		t.Fatal(err)
	}
	h := Rooms(RoomsConfig{Registry: e.reg, Kept: lobby.Kept{Recordings: e.store}})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/rooms", nil))
	if strings.Contains(w.Body.String(), "recording_until") {
		t.Fatal("recording_until listed while recording is off")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/rooms/"+slug, strings.NewReader(`{"pin":"432100"}`)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete room %d", w.Code)
	}
	if _, ok := e.store.Until(slug); ok {
		t.Fatal("recording stays after the room is deleted while recording is off")
	}
}

func TestUploadLargerThanTheLimitIsRefusedBeforeReading(t *testing.T) {
	e := newRecEnv(t)
	r := httptest.NewRequest(http.MethodPut, "/internal/recording/ops", strings.NewReader("x"))
	r.RemoteAddr = "127.0.0.1:5000"
	r.ContentLength = 1<<20 + 1
	r.Header.Set("X-Kunveno-Meeting", meeting.Format(time.RFC3339Nano))
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", w.Code)
	}
	if _, ok := e.store.Until("ops"); ok {
		t.Fatal("something was stored")
	}
}
