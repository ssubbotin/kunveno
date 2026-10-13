// Command kunveno-player keeps a picture playing in every room whose configuration has a player:
// a media file in a loop (transcoded once with ffmpeg and cached) or a live RTSP feed (encoded as
// it arrives). It joins the room as the "player" helper over loopback and sends the video on the
// camera slot and the sound as audio.
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
		rlog := log.With("room", r.Slug)
		if r.Player.URL != "" {
			go serveFeed(ctx, rlog, *url, r.Slug, r.Player.Name, r.Player.URL)
		} else {
			go serveRoom(ctx, rlog, *url, r.Slug, r.Player.Name, filepath.Join(*mediaDir, r.Player.File), *cacheDir)
		}
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
	loop := func(ctx context.Context, writeVideo, writeAudio func(player.Sample) error) error {
		return player.Loop(ctx, m.OpenVideo, m.OpenAudio, writeVideo, writeAudio, func(round int) {
			if round == 1 || round%100 == 0 {
				log.Info("loop", "round", round)
			}
		})
	}
	stayInRoom(ctx, log, url, room, name, "player", loop)
}

// serveFeed keeps a live RTSP feed in the room. While the camera is unreachable the player stays
// in the room and retries every 5 s.
func serveFeed(ctx context.Context, log *slog.Logger, url, room, name, feed string) {
	log = log.With("feed", player.Redact(feed))
	input := player.RTSPInput(feed)
	run := func(ctx context.Context, writeVideo, writeAudio func(player.Sample) error) error {
		for ctx.Err() == nil {
			if err := playFeed(ctx, log, input, writeVideo, writeAudio); err != nil && ctx.Err() == nil {
				log.Warn("feed stopped; retrying in 5 s", "err", err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
		return ctx.Err()
	}
	stayInRoom(ctx, log, url, room, name, "camera", run)
}

func playFeed(ctx context.Context, log *slog.Logger, input []string, writeVideo, writeAudio func(player.Sample) error) error {
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	withAudio, err := player.Probe(probeCtx, input)
	cancel()
	if err != nil {
		return err
	}
	live, err := player.StartLive(ctx, input, withAudio)
	if err != nil {
		return err
	}
	log.Info("feed playing", "audio", withAudio)
	err = player.PlayLive(ctx, live, writeVideo, writeAudio)
	if msg := live.Err(); msg != "" && ctx.Err() == nil {
		log.Warn("ffmpeg", "stderr", msg)
	}
	return err
}

// stayInRoom keeps the player joined, rejoining after drops, and runs the media while connected.
func stayInRoom(ctx context.Context, log *slog.Logger, url, room, name, role string, run mediaFunc) {
	for {
		err := play(ctx, log, url, room, name, role, run)
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

// mediaFunc sends video and audio samples until ctx ends or the media fails.
type mediaFunc func(ctx context.Context, writeVideo, writeAudio func(player.Sample) error) error

func play(ctx context.Context, log *slog.Logger, url, room, name, role string, run mediaFunc) error {
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
		if err := run(ctx, write(video), write(audio)); err != nil && ctx.Err() == nil {
			log.Error("playback", "err", err)
			cancel()
		}
	}()
	return botclient.Serve(ctx, conn, pc, room, name, role, func(send func(any) error) {
		log.Info("joined")
		_ = send(map[string]any{"type": "camera", "on": true}) // the video shows on our tile
	})
}
