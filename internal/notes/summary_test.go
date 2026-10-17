package notes

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// endedStore is a store with one room's ended notes (meeting at(0,0)–at(10,0)), keep one hour, and
// a clock the test moves. m0 names that meeting, e0 its end.
func endedStore(t *testing.T) (s *Store, dir string, now *time.Time) {
	t.Helper()
	dir = t.TempDir()
	clock := at(20, 0)
	now = &clock
	s, err := Open(dir, time.Hour, func() time.Time { return *now })
	must(t, err)
	must(t, s.Begin("ops", "Планёрка", at(0, 0)))
	must(t, s.Phrase("ops", Phrase{Name: "Анна", Start: at(1, 0), End: at(1, 5), Text: "Привет"}))
	must(t, s.End("ops", at(10, 0)))
	return s, dir, now
}

var (
	m0 = at(0, 0).UnixMilli()
	e0 = at(10, 0).UnixMilli()
)

func summaryFiles(dir, pattern string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "ops", pattern))
	return files
}

func assertNoSummary(t *testing.T, s *Store, dir string, meeting int64, after string) {
	t.Helper()
	if st := s.SummaryState("ops", meeting); st != SummaryNone {
		t.Errorf("after %s: state %q", after, st)
	}
	if _, err := s.LoadSummary("ops", meeting); !errors.Is(err, ErrNoSummary) {
		t.Errorf("after %s: LoadSummary err %v", after, err)
	}
	if f := summaryFiles(dir, "*.summary.md"); len(f) != 0 {
		t.Errorf("after %s: summary files %v", after, f)
	}
}

func TestSummaryFollowsTheNotes(t *testing.T) {
	s, _, _ := endedStore(t)
	if got := s.PendingSummaries(); !reflect.DeepEqual(got, []Pending{{Slug: "ops", Meeting: m0, End: e0}}) {
		t.Fatalf("pending %v", got)
	}
	if got, ok := s.EndedAt("ops", m0); !ok || got != e0 {
		t.Fatalf("EndedAt %v %v", got, ok)
	}
	if m, ok := s.MeetingEndedAt("ops", e0); !ok || m != m0 {
		t.Fatalf("MeetingEndedAt %v %v", m, ok)
	}
	if st := s.SummaryState("ops", m0); st != SummaryPending {
		t.Fatalf("state %q", st)
	}
	must(t, s.PutSummary("ops", m0, e0, []byte("x")))
	if st := s.SummaryState("ops", m0); st != SummaryReady {
		t.Fatalf("state %q", st)
	}
	sm, err := s.LoadSummary("ops", m0)
	must(t, err)
	if string(sm.Body) != "x" || sm.Room != "Планёрка" || !sm.Start.Equal(at(0, 0)) || !sm.End.Equal(at(10, 0)) {
		t.Fatalf("summary %+v", sm)
	}
	if got := s.PendingSummaries(); len(got) != 0 {
		t.Fatalf("pending after upload %v", got)
	}
	if m, ok := s.LatestSummary("ops"); !ok || m != m0 {
		t.Fatalf("LatestSummary %v %v", m, ok)
	}
}

func TestSummaryRefusedForOtherNotes(t *testing.T) {
	s, _, _ := endedStore(t)
	if err := s.PutSummary("ops", m0, e0+1, []byte("x")); !errors.Is(err, ErrStale) {
		t.Fatalf("other end: %v", err)
	}
	if err := s.PutSummary("ops", m0+1, e0, []byte("x")); !errors.Is(err, ErrStale) {
		t.Fatalf("other meeting: %v", err)
	}
	if err := s.PutSummary("nope", m0, e0, []byte("x")); !errors.Is(err, ErrStale) {
		t.Fatalf("unknown room: %v", err)
	}
	must(t, s.Begin("ops", "Планёрка", at(15, 0)))
	m1 := at(15, 0).UnixMilli()
	if err := s.PutSummary("ops", m1, e0, []byte("x")); !errors.Is(err, ErrStale) {
		t.Fatalf("open notes: %v", err)
	}
}

// A second meeting does not disturb the first one's summary.
func TestSummaryOfEarlierMeeting(t *testing.T) {
	s, _, _ := endedStore(t)
	must(t, s.Begin("ops", "Планёрка", at(15, 0)))
	m1 := at(15, 0).UnixMilli()
	if got := s.PendingSummaries(); !reflect.DeepEqual(got, []Pending{{Slug: "ops", Meeting: m0, End: e0}}) {
		t.Fatalf("pending while the next meeting runs %v", got)
	}
	must(t, s.PutSummary("ops", m0, e0, []byte("первая")))
	must(t, s.End("ops", at(18, 0)))
	if got := s.PendingSummaries(); !reflect.DeepEqual(got, []Pending{{Slug: "ops", Meeting: m1, End: at(18, 0).UnixMilli()}}) {
		t.Fatalf("pending %v", got)
	}
	if st := s.SummaryState("ops", m0); st != SummaryReady {
		t.Fatalf("first meeting %q", st)
	}
	if m, ok := s.LatestSummary("ops"); !ok || m != m1 {
		t.Fatalf("latest summary meeting %v %v (the pending one is the latest)", m, ok)
	}
}

func TestSummaryGoesWithReopenedNotesRoomAndExpiry(t *testing.T) {
	ready := func(t *testing.T) (*Store, string, *time.Time) {
		s, dir, now := endedStore(t)
		must(t, s.PutSummary("ops", m0, e0, []byte("x")))
		return s, dir, now
	}
	t.Run("resume", func(t *testing.T) {
		s, dir, _ := ready(t)
		must(t, s.Resume("ops", at(12, 0)))
		assertNoSummary(t, s, dir, m0, "Resume")
		must(t, s.End("ops", at(14, 0)))
		if got := s.PendingSummaries(); !reflect.DeepEqual(got, []Pending{{Slug: "ops", Meeting: m0, End: at(14, 0).UnixMilli()}}) {
			t.Fatalf("pending after the new end %v", got)
		}
	})
	t.Run("room deleted", func(t *testing.T) {
		s, dir, _ := ready(t)
		must(t, s.Delete("ops"))
		assertNoSummary(t, s, dir, m0, "Delete")
	})
	t.Run("sweep", func(t *testing.T) {
		s, dir, now := ready(t)
		*now = at(10, 0).Add(2 * time.Hour)
		s.Sweep()
		assertNoSummary(t, s, dir, m0, "Sweep")
	})
	t.Run("expired, before the sweep", func(t *testing.T) {
		s, _, now := ready(t)
		*now = at(10, 0).Add(2 * time.Hour)
		if st := s.SummaryState("ops", m0); st != SummaryNone {
			t.Fatalf("state of expired %q", st)
		}
	})
	t.Run("a new meeting keeps it", func(t *testing.T) {
		s, _, _ := ready(t)
		must(t, s.Begin("ops", "Планёрка", at(15, 0)))
		if st := s.SummaryState("ops", m0); st != SummaryReady {
			t.Fatalf("state %q", st)
		}
	})
}

func TestSummaryOutlivesDeletedNotes(t *testing.T) {
	s, dir, now := endedStore(t)
	must(t, s.PutSummary("ops", m0, e0, []byte("итог")))
	must(t, s.DeleteEnded("ops", m0))
	if st := s.SummaryState("ops", m0); st != SummaryReady {
		t.Fatalf("state %q", st)
	}
	if u, ok := s.SummaryUntil("ops", m0); !ok || !u.Equal(at(10, 0).Add(time.Hour)) {
		t.Fatalf("until %v %v", u, ok)
	}
	s2, err := Open(dir, time.Hour, func() time.Time { return *now })
	must(t, err)
	if sm, err := s2.LoadSummary("ops", m0); err != nil || string(sm.Body) != "итог" || sm.Room != "Планёрка" || !sm.Start.Equal(at(0, 0)) {
		t.Fatalf("after restart: %+v %v", sm, err)
	}
	must(t, s2.DeleteSummary("ops", m0))
	if files, _ := filepath.Glob(filepath.Join(dir, "ops", "*")); len(files) != 0 {
		t.Fatalf("files left %v", files)
	}
}

func TestLateUploadAfterDelete(t *testing.T) {
	s, dir, _ := endedStore(t)
	must(t, s.DeleteEnded("ops", m0))
	if err := s.PutSummary("ops", m0, e0, []byte("x")); !errors.Is(err, ErrStale) {
		t.Fatalf("upload after delete: %v", err)
	}
	if err := s.FailSummary("ops", m0, e0); !errors.Is(err, ErrStale) {
		t.Fatalf("fail after delete: %v", err)
	}
	if f := summaryFiles(dir, "*.summary.md"); len(f) != 0 {
		t.Fatalf("file written for deleted notes %v", f)
	}
}

func TestFailedSummary(t *testing.T) {
	s, _, _ := endedStore(t)
	must(t, s.FailSummary("ops", m0, e0))
	if st := s.SummaryState("ops", m0); st != SummaryFailed {
		t.Fatalf("state %q", st)
	}
	if got := s.PendingSummaries(); len(got) != 0 {
		t.Fatalf("failed still pending %v", got)
	}
	must(t, s.PutSummary("ops", m0, e0, []byte("late but fine")))
	if st := s.SummaryState("ops", m0); st != SummaryReady {
		t.Fatalf("state after a late upload %q", st)
	}
}

func TestDeleteSummaryAlone(t *testing.T) {
	s, dir, now := endedStore(t)
	must(t, s.PutSummary("ops", m0, e0, []byte("x")))
	must(t, s.DeleteSummary("ops", m0))
	if st := s.SummaryState("ops", m0); st != SummaryNone {
		t.Fatalf("state after delete %q", st)
	}
	if got := s.PendingSummaries(); len(got) != 0 {
		t.Fatalf("deleted summary pending again %v", got)
	}
	if err := s.PutSummary("ops", m0, e0, []byte("again")); !errors.Is(err, ErrStale) {
		t.Fatalf("upload after delete: %v", err)
	}
	if err := s.DeleteSummary("ops", m0); !errors.Is(err, ErrNoSummary) {
		t.Fatalf("second delete: %v", err)
	}
	s2, err := Open(dir, time.Hour, func() time.Time { return *now })
	must(t, err)
	if st := s2.SummaryState("ops", m0); st != SummaryNone || len(s2.PendingSummaries()) != 0 {
		t.Fatalf("after restart: state %q, pending %v", st, s2.PendingSummaries())
	}
	must(t, s2.Resume("ops", at(11, 0)))
	must(t, s2.End("ops", at(12, 0)))
	if st := s2.SummaryState("ops", m0); st != SummaryPending {
		t.Fatalf("state for reopened notes %q", st)
	}
}

func TestOpenDropsOrphanSummaries(t *testing.T) {
	s, dir, now := endedStore(t)
	must(t, s.PutSummary("ops", m0, e0, []byte("x")))
	must(t, os.WriteFile(filepath.Join(dir, "ops", "garbage.summary.md"), []byte("y"), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "ops", "1.2.summary.md.tmp"), []byte("y"), 0o600))
	*now = at(10, 30)
	s2, err := Open(dir, time.Hour, func() time.Time { return *now })
	must(t, err)
	if st := s2.SummaryState("ops", m0); st != SummaryReady {
		t.Fatalf("ops after restart %q", st)
	}
	if f := summaryFiles(dir, "garbage*"); len(f) != 0 {
		t.Fatalf("orphan kept %v", f)
	}
	if f := summaryFiles(dir, "*.tmp"); len(f) != 0 {
		t.Fatalf("tmp kept %v", f)
	}
}

// A summary file of the same meeting's earlier end (a crash after the notes were reopened) is not
// served for the longer notes.
func TestOpenDropsASummaryOfOtherNotes(t *testing.T) {
	s, dir, now := endedStore(t)
	must(t, s.PutSummary("ops", m0, e0, []byte("old")))
	saved := summaryFiles(dir, "*.summary.md")[0]
	data, err := os.ReadFile(saved)
	must(t, err)
	must(t, s.Resume("ops", at(11, 0)))
	must(t, s.End("ops", at(15, 0)))
	must(t, os.WriteFile(saved, data, 0o600))
	*now = at(16, 0)
	s2, err := Open(dir, time.Hour, func() time.Time { return *now })
	must(t, err)
	if st := s2.SummaryState("ops", m0); st != SummaryPending {
		t.Fatalf("state %q", st)
	}
	if _, err := os.Stat(saved); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old summary file kept (%v)", err)
	}
}

// The layout before meeting history: <slug>.<end>.summary.md and its mark at the top level.
func TestMigrateSummaries(t *testing.T) {
	dir := t.TempDir()
	clock := at(20, 0)
	head := `{"type":"header","room":"Планёрка","at":` + itoa(m0) + "}\n" + `{"type":"end","at":` + itoa(e0) + "}\n"
	must(t, os.WriteFile(filepath.Join(dir, "ops.jsonl"), []byte(head), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "ops."+itoa(e0)+".summary.md"), []byte(`{"room":"Планёрка","start":`+itoa(m0)+"}\n## Кратко\n"), 0o600))
	// A summary whose notes were deleted before the upgrade, and a mark next to nothing.
	m9, e9 := at(2, 0).UnixMilli(), at(9, 0).UnixMilli()
	must(t, os.WriteFile(filepath.Join(dir, "dev."+itoa(e9)+".summary.md"), []byte(`{"room":"Dev","start":`+itoa(m9)+"}\nитог\n"), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "qa.123.summary.deleted"), nil, 0o600))
	for i := 0; i < 2; i++ {
		s, err := Open(dir, time.Hour, func() time.Time { return clock })
		must(t, err)
		if sm, err := s.LoadSummary("ops", m0); err != nil || string(sm.Body) != "## Кратко\n" {
			t.Fatalf("start %d ops: %+v %v", i, sm, err)
		}
		if sm, err := s.LoadSummary("dev", m9); err != nil || sm.Room != "Dev" {
			t.Fatalf("start %d dev: %+v %v", i, sm, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.summary.*")); len(left) != 0 {
		t.Fatalf("top-level files left %v", left)
	}
}
