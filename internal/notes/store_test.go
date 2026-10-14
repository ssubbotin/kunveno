package notes

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func at(min, sec int) time.Time { return time.Date(2026, 10, 14, 9, min, sec, 0, time.UTC) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestStoreKeepsAMeeting(t *testing.T) {
	dir := t.TempDir()
	now := at(0, 0)
	s, err := Open(dir, 24*time.Hour, func() time.Time { return now })
	must(t, err)
	must(t, s.Begin("ops", "Планёрка", at(37, 0)))
	must(t, s.Phrase("ops", Phrase{Name: "Сергей", Start: at(37, 5), End: at(37, 12), Text: "Начнём."}))
	must(t, s.Pause("ops", at(51, 0)))
	if _, ok := s.Until("ops"); ok {
		t.Fatal("an open transcript reported ready")
	}
	if _, err := s.Load("ops"); !errors.Is(err, ErrNoNotes) {
		t.Fatalf("Load of an open transcript: %v", err)
	}
	must(t, s.Resume("ops", at(55, 0)))
	must(t, s.Phrase("ops", Phrase{Name: "Анна", Start: at(56, 0), End: at(56, 4), Text: "Продолжаем."}))
	must(t, s.End("ops", at(59, 0)))
	now = at(59, 30)
	until, ok := s.Until("ops")
	if !ok || !until.Equal(at(59, 0).Add(24*time.Hour)) {
		t.Fatalf("Until = %v, %v", until, ok)
	}
	tr, err := s.Load("ops")
	must(t, err)
	if tr.Slug != "ops" || tr.Room != "Планёрка" || !tr.Start.Equal(at(37, 0)) || !tr.End.Equal(at(59, 0)) {
		t.Fatalf("transcript = %+v", tr)
	}
	if len(tr.Phrases) != 2 || tr.Phrases[1].Name != "Анна" || tr.Phrases[1].Text != "Продолжаем." || !tr.Phrases[0].End.Equal(at(37, 12)) {
		t.Fatalf("phrases = %+v", tr.Phrases)
	}
	if len(tr.Events) != 2 || tr.Events[0].Kind != KindPaused || tr.Events[1].Kind != KindResumed || !tr.Events[1].At.Equal(at(55, 0)) {
		t.Fatalf("events = %+v", tr.Events)
	}
	if err := s.Phrase("ops", Phrase{Name: "Анна", Start: at(59, 10), End: at(59, 11), Text: "Ещё."}); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("phrase after the end: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, "ops.jsonl"))
	must(t, err)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v, want 0600", fi.Mode().Perm())
	}
}

// Review focus: a file cut mid-line by a crash stays readable and ends at its last full line.
func TestStoreRecoversAfterRestart(t *testing.T) {
	dir := t.TempDir()
	now := at(40, 0)
	clock := func() time.Time { return now }
	s, err := Open(dir, time.Hour, clock)
	must(t, err)
	must(t, s.Begin("ops", "Планёрка", at(37, 0)))
	must(t, s.Phrase("ops", Phrase{Name: "Сергей", Start: at(37, 5), End: at(37, 12), Text: "Начнём."}))
	f, err := os.OpenFile(filepath.Join(dir, "ops.jsonl"), os.O_WRONLY|os.O_APPEND, 0)
	must(t, err)
	_, err = f.WriteString(`{"type":"phrase","at":17604`)
	must(t, err)
	must(t, f.Close())

	s2, err := Open(dir, time.Hour, clock)
	must(t, err)
	until, ok := s2.Until("ops")
	if !ok || !until.Equal(at(37, 12).Add(time.Hour)) {
		t.Fatalf("Until after restart = %v, %v", until, ok)
	}
	tr, err := s2.Load("ops")
	must(t, err)
	if len(tr.Phrases) != 1 || !tr.End.Equal(at(37, 12)) {
		t.Fatalf("transcript after restart = %+v", tr)
	}
	data, err := os.ReadFile(filepath.Join(dir, "ops.jsonl"))
	must(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, `"type":"end"`) || strings.Contains(last, "17604") {
		t.Fatalf("last line = %q", last)
	}
	// A third start finds the end line and appends nothing.
	s3, err := Open(dir, time.Hour, clock)
	must(t, err)
	if _, ok := s3.Until("ops"); !ok {
		t.Fatal("not ready after a second restart")
	}
	again, _ := os.ReadFile(filepath.Join(dir, "ops.jsonl"))
	if string(again) != string(data) {
		t.Fatal("a clean restart changed the file")
	}
}

func TestStoreNextMeetingReplacesTheLast(t *testing.T) {
	s, err := Open(t.TempDir(), time.Hour, func() time.Time { return at(59, 0) })
	must(t, err)
	must(t, s.Begin("ops", "Планёрка", at(0, 0)))
	must(t, s.Phrase("ops", Phrase{Name: "Сергей", Start: at(1, 0), End: at(1, 2), Text: "Первая встреча."}))
	must(t, s.End("ops", at(10, 0)))
	must(t, s.Begin("ops", "Планёрка", at(30, 0)))
	if _, ok := s.Until("ops"); ok {
		t.Fatal("the old meeting's notes survive a new meeting's first phrase")
	}
	must(t, s.Phrase("ops", Phrase{Name: "Анна", Start: at(31, 0), End: at(31, 2), Text: "Вторая встреча."}))
	must(t, s.End("ops", at(40, 0)))
	tr, err := s.Load("ops")
	must(t, err)
	if len(tr.Phrases) != 1 || tr.Phrases[0].Text != "Вторая встреча." || !tr.Start.Equal(at(30, 0)) {
		t.Fatalf("transcript = %+v", tr)
	}
}

func TestStoreExpiryAndSweep(t *testing.T) {
	dir := t.TempDir()
	now := at(10, 0)
	s, err := Open(dir, time.Hour, func() time.Time { return now })
	must(t, err)
	for _, slug := range []string{"ops", "dev"} {
		must(t, s.Begin(slug, slug, at(0, 0)))
		must(t, s.Phrase(slug, Phrase{Name: "Анна", Start: at(1, 0), End: at(1, 2), Text: "Текст."}))
	}
	must(t, s.End("ops", at(10, 0)))
	now = at(10, 0).Add(59 * time.Minute)
	if _, ok := s.Until("ops"); !ok {
		t.Fatal("expired too early")
	}
	now = at(10, 0).Add(61 * time.Minute)
	if _, ok := s.Until("ops"); ok {
		t.Fatal("still ready after keep")
	}
	if _, err := s.Load("ops"); !errors.Is(err, ErrNoNotes) {
		t.Fatalf("Load after expiry: %v", err)
	}
	if gone := s.Sweep(); len(gone) != 1 || gone[0] != "ops" {
		t.Fatalf("Sweep = %v (an open transcript must stay)", gone)
	}
	if _, err := os.Stat(filepath.Join(dir, "ops.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expired file still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dev.jsonl")); err != nil {
		t.Fatalf("open transcript swept: %v", err)
	}
}

func TestStoreDeleteAndBadSlugs(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, time.Hour, func() time.Time { return at(30, 0) })
	must(t, err)
	must(t, s.Begin("retro", "Retro", at(0, 0)))
	must(t, s.Phrase("retro", Phrase{Name: "Анна", Start: at(1, 0), End: at(1, 2), Text: "Текст."}))
	must(t, s.End("retro", at(2, 0)))
	must(t, s.Delete("retro"))
	if _, ok := s.Until("retro"); ok {
		t.Fatal("deleted notes still ready")
	}
	if _, err := os.Stat(filepath.Join(dir, "retro.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("file not removed: %v", err)
	}
	must(t, s.Delete("retro")) // deleting nothing is fine
	for _, bad := range []string{"../etc", "Ops", "", strings.Repeat("a", 33)} {
		if err := s.Begin(bad, "x", at(0, 0)); !errors.Is(err, ErrBadSlug) {
			t.Errorf("Begin(%q) = %v", bad, err)
		}
		if _, err := s.Load(bad); !errors.Is(err, ErrBadSlug) {
			t.Errorf("Load(%q) = %v", bad, err)
		}
	}
	if err := s.Phrase("nope", Phrase{Text: "x"}); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("phrase without Begin: %v", err)
	}
}

func TestOpenWithoutDirectory(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "missing"), time.Hour, nil)
	must(t, err)
	if _, ok := s.Until("ops"); ok {
		t.Fatal("notes from nowhere")
	}
	must(t, s.Begin("ops", "Ops", time.Now())) // creates the directory
}

// A failed file removal still forgets the room's notes, so a room created again under the same
// slug neither lists nor serves the old ones.
func TestStoreDeleteForgetsNotesWhenRemoveFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	s, err := Open(dir, time.Hour, func() time.Time { return at(30, 0) })
	must(t, err)
	must(t, s.Begin("retro", "Retro", at(0, 0)))
	must(t, s.Phrase("retro", Phrase{Name: "Анна", Start: at(1, 0), End: at(1, 2), Text: "Текст."}))
	must(t, s.End("retro", at(2, 0)))
	must(t, os.Chmod(dir, 0o500))
	defer os.Chmod(dir, 0o700)
	if err := s.Delete("retro"); err == nil {
		t.Fatal("Delete reported success though the file cannot be removed")
	}
	if _, ok := s.Until("retro"); ok {
		t.Fatal("notes still listed after a failed removal")
	}
	if _, err := s.Load("retro"); !errors.Is(err, ErrNoNotes) {
		t.Fatalf("Load after a failed removal = %v", err)
	}
}

// One damaged notes file (unreadable, or a line over the reader's limit) does not stop the store
// from opening: the file is skipped and left as it is, the other rooms' notes load.
func TestOpenSkipsAFileItCannotRead(t *testing.T) {
	dir := t.TempDir()
	now := at(40, 0)
	clock := func() time.Time { return now }
	s, err := Open(dir, time.Hour, clock)
	must(t, err)
	must(t, s.Begin("ops", "Планёрка", at(37, 0)))
	must(t, s.Phrase("ops", Phrase{Name: "Сергей", Start: at(37, 5), End: at(37, 12), Text: "Начнём."}))
	must(t, s.End("ops", at(38, 0)))

	header := `{"type":"header","room":"Big","at":1760424000000}` + "\n"
	big := header + `{"type":"phrase","at":1760424001000,"text":"` + strings.Repeat("a", 2<<20) + `"}` + "\n"
	must(t, os.WriteFile(filepath.Join(dir, "big.jsonl"), []byte(big), 0o600))
	locked := filepath.Join(dir, "locked.jsonl")
	asRoot := os.Getuid() == 0 // root reads a 0000 file; that half of the test needs another user
	if !asRoot {
		must(t, os.WriteFile(locked, []byte(header), 0o600))
		must(t, os.Chmod(locked, 0))
	}

	s2, err := Open(dir, time.Hour, clock)
	if err != nil {
		t.Fatalf("Open with a damaged file: %v", err)
	}
	tr, err := s2.Load("ops")
	must(t, err)
	if len(tr.Phrases) != 1 || tr.Phrases[0].Text != "Начнём." {
		t.Fatalf("good transcript = %+v", tr)
	}
	if _, ok := s2.Until("big"); ok {
		t.Fatal("the oversized file was adopted")
	}
	if _, ok := s2.Until("locked"); ok && !asRoot {
		t.Fatal("the unreadable file was adopted")
	}
	data, err := os.ReadFile(filepath.Join(dir, "big.jsonl"))
	must(t, err)
	if string(data) != big {
		t.Fatal("the oversized file was changed")
	}
	if asRoot {
		return
	}
	fi, err := os.Stat(locked)
	must(t, err)
	if fi.Mode().Perm() != 0 || fi.Size() != int64(len(header)) {
		t.Fatalf("the unreadable file was changed: mode %v, size %d", fi.Mode().Perm(), fi.Size())
	}
}

// DeleteEnded removes ended notes only: open ones (still being recorded) are refused, missing or
// expired ones are reported as none.
func TestStoreDeleteEnded(t *testing.T) {
	dir := t.TempDir()
	now := at(30, 0)
	s, err := Open(dir, time.Hour, func() time.Time { return now })
	must(t, err)
	if err := s.DeleteEnded("ops"); !errors.Is(err, ErrNoNotes) {
		t.Fatalf("DeleteEnded of nothing = %v", err)
	}
	if err := s.DeleteEnded("../etc"); !errors.Is(err, ErrBadSlug) {
		t.Fatalf("DeleteEnded of a bad slug = %v", err)
	}
	must(t, s.Begin("ops", "Ops", at(0, 0)))
	must(t, s.Phrase("ops", Phrase{Name: "Анна", Start: at(1, 0), End: at(1, 2), Text: "Текст."}))
	if err := s.DeleteEnded("ops"); !errors.Is(err, ErrRecording) {
		t.Fatalf("DeleteEnded of an open transcript = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ops.jsonl")); err != nil {
		t.Fatalf("open transcript removed: %v", err)
	}
	must(t, s.Phrase("ops", Phrase{Name: "Анна", Start: at(2, 0), End: at(2, 2), Text: "Ещё."})) // still open
	must(t, s.End("ops", at(3, 0)))
	must(t, s.DeleteEnded("ops"))
	if _, ok := s.Until("ops"); ok {
		t.Fatal("deleted notes still ready")
	}
	if _, err := s.Load("ops"); !errors.Is(err, ErrNoNotes) {
		t.Fatalf("Load after DeleteEnded = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ops.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("file not removed: %v", err)
	}
	if err := s.Resume("ops", at(4, 0)); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("Resume after DeleteEnded = %v", err) // the signal handler then begins anew
	}
	if err := s.DeleteEnded("ops"); !errors.Is(err, ErrNoNotes) {
		t.Fatalf("second DeleteEnded = %v", err)
	}

	must(t, s.Begin("dev", "Dev", at(0, 0)))
	must(t, s.End("dev", at(5, 0)))
	now = at(5, 0).Add(61 * time.Minute)
	if err := s.DeleteEnded("dev"); !errors.Is(err, ErrNoNotes) {
		t.Fatalf("DeleteEnded of expired notes = %v", err)
	}
}

// A file that cannot be removed keeps the notes listed, so the person can try again.
func TestStoreDeleteEndedKeepsNotesWhenRemoveFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	s, err := Open(dir, time.Hour, func() time.Time { return at(30, 0) })
	must(t, err)
	must(t, s.Begin("ops", "Ops", at(0, 0)))
	must(t, s.End("ops", at(2, 0)))
	must(t, os.Chmod(dir, 0o500))
	defer os.Chmod(dir, 0o700)
	if err := s.DeleteEnded("ops"); err == nil || errors.Is(err, ErrNoNotes) || errors.Is(err, ErrRecording) {
		t.Fatalf("DeleteEnded with an unremovable file = %v", err)
	}
	if _, ok := s.Until("ops"); !ok {
		t.Fatal("notes forgotten though the file stays")
	}
}
