package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInvitesCreateCheckRevokePersist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "invites.json")
	s, err := OpenInvites(p)
	if err != nil {
		t.Fatal(err)
	}
	tok, inv, err := s.Create("ops", "ann@example.com", 24*time.Hour)
	if err != nil || len(tok) != 43 || inv.ID == "" || inv.Room != "ops" {
		t.Fatalf("create %q %+v %v", tok, inv, err)
	}
	if got, err := s.Check("ops", tok); err != nil || got.ID != inv.ID {
		t.Fatalf("check %+v %v", got, err)
	}
	if _, err := s.Check("dev", tok); !errors.Is(err, ErrBadInvite) {
		t.Fatal("a token was accepted for another room")
	}
	wrong := tok[:42] + "A"
	if tok[42] == 'A' {
		wrong = tok[:42] + "B" // the last character is random: make sure it differs
	}
	if _, err := s.Check("ops", wrong); !errors.Is(err, ErrBadInvite) {
		t.Fatal("a wrong token was accepted")
	}
	data, _ := os.ReadFile(p)
	if strings.Contains(string(data), tok) {
		t.Fatal("the token itself is stored")
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	s2, err := OpenInvites(p) // a restart
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Check("ops", tok); err != nil {
		t.Fatal("the invitation did not survive a restart")
	}
	if l := s2.List("ops"); len(l) != 1 || l[0].Hash != "" {
		t.Fatalf("list %+v (the hash must not leave the store)", l)
	}
	if _, err := s2.Revoke("ops", inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Check("ops", tok); !errors.Is(err, ErrBadInvite) {
		t.Fatal("a revoked invitation was accepted")
	}
	if _, err := s2.Revoke("dev", inv.ID); !errors.Is(err, ErrBadInvite) {
		t.Fatal("revoked through another room")
	}
}

func TestInvitesExpireAndAreSwept(t *testing.T) {
	s, _ := OpenInvites(filepath.Join(t.TempDir(), "invites.json"))
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	tok, _, _ := s.Create("ops", "ann@example.com", time.Hour)
	now = now.Add(time.Hour)
	if _, err := s.Check("ops", tok); !errors.Is(err, ErrBadInvite) {
		t.Fatal("an expired invitation was accepted")
	}
	if len(s.List("ops")) != 0 || s.Active() != 0 {
		t.Fatal("an expired invitation is listed")
	}
	if err := s.Sweep(); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	n := len(s.all)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d entries after the sweep", n)
	}
}
