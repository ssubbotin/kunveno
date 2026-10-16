package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"
)

// SessionCookie holds a signed-in person. The __Host- prefix makes browsers insist on Secure,
// Path=/ and no Domain.
const SessionCookie = "__Host-kunveno"

const keyLen = 32

// LoadKey reads the signing key at path, creating it (32 random bytes, mode 0600) when missing,
// so that restarts keep sessions. Deleting the file signs everyone out.
func LoadKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		b = make([]byte, keyLen)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return nil, err
		}
		return b, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) != keyLen {
		return nil, fmt.Errorf("%s: %d bytes, want %d (delete it to make a new one; everyone is signed out)", path, len(b), keyLen)
	}
	return b, nil
}

// Sessions issues and reads the session cookie: base64url(JSON) "." base64url(HMAC-SHA256).
type Sessions struct {
	key []byte
	ttl time.Duration
	Now func() time.Time // time.Now; tests set it
}

func NewSessions(key []byte, ttl time.Duration) *Sessions {
	return &Sessions{key: key, ttl: ttl, Now: time.Now}
}

type sessionPayload struct {
	Identity
	Exp int64 `json:"exp"` // unix seconds
}

// maxCookie bounds what Read decodes.
const maxCookie = 4096

// mac covers the purpose too, so a value signed for one cookie is worthless as another.
func (s *Sessions) mac(purpose string, payload []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(purpose + "\x00"))
	m.Write(payload)
	return m.Sum(nil)
}

func (s *Sessions) sign(purpose string, payload []byte) string {
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(s.mac(purpose, payload))
}

func (s *Sessions) verify(purpose, v string) ([]byte, bool) {
	if len(v) > maxCookie {
		return nil, false
	}
	p, sig, ok := strings.Cut(v, ".")
	if !ok {
		return nil, false
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(p)
	got, err2 := base64.RawURLEncoding.DecodeString(sig)
	if err1 != nil || err2 != nil {
		return nil, false
	}
	if !hmac.Equal(got, s.mac(purpose, payload)) {
		return nil, false
	}
	return payload, true
}

func cookie(name, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
}

// Issue sets the session cookie for id, valid for the session lifetime.
func (s *Sessions) Issue(w http.ResponseWriter, id Identity) {
	b, _ := json.Marshal(sessionPayload{Identity: id, Exp: s.Now().Add(s.ttl).Unix()})
	http.SetCookie(w, cookie(SessionCookie, s.sign("session", b), int(s.ttl.Seconds())))
}

// Read returns the signed-in identity, if the cookie is present, intact and not expired.
func (s *Sessions) Read(r *http.Request) (Identity, bool) {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return Identity{}, false
	}
	b, ok := s.verify("session", c.Value)
	if !ok {
		return Identity{}, false
	}
	var p sessionPayload
	if json.Unmarshal(b, &p) != nil || p.Login == "" || s.Now().Unix() >= p.Exp {
		return Identity{}, false
	}
	return p.Identity, true
}

// Clear removes the session cookie.
func (s *Sessions) Clear(w http.ResponseWriter) { http.SetCookie(w, cookie(SessionCookie, "", -1)) }
