package recording

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 15, 11, 0, 0, 0, time.UTC)

func newStore(t *testing.T) (*Store, *time.Time, string) {
	t.Helper()
	dir := t.TempDir()
	now := t0
	s, err := Open(dir, 24*time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return s, &now, dir
}

func read(t *testing.T, s *Store, slug string) (string, Meta) {
	t.Helper()
	f, m, err := s.Open(slug)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	return string(b), m
}

func TestPutAndOpen(t *testing.T) {
	s, now, dir := newStore(t)
	*now = t0.Add(30 * time.Minute)
	m, err := s.Put("ops", t0, strings.NewReader("video"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Meeting.Equal(t0) || !m.Ended.Equal(*now) || m.Size != 5 {
		t.Fatalf("meta %+v", m)
	}
	if body, got := read(t, s, "ops"); body != "video" || got != m {
		t.Fatalf("read %q %+v", body, got)
	}
	if until, ok := s.Until("ops"); !ok || !until.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("until %v %v", until, ok)
	}
	if fi, err := os.Stat(filepath.Join(dir, "ops.mp4")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v %v", fi, err)
	}
}

func TestPutTooLargeKeepsTheOldFile(t *testing.T) {
	s, _, dir := newStore(t)
	if _, err := s.Put("ops", t0, strings.NewReader("old"), 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put("ops", t0.Add(time.Hour), strings.NewReader("0123456789"), 5); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err %v", err)
	}
	if body, _ := read(t, s, "ops"); body != "old" {
		t.Fatalf("body %q", body)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(matches) != 0 {
		t.Fatalf("left %v", matches)
	}
}

func TestPutStaleMeeting(t *testing.T) {
	s, _, _ := newStore(t)
	if _, err := s.Put("ops", t0.Add(time.Hour), strings.NewReader("new"), 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put("ops", t0, strings.NewReader("old"), 100); !errors.Is(err, ErrStale) {
		t.Fatalf("err %v", err)
	}
	if body, _ := read(t, s, "ops"); body != "new" {
		t.Fatalf("body %q", body)
	}
}

func TestPutSameMeetingReplaces(t *testing.T) {
	s, _, _ := newStore(t)
	for _, body := range []string{"first part", "second part"} {
		if _, err := s.Put("ops", t0, strings.NewReader(body), 100); err != nil {
			t.Fatal(err)
		}
	}
	if body, _ := read(t, s, "ops"); body != "second part" {
		t.Fatalf("body %q", body)
	}
}

func TestDeleteWhileActiveAndRemove(t *testing.T) {
	s, _, _ := newStore(t)
	if err := s.Delete("ops"); !errors.Is(err, ErrNoRecording) {
		t.Fatalf("empty delete %v", err)
	}
	if _, err := s.Put("ops", t0, strings.NewReader("v"), 100); err != nil {
		t.Fatal(err)
	}
	s.SetActive("ops", true)
	if err := s.Delete("ops"); !errors.Is(err, ErrRecording) {
		t.Fatalf("active delete %v", err)
	}
	s.SetActive("ops", false)
	if err := s.Delete("ops"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Open("ops"); !errors.Is(err, ErrNoRecording) {
		t.Fatalf("open after delete %v", err)
	}
	if _, err := s.Put("ops", t0, strings.NewReader("v"), 100); err != nil {
		t.Fatal(err)
	}
	s.SetActive("ops", true)
	if err := s.Remove("ops"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Until("ops"); ok {
		t.Fatal("removed recording still listed")
	}
}

func TestSweepAndExpiry(t *testing.T) {
	s, now, _ := newStore(t)
	if _, err := s.Put("ops", t0, strings.NewReader("v"), 100); err != nil {
		t.Fatal(err)
	}
	*now = t0.Add(24*time.Hour - time.Second)
	if got := s.Sweep(); len(got) != 0 {
		t.Fatalf("swept early %v", got)
	}
	*now = t0.Add(24 * time.Hour)
	if _, ok := s.Until("ops"); ok {
		t.Fatal("expired recording listed")
	}
	if _, _, err := s.Open("ops"); !errors.Is(err, ErrNoRecording) {
		t.Fatalf("expired recording opened: %v", err)
	}
	if got := s.Sweep(); len(got) != 1 || got[0] != "ops" {
		t.Fatalf("swept %v", got)
	}
}

func TestOpenRemovesPartialUploads(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ops.mp4.tmp"), []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ops.mp4.tmp")); !os.IsNotExist(err) {
		t.Fatalf("tmp kept: %v", err)
	}
	if _, ok := s.Until("ops"); ok {
		t.Fatal("partial upload listed")
	}
}

func TestRestartKeepsRecordings(t *testing.T) {
	s, now, dir := newStore(t)
	if _, err := s.Put("ops", t0, strings.NewReader("v"), 100); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir, 24*time.Hour, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	if body, m := read(t, s2, "ops"); body != "v" || !m.Meeting.Equal(t0) {
		t.Fatalf("after restart %q %+v", body, m)
	}
}

func TestFileName(t *testing.T) {
	if got := FileName("ops", t0, time.UTC); got != "ops-2026-10-15-1100.mp4" {
		t.Fatal(got)
	}
}

func TestConcurrentPutSameSlug(t *testing.T) {
	s, _, dir := newStore(t)
	const n = 8
	bodies := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range bodies {
		bodies[i] = strings.Repeat(string(rune('a'+i)), 1000+i*100)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.Put("ops", t0, strings.NewReader(bodies[i]), 1<<20)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
	body, m := read(t, s, "ops")
	found := false
	for _, b := range bodies {
		if b == body {
			found = true
		}
	}
	if !found {
		t.Fatalf("stored body matches none of the uploads (len %d)", len(body))
	}
	if m.Size != int64(len(body)) {
		t.Fatalf("meta size %d, file %d", m.Size, len(body))
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(matches) != 0 {
		t.Fatalf("left %v", matches)
	}
}

func TestPutFailureLeavesNoRecording(t *testing.T) {
	s, _, dir := newStore(t)
	if err := os.Mkdir(filepath.Join(dir, "ops.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put("ops", t0, strings.NewReader("v"), 100); err == nil {
		t.Fatal("put over a directory succeeded")
	}
	if _, _, err := s.Open("ops"); !errors.Is(err, ErrNoRecording) {
		t.Fatalf("open after failed put: %v", err)
	}
	if _, ok := s.Until("ops"); ok {
		t.Fatal("failed put listed")
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(matches) != 0 {
		t.Fatalf("left %v", matches)
	}
}

func TestOpenRemovesOrphans(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put("ops", t0, strings.NewReader("v"), 100); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("lone.mp4", "video without meta")
	write("half.json", `{"meeting":"2026-10-15T11:00:00Z","ended":"2026-10-15T11:00:00Z","size":1}`)
	write("ops.json.tmp", "{}")
	if _, err := Open(dir, time.Hour, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lone.mp4", "half.json", "ops.json.tmp"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s kept: %v", name, err)
		}
	}
	s2, _ := Open(dir, time.Hour, nil)
	if _, ok := s2.Until("ops"); !ok {
		t.Fatal("valid recording dropped")
	}
}

func TestPutOlderMeetingAfterExpiry(t *testing.T) {
	s, now, _ := newStore(t)
	if _, err := s.Put("ops", t0.Add(time.Hour), strings.NewReader("new"), 100); err != nil {
		t.Fatal(err)
	}
	*now = t0.Add(time.Hour + 24*time.Hour)
	if _, err := s.Put("ops", t0, strings.NewReader("old"), 100); err != nil {
		t.Fatalf("older meeting after expiry: %v", err)
	}
	if body, m := read(t, s, "ops"); body != "old" || !m.Meeting.Equal(t0) {
		t.Fatalf("body %q meta %+v", body, m)
	}
}
