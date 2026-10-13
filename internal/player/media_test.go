package player

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func needFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
}

// clip writes a short test video (and tone, unless silent) with ffmpeg's own generators.
func clip(t *testing.T, dir string, withAudio bool) string {
	t.Helper()
	out := filepath.Join(dir, "clip.mkv")
	args := []string{"-nostdin", "-y", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=25:duration=2"}
	if withAudio {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:duration=2")
	}
	args = append(args, "-c:v", "ffv1", out)
	if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("make clip: %v\n%s", err, b)
	}
	return out
}

func drain(t *testing.T, open Opener) (n int, total time.Duration) {
	t.Helper()
	src, err := open()
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for {
		s, err := src.Next()
		if errors.Is(err, io.EOF) {
			return n, total
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Data) == 0 || s.Duration <= 0 {
			t.Fatalf("sample %d: %d bytes, %v", n, len(s.Data), s.Duration)
		}
		n++
		total += s.Duration
	}
}

func TestTranscodeAndRead(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	src := clip(t, dir, true)
	cache := filepath.Join(dir, "cache")
	m, err := Transcode(context.Background(), src, cache)
	if err != nil {
		t.Fatal(err)
	}
	if m.Video == "" || m.Audio == "" {
		t.Fatalf("media = %+v", m)
	}
	n, total := drain(t, m.OpenVideo)
	if n < 28 || n > 32 || total < 1900*time.Millisecond || total > 2100*time.Millisecond {
		t.Fatalf("video: %d frames over %v, want ~30 over 2 s (15 fps)", n, total)
	}
	n, total = drain(t, m.OpenAudio)
	if n < 95 || total < 1900*time.Millisecond || total > 2100*time.Millisecond {
		t.Fatalf("audio: %d packets over %v, want ~100 over 2 s (20 ms)", n, total)
	}

	// A second call reuses the cache instead of encoding again.
	st, _ := os.Stat(m.Video)
	m2, err := Transcode(context.Background(), src, cache)
	if err != nil || m2.Video != m.Video {
		t.Fatalf("second transcode: %+v %v", m2, err)
	}
	if st2, _ := os.Stat(m2.Video); !st2.ModTime().Equal(st.ModTime()) {
		t.Fatal("video encoded again although cached")
	}
}

func TestTranscodeSilentFile(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	m, err := Transcode(context.Background(), clip(t, dir, false), filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Video == "" || m.Audio != "" || m.OpenAudio != nil {
		t.Fatalf("silent file: %+v", m)
	}
}

// A still picture becomes a short silent clip that loops like any video.
func TestTranscodeStill(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	img := filepath.Join(dir, "pet.png")
	if b, err := exec.Command("ffmpeg", "-nostdin", "-y", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=800x450", "-frames:v", "1", img).CombinedOutput(); err != nil {
		t.Fatalf("make image: %v\n%s", err, b)
	}
	m, err := TranscodeStill(context.Background(), img, filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Audio != "" || m.OpenAudio != nil {
		t.Fatalf("still: %+v", m)
	}
	n, total := drain(t, m.OpenVideo)
	if n < 28 || total < 1900*time.Millisecond || total > 2100*time.Millisecond {
		t.Fatalf("still: %d frames over %v, want ~30 over 2 s (15 fps)", n, total)
	}
}
