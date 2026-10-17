package recording

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func ms(t time.Time) int64 { return t.UnixMilli() }

func readPart(t *testing.T, s *Store, slug string, meeting, part int64) string {
	t.Helper()
	f, _, err := s.OpenPart(slug, meeting, part)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	return string(b)
}

func put(t *testing.T, s *Store, meeting, part time.Time, body string) {
	t.Helper()
	if _, err := s.Put("ops", meeting, part, strings.NewReader(body), 100); err != nil {
		t.Fatal(err)
	}
}

// A recorder that retries an upload replaces its own part only.
func TestPartsReplaceOnlyTheirOwn(t *testing.T) {
	s, _, _ := newStore(t)
	p1, p2 := t0.Add(time.Minute), t0.Add(20*time.Minute)
	put(t, s, t0, p1, "first")
	put(t, s, t0, p2, "second")
	put(t, s, t0, p1, "first again")
	parts := s.Parts("ops", ms(t0))
	if len(parts) != 2 || !parts[0].Part.Equal(p1) || !parts[1].Part.Equal(p2) {
		t.Fatalf("parts %+v", parts)
	}
	if got := readPart(t, s, "ops", ms(t0), ms(p1)); got != "first again" {
		t.Fatalf("p1 %q", got)
	}
	if got := readPart(t, s, "ops", ms(t0), ms(p2)); got != "second" {
		t.Fatalf("p2 %q", got)
	}
}

func TestPartsOfSeveralMeetings(t *testing.T) {
	s, _, _ := newStore(t)
	m2 := t0.Add(2 * time.Hour)
	put(t, s, t0, t0, "a")
	put(t, s, m2, m2, "b")
	if got := s.Meetings("ops"); !reflect.DeepEqual(got, []int64{ms(m2), ms(t0)}) {
		t.Fatalf("meetings %v", got)
	}
	if m, p, ok := s.Latest("ops"); !ok || m != ms(m2) || p != ms(m2) {
		t.Fatalf("latest %v %v %v", m, p, ok)
	}
	if body, _ := read(t, s, "ops"); body != "b" {
		t.Fatalf("latest body %q", body)
	}
}

// An older recorder sends no part: its part is the meeting's start, and the last upload wins.
func TestOldRecorderReplacesPartOne(t *testing.T) {
	s, _, _ := newStore(t)
	put(t, s, t0, t0, "one")
	put(t, s, t0, t0, "two")
	if parts := s.Parts("ops", ms(t0)); len(parts) != 1 {
		t.Fatalf("parts %+v", parts)
	}
	if got := readPart(t, s, "ops", ms(t0), ms(t0)); got != "two" {
		t.Fatalf("body %q", got)
	}
}

func TestDeletePart(t *testing.T) {
	s, _, dir := newStore(t)
	p2 := t0.Add(20 * time.Minute)
	put(t, s, t0, t0, "a")
	put(t, s, t0, p2, "b")
	s.SetActive("ops", true) // a finished part can go while the room records another
	if err := s.DeletePart("ops", ms(t0), ms(t0)); err != nil {
		t.Fatal(err)
	}
	if parts := s.Parts("ops", ms(t0)); len(parts) != 1 || !parts[0].Part.Equal(p2) {
		t.Fatalf("parts %+v", parts)
	}
	if err := s.DeletePart("ops", ms(t0), ms(t0)); !errors.Is(err, ErrNoRecording) {
		t.Fatalf("second delete: %v", err)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "ops", strconv.FormatInt(ms(t0), 10)+"."+strconv.FormatInt(ms(t0), 10)+".*")); len(files) != 0 {
		t.Fatalf("files left %v", files)
	}
}

func TestSweepParts(t *testing.T) {
	s, now, _ := newStore(t)
	put(t, s, t0, t0, "a")
	*now = t0.Add(2 * time.Hour)
	m2 := t0.Add(2 * time.Hour)
	put(t, s, m2, m2, "b")
	*now = t0.Add(24*time.Hour + time.Minute) // the first part expired, the second not
	if gone := s.Sweep(); !reflect.DeepEqual(gone, []Gone{{Slug: "ops", Meeting: ms(t0), Part: ms(t0)}}) {
		t.Fatalf("swept %v", gone)
	}
	if got := s.Meetings("ops"); !reflect.DeepEqual(got, []int64{ms(m2)}) {
		t.Fatalf("meetings %v", got)
	}
}

func TestRemoveRoom(t *testing.T) {
	s, _, dir := newStore(t)
	put(t, s, t0, t0, "a")
	if err := s.Remove("ops"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ops")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("room directory kept (%v)", err)
	}
}

// The layout before meeting history: <slug>.mp4 and <slug>.json at the top level.
func TestMigrateRecording(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("ops.mp4", "old video")
	write("ops.json", `{"meeting":"2026-10-15T11:00:00Z","ended":"2026-10-15T11:40:00Z","size":9}`)
	for i := 0; i < 2; i++ {
		s, err := Open(dir, 24*time.Hour, func() time.Time { return t0.Add(time.Hour) })
		if err != nil {
			t.Fatal(err)
		}
		parts := s.Parts("ops", ms(t0))
		if len(parts) != 1 || !parts[0].Part.Equal(t0) || parts[0].Size != 9 {
			t.Fatalf("start %d: parts %+v", i, parts)
		}
		if got := readPart(t, s, "ops", ms(t0), ms(t0)); got != "old video" {
			t.Fatalf("start %d: body %q", i, got)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "ops.*")); len(left) != 0 {
		t.Fatalf("top-level files left %v", left)
	}
}
