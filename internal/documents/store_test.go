package documents

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

// clock is a settable now for the store.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func open(t *testing.T, dir string, c *clock) *Store {
	t.Helper()
	s, err := Open(dir, 24*time.Hour, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPutListLoad(t *testing.T) {
	c := &clock{t0}
	s := open(t, t.TempDir(), c)
	meeting := t0.Add(-time.Hour).UnixMilli()
	info, err := s.Put("ops", meeting, "minutes", "Протокол", []byte("# Протокол\n"))
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "minutes" || info.Title != "Протокол" || info.Rev != 1 || !info.Updated.Equal(t0) {
		t.Fatalf("info = %+v", info)
	}
	c.t = t0.Add(time.Minute)
	if _, err := s.Put("ops", meeting, "plan", "План", []byte("план")); err != nil {
		t.Fatal(err)
	}
	list := s.List("ops", meeting)
	if len(list) != 2 || list[0].ID != "minutes" || list[1].ID != "plan" {
		t.Fatalf("list = %+v", list)
	}
	got, body, err := s.Load("ops", meeting, "minutes")
	if err != nil || string(body) != "# Протокол\n" || got.Title != "Протокол" {
		t.Fatalf("load = %+v %q %v", got, body, err)
	}
	if _, _, err := s.Load("ops", meeting, "nope"); !errors.Is(err, ErrNoDocument) {
		t.Fatalf("missing: %v", err)
	}
	if _, _, err := s.Load("ops", meeting+1, "minutes"); !errors.Is(err, ErrNoDocument) {
		t.Fatalf("other meeting: %v", err)
	}
}

// The same id replaces the document: the revision grows, the update time moves, the title may change,
// and its place in the list stays.
func TestReplaceBumpsRevision(t *testing.T) {
	c := &clock{t0}
	s := open(t, t.TempDir(), c)
	m := t0.UnixMilli()
	s.Put("ops", m, "minutes", "Протокол", []byte("один"))
	s.Put("ops", m, "plan", "План", []byte("план"))
	c.t = t0.Add(5 * time.Minute)
	info, err := s.Put("ops", m, "minutes", "Протокол встречи", []byte("два"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Rev != 2 || !info.Updated.Equal(c.t) || info.Title != "Протокол встречи" {
		t.Fatalf("info = %+v", info)
	}
	list := s.List("ops", m)
	if len(list) != 2 || list[0].ID != "minutes" || list[0].Rev != 2 {
		t.Fatalf("list = %+v", list)
	}
	if _, body, _ := s.Load("ops", m, "minutes"); string(body) != "два" {
		t.Fatalf("body %q", body)
	}
}

func TestLimitsAndNames(t *testing.T) {
	s := open(t, t.TempDir(), &clock{t0})
	m := t0.UnixMilli()
	for _, id := range []string{"", "A", "a_b", "a/b", "..", strings.Repeat("a", 41)} {
		if _, err := s.Put("ops", m, id, "T", []byte("x")); !errors.Is(err, ErrBadID) {
			t.Errorf("id %q: %v", id, err)
		}
	}
	if _, err := s.Put("../x", m, "a", "T", []byte("x")); !errors.Is(err, ErrBadSlug) {
		t.Errorf("slug: %v", err)
	}
	if _, err := s.Put("ops", m, strings.Repeat("a", 40), "T", []byte("x")); err != nil {
		t.Errorf("40 chars: %v", err)
	}
	for i := 1; i < MaxPerMeeting; i++ {
		if _, err := s.Put("ops", m, "d"+string(rune('a'+i)), "T", []byte("x")); err != nil {
			t.Fatalf("doc %d: %v", i, err)
		}
	}
	if _, err := s.Put("ops", m, "one-more", "T", []byte("x")); !errors.Is(err, ErrTooMany) {
		t.Fatalf("21st: %v", err)
	}
	if _, err := s.Put("ops", m, "db", "T", []byte("y")); err != nil {
		t.Fatalf("replace at the limit: %v", err)
	}
	if _, err := s.Put("ops", m+1, "one-more", "T", []byte("x")); err != nil {
		t.Fatalf("another meeting: %v", err)
	}
}

// What was stored survives a restart.
func TestReopen(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t0}
	s := open(t, dir, c)
	m := t0.UnixMilli()
	s.Put("ops", m, "minutes", "Протокол", []byte("один"))
	s.Put("ops", m, "minutes", "Протокол", []byte("два"))
	s2 := open(t, dir, c)
	list := s2.List("ops", m)
	if len(list) != 1 || list[0].Rev != 2 || list[0].Title != "Протокол" {
		t.Fatalf("reopened = %+v", list)
	}
	if _, body, _ := s2.Load("ops", m, "minutes"); string(body) != "два" {
		t.Fatalf("body %q", body)
	}
	if got := s2.Meetings("ops"); len(got) != 1 || got[0] != m {
		t.Fatalf("meetings %v", got)
	}
}

// A broken index is skipped without stopping the store.
func TestOpenSkipsBrokenIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ops", "123"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "ops", "123", "index.json"), []byte("{"), 0o600)
	s := open(t, dir, &clock{t0})
	if len(s.Meetings("ops")) != 0 {
		t.Fatal("broken index listed")
	}
}

func TestExpiryAndSweep(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t0}
	s := open(t, dir, c)
	old, recent := t0.Add(-2*time.Hour).UnixMilli(), t0.UnixMilli()
	s.Put("ops", old, "minutes", "Старый", []byte("x"))
	c.t = t0.Add(12 * time.Hour)
	s.Put("ops", recent, "minutes", "Новый", []byte("y"))
	if l := s.List("ops", old); len(l) != 1 || !l[0].Until(s.Keep()).Equal(t0.Add(24*time.Hour)) {
		t.Fatalf("until = %+v", l)
	}
	c.t = t0.Add(25 * time.Hour)
	if len(s.List("ops", old)) != 0 || s.Has("ops", old) {
		t.Fatal("expired document still listed")
	}
	if !s.Has("ops", recent) {
		t.Fatal("recent document gone")
	}
	gone := s.Sweep()
	if len(gone) != 1 || gone[0].Slug != "ops" || gone[0].Meeting != old {
		t.Fatalf("gone = %+v", gone)
	}
	if _, err := os.Stat(filepath.Join(dir, "ops", strconv.FormatInt(old, 10))); err == nil {
		t.Fatal("meeting directory left")
	}
	if got := s.Meetings("ops"); len(got) != 1 || got[0] != recent {
		t.Fatalf("meetings %v", got)
	}
	c.t = t0.Add(40 * time.Hour)
	s.Sweep()
	if _, err := os.Stat(filepath.Join(dir, "ops")); err == nil {
		t.Fatal("room directory left after its last document expired")
	}
}

func TestDeleteAndRemove(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, &clock{t0})
	m := t0.UnixMilli()
	s.Put("ops", m, "minutes", "Протокол", []byte("x"))
	s.Put("ops", m, "plan", "План", []byte("y"))
	s.Put("dev", m, "plan", "План", []byte("y"))
	if err := s.Delete("ops", m, "minutes"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("ops", m, "minutes"); !errors.Is(err, ErrNoDocument) {
		t.Fatalf("second delete: %v", err)
	}
	if l := s.List("ops", m); len(l) != 1 || l[0].ID != "plan" {
		t.Fatalf("after delete %+v", l)
	}
	if err := s.Remove("ops"); err != nil {
		t.Fatal(err)
	}
	if s.Has("ops", m) || len(s.Meetings("ops")) != 0 {
		t.Fatal("removed room still keeps documents")
	}
	if _, err := os.Stat(filepath.Join(dir, "ops")); err == nil {
		t.Fatal("room directory left")
	}
	if !s.Has("dev", m) {
		t.Fatal("another room lost its documents")
	}
	if s2 := open(t, dir, &clock{t0}); s2.Has("ops", m) {
		t.Fatal("removed documents back after a restart")
	}
}

func TestLatest(t *testing.T) {
	c := &clock{t0}
	s := open(t, t.TempDir(), c)
	a, b := t0.UnixMilli(), t0.Add(time.Hour).UnixMilli()
	s.Put("ops", a, "minutes", "A", []byte("a"))
	s.Put("ops", a, "plan", "A", []byte("a"))
	s.Put("ops", b, "minutes", "B", []byte("b"))
	if m, ok := s.Latest("ops", "minutes"); !ok || m != b {
		t.Fatalf("minutes: %d %v", m, ok)
	}
	if m, ok := s.Latest("ops", "plan"); !ok || m != a {
		t.Fatalf("plan: %d %v", m, ok)
	}
	if _, ok := s.Latest("ops", "nope"); ok {
		t.Fatal("unknown id found")
	}
}

func TestCleanTitle(t *testing.T) {
	for in, want := range map[string]string{"  План  ": "План", "a\n\tb": "a b", strings.Repeat("я", 120): strings.Repeat("я", 120)} {
		if got, ok := CleanTitle(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "   ", strings.Repeat("я", 121), "a\x00b", "\xff"} {
		if _, ok := CleanTitle(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFileName(t *testing.T) {
	loc := time.FixedZone("MSK", 3*3600)
	start := time.Date(2026, 11, 14, 7, 30, 0, 0, time.UTC)
	for _, c := range []struct{ title, id, want string }{
		{"Minutes", "minutes", "ops-2026-11-14-1030-Minutes.md"},
		{"Протокол встречи", "minutes", "ops-2026-11-14-1030-Протокол-встречи.md"},
		{"Plan: Q4 / \"draft\"", "plan", "ops-2026-11-14-1030-Plan-Q4-draft.md"},
		{"???", "plan", "ops-2026-11-14-1030-plan.md"},
		{strings.Repeat("я", 100), "x", "ops-2026-11-14-1030-" + strings.Repeat("я", 60) + ".md"},
	} {
		if got := FileName("ops", start, c.title, c.id, loc); got != c.want {
			t.Errorf("%q: %q, want %q", c.title, got, c.want)
		}
	}
}
