package lobby

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ssubbotin/kunveno/internal/documents"
	"github.com/ssubbotin/kunveno/internal/meetings"
	"github.com/ssubbotin/kunveno/internal/notes"
)

// A recording is accepted for a meeting the room still knows of: listed, with notes, or one that
// began recently (a meeting cut by a server restart has no index entry).
func TestKnows(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	ns, err := notes.Open(t.TempDir(), 24*time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	withNotes := now.Add(-3 * time.Hour)
	if err := ns.Begin("ops", "Ops", withNotes); err != nil {
		t.Fatal(err)
	}
	idx := meetings.Open(filepath.Join(t.TempDir(), "meetings.json"), nil)
	listed := now.Add(-40 * time.Hour)
	_ = idx.Add("ops", meetings.Summary{Start: listed, End: listed.Add(time.Hour), People: 2})
	k := Kept{Notes: ns, NotesEnabled: true, Meetings: idx}
	for name, c := range map[string]struct {
		meeting time.Time
		want    bool
	}{
		"listed":                {listed, true},
		"with notes":            {withNotes, true},
		"cut by a restart":      {now.Add(-30 * time.Minute), true},
		"older than the window": {now.Add(-30 * time.Hour), false},
		"in the future":         {now.Add(time.Hour), false},
	} {
		if got := k.Knows("ops", c.meeting, now, 24*time.Hour); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// Count gives the number of meetings History lists, without building them.
func TestCountMatchesHistory(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	ns, err := notes.Open(t.TempDir(), 24*time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	idx := meetings.Open(filepath.Join(t.TempDir(), "meetings.json"), nil)
	a, b := now.Add(-5*time.Hour), now.Add(-2*time.Hour)
	_ = idx.Add("ops", meetings.Summary{Start: a, End: a.Add(time.Hour), People: 2}) // listed only
	if err := ns.Begin("ops", "Ops", b); err != nil {                                // notes, not listed
		t.Fatal(err)
	}
	if err := ns.End("ops", b.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_ = idx.Add("dev", meetings.Summary{Start: a, End: a.Add(time.Hour), People: 2})
	k := Kept{Notes: ns, NotesEnabled: true, SummaryEnabled: true, Meetings: idx}
	if got, want := k.Count("ops"), len(k.History("ops")); got != want || want != 2 {
		t.Fatalf("Count %d, History %d", got, want)
	}
}

// Documents count as a meeting's material while documents are on, and go with a deleted room
// whatever is on.
func TestDocuments(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	ds, err := documents.Open(t.TempDir(), 24*time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	idx := meetings.Open(filepath.Join(t.TempDir(), "meetings.json"), nil)
	m := now.Add(-time.Hour)
	if _, err := ds.Put("ops", m.UnixMilli(), "minutes", "Протокол", []byte("x")); err != nil {
		t.Fatal(err)
	}
	k := Kept{Documents: ds, Meetings: idx}
	if k.Count("ops") != 0 || len(k.History("ops")) != 0 {
		t.Fatal("documents listed while off")
	}
	k.DocumentsEnabled = true
	h := k.History("ops")
	if k.Count("ops") != 1 || len(h) != 1 || !h[0].Start.Equal(m) || len(h[0].Documents) != 1 || h[0].Documents[0].ID != "minutes" {
		t.Fatalf("history %+v", h)
	}
	if !k.Knows("ops", m, now.Add(72*time.Hour), time.Hour) {
		t.Fatal("a meeting with documents is not known")
	}
	k.DocumentsEnabled = false
	k.Forget("ops")
	if ds.Has("ops", m.UnixMilli()) {
		t.Fatal("documents of a deleted room kept")
	}
}
