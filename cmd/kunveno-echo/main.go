// Command kunveno-echo is the sound-check helper. It asks the server which rooms want an echo
// (GET /internal/echo, loopback only), joins each of them as a participant, and plays the
// room's audio back with a delay. People switch it on and off with the room's echo button.
// Every flag can also be set by the environment variable named in its help text.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/pion/webrtc/v4"

	"github.com/ssubbotin/kunveno/internal/echo"
)

var version = "dev"

type serverMsg struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	SDP     string `json:"sdp"`
}

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
	running := map[string]context.CancelFunc{}
	client := &http.Client{Timeout: 3 * time.Second}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		if want, err := wanted(ctx, client, *control); err != nil {
			log.Warn("control poll", "err", err)
		} else {
			for slug, cancel := range running {
				if !want[slug] {
					cancel()
					delete(running, slug)
				}
			}
			for slug := range want {
				if running[slug] == nil {
					rctx, cancel := context.WithCancel(ctx)
					running[slug] = cancel
					go serveRoom(rctx, log.With("room", slug), *url, slug, *name, *delay, *minSpeech, *insecure)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func wanted(ctx context.Context, client *http.Client, url string) (map[string]bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", res.Status)
	}
	var slugs []string
	if err := json.NewDecoder(res.Body).Decode(&slugs); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(slugs))
	for _, s := range slugs {
		out[s] = true
	}
	return out, nil
}

// serveRoom keeps the echo in one room until ctx is cancelled, rejoining after drops.
func serveRoom(ctx context.Context, log *slog.Logger, url, room, name string, delay time.Duration, minSpeech int, insecure bool) {
	log.Info("echo on")
	defer log.Info("echo off")
	for {
		err := run(ctx, log, url, room, name, delay, minSpeech, insecure)
		if ctx.Err() != nil {
			return
		}
		log.Warn("disconnected; retrying in 3 s", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func run(ctx context.Context, log *slog.Logger, url, room, name string, delay time.Duration, minSpeech int, insecure bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	opts := &websocket.DialOptions{}
	if insecure {
		opts.HTTPClient = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	}
	conn, _, err := websocket.Dial(ctx, url, opts)
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(256 * 1024)

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

	if err := wsjson.Write(ctx, conn, map[string]string{"type": "join", "room": room, "name": name, "role": "echo"}); err != nil {
		return err
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var msg serverMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		switch msg.Type {
		case "joined":
			log.Info("joined", "room", room)
		case "error":
			return fmt.Errorf("server error %s: %s", msg.Code, msg.Message)
		case "offer":
			if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: msg.SDP}); err != nil {
				return err
			}
			answer, err := pc.CreateAnswer(nil)
			if err != nil {
				return err
			}
			gathered := webrtc.GatheringCompletePromise(pc)
			if err := pc.SetLocalDescription(answer); err != nil {
				return err
			}
			<-gathered
			if err := wsjson.Write(ctx, conn, map[string]string{"type": "answer", "sdp": pc.LocalDescription().SDP}); err != nil {
				return err
			}
		}
	}
}
