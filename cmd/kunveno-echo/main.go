// Command kunveno-echo is the sound-check helper. It asks the server which rooms want an echo
// (GET /internal/echo, loopback only), joins each of them as a participant, and plays the
// room's audio back with a delay. People switch it on and off with the room's echo button.
// Every flag can also be set by the environment variable named in its help text.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/ssubbotin/kunveno/internal/echo"
	"github.com/ssubbotin/kunveno/pkg/botclient"
)

var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return d
	}
	return def
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return n
	}
	return def
}

func main() {
	url := flag.String("url", env("ECHO_URL", "ws://127.0.0.1:8080/ws"), "signaling URL (ECHO_URL)")
	control := flag.String("control", env("ECHO_CONTROL_URL", "http://127.0.0.1:8080/internal/echo"), "rooms-wanting-echo endpoint (ECHO_CONTROL_URL)")
	name := flag.String("name", env("ECHO_NAME", "Эхо-тест"), "participant name (ECHO_NAME)")
	delay := flag.Duration("delay", envDuration("ECHO_DELAY", 2*time.Second), "echo delay (ECHO_DELAY)")
	minSpeech := flag.Int("min-speech", envInt("ECHO_MIN_SPEECH_BYTES", 30), "Opus payload bytes that count as speech (ECHO_MIN_SPEECH_BYTES)")
	insecure := flag.Bool("insecure", os.Getenv("ECHO_INSECURE") == "1", "skip TLS verification (ECHO_INSECURE=1)")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("kunveno-echo started", "version", version, "url", *url, "control", *control, "delay", delay.String())

	// One goroutine per room that wants the echo; cancelled when the room stops asking.
	rooms := &botclient.Rooms{Control: *control, Poll: 2 * time.Second, Retry: 3 * time.Second, Log: log, CancelUnlisted: true}
	rooms.Serve = func(ctx context.Context, slug string) {
		log := log.With("room", slug)
		log.Info("echo on")
		defer log.Info("echo off")
		rooms.Stay(ctx, slug, log, func(ctx context.Context) (bool, error) {
			return false, run(ctx, log, *url, slug, *name, *delay, *minSpeech, *insecure)
		})
	}
	rooms.Run(ctx)
}

func run(ctx context.Context, log *slog.Logger, url, room, name string, delay time.Duration, minSpeech int, insecure bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, err := botclient.Dial(ctx, url, insecure)
	if err != nil {
		return err
	}
	defer conn.CloseNow()

	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return err
	}
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(m)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	defer pc.Close()
	out, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "echo")
	if err != nil {
		return err
	}
	if _, err := pc.AddTrack(out); err != nil {
		return err
	}
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Info("connection state", "state", s.String())
		if s == webrtc.PeerConnectionStateFailed {
			cancel()
		}
	})

	d := echo.New(delay, minSpeech, time.Second)
	var rx, rxMax, tx atomic.Int64
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n := rx.Swap(0); n > 0 {
					log.Info("stats", "rx_packets", n, "rx_max_payload", rxMax.Swap(0), "tx_packets", tx.Swap(0))
				}
			}
		}
	}()
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		src := tr.StreamID() // the publishing participant's id
		audio := tr.Kind() == webrtc.RTPCodecTypeAudio
		log.Info("track", "from", src, "kind", tr.Kind().String())
		for {
			p, _, err := tr.ReadRTP()
			if err != nil {
				return
			}
			if audio {
				rx.Add(1)
				if n := int64(len(p.Payload)); n > rxMax.Load() {
					rxMax.Store(n)
				}
				d.Push(src, p, time.Now())
			}
		}
	})
	go func() {
		t := time.NewTicker(5 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				for _, p := range d.Due(now) {
					if err := out.WriteRTP(p); err == nil {
						tx.Add(1)
					}
				}
			}
		}
	}()

	return botclient.Serve(ctx, conn, pc, room, name, "echo", func(func(any) error) { log.Info("joined") })
}
