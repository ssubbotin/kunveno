package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ssubbotin/kunveno/internal/auth"
	"github.com/ssubbotin/kunveno/internal/conference"
	"github.com/ssubbotin/kunveno/internal/config"
	"github.com/ssubbotin/kunveno/internal/meetings"
	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/recording"
	"github.com/ssubbotin/kunveno/internal/room"
	sig "github.com/ssubbotin/kunveno/internal/signal"
)

const publicURL = "https://meet.example.com"

// testServer builds the server's routes with room ops (PIN 1234); gate nil is sign-in off.
func testServer(t *testing.T, gate *auth.Gate) http.Handler {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Parse([]byte("media:\n  advertise_ip: 192.0.2.1\nrooms_file: " + filepath.Join(dir, "rooms.json") +
		"\nbackgrounds_dir: " + filepath.Join(dir, "backgrounds") + "\nrooms:\n  - slug: ops\n    name: Ops\n    pin: \"1234\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Ops", "1234", 15))
	store, err := notes.Open(filepath.Join(dir, "notes"), time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	recStore, err := recording.Open(filepath.Join(dir, "recordings"), time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	conf := conference.New(reg, nil, log)
	return routes(server{
		cfg: cfg, reg: reg, persist: func() error { return nil }, notes: store, recordings: recStore,
		meetings: meetings.Open(filepath.Join(dir, "meetings.json"), nil),
		conf:     conf, ws: sig.New(conf, nil, log), gate: gate, log: log,
	})
}

func testGate(t *testing.T) (*auth.Gate, *http.Cookie) {
	t.Helper()
	inv, err := auth.OpenInvites(filepath.Join(t.TempDir(), "invites.json"))
	if err != nil {
		t.Fatal(err)
	}
	g := &auth.Gate{Sessions: auth.NewSessions([]byte(strings.Repeat("k", 32)), time.Hour), Invites: inv,
		PublicURL: publicURL, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Rules: map[string]auth.Rule{"yandex": {"*@example.com"}}}
	w := httptest.NewRecorder()
	g.Sessions.Issue(w, auth.Identity{Provider: "yandex", ID: "1", Login: "ann@example.com", Name: "Ann"})
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			return g, c
		}
	}
	t.Fatal("no session cookie")
	return nil, nil
}

func do(h http.Handler, method, path, body string, session *http.Cookie, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if session != nil {
		r.AddCookie(session)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSignInGuardsTheAPI(t *testing.T) {
	gate, session := testGate(t)
	h := testServer(t, gate)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/rooms"},
		{"POST", "/api/rooms"},
		{"DELETE", "/api/rooms/ops"},
		{"POST", "/api/rooms/ops/notes"},
		{"DELETE", "/api/rooms/ops/notes"},
		{"POST", "/api/rooms/ops/recording"},
		{"GET", "/api/rooms/ops/recording/0123456789abcdef0123456789abcdef"},
		{"DELETE", "/api/rooms/ops/recording"},
		{"POST", "/api/rooms/ops/invites"},
		{"POST", "/api/rooms/ops/invites/list"},
		{"DELETE", "/api/rooms/ops/invites/x"},
	} {
		if w := do(h, c.method, c.path, `{"pin":"1234"}`, nil, publicURL); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d, want 401", c.method, c.path, w.Code)
		}
	}
	if w := do(h, "GET", "/api/info", "", nil, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"auth":{"enabled":true,"providers":[]}`) {
		t.Errorf("/api/info without a session: %d %s", w.Code, w.Body)
	}
	for _, p := range []string{"/api/backgrounds", "/api/backgrounds/x.jpg", "/healthz", "/auth/me"} {
		if w := do(h, "GET", p, "", session, ""); w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
			t.Errorf("%s with a session: %d", p, w.Code)
		}
	}
	if w := do(h, "GET", "/api/backgrounds", "", nil, ""); w.Code == http.StatusUnauthorized {
		t.Error("/api/backgrounds needs a session; guests use it without one")
	}
	if w := do(h, "GET", "/auth/me", "", nil, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("/auth/me is not served: %d", w.Code)
	}
	if w := do(h, "GET", "/api/rooms", "", session, ""); w.Code != 200 {
		t.Errorf("rooms with a session: %d", w.Code)
	}
	if w := do(h, "POST", "/api/rooms", `{"name":"Retro","pin":"432100"}`, session, "https://evil.example"); w.Code != http.StatusForbidden {
		t.Errorf("POST with a session from another site: %d, want 403", w.Code)
	}
	if w := do(h, "POST", "/api/rooms/ops/invites", `{"pin":"1234","hours":24}`, session, publicURL); w.Code != http.StatusCreated ||
		!strings.Contains(w.Body.String(), publicURL+"/join/ops#invite=") {
		t.Errorf("invitation with a session: %d %s", w.Code, w.Body)
	}
	w := do(h, "POST", "/api/rooms/ops/invites/list", `{"pin":"1234"}`, session, publicURL)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"created_by":"Ann"`) {
		t.Errorf("list: %d %s", w.Code, w.Body)
	}
}

func TestSignInOffChangesNothing(t *testing.T) {
	h := testServer(t, nil)
	if w := do(h, "GET", "/api/rooms", "", nil, ""); w.Code != 200 {
		t.Errorf("rooms: %d", w.Code)
	}
	if w := do(h, "GET", "/api/info", "", nil, ""); !strings.Contains(w.Body.String(), `"auth":{"enabled":false,"providers":[]}`) {
		t.Errorf("info: %s", w.Body)
	}
	if w := do(h, "POST", "/api/rooms", `{"name":"Retro","pin":"432100"}`, nil, "https://evil.example"); w.Code != http.StatusCreated {
		t.Errorf("create: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/rooms/ops/invites", `{"pin":"1234","hours":24}`, nil, ""); w.Code == http.StatusCreated {
		t.Error("invitations served without sign-in")
	}
	if w := do(h, "GET", "/auth/me", "", nil, ""); w.Code == http.StatusUnauthorized || w.Code == 200 {
		t.Errorf("/auth/ served without sign-in: %d", w.Code)
	}
}
