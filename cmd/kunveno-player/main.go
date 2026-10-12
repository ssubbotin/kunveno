// Command kunveno-player keeps a looped video playing in every room whose configuration has a
// player: it transcodes the file once (ffmpeg, cached), joins the room as the "player" helper
// over loopback, and sends the video on the camera slot and the sound as audio, round after round.
// Every flag can also be set by the environment variable named in its help text.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"github.com/ssubbotin/kunveno/internal/botclient"
	"github.com/ssubbotin/kunveno/internal/config"
	"github.com/ssubbotin/kunveno/internal/player"
)

var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	cfgPath := flag.String("config", env("PLAYER_CONFIG", "/etc/kunveno/config.yaml"), "server configuration with the rooms (PLAYER_CONFIG)")
	mediaDir := flag.String("media", env("PLAYER_MEDIA", "/media"), "directory with the media files (PLAYER_MEDIA)")
	cacheDir := flag.String("cache", env("PLAYER_CACHE", "/var/lib/kunveno/player-cache"), "transcoded files (PLAYER_CACHE)")
	url := flag.String("url", env("PLAYER_URL", "ws://127.0.0.1:8080/ws"), "signaling URL (PLAYER_URL)")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("kunveno-player started", "version", version, "url", *url)

	players := 0
	for _, r := range cfg.Rooms {
		if r.Player == nil {
			continue
		}
		players++
		go serveRoom(ctx, log.With("room", r.Slug), *url, r.Slug, r.Player.Name, filepath.Join(*mediaDir, r.Player.File), *cacheDir)
	}
	if players == 0 {
		log.Info("no room has a player; idle")
	}
	<-ctx.Done()
}

// serveRoom prepares the file and keeps the player in the room, rejoining after drops.
func serveRoom(ctx context.Context, log *slog.Logger, url, room, name, file, cacheDir string) {
	var m player.Media
	for {
		var err error
		log.Info("transcoding", "file", file)
		if m, err = player.Transcode(ctx, file, cacheDir); err == nil {
			break
		}
		log.Error("cannot prepare the file; retrying in 1 min", "file", file, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
	log.Info("ready", "video", m.Video, "audio", m.Audio != "")
	for {
		err := play(ctx, log, url, room, name, m)
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

func play(ctx context.Context, log *slog.Logger, url, room, name string, m player.Media) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn, err := botclient.Dial(ctx, url, false)
	if err != nil {
		return err
	}
	defer conn.CloseNow()

	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		return err
	}
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(me)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	defer pc.Close()
	audio, _ := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "player")
	screen, _ := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, "screen", "player")
	video, _ := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, "camera", "player")
	// The server's slots are mid 0 audio, mid 1 screen, mid 2 camera; tracks fill them in order.
	// The screen slot stays silent.
	for _, tr := range []webrtc.TrackLocal{audio, screen, video} {
		if _, err := pc.AddTrack(tr); err != nil {
			return err
		}
	}
	connected := make(chan struct{})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Info("connection state", "state", s.String())
		switch s {
		case webrtc.PeerConnectionStateConnected:
			select {
			case <-connected:
			default:
				close(connected)
			}
		case webrtc.PeerConnectionStateFailed:
			cancel()
		}
	})
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-connected:
		}
		write := func(t *webrtc.TrackLocalStaticSample) func(player.Sample) error {
			return func(s player.Sample) error { return t.WriteSample(media.Sample{Data: s.Data, Duration: s.Duration}) }
		}
		err := player.Loop(ctx, m.OpenVideo, m.OpenAudio, write(video), write(audio), func(round int) {
			if round == 1 || round%100 == 0 {
				log.Info("loop", "round", round)
			}
		})
		if err != nil && ctx.Err() == nil {
			log.Error("playback", "err", err)
			cancel()
		}
	}()
	return botclient.Serve(ctx, conn, pc, room, name, "player", func(send func(any) error) {
		log.Info("joined")
		_ = send(map[string]any{"type": "camera", "on": true}) // the video shows on our tile
	})
}
