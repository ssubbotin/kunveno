package notes

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// meetingStore holds two ended meetings of ops: m1 at(0,0)–at(10,0), m2 at(20,0)–at(30,0); keep 1h.
func meetingStore(t *testing.T) (s *Store, dir string, now *time.Time, m1, m2 int64) {
	t.Helper()
	dir = t.TempDir()
	clock := at(31, 0)
	now = &clock
	s, err := Open(dir, time.Hour, func() time.Time { return *now })
	must(t, err)
	must(t, s.Begin("ops", "Планёрка", at(0, 0)))
	must(t, s.Phrase("ops", Phrase{Name: "Анна", Start: at(1, 0), End: at(1, 5), Text: "первая"}))
	must(t, s.End("ops", at(10, 0)))
	must(t, s.Begin("ops", "Планёрка", at(20, 0)))
	must(t, s.Phrase("ops", Phrase{Name: "Борис", Start: at(21, 0), End: at(21, 5), Text: "вторая"}))
	must(t, s.End("ops", at(30, 0)))
	return s, dir, now, at(0, 0).UnixMilli(), at(20, 0).UnixMilli()
}

func TestMeetingsKeptApart(t *testing.T) {
	s, dir, _, m1, m2 := meetingStore(t)
	got := s.List("ops")
	if len(got) != 2 || got[0].Meeting != m2 || got[1].Meeting != m1 || !got[1].Ended.Equal(at(10, 0)) || !got[1].Until.Equal(at(10, 0).Add(time.Hour)) {
		t.Fatalf("list %+v", got)
	}
	t1, err := s.Load("ops", m1)
	must(t, err)
	if len(t1.Phrases) != 1 || t1.Phrases[0].Text != "первая" {
		t.Fatalf("m1 %+v", t1.Phrases)
	}
	if l, ok := s.Latest("ops"); !ok || l != m2 {
		t.Fatalf("latest %v %v", l, ok)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "ops", "*.jsonl"))
	if len(files) != 2 {
		t.Fatalf("files %v", files)
	}
}

func TestReopenInSameMeeting(t *testing.T) {
	s, _, _, _, m2 := meetingStore(t)
	must(t, s.Resume("ops", at(30, 30)))
	must(t, s.Phrase("ops", Phrase{Name: "Борис", Start: at(30, 40), End: at(30, 45), Text: "ещё"}))
	must(t, s.End("ops", at(30, 50)))
	if got := s.List("ops"); len(got) != 2 {
		t.Fatalf("a reopening made another meeting: %+v", got)
	}
	tr, err := s.Load("ops", m2)
	must(t, err)
	if len(tr.Phrases) != 2 || tr.Phrases[1].Text != "ещё" {
		t.Fatalf("m2 %+v", tr.Phrases)
	}
}

func TestDeleteOneMeeting(t *testing.T) {
	s, _, _, m1, m2 := meetingStore(t)
	must(t, s.DeleteEnded("ops", m1))
	if got := s.List("ops"); len(got) != 1 || got[0].Meeting != m2 {
		t.Fatalf("list %+v", got)
	}
	if _, err := s.Load("ops", m1); !errors.Is(err, ErrNoNotes) {
		t.Fatalf("deleted meeting: %v", err)
	}
	must(t, s.Begin("ops", "Планёрка", at(40, 0)))
	if err := s.DeleteEnded("ops", at(40, 0).UnixMilli()); !errors.Is(err, ErrRecording) {
		t.Fatalf("open meeting: %v", err)
	}
	if err := s.DeleteEnded("ops", 12345); !errors.Is(err, ErrNoNotes) {
		t.Fatalf("unknown meeting: %v", err)
	}
}

func TestSweepPerMeeting(t *testing.T) {
	s, _, now, m1, m2 := meetingStore(t)
	*now = at(10, 0).Add(time.Hour + time.Second) // m1 expired, m2 not
	if gone := s.Sweep(); !reflect.DeepEqual(gone, []Gone{{Slug: "ops", Meeting: m1}}) {
		t.Fatalf("swept %v", gone)
	}
	if got := s.List("ops"); len(got) != 1 || got[0].Meeting != m2 {
		t.Fatalf("list %+v", got)
	}
}

func TestDeleteRoomRemovesDirectory(t *testing.T) {
	s, dir, _, _, _ := meetingStore(t)
	must(t, s.Delete("ops"))
	if _, err := os.Stat(filepath.Join(dir, "ops")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("room directory kept (%v)", err)
	}
	if got := s.List("ops"); len(got) != 0 {
		t.Fatalf("list %+v", got)
	}
}

// The layout before meeting history: one top-level transcript per room.
func TestMigrateNotes(t *testing.T) {
	dir := t.TempDir()
	clock := at(31, 0)
	old := `{"type":"header","room":"Планёрка","at":` + itoa(at(0, 0).UnixMilli()) + "}\n" +
		`{"type":"phrase","at":` + itoa(at(1, 0).UnixMilli()) + `,"end":` + itoa(at(1, 5).UnixMilli()) + `,"name":"Анна","text":"старое"}` + "\n" +
		`{"type":"end","at":` + itoa(at(10, 0).UnixMilli()) + "}\n"
	must(t, os.WriteFile(filepath.Join(dir, "ops.jsonl"), []byte(old), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "bad.jsonl"), []byte("not json\n"), 0o600))
	for i := 0; i < 2; i++ { // a second start changes nothing
		s, err := Open(dir, time.Hour, func() time.Time { return clock })
		must(t, err)
		tr, err := s.Load("ops", at(0, 0).UnixMilli())
		if err != nil || len(tr.Phrases) != 1 || tr.Phrases[0].Text != "старое" {
			t.Fatalf("start %d: %+v %v", i, tr, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "ops.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old file left")
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.jsonl")); err != nil {
		t.Fatal("an unreadable file was moved or removed")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// Has: a meeting keeps something while its transcript or its summary is kept.
func TestHas(t *testing.T) {
	s, _, _, m1, _ := meetingStore(t)
	if !s.Has("ops", m1) || s.Has("ops", 1) || s.Has("dev", m1) {
		t.Fatal("Has of the transcripts")
	}
	must(t, s.PutSummary("ops", m1, at(10, 0).UnixMilli(), []byte("x")))
	must(t, s.DeleteEnded("ops", m1))
	if !s.Has("ops", m1) {
		t.Fatal("a meeting with only its summary keeps nothing")
	}
	must(t, s.DeleteSummary("ops", m1))
	if s.Has("ops", m1) {
		t.Fatal("a meeting with nothing left keeps something")
	}
}
