package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeProvider admits code "ann" as ann@example.com and "eve" as a personal account.
type fakeProvider struct{}

func (fakeProvider) Type() string  { return "fake" }
func (fakeProvider) Label() string { return "Fake" }
func (fakeProvider) AuthURL(state, cb string) string {
	return "https://idp.example/authorize?state=" + url.QueryEscape(state) + "&redirect_uri=" + url.QueryEscape(cb)
}
func (fakeProvider) Exchange(_ context.Context, code, cb string) (Identity, error) {
	if cb != "https://meet.example.com/auth/fake/callback" {
		return Identity{}, errors.New("wrong callback")
	}
	switch code {
	case "ann":
		return Identity{Provider: "fake", ID: "1", Login: "ann@example.com", Name: "Ann"}, nil
	case "eve":
		return Identity{Provider: "fake", ID: "2", Login: "eve"}, nil
	}
	return Identity{}, errors.New("provider down")
}

func newGate(t *testing.T) (*Gate, *http.ServeMux) {
	inv, _ := OpenInvites(t.TempDir() + "/invites.json")
	g := &Gate{Providers: []Provider{fakeProvider{}}, Rules: map[string]Rule{"fake": {"*@example.com"}},
		Sessions: NewSessions([]byte(strings.Repeat("k", 32)), 12*time.Hour), Invites: inv,
		PublicURL: "https://meet.example.com", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	mux := http.NewServeMux()
	g.Routes(mux)
	return g, mux
}

// signIn runs start and callback; it returns the final response.
func signIn(t *testing.T, mux *http.ServeMux, code, ret string) *http.Response {
	t.Helper()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/auth/fake/start?return="+url.QueryEscape(ret), nil))
	if w.Code != http.StatusFound {
		t.Fatalf("start %d", w.Code)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	state := loc.Query().Get("state")
	login := w.Result().Cookies()[0]
	r := httptest.NewRequest("GET", "/auth/fake/callback?code="+code+"&state="+url.QueryEscape(state), nil)
	r.AddCookie(login)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w.Result()
}

func sessionOf(res *http.Response) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == SessionCookie && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestSignInAndBackToTheSavedPath(t *testing.T) {
	g, mux := newGate(t)
	res := signIn(t, mux, "ann", "/join/ops")
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/join/ops" {
		t.Fatalf("callback %d to %q", res.StatusCode, res.Header.Get("Location"))
	}
	c := sessionOf(res)
	if c == nil {
		t.Fatal("no session")
	}
	r := httptest.NewRequest("GET", "/auth/me", nil)
	r.AddCookie(c)
	if id, ok := g.Session(r); !ok || id.Login != "ann@example.com" {
		t.Fatalf("session %+v %v", id, ok)
	}
}

func TestReturnPathStaysOnOurSite(t *testing.T) {
	_, mux := newGate(t)
	for _, ret := range []string{"//evil.example", "/\\evil.example", "https://evil.example/", "", "join", "/%2F%2Fevil.example",
		"/\t/evil.example", "/\n/evil.example", "/\r/evil.example", "/ok\\..\\evil", "/a\x7fb"} {
		res := signIn(t, mux, "ann", ret)
		if loc := res.Header.Get("Location"); loc != "/" && !(ret == "/%2F%2Fevil.example" && loc == ret) {
			t.Errorf("return %q led to %q", ret, loc)
		}
	}
}

func TestRefusedAccountGetsNoSession(t *testing.T) {
	_, mux := newGate(t)
	res := signIn(t, mux, "eve", "/")
	if sessionOf(res) != nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("refused account: %d, session %v", res.StatusCode, sessionOf(res))
	}
}

func TestCallbackChecksState(t *testing.T) {
	_, mux := newGate(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/auth/fake/start?return=/", nil))
	login := w.Result().Cookies()[0]
	for name, r := range map[string]*http.Request{
		"no cookie": httptest.NewRequest("GET", "/auth/fake/callback?code=ann&state=x", nil),
		"other state": func() *http.Request {
			r := httptest.NewRequest("GET", "/auth/fake/callback?code=ann&state=forged", nil)
			r.AddCookie(login)
			return r
		}(),
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || sessionOf(w.Result()) != nil {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
}

func TestProviderErrorIsUnavailable(t *testing.T) {
	_, mux := newGate(t)
	if res := signIn(t, mux, "down", "/"); res.StatusCode != http.StatusBadGateway || sessionOf(res) != nil {
		t.Fatalf("provider error: %d", res.StatusCode)
	}
}

func TestRequireAndOrigin(t *testing.T) {
	g, mux := newGate(t)
	c := sessionOf(signIn(t, mux, "ann", "/"))
	h := g.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	call := func(method, origin string, withCookie bool) int {
		r := httptest.NewRequest(method, "/api/rooms", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if withCookie {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if call("GET", "", false) != 401 || call("GET", "", true) != 204 {
		t.Error("GET")
	}
	if call("POST", "https://evil.example", true) != 403 || call("POST", "", true) != 403 || call("POST", "https://meet.example.com", true) != 204 {
		t.Error("POST origin")
	}
}

func TestLogoutAndMe(t *testing.T) {
	_, mux := newGate(t)
	c := sessionOf(signIn(t, mux, "ann", "/"))
	r := httptest.NewRequest("GET", "/auth/me", nil)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Ann"`) {
		t.Fatalf("me %d %s", w.Code, w.Body)
	}
	r = httptest.NewRequest("POST", "/auth/logout", nil)
	r.Header.Set("Origin", "https://meet.example.com")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 204 || w.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatalf("logout %d", w.Code)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/auth/me", nil))
	if w.Code != 401 {
		t.Fatalf("me without a session %d", w.Code)
	}
}

func TestReturnPathKeepsOrdinaryPaths(t *testing.T) {
	_, mux := newGate(t)
	for _, ret := range []string{"/join/ops", "/join/ops?x=1", "/"} {
		if loc := signIn(t, mux, "ann", ret).Header.Get("Location"); loc != ret {
			t.Errorf("return %q led to %q", ret, loc)
		}
	}
}

func startLogin(t *testing.T, mux *http.ServeMux) (*http.Cookie, string) {
	t.Helper()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/auth/fake/start?return=/", nil))
	loc, _ := url.Parse(w.Header().Get("Location"))
	return w.Result().Cookies()[0], loc.Query().Get("state")
}

func callbackWith(mux *http.ServeMux, c *http.Cookie, state string) *http.Response {
	r := httptest.NewRequest("GET", "/auth/fake/callback?code=ann&state="+url.QueryEscape(state), nil)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w.Result()
}

func TestLoginStateExpiresOnTheServer(t *testing.T) {
	g, mux := newGate(t)
	login, state := startLogin(t, mux)
	g.Sessions.Now = func() time.Time { return time.Now().Add(loginTTL + time.Minute) }
	res := callbackWith(mux, login, state)
	if res.StatusCode != http.StatusBadRequest || sessionOf(res) != nil {
		t.Fatalf("expired login state: %d", res.StatusCode)
	}
}

func TestCookiesAreNotInterchangeable(t *testing.T) {
	g, mux := newGate(t)
	login, state := startLogin(t, mux)
	asSession := &http.Cookie{Name: SessionCookie, Value: login.Value}
	if _, ok := g.Sessions.Read(cookieRequest(asSession)); ok {
		t.Error("a login cookie was accepted as a session")
	}
	sess := sessionOf(signIn(t, mux, "ann", "/"))
	res := callbackWith(mux, &http.Cookie{Name: LoginCookie, Value: sess.Value}, state)
	if res.StatusCode != http.StatusBadRequest || sessionOf(res) != nil {
		t.Errorf("a session cookie was accepted as login state: %d", res.StatusCode)
	}
}
