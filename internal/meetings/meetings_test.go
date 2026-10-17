package meetings

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 15, 11, 0, 0, 0, time.UTC)

func meeting(i int) Summary {
	start := t0.Add(time.Duration(i) * time.Hour)
	return Summary{Start: start, End: start.Add(27 * time.Minute), People: i + 1}
}

// Every meeting is listed, newest first; the list survives a restart.
func TestIndexHasNoCountLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "meetings.json")
	clock := t0
	s := Open(path, func() time.Time { return clock })
	for i := 0; i < 12; i++ {
		if err := s.Add("ops", meeting(i)); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Add("ops", meeting(11)) // the same meeting saved twice stays one entry
	_ = s.Add("dev", meeting(0))
	got := s.List("ops")
	if len(got) != 12 || s.Count("ops") != 12 {
		t.Fatalf("kept %d (count %d), want 12", len(got), s.Count("ops"))
	}
	for i, m := range got {
		if want := meeting(11 - i); !m.Start.Equal(want.Start) || m.People != want.People {
			t.Fatalf("entry %d = %+v, want %+v", i, m, want)
		}
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v, %v", st, err)
	}
	again := Open(path, func() time.Time { return clock })
	if got := again.List("ops"); len(got) != 12 || !got[0].End.Equal(meeting(11).End) {
		t.Fatalf("after a restart: %+v", got)
	}
}

func TestFindMatchesTheStartToTheMillisecond(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "meetings.json"), nil)
	m := meeting(0)
	m.Start = m.Start.Add(123456789 * time.Nanosecond)
	_ = s.Add("ops", m)
	_ = s.Add("ops", meeting(1))
	got, ok := s.Find("ops", time.UnixMilli(m.Start.UnixMilli())) // notes keep milliseconds
	if !ok || got.People != m.People {
		t.Fatalf("find = %+v, %v", got, ok)
	}
	if _, ok := s.Find("ops", t0.Add(30*time.Minute)); ok {
		t.Fatal("found a meeting that was never saved")
	}
	if _, ok := s.Find("dev", m.Start); ok {
		t.Fatal("found a meeting of another room")
	}
}

// An entry with material (notes, a summary, a recording part) stays however old; one with nothing
// left goes once its end is older than keep.
func TestIndexKeepsMeetingsWithMaterial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meetings.json")
	clock := t0
	s := Open(path, func() time.Time { return clock })
	s.SetKeep(30 * 24 * time.Hour)
	_ = s.Add("ops", meeting(0))
	_ = s.Add("ops", meeting(1))
	_ = s.Add("gone", meeting(2)) // a room no longer in the configuration
	clock = meeting(2).End.Add(31 * 24 * time.Hour)
	has := func(slug string, start int64) bool {
		return (slug == "ops" && start == meeting(1).Start.UnixMilli()) || slug == "gone"
	}
	if err := s.Sweep(has); err != nil {
		t.Fatal(err)
	}
	if got := s.List("ops"); len(got) != 1 || !got[0].Start.Equal(meeting(1).Start) {
		t.Fatalf("ops after the sweep: %+v", got)
	}
	if got := s.List("gone"); len(got) != 1 {
		t.Fatalf("a removed room's meeting with material was dropped: %+v", got)
	}
	if got := Open(path, nil).List("ops"); len(got) != 1 {
		t.Fatal("the sweep was not saved")
	}
}

func TestIndexDropsEmptyAfterKeep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meetings.json")
	clock := t0
	s := Open(path, func() time.Time { return clock })
	s.SetKeep(48 * time.Hour)
	_ = s.Add("ops", meeting(0))
	clock = meeting(0).End.Add(47 * time.Hour)
	_ = s.Sweep(nil)
	if s.Count("ops") != 1 {
		t.Fatal("dropped before keep")
	}
	clock = meeting(0).End.Add(49 * time.Hour)
	_ = s.Sweep(nil)
	if s.Count("ops") != 0 {
		t.Fatal("kept after keep with nothing left")
	}
}

func TestDeleteForgetsTheRoom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meetings.json")
	s := Open(path, nil)
	_ = s.Add("ops", meeting(0))
	_ = s.Add("dev", meeting(1))
	if err := s.Delete("ops"); err != nil {
		t.Fatal(err)
	}
	if len(s.List("ops")) != 0 || len(Open(path, nil).List("ops")) != 0 {
		t.Fatal("the deleted room's meetings stay")
	}
	if len(Open(path, nil).List("dev")) != 1 {
		t.Fatal("another room's meetings were lost")
	}
}

func TestCorruptFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meetings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(path, nil)
	if len(s.List("ops")) != 0 {
		t.Fatal("entries from a corrupt file")
	}
	if err := s.Add("ops", meeting(0)); err != nil {
		t.Fatal(err)
	}
	if len(Open(path, nil).List("ops")) != 1 {
		t.Fatal("the store cannot write over a corrupt file")
	}
}

func TestNilStoreIsSafe(t *testing.T) {
	var s *Store
	if err := s.Add("ops", meeting(0)); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Find("ops", t0); ok {
		t.Fatal("a nil store found a meeting")
	}
}
