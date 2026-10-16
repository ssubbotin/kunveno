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
// a clock the test moves.
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

func summaryFile(dir string) string { return filepath.Join(dir, "ops.summary.md") }

func assertNoSummary(t *testing.T, s *Store, dir, after string) {
	t.Helper()
	if st := s.SummaryState("ops"); st != SummaryNone {
		t.Errorf("after %s: state %q", after, st)
	}
	if _, err := s.LoadSummary("ops"); !errors.Is(err, ErrNoSummary) {
		t.Errorf("after %s: LoadSummary err %v", after, err)
	}
	if _, err := os.Stat(summaryFile(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after %s: summary file still there (%v)", after, err)
	}
}

func TestSummaryFollowsTheNotes(t *testing.T) {
	s, _, _ := endedStore(t)
	end := at(10, 0).UnixMilli()
	if got := s.PendingSummaries(); !reflect.DeepEqual(got, []Pending{{Slug: "ops", End: end}}) {
		t.Fatalf("pending %v", got)
	}
	if got, ok := s.EndedAt("ops"); !ok || got != end {
		t.Fatalf("EndedAt %v %v", got, ok)
	}
	if st := s.SummaryState("ops"); st != SummaryPending {
		t.Fatalf("state %q", st)
	}
	must(t, s.PutSummary("ops", end, []byte("x")))
	if st := s.SummaryState("ops"); st != SummaryReady {
		t.Fatalf("state %q", st)
	}
	sm, err := s.LoadSummary("ops")
	must(t, err)
	if string(sm.Body) != "x" || sm.Room != "Планёрка" || !sm.Start.Equal(at(0, 0)) || !sm.End.Equal(at(10, 0)) {
		t.Fatalf("summary %+v", sm)
	}
	if got := s.PendingSummaries(); len(got) != 0 {
		t.Fatalf("pending after upload %v", got)
	}
}

func TestSummaryRefusedForOtherNotes(t *testing.T) {
	s, _, _ := endedStore(t)
	end := at(10, 0).UnixMilli()
	if err := s.PutSummary("ops", end+1, []byte("x")); !errors.Is(err, ErrStale) {
		t.Fatalf("other end: %v", err)
	}
	if err := s.PutSummary("nope", end, []byte("x")); !errors.Is(err, ErrStale) {
		t.Fatalf("unknown room: %v", err)
	}
	must(t, s.Begin("ops", "Планёрка", at(15, 0)))
	if err := s.PutSummary("ops", end, []byte("x")); !errors.Is(err, ErrStale) {
		t.Fatalf("open notes: %v", err)
	}
	if got := s.PendingSummaries(); len(got) != 0 {
		t.Fatalf("pending while open %v", got)
	}
}

func TestSummaryGoesWithTheNotes(t *testing.T) {
	end := at(10, 0).UnixMilli()
	ready := func(t *testing.T) (*Store, string, *time.Time) {
		s, dir, now := endedStore(t)
		must(t, s.PutSummary("ops", end, []byte("x")))
		return s, dir, now
	}
	t.Run("resume", func(t *testing.T) {
		s, dir, _ := ready(t)
		must(t, s.Resume("ops", at(12, 0)))
		assertNoSummary(t, s, dir, "Resume")
		must(t, s.End("ops", at(14, 0)))
		if got := s.PendingSummaries(); !reflect.DeepEqual(got, []Pending{{Slug: "ops", End: at(14, 0).UnixMilli()}}) {
			t.Fatalf("pending after the new end %v", got)
		}
	})
	t.Run("begin", func(t *testing.T) {
		s, dir, _ := ready(t)
		must(t, s.Begin("ops", "Планёрка", at(15, 0)))
		assertNoSummary(t, s, dir, "Begin")
	})
	t.Run("delete ended", func(t *testing.T) {
		s, dir, _ := ready(t)
		must(t, s.DeleteEnded("ops"))
		assertNoSummary(t, s, dir, "DeleteEnded")
	})
	t.Run("delete", func(t *testing.T) {
		s, dir, _ := ready(t)
		must(t, s.Delete("ops"))
		assertNoSummary(t, s, dir, "Delete")
	})
	t.Run("sweep", func(t *testing.T) {
		s, dir, now := ready(t)
		*now = at(10, 0).Add(2 * time.Hour)
		if gone := s.Sweep(); !reflect.DeepEqual(gone, []string{"ops"}) {
			t.Fatalf("swept %v", gone)
		}
		assertNoSummary(t, s, dir, "Sweep")
	})
	t.Run("expired, before the sweep", func(t *testing.T) {
		s, _, now := ready(t)
		*now = at(10, 0).Add(2 * time.Hour)
		if st := s.SummaryState("ops"); st != SummaryNone {
			t.Fatalf("state of expired notes %q", st)
		}
		if _, err := s.LoadSummary("ops"); !errors.Is(err, ErrNoSummary) {
			t.Fatalf("LoadSummary of expired notes: %v", err)
		}
	})
}

func TestLateUploadAfterDelete(t *testing.T) {
	s, dir, _ := endedStore(t)
	end := at(10, 0).UnixMilli()
	must(t, s.DeleteEnded("ops"))
	if err := s.PutSummary("ops", end, []byte("x")); !errors.Is(err, ErrStale) {
		t.Fatalf("upload after delete: %v", err)
	}
	if err := s.FailSummary("ops", end); !errors.Is(err, ErrStale) {
		t.Fatalf("fail after delete: %v", err)
	}
	if _, err := os.Stat(summaryFile(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file written for deleted notes (%v)", err)
	}
}

func TestFailedSummary(t *testing.T) {
	s, _, _ := endedStore(t)
	end := at(10, 0).UnixMilli()
	must(t, s.FailSummary("ops", end))
	if st := s.SummaryState("ops"); st != SummaryFailed {
		t.Fatalf("state %q", st)
	}
	if got := s.PendingSummaries(); len(got) != 0 {
		t.Fatalf("failed still pending %v", got)
	}
	must(t, s.PutSummary("ops", end, []byte("late but fine")))
	if st := s.SummaryState("ops"); st != SummaryReady {
		t.Fatalf("state after a late upload %q", st)
	}
}

func TestOpenDropsOrphanSummaries(t *testing.T) {
	s, dir, now := endedStore(t)
	must(t, s.PutSummary("ops", at(10, 0).UnixMilli(), []byte("x")))
	// An orphan summary, and an expired room with one.
	must(t, os.WriteFile(filepath.Join(dir, "lost.summary.md"), []byte("y"), 0o600))
	must(t, s.Begin("old", "Старая", at(0, 0)))
	must(t, s.End("old", at(1, 0)))
	must(t, s.PutSummary("old", at(1, 0).UnixMilli(), []byte("z")))

	*now = at(10, 30)
	s2, err := Open(dir, time.Hour, func() time.Time { return *now })
	must(t, err)
	if _, err := os.Stat(filepath.Join(dir, "lost.summary.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan summary kept (%v)", err)
	}
	if st := s2.SummaryState("ops"); st != SummaryReady {
		t.Fatalf("ops after restart %q", st)
	}
	*now = at(1, 0).Add(2 * time.Hour) // "old" expired; ops (ended 10:00) still kept
	if st := s2.SummaryState("old"); st != SummaryNone {
		t.Fatalf("expired room after restart %q", st)
	}
}
