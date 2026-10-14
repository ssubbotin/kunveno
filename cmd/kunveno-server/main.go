// Command kunveno-server is the kunveno signaling and media server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ssubbotin/kunveno/internal/backgrounds"
	"github.com/ssubbotin/kunveno/internal/config"
	"github.com/ssubbotin/kunveno/internal/httpapi"
	"github.com/ssubbotin/kunveno/internal/metrics"
	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/room"
	"github.com/ssubbotin/kunveno/internal/roomstore"
	"github.com/ssubbotin/kunveno/internal/sfu"
	sig "github.com/ssubbotin/kunveno/internal/signal"
)

var version = "dev"

// engineMedia adapts *sfu.Engine to signal.Media (interface return type).
type engineMedia struct{ e *sfu.Engine }

func (m engineMedia) NewPeer(id string, s sfu.Signaler, h sfu.Handlers) (sig.MediaPeer, error) {
	p, err := m.e.NewPeer(id, s, h)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func main() {
	cfgPath := flag.String("config", "/etc/kunveno/config.yaml", "path to config.yaml")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("kunveno-server", version)
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(*cfgPath, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath string, log *slog.Logger) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	engine, err := sfu.NewEngine(sfu.EngineConfig{
		UDPPort: cfg.Media.UDPPort, TCP: cfg.Media.TCPPort > 0, TCPPort: cfg.Media.TCPPort, AdvertiseIP: cfg.Media.AdvertiseIP,
	}, log)
	if err != nil {
		return err
	}
	defer engine.Close()

	reg := room.NewRegistry()
	for _, r := range cfg.Rooms {
		rm := room.New(r.Slug, r.Name, r.PIN, r.MaxParticipants)
		rm.SetPlayer(r.Player != nil)
		reg.Add(rm)
	}
	saved, err := roomstore.Load(cfg.RoomsFile)
	if err != nil {
		return fmt.Errorf("load %s: %w", cfg.RoomsFile, err)
	}
	for _, s := range saved {
		if _, clash := reg.Get(s.Slug); clash {
			log.Warn("saved room clashes with a configured room; skipped", "slug", s.Slug)
			continue
		}
		reg.Restore(s)
	}
	var saveMu sync.Mutex
	persist := func() error {
		saveMu.Lock()
		defer saveMu.Unlock()
		return roomstore.Save(cfg.RoomsFile, reg.CreatedSpecs())
	}
	store, err := notes.Open(cfg.Notes.Dir, cfg.Notes.KeepFor, nil)
	if err != nil {
		return fmt.Errorf("notes %s: %w", cfg.Notes.Dir, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go sweepNotes(ctx, store, log) // stored notes expire whether or not notes are enabled

	mux := http.NewServeMux()
	ws := sig.New(reg, engineMedia{engine}, cfg.HTTP.AllowedOrigins, log)
	ws.SetReactions(cfg.UI.Reactions)
	if cfg.Notes.Enabled {
		ws.SetNotes(store, cfg.Notes.Live)
	}
	mux.Handle("/ws", ws)
	rooms := httpapi.Rooms(reg, persist, ws.PINLimiter(), store, cfg.Notes.Enabled)
	mux.Handle("/api/rooms", rooms)
	mux.Handle("/api/rooms/", rooms)
	mux.Handle("POST /api/rooms/{slug}/notes", httpapi.NotesDownload(reg, store, cfg.Notes.Enabled, ws.PINLimiter(), time.Local))
	bg := backgrounds.New(cfg.BackgroundsDir, log).Handler()
	mux.Handle("/api/backgrounds", bg)
	mux.Handle("/api/backgrounds/", bg)
	mux.Handle("/healthz", httpapi.Health())
	mux.Handle("/api/info", httpapi.Info(cfg.UI.Title, cfg.UI.Reactions, cfg.UI.NoiseSuppression, cfg.Notes.Enabled, cfg.Notes.Live))
	mux.Handle("/internal/echo", httpapi.EchoRooms(reg))                       // never proxied by nginx
	mux.Handle("/internal/notes", httpapi.NotesRooms(reg, cfg.Notes.Enabled))  // never proxied by nginx
	mux.Handle("/metrics", metrics.Handler(metrics.NewRegistry(reg, version))) // local scrapers only

	srv := &http.Server{Addr: cfg.HTTP.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("kunveno-server started", "version", version, "listen", cfg.HTTP.Listen, "udp_port", engine.Port(), "tcp_port", engine.TCPPort(), "advertise_ip", cfg.Media.AdvertiseIP, "rooms", len(reg.All()), "rooms_file", cfg.RoomsFile, "backgrounds", cfg.BackgroundsDir, "notes", cfg.Notes.Enabled)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// sweepNotes deletes expired meeting notes every minute.
func sweepNotes(ctx context.Context, store *notes.Store, log *slog.Logger) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		for _, slug := range store.Sweep() {
			log.Info("notes expired", "room", slug)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
