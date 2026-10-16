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

// signInBack checks that a failed sign-in went back to the page it started on (want: the path
// and query), which shows the sign-in screen with the reason, and left no session.
func signInBack(t *testing.T, res *http.Response, want string) {
	t.Helper()
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != want || sessionOf(res) != nil {
		t.Fatalf("want back to %s: %d to %q, session %v", want, res.StatusCode, res.Header.Get("Location"), sessionOf(res))
	}
}

func TestFailedSignInKeepsTheReturnPathOnOurSite(t *testing.T) {
	_, mux := newGate(t)
	for ret, want := range map[string]string{
		"/join/ops":           "/join/ops?signin=refused",
		"/join/ops?x=1":       "/join/ops?signin=refused&x=1",
		"/?signin=cancelled":  "/?signin=refused",
		"//evil.example":      "/?signin=refused",
		"/%2F%2Fevil.example": "/%2F%2Fevil.example?signin=refused",
	} {
		signInBack(t, signIn(t, mux, "eve", ret), want)
	}
}

func countResults(g *Gate) *[]string {
	var got []string
	g.OnSignIn = func(p, res string) { got = append(got, p+":"+res) }
	return &got
}

func TestRefusedAccountGetsNoSession(t *testing.T) {
	g, mux := newGate(t)
	got := countResults(g)
	signInBack(t, signIn(t, mux, "eve", "/join/ops"), "/join/ops?signin=refused")
	if len(*got) != 1 || (*got)[0] != "fake:refused" {
		t.Fatalf("counted %v", *got)
	}
}

// Cancel in the provider's window comes back with ?error= and no code: no exchange, no provider
// error.
func TestCancelledSignInIsNoProviderError(t *testing.T) {
	g, mux := newGate(t)
	got := countResults(g)
	login, state := startLogin(t, mux)
	r := httptest.NewRequest("GET", "/auth/fake/callback?error=access_denied&state="+url.QueryEscape(state), nil)
	r.AddCookie(login)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	signInBack(t, w.Result(), "/?signin=cancelled")
	if len(*got) != 1 || (*got)[0] != "fake:cancelled" {
		t.Fatalf("counted %v", *got)
	}
	login, state = startLogin(t, mux) // no error and no code either
	r = httptest.NewRequest("GET", "/auth/fake/callback?state="+url.QueryEscape(state), nil)
	r.AddCookie(login)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	signInBack(t, w.Result(), "/?signin=cancelled")
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
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/?signin=expired" || sessionOf(w.Result()) != nil {
			t.Errorf("%s: %d to %q", name, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestProviderErrorIsUnavailable(t *testing.T) {
	g, mux := newGate(t)
	got := countResults(g)
	signInBack(t, signIn(t, mux, "down", "/join/ops"), "/join/ops?signin=unavailable")
	if len(*got) != 1 || (*got)[0] != "fake:error" {
		t.Fatalf("counted %v", *got)
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
	signInBack(t, callbackWith(mux, login, state), "/?signin=expired")
}

func TestCookiesAreNotInterchangeable(t *testing.T) {
	g, mux := newGate(t)
	login, state := startLogin(t, mux)
	asSession := &http.Cookie{Name: SessionCookie, Value: login.Value}
	if _, ok := g.Sessions.Read(cookieRequest(asSession)); ok {
		t.Error("a login cookie was accepted as a session")
	}
	sess := sessionOf(signIn(t, mux, "ann", "/"))
	signInBack(t, callbackWith(mux, &http.Cookie{Name: LoginCookie, Value: sess.Value}, state), "/?signin=expired")
}

func TestRequirePassesTheIdentityOn(t *testing.T) {
	g, mux := newGate(t)
	c := sessionOf(signIn(t, mux, "ann", "/"))
	var got Identity
	var ok bool
	h := g.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got, ok = IdentityFrom(r.Context()) }))
	r := httptest.NewRequest("GET", "/api/rooms", nil)
	r.AddCookie(c)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !ok || got.Login != "ann@example.com" || got.Name != "Ann" {
		t.Fatalf("identity %+v %v", got, ok)
	}
	if _, ok := IdentityFrom(context.Background()); ok {
		t.Fatal("an identity without Require")
	}
	if id, ok := IdentityFrom(WithIdentity(context.Background(), Identity{Login: "bob@example.com"})); !ok || id.Login != "bob@example.com" {
		t.Fatal("WithIdentity")
	}
}

// A session outlives neither its provider in the config nor a narrowed allow list.
func TestSessionFollowsTheCurrentRules(t *testing.T) {
	g, mux := newGate(t)
	c := sessionOf(signIn(t, mux, "ann", "/"))
	r := httptest.NewRequest("GET", "/auth/me", nil)
	r.AddCookie(c)
	if _, ok := g.Session(r); !ok {
		t.Fatal("a fresh session refused")
	}
	g.Rules["fake"] = Rule{"boss@example.com"}
	if _, ok := g.Session(r); ok {
		t.Fatal("a session accepted after the allow list stopped matching its login")
	}
	delete(g.Rules, "fake")
	if _, ok := g.Session(r); ok {
		t.Fatal("a session accepted after its provider left the config")
	}
}

func TestExpiredSessionFollowsTheRules(t *testing.T) {
	g, mux := newGate(t)
	c := sessionOf(signIn(t, mux, "ann", "/"))
	r := httptest.NewRequest("GET", "/ws", nil)
	r.AddCookie(c)
	if _, ok := g.ExpiredSession(r); ok {
		t.Fatal("a valid session read as expired")
	}
	g.Sessions.Now = func() time.Time { return time.Now().Add(13 * time.Hour) }
	if _, ok := g.Session(r); ok {
		t.Fatal("an expired session accepted")
	}
	if id, ok := g.ExpiredSession(r); !ok || id.Login != "ann@example.com" {
		t.Fatalf("expired session %+v %v", id, ok)
	}
	g.Rules["fake"] = Rule{"boss@example.com"}
	if _, ok := g.ExpiredSession(r); ok {
		t.Fatal("an expired session accepted against the current rules")
	}
}
