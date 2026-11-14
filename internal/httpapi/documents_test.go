package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ssubbotin/kunveno/internal/documents"
	"github.com/ssubbotin/kunveno/internal/lobby"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"github.com/ssubbotin/kunveno/internal/room"
)

// docRig: ops, a document store and a live meeting at 10:30 MSK whose facilitator presence the test
// sets.
type docRig struct {
	mux         *http.ServeMux
	store       *documents.Store
	reg         *room.Registry
	live        time.Time
	facilitator bool
	stored      []time.Time
	changed     []time.Time // deletes reported to the room
	pins        *ratelimit.Limiter
}

func newDocRig(t *testing.T, enabled bool) *docRig {
	t.Helper()
	d := &docRig{reg: room.NewRegistry(), live: meetingAt(10, 30), facilitator: true, mux: http.NewServeMux()}
	d.reg.Add(room.New("ops", "Операции", "1234", 15))
	store, err := documents.Open(t.TempDir(), 24*time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.store = store
	var s *documents.Store
	if enabled {
		s = store
	}
	d.pins = ratelimit.New(5, 10*time.Minute, time.Minute)
	meeting := func(slug string) (time.Time, bool) { return d.live, d.facilitator }
	d.mux.Handle("PUT /internal/documents/{slug}/{id}", DocumentUpload(d.reg, s, meeting, func(slug string, m time.Time) { d.stored = append(d.stored, m) }))
	RegisterMeetings(d.mux, MeetingsConfig{Registry: d.reg, Kept: lobby.Kept{Documents: store, DocumentsEnabled: enabled, Meetings: meetingsStore(t)},
		PINs: d.pins, Links: NewLinks(time.Minute), Loc: msk, DocumentsChanged: func(slug string, m time.Time) { d.changed = append(d.changed, m) }}, nil)
	return d
}

func (d *docRig) req(method, target, body string, local bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:5000"
	if !local { // through nginx
		r.Header.Set("X-Forwarded-For", "10.0.0.1")
	}
	w := httptest.NewRecorder()
	d.mux.ServeHTTP(w, r)
	return w
}

func (d *docRig) put(id, title, body string) *httptest.ResponseRecorder {
	return d.req(http.MethodPut, "/internal/documents/ops/"+id+"?title="+url.QueryEscape(title), body, true)
}

// doc is the per-meeting route of a document of the live meeting.
func (d *docRig) doc(id string) string {
	return fmt.Sprintf("/api/rooms/ops/meetings/%d/documents/%s", d.live.UnixMilli(), id)
}

func TestDocumentUploadChecks(t *testing.T) {
	d := newDocRig(t, true)
	for _, c := range []struct {
		name   string
		w      *httptest.ResponseRecorder
		status int
	}{
		{"off loopback", d.req(http.MethodPut, "/internal/documents/ops/minutes?title=T", "x", false), http.StatusForbidden},
		{"unknown room", d.req(http.MethodPut, "/internal/documents/nope/minutes?title=T", "x", true), http.StatusNotFound},
		{"bad id", d.put("Minutes", "T", "x"), http.StatusBadRequest},
		{"long id", d.put(strings.Repeat("a", 41), "T", "x"), http.StatusBadRequest},
		{"no title", d.req(http.MethodPut, "/internal/documents/ops/minutes", "x", true), http.StatusBadRequest},
		{"blank title", d.put("minutes", "   ", "x"), http.StatusBadRequest},
		{"long title", d.put("minutes", strings.Repeat("я", 121), "x"), http.StatusBadRequest},
		{"blank body", d.put("minutes", "T", " \n\t"), http.StatusBadRequest},
		{"not UTF-8", d.put("minutes", "T", "\xff\xfe"), http.StatusBadRequest},
		{"too large", d.put("minutes", "T", strings.Repeat("a", 512<<10+1)), http.StatusRequestEntityTooLarge},
	} {
		if c.w.Code != c.status {
			t.Errorf("%s: %d %s, want %d", c.name, c.w.Code, c.w.Body, c.status)
		}
	}
	if len(d.store.Meetings("ops")) != 0 || len(d.stored) != 0 {
		t.Fatal("a refused upload was stored")
	}
	if w := d.put("minutes", strings.Repeat("я", 120), strings.Repeat("a", 512<<10)); w.Code != http.StatusOK {
		t.Fatalf("at the limits: %d %s", w.Code, w.Body)
	}
}

func TestDocumentUploadDisabled(t *testing.T) {
	d := newDocRig(t, false)
	if w := d.put("minutes", "T", "x"); w.Code != http.StatusNotFound {
		t.Fatalf("%d", w.Code)
	}
	if w := d.req(http.MethodPost, d.doc("minutes"), `{"pin":"1234"}`, false); w.Code != http.StatusNotFound {
		t.Fatalf("download: %d", w.Code)
	}
}

// Without a facilitator in the room's live meeting the upload is refused.
func TestDocumentUploadNeedsAFacilitator(t *testing.T) {
	d := newDocRig(t, true)
	d.facilitator = false
	if w := d.put("minutes", "Протокол", "# x"); w.Code != http.StatusConflict {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(d.store.Meetings("ops")) != 0 {
		t.Fatal("stored without a facilitator")
	}
}

func TestDocumentUploadStoresAndReplaces(t *testing.T) {
	d := newDocRig(t, true)
	type answer struct {
		ID      string
		Rev     int
		Updated int64
	}
	read := func(w *httptest.ResponseRecorder) answer {
		t.Helper()
		var a answer
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" || json.Unmarshal(w.Body.Bytes(), &a) != nil {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		return a
	}
	if a := read(d.put("minutes", "  Протокол  ", "# один")); a.ID != "minutes" || a.Rev != 1 || a.Updated <= 0 {
		t.Fatalf("first %+v", a)
	}
	if a := read(d.put("minutes", "Протокол встречи", "# два")); a.ID != "minutes" || a.Rev != 2 {
		t.Fatalf("replace %+v", a)
	}
	list := d.store.List("ops", d.live.UnixMilli())
	if len(list) != 1 || list[0].Rev != 2 || list[0].Title != "Протокол встречи" {
		t.Fatalf("list %+v", list)
	}
	if len(d.stored) != 2 || !d.stored[0].Equal(d.live) {
		t.Fatalf("stored %v", d.stored)
	}
	for i := 1; i < documents.MaxPerMeeting; i++ {
		if w := d.put(fmt.Sprintf("d%d", i), "T", "x"); w.Code != http.StatusOK {
			t.Fatalf("doc %d: %d", i, w.Code)
		}
	}
	if w := d.put("one-more", "T", "x"); w.Code != http.StatusConflict {
		t.Fatalf("21st: %d %s", w.Code, w.Body)
	}
	if w := d.put("minutes", "Протокол", "# три"); w.Code != http.StatusOK {
		t.Fatalf("replace at the limit: %d", w.Code)
	}
}

func TestDocumentFileNames(t *testing.T) {
	d := newDocRig(t, true)
	d.put("minutes", "Протокол встречи", "# Протокол\n")
	w := d.req(http.MethodPost, d.doc("minutes"), `{"pin":"1234"}`, false)
	if w.Code != 200 || w.Body.String() != "# Протокол\n" || w.Header().Get("Content-Type") != "text/markdown; charset=utf-8" {
		t.Fatalf("%d %q %v", w.Code, w.Body, w.Header())
	}
	cd := w.Header().Get("Content-Disposition")
	want := url.PathEscape("ops-2026-10-14-1030-Протокол-встречи.md")
	if !strings.HasPrefix(cd, `attachment; filename="ops-2026-10-14-1030-minutes.md"; filename*=UTF-8''`) || !strings.HasSuffix(cd, want) {
		t.Fatalf("disposition %q", cd)
	}
	d.put("plan", "Plan Q4", "x")
	w = d.req(http.MethodPost, d.doc("plan"), `{"pin":"1234"}`, false)
	if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="ops-2026-10-14-1030-Plan-Q4.md"` {
		t.Fatalf("%q", cd)
	}
	if w := d.req(http.MethodPost, d.doc("Bad"), `{"pin":"1234"}`, false); w.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", w.Code)
	}
}

// Wrong PINs on the document routes count toward the shared limit: the address is blocked.
func TestDocumentRoutesCountWrongPINs(t *testing.T) {
	d := newDocRig(t, true)
	d.put("minutes", "Протокол", "x")
	for i := 0; i < 5; i++ {
		method := http.MethodPost
		if i%2 == 1 {
			method = http.MethodDelete
		}
		if w := d.req(method, d.doc("minutes"), `{"pin":"0000"}`, false); w.Code != http.StatusForbidden {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	w := d.req(http.MethodPost, d.doc("minutes"), `{"pin":"1234"}`, false)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("blocked: %d %v", w.Code, w.Header())
	}
	if _, _, err := d.store.Load("ops", d.live.UnixMilli(), "minutes"); err != nil {
		t.Fatal("deleted under a wrong PIN")
	}
}

type listedDoc struct {
	ID, Title, Updated, Until string
	Rev                       int
}

func (d *docRig) history(t *testing.T) (string, []struct {
	Meeting   int64
	Documents []listedDoc
}) {
	t.Helper()
	w := d.req("GET", "/api/rooms/ops/meetings", "", false)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out []struct {
		Meeting   int64
		Documents []listedDoc
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return w.Body.String(), out
}

func TestMeetingHistoryDocuments(t *testing.T) {
	d := newDocRig(t, true)
	d.put("minutes", "Протокол", "# один")
	d.put("plan", "План", "# план")
	_, rows := d.history(t)
	if len(rows) != 1 || rows[0].Meeting != d.live.UnixMilli() || len(rows[0].Documents) != 2 {
		t.Fatalf("rows %+v", rows)
	}
	doc := rows[0].Documents[0]
	if doc.ID != "minutes" || doc.Title != "Протокол" || doc.Rev != 1 || doc.Updated == "" || doc.Until == "" {
		t.Fatalf("doc %+v", doc)
	}
	base := fmt.Sprintf("/api/rooms/ops/meetings/%d/documents/", d.live.UnixMilli())
	if w := d.req("POST", base+"minutes", `{"pin":"1234"}`, false); w.Code != 200 || w.Body.String() != "# один" {
		t.Fatalf("download %d %s", w.Code, w.Body)
	}
	if w := d.req("POST", base+"minutes", `{"pin":"0"}`, false); w.Code != http.StatusForbidden {
		t.Fatalf("wrong PIN: %d", w.Code)
	}
	if w := d.req("DELETE", base+"minutes", `{"pin":"1234"}`, false); w.Code != http.StatusNoContent {
		t.Fatalf("delete %d %s", w.Code, w.Body)
	}
	if len(d.changed) != 1 || !d.changed[0].Equal(d.live) {
		t.Fatalf("delete not reported to the room: %v", d.changed)
	}
	if w := d.req("DELETE", base+"minutes", `{"pin":"1234"}`, false); w.Code != http.StatusNotFound {
		t.Fatalf("delete again %d", w.Code)
	}
	if w := d.req("POST", fmt.Sprintf("/api/rooms/ops/meetings/%d/documents/plan", d.live.UnixMilli()+1), `{"pin":"1234"}`, false); w.Code != http.StatusNotFound {
		t.Fatalf("other meeting %d", w.Code)
	}
	if _, rows := d.history(t); len(rows) != 1 || len(rows[0].Documents) != 1 || rows[0].Documents[0].ID != "plan" {
		t.Fatalf("after delete %+v", rows)
	}
}

// With documents off the history neither lists nor serves them.
func TestMeetingHistoryDocumentsOff(t *testing.T) {
	d := newDocRig(t, false)
	if _, err := d.store.Put("ops", d.live.UnixMilli(), "minutes", "Протокол", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if body, rows := d.history(t); len(rows) != 0 || strings.Contains(body, "documents") {
		t.Fatalf("listed while off: %s", body)
	}
	if w := d.req("POST", fmt.Sprintf("/api/rooms/ops/meetings/%d/documents/minutes", d.live.UnixMilli()), `{"pin":"1234"}`, false); w.Code != http.StatusNotFound {
		t.Fatalf("served while off: %d", w.Code)
	}
}

func TestInfoDocuments(t *testing.T) {
	for on, want := range map[bool]string{true: `"documents":true`, false: `"documents":false`} {
		rec := httptest.NewRecorder()
		Info("T", nil, false, false, false, false, false, on, 15, nil).ServeHTTP(rec, httptest.NewRequest("GET", "/api/info", nil))
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%v: %s", on, rec.Body)
		}
	}
}
