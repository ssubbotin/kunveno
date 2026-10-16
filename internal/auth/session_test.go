package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cookieRequest(c *http.Cookie) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	if c != nil {
		r.AddCookie(c)
	}
	return r
}

func issued(t *testing.T, s *Sessions, id Identity) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	s.Issue(w, id)
	cs := w.Result().Cookies()
	if len(cs) != 1 {
		t.Fatalf("%d cookies", len(cs))
	}
	return cs[0]
}

func TestSessionRoundTripAndAttributes(t *testing.T) {
	s := NewSessions([]byte(strings.Repeat("k", 32)), 12*time.Hour)
	id := Identity{Provider: "yandex", ID: "42", Login: "ann@example.com", Name: "Анна"}
	c := issued(t, s, id)
	if c.Name != SessionCookie || !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie attributes %+v", c)
	}
	if c.MaxAge != int((12 * time.Hour).Seconds()) {
		t.Fatalf("max age %d", c.MaxAge)
	}
	got, ok := s.Read(cookieRequest(c))
	if !ok || got != id {
		t.Fatalf("read %+v %v", got, ok)
	}
}

func TestSessionRefusesTamperingExpiryAndOtherKeys(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	s := NewSessions(key, time.Hour)
	c := issued(t, s, Identity{Provider: "yandex", ID: "42", Login: "ann@example.com"})

	tampered := *c
	payload, sig, _ := strings.Cut(c.Value, ".")
	tampered.Value = payload[:len(payload)-2] + "AA." + sig
	if _, ok := s.Read(cookieRequest(&tampered)); ok {
		t.Error("tampered payload accepted")
	}
	if _, ok := NewSessions([]byte(strings.Repeat("x", 32)), time.Hour).Read(cookieRequest(c)); ok {
		t.Error("a cookie signed with another key accepted")
	}
	later := NewSessions(key, time.Hour)
	later.Now = func() time.Time { return time.Now().Add(61 * time.Minute) }
	if _, ok := later.Read(cookieRequest(c)); ok {
		t.Error("expired session accepted")
	}
	for _, v := range []string{"", ".", "abc", "abc.def", strings.Repeat("a", 5000)} {
		if _, ok := s.Read(cookieRequest(&http.Cookie{Name: SessionCookie, Value: v})); ok {
			t.Errorf("value %q accepted", v)
		}
	}
	if _, ok := s.Read(cookieRequest(nil)); ok {
		t.Error("no cookie accepted")
	}
}

func TestClearExpiresTheCookie(t *testing.T) {
	w := httptest.NewRecorder()
	NewSessions([]byte(strings.Repeat("k", 32)), time.Hour).Clear(w)
	c := w.Result().Cookies()[0]
	if c.Name != SessionCookie || c.MaxAge >= 0 || c.Value != "" {
		t.Fatalf("clear %+v", c)
	}
}

func TestLoadKeyCreatesOnceWithMode0600(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auth.key")
	k1, err := LoadKey(p)
	if err != nil || len(k1) != 32 {
		t.Fatalf("%v %d", err, len(k1))
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	k2, _ := LoadKey(p)
	if string(k1) != string(k2) {
		t.Fatal("the key changed on the second load: a restart would sign everyone out")
	}
	_ = os.WriteFile(p, []byte("short"), 0o600)
	if _, err := LoadKey(p); err == nil {
		t.Fatal("a short key was accepted")
	}
}

func TestSignaturesCarryAPurpose(t *testing.T) {
	s := NewSessions([]byte(strings.Repeat("k", 32)), time.Hour)
	v := s.sign("login", []byte(`{"s":"x"}`))
	if _, ok := s.verify("login", v); !ok {
		t.Fatal("own purpose refused")
	}
	if _, ok := s.verify("session", v); ok {
		t.Fatal("a value signed for one purpose verified for another")
	}
}
