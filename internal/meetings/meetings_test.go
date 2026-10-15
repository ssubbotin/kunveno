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

func TestAddKeepsTheLastFiveNewestFirstAndSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "meetings.json")
	clock := t0
	s := Open(path, func() time.Time { return clock })
	for i := 0; i < 7; i++ {
		if err := s.Add("ops", meeting(i)); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Add("ops", meeting(6)) // the same meeting saved twice stays one entry
	_ = s.Add("dev", meeting(0))
	got := s.List("ops")
	if len(got) != Keep {
		t.Fatalf("kept %d, want %d", len(got), Keep)
	}
	for i, m := range got {
		if want := meeting(6 - i); !m.Start.Equal(want.Start) || m.People != want.People {
			t.Fatalf("entry %d = %+v, want %+v", i, m, want)
		}
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v, %v", st, err)
	}
	again := Open(path, func() time.Time { return clock })
	if got := again.List("ops"); len(got) != Keep || !got[0].End.Equal(meeting(6).End) {
		t.Fatalf("after a restart: %+v", got)
	}
	if got := again.List("dev"); len(got) != 1 {
		t.Fatalf("dev after a restart: %+v", got)
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

func TestSweepDropsEntriesOlderThanThirtyDays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meetings.json")
	clock := t0
	s := Open(path, func() time.Time { return clock })
	_ = s.Add("ops", meeting(0))
	_ = s.Add("ops", meeting(24*20)) // twenty days later
	_ = s.Add("dev", meeting(1))
	clock = meeting(1).End.Add(MaxAge + time.Minute)
	if err := s.Sweep(); err != nil {
		t.Fatal(err)
	}
	if got := s.List("ops"); len(got) != 1 || !got[0].Start.Equal(meeting(24*20).Start) {
		t.Fatalf("ops after the sweep: %+v", got)
	}
	if got := s.List("dev"); len(got) != 0 {
		t.Fatalf("dev after the sweep: %+v", got)
	}
	if got := Open(path, nil).List("dev"); len(got) != 0 {
		t.Fatal("the sweep was not saved")
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
