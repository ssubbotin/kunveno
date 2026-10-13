package player

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4/pkg/media/ivfreader"
	"github.com/pion/webrtc/v4/pkg/media/oggreader"
)

// Live encoding: the same picture and sound as the file player, encoded as the frames arrive.
var (
	liveVideoArgs = []string{
		"-vf", "scale=640:360:force_original_aspect_ratio=decrease,pad=640:360:(ow-iw)/2:(oh-ih)/2,fps=15",
		"-c:v", "libvpx", "-b:v", "450k", "-deadline", "realtime", "-cpu-used", "8",
		"-g", "30", "-keyint_min", "30", "-auto-alt-ref", "0", "-f", "ivf"}
	liveAudioArgs = []string{
		"-c:a", "libopus", "-b:a", "64k", "-ar", "48000", "-ac", "2",
		"-frame_duration", "20", "-page_duration", "20000", "-f", "ogg"}
)

// RTSPInput is the ffmpeg input for a camera feed: over TCP, which survives the VPN better than
// RTP over UDP, and without input buffering.
func RTSPInput(feed string) []string {
	return []string{"-rtsp_transport", "tcp", "-fflags", "nobuffer", "-i", feed}
}

// Redact hides the password of a feed URL for logs.
func Redact(feed string) string {
	u, err := url.Parse(feed)
	if err != nil || u.User == nil {
		return feed
	}
	return strings.Replace(u.Redacted(), ":xxxxx@", ":***@", 1)
}

// Probe reports whether an input has a sound track.
func Probe(ctx context.Context, input []string) (bool, error) {
	args := []string{"-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0"}
	for _, a := range input {
		if a != "-re" { // an ffmpeg-only option: read in real time
			args = append(args, a)
		}
	}
	out, err := exec.CommandContext(ctx, "ffprobe", args...).Output()
	if err != nil {
		return false, fmt.Errorf("ffprobe: %w", err)
	}
	return bytes.Contains(out, []byte("audio")), nil
}

// Live is a running ffmpeg turning an input into VP8 frames and Opus packets.
type Live struct {
	Video, Audio Source // Audio is nil for an input without sound
	cmd          *exec.Cmd
	stderr       *bytes.Buffer
}

// StartLive starts encoding input; the sources end when ffmpeg stops or ctx ends.
func StartLive(ctx context.Context, input []string, withAudio bool) (*Live, error) {
	args := append([]string{"-nostdin", "-loglevel", "error"}, input...)
	args = append(args, "-map", "0:v:0")
	args = append(args, liveVideoArgs...)
	args = append(args, "pipe:1")
	var audioR, audioW *os.File
	if withAudio {
		var err error
		if audioR, audioW, err = os.Pipe(); err != nil {
			return nil, err
		}
		args = append(args, "-map", "0:a:0")
		args = append(args, liveAudioArgs...)
		args = append(args, "pipe:3") // the first of ExtraFiles
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if audioW != nil {
		cmd.ExtraFiles = []*os.File{audioW}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if audioW != nil {
		audioW.Close() // ffmpeg holds its copy
	}
	l := &Live{cmd: cmd, stderr: stderr}
	v, h, err := ivfreader.NewWith(stdout)
	if err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("ivf: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	l.Video = &ivfSource{r: v, tick: time.Duration(float64(time.Second) * float64(h.TimebaseNumerator) / float64(h.TimebaseDenominator))}
	if audioR != nil {
		a, _, err := oggreader.NewWith(audioR)
		if err != nil {
			_ = l.Close()
			return nil, fmt.Errorf("ogg: %w", err)
		}
		l.Audio = &oggSource{f: audioR, r: a}
	}
	return l, nil
}

// Close stops ffmpeg.
func (l *Live) Close() error {
	if l.cmd.Process != nil {
		_ = l.cmd.Process.Kill()
	}
	_ = l.cmd.Wait()
	return nil
}

// Err is ffmpeg's last words, for logs.
func (l *Live) Err() string { return string(bytes.TrimSpace(l.stderr.Bytes())) }

// PlayLive sends samples as they arrive, without pacing: the input already runs in real time.
// It returns when either source ends or ctx does.
func PlayLive(ctx context.Context, l *Live, writeVideo, writeAudio func(Sample) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer l.Close()
	var once sync.Once
	var first error
	finish := func(err error) {
		once.Do(func() { first = err })
		cancel()
	}
	var wg sync.WaitGroup
	pump := func(src Source, write func(Sample) error) {
		defer wg.Done()
		for ctx.Err() == nil {
			s, err := src.Next()
			if err != nil {
				finish(err)
				return
			}
			if err := write(s); err != nil {
				finish(err)
				return
			}
		}
	}
	wg.Add(1)
	go pump(l.Video, writeVideo)
	if l.Audio != nil {
		wg.Add(1)
		go pump(l.Audio, writeAudio)
	}
	<-ctx.Done()
	_ = l.Close() // unblocks the readers
	wg.Wait()
	if first == nil {
		first = ctx.Err()
	}
	if errors.Is(first, context.Canceled) && ctx.Err() != nil {
		return ctx.Err()
	}
	return first
}
