package player

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"rtsp://admin:secret@10.10.10.50:554/stream1": "rtsp://admin:***@10.10.10.50:554/stream1",
		"rtsp://10.10.10.50/stream1":                  "rtsp://10.10.10.50/stream1",
		"::nope":                                      "::nope",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

// generated stands in for a camera: ffmpeg's own test pattern and tone, produced in real time.
func generated(audio bool) []string {
	graph := "testsrc=size=320x240:rate=25"
	if audio {
		graph += "[out0];sine=frequency=440[out1]"
	}
	return []string{"-re", "-f", "lavfi", "-i", graph}
}

func TestProbeFindsAudio(t *testing.T) {
	needFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if has, err := Probe(ctx, generated(true)); err != nil || !has {
		t.Fatalf("with sound: %v %v", has, err)
	}
	if has, err := Probe(ctx, generated(false)); err != nil || has {
		t.Fatalf("without sound: %v %v", has, err)
	}
}

func TestLiveDeliversFramesInRealTime(t *testing.T) {
	needFFmpeg(t)
	for _, withAudio := range []bool{true, false} {
		ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
		live, err := StartLive(ctx, generated(withAudio), withAudio)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		var video, audio atomic.Int64
		err = PlayLive(ctx, live,
			func(s Sample) error { video.Add(1); return nil },
			func(s Sample) error { audio.Add(1); return nil })
		cancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, io.EOF) {
			t.Fatalf("audio=%v: %v", withAudio, err)
		}
		v, a := video.Load(), audio.Load()
		// 2.5 s minus encoder start-up: well over 15 frames at 15 fps, and 20 ms Opus packets.
		if v < 15 || v > 45 {
			t.Errorf("audio=%v: %d video frames", withAudio, v)
		}
		if withAudio && a < 50 {
			t.Errorf("%d audio packets, want ~100", a)
		}
		if !withAudio && a != 0 {
			t.Errorf("silent source produced %d audio packets", a)
		}
	}
}
