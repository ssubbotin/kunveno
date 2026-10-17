package lobby

import (
	"path/filepath"
	"testing"
	"time"

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
