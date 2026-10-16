// Package auth is kunveno's optional sign-in: who a person is (Identity, from a Provider), whether
// they may use the server (Rule), the signed session cookie, guest invitations and the /auth/
// routes. It knows no provider; each lives in a package of its own (auth/yandex).
package auth

import (
	"context"
	"path"
	"strings"
	"unicode/utf8"
)

// Identity is a person as a provider verified them.
type Identity struct {
	Provider string // the provider's type, e.g. "yandex"
	ID       string // the account's permanent id at the provider
	Login    string // what Rule matches: for Yandex 360 organization accounts name@domain
	Name     string // display name from the account; may be empty
}

// Provider signs people in through an OAuth 2.0 authorization code flow.
type Provider interface {
	Type() string  // "yandex"; the path segment of /auth/<type>/
	Label() string // the sign-in button's text
	// AuthURL is where the browser goes to sign in; state and callback come back with the code.
	AuthURL(state, callback string) string
	// Exchange turns the code into a verified identity. It talks to the provider server to server.
	Exchange(ctx context.Context, code, callback string) (Identity, error)
}

// Rule lists the logins a provider's accounts must match: glob patterns such as "*@example.com".
type Rule []string

// Allows reports whether login matches a pattern, ignoring case. An empty rule allows nobody.
func (r Rule) Allows(login string) bool {
	login = strings.ToLower(login)
	if login == "" {
		return false
	}
	for _, p := range r {
		if ok, err := path.Match(strings.ToLower(p), login); err == nil && ok {
			return true
		}
	}
	return false
}

// MaxNameLen matches the server's limit on participant names.
const MaxNameLen = 32

// DisplayName is the name an employee has in rooms: the account's name, trimmed and cut to
// MaxNameLen runes, or the login's part before "@" when the account has none.
func DisplayName(id Identity) string {
	name := strings.TrimSpace(id.Name)
	if name == "" {
		name, _, _ = strings.Cut(id.Login, "@")
	}
	if utf8.RuneCountInString(name) > MaxNameLen {
		name = strings.TrimSpace(string([]rune(name)[:MaxNameLen]))
	}
	return name
}
