// Command kunveno-echo joins a room as an ordinary participant and plays the room's audio
// back with a delay, so people can check that their voice reaches the server and others.
// Every flag can also be set by the environment variable named in its help text.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
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

// errFatal marks errors that a reconnect cannot fix (wrong room or PIN).
var errFatal = errors.New("fatal")

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
	room := flag.String("room", env("ECHO_ROOM", ""), "room slug (ECHO_ROOM)")
	pin := flag.String("pin", env("ECHO_PIN", ""), "room PIN (ECHO_PIN)")
	name := flag.String("name", env("ECHO_NAME", "Echo"), "participant name (ECHO_NAME)")
	delay := flag.Duration("delay", envDuration("ECHO_DELAY", 2*time.Second), "echo delay (ECHO_DELAY)")
	minSpeech := flag.Int("min-speech", envInt("ECHO_MIN_SPEECH_BYTES", 30), "Opus payload bytes that count as speech (ECHO_MIN_SPEECH_BYTES)")
	insecure := flag.Bool("insecure", os.Getenv("ECHO_INSECURE") == "1", "skip TLS verification (ECHO_INSECURE=1)")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if *room == "" || *pin == "" {
		log.Error("room and pin are required (ECHO_ROOM, ECHO_PIN)")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("kunveno-echo started", "version", version, "url", *url, "room", *room, "delay", delay.String())

	for {
		err := run(ctx, log, *url, *room, *pin, *name, *delay, *minSpeech, *insecure)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errFatal) {
			log.Error("giving up", "err", err)
			os.Exit(1)
		}
		log.Warn("disconnected; retrying in 3 s", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func run(ctx context.Context, log *slog.Logger, url, room, pin, name string, delay time.Duration, minSpeech int, insecure bool) error {
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
					_ = out.WriteRTP(p)
				}
			}
		}
	}()

	if err := wsjson.Write(ctx, conn, map[string]string{"type": "join", "room": room, "pin": pin, "name": name}); err != nil {
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
			switch msg.Code {
			case "bad_room", "bad_pin":
				return fmt.Errorf("%w: %s: %s", errFatal, msg.Code, msg.Message)
			}
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
