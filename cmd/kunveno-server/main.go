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
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/ssubbotin/kunveno/internal/access"
	"github.com/ssubbotin/kunveno/internal/auth"
	"github.com/ssubbotin/kunveno/internal/auth/yandex"
	"github.com/ssubbotin/kunveno/internal/backgrounds"
	"github.com/ssubbotin/kunveno/internal/conference"
	"github.com/ssubbotin/kunveno/internal/config"
	"github.com/ssubbotin/kunveno/internal/httpapi"
	"github.com/ssubbotin/kunveno/internal/lobby"
	"github.com/ssubbotin/kunveno/internal/media"
	"github.com/ssubbotin/kunveno/internal/meetings"
	"github.com/ssubbotin/kunveno/internal/metrics"
	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/recording"
	"github.com/ssubbotin/kunveno/internal/room"
	"github.com/ssubbotin/kunveno/internal/roomstore"
	"github.com/ssubbotin/kunveno/internal/sfu"
	sig "github.com/ssubbotin/kunveno/internal/signal"
)

var version = "dev"

// engineMedia adapts *sfu.Engine to conference.Media (interface return type).
type engineMedia struct{ e *sfu.Engine }

func (m engineMedia) NewPeer(id string, s media.Signaler, h media.Handlers) (conference.MediaPeer, error) {
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
	// Every directory the server writes, so that a bad owner stops it now with the fix, not later.
	// The notes and recording directories only when their feature is on.
	dirs := []string{filepath.Dir(cfg.RoomsFile)}
	if cfg.Notes.Enabled {
		dirs = append(dirs, cfg.Notes.Dir)
	}
	if cfg.Recording.Enabled {
		dirs = append(dirs, cfg.Recording.Dir)
	}
	for _, dir := range dirs {
		if err := access.Writable(dir); err != nil {
			return err
		}
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
	recStore, err := recording.Open(cfg.Recording.Dir, cfg.Recording.KeepFor, nil)
	if err != nil {
		return fmt.Errorf("recording %s: %w", cfg.Recording.Dir, err)
	}
	go sweepRecordings(ctx, recStore, log) // recordings expire whether or not recording is enabled
	// Summaries of ended meetings, next to the rooms file: shown in the lobby while notes or a
	// recording of the meeting are kept.
	meets := meetings.Open(filepath.Join(filepath.Dir(cfg.RoomsFile), "meetings.json"), nil)
	go sweepMeetings(ctx, meets, log)

	conf := conference.New(reg, engineMedia{engine}, log)
	conf.SetReactions(cfg.UI.Reactions)
	conf.SetMeetings(meets)
	if cfg.Notes.Enabled {
		conf.SetNotes(store, cfg.Notes.Live)
	}
	var rec *recording.Store // nil while recording.enabled is off
	if cfg.Recording.Enabled {
		rec = recStore
		conf.SetRecording(rec)
	}
	ws := sig.New(conf, cfg.HTTP.AllowedOrigins, log)
	var gate *auth.Gate // nil while auth.enabled is off
	if cfg.Auth.Enabled {
		if gate, err = newGate(cfg, log); err != nil {
			return err
		}
		ws.SetAccess(gate)
		go sweepInvites(ctx, gate.Invites, log)
	}
	mux := routes(server{cfg: cfg, reg: reg, persist: persist, notes: store, recordings: recStore, meetings: meets,
		conf: conf, ws: ws, gate: gate, log: log})

	srv := &http.Server{Addr: cfg.HTTP.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("kunveno-server started", "version", version, "listen", cfg.HTTP.Listen, "udp_port", engine.Port(), "tcp_port", engine.TCPPort(), "advertise_ip", cfg.Media.AdvertiseIP, "rooms", len(reg.All()), "rooms_file", cfg.RoomsFile, "backgrounds", cfg.BackgroundsDir, "notes", cfg.Notes.Enabled, "recording", cfg.Recording.Enabled, "auth", cfg.Auth.Enabled)

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

// newGate builds the sign-in: the session key and the invitations next to the rooms file, and
// the configured providers with their rules.
func newGate(cfg *config.Config, log *slog.Logger) (*auth.Gate, error) {
	key, err := auth.LoadKey(filepath.Join(cfg.DataDir(), "auth.key"))
	if err != nil {
		return nil, fmt.Errorf("auth key: %w", err)
	}
	invites, err := auth.OpenInvites(filepath.Join(cfg.DataDir(), "invites.json"))
	if err != nil {
		return nil, fmt.Errorf("invites: %w", err)
	}
	gate := &auth.Gate{Sessions: auth.NewSessions(key, cfg.Auth.SessionTTL), Invites: invites,
		PublicURL: cfg.Auth.PublicURL, Log: log, Rules: map[string]auth.Rule{},
		OnSignIn: func(p, res string) { metrics.SignIns.WithLabelValues(p, res).Inc() }}
	for _, p := range cfg.Auth.Providers {
		prov, err := newProvider(p)
		if err != nil {
			return nil, err
		}
		gate.Providers = append(gate.Providers, prov)
		gate.Rules[p.Type] = auth.Rule(p.Allow)
	}
	return gate, nil
}

// newProvider builds a configured sign-in provider; a new provider type adds a case here.
func newProvider(p config.AuthProvider) (auth.Provider, error) {
	switch p.Type {
	case "yandex":
		return yandex.New(p.Name, p.ClientID, p.Secret), nil
	}
	return nil, fmt.Errorf("auth provider %q is not built in", p.Type)
}

// server is what the HTTP routes serve.
type server struct {
	cfg        *config.Config
	reg        *room.Registry
	persist    func() error // saves the created rooms
	notes      *notes.Store
	recordings *recording.Store // served only while recording.enabled is on
	meetings   *meetings.Store
	conf       *conference.Conference
	ws         *sig.Handler
	gate       *auth.Gate // nil: sign-in off
	log        *slog.Logger
}

// routes registers every route. With sign-in on, the /api/ routes need a session (and requests
// that change something must come from our site), except /api/info, which tells the client how
// to sign in, and /api/backgrounds, which guests use; /auth/ and the invitations are served only
// then. /healthz, /metrics and /internal/ never pass through the sign-in.
func routes(s server) *http.ServeMux {
	cfg, reg, gate := s.cfg, s.reg, s.gate
	protect := func(h http.Handler) http.Handler {
		if gate == nil {
			return h
		}
		return gate.Require(h)
	}
	var rec *recording.Store // nil while recording.enabled is off
	if cfg.Recording.Enabled {
		rec = s.recordings
	}
	pins := s.ws.PINLimiter()
	links := httpapi.NewLinks(5 * time.Minute)
	mux := http.NewServeMux()
	mux.Handle("/ws", s.ws)
	var deleted func(slug string) error // with sign-in on, a deleted room's invitations end with it
	if gate != nil {
		deleted = func(slug string) error {
			ids, err := gate.Invites.RemoveRoom(slug)
			for _, id := range ids {
				s.conf.DropInvite(id)
			}
			if err != nil {
				return fmt.Errorf("cannot save the invitations: %w", err)
			}
			return nil
		}
	}
	rooms := protect(httpapi.Rooms(httpapi.RoomsConfig{Registry: reg, Persist: s.persist, PINs: pins, Deleted: deleted, Kept: lobby.Kept{
		Notes: s.notes, NotesEnabled: cfg.Notes.Enabled, Recordings: s.recordings, RecordingEnabled: cfg.Recording.Enabled, Meetings: s.meetings,
	}}))
	mux.Handle("/api/rooms", rooms)
	mux.Handle("/api/rooms/", rooms)
	mux.Handle("POST /api/rooms/{slug}/notes", protect(httpapi.NotesDownload(reg, s.notes, cfg.Notes.Enabled, pins, time.Local)))
	mux.Handle("DELETE /api/rooms/{slug}/notes", protect(httpapi.NotesDelete(reg, s.notes, cfg.Notes.Enabled, pins)))
	mux.Handle("POST /api/rooms/{slug}/recording", protect(httpapi.RecordingLink(reg, rec, pins, links)))
	mux.Handle("GET /api/rooms/{slug}/recording/{token}", protect(httpapi.RecordingFile(rec, links, time.Local)))
	mux.Handle("DELETE /api/rooms/{slug}/recording", protect(httpapi.RecordingDelete(reg, rec, pins)))
	mux.Handle("/internal/recording", httpapi.RecordingRooms(reg, cfg.Recording.Enabled))                                           // never proxied by nginx
	mux.Handle("PUT /internal/recording/{slug}", httpapi.RecordingUpload(reg, rec, cfg.Recording.MaxBytes, s.conf.RecordingStored)) // never proxied by nginx
	bg := backgrounds.New(cfg.BackgroundsDir, s.log).Handler()                                                                      // public: guests use backgrounds too
	mux.Handle("/api/backgrounds", bg)
	mux.Handle("/api/backgrounds/", bg)
	mux.Handle("/healthz", httpapi.Health())
	signIn := &httpapi.SignInInfo{}
	if gate != nil {
		signIn = &httpapi.SignInInfo{Enabled: true, Providers: gate.ProviderInfos()}
		gate.Routes(mux)
		inv := protect(httpapi.Invites(reg, gate.Invites, pins, cfg.Auth.InviteMaxTTL, gate.PublicURL,
			func(id string) { s.conf.DropInvite(id) }, s.log))
		mux.Handle("POST /api/rooms/{slug}/invites", inv)
		mux.Handle("POST /api/rooms/{slug}/invites/list", inv)
		mux.Handle("DELETE /api/rooms/{slug}/invites/{id}", inv)
	}
	mux.Handle("/api/info", httpapi.Info(cfg.UI.Title, cfg.UI.Reactions, cfg.UI.NoiseSuppression, cfg.Notes.Enabled, cfg.Notes.Live, cfg.Recording.Enabled, cfg.Media.ReconnectGrace, signIn))
	mux.Handle("/internal/echo", httpapi.EchoRooms(reg))                      // never proxied by nginx
	mux.Handle("/internal/notes", httpapi.NotesRooms(reg, cfg.Notes.Enabled)) // never proxied by nginx
	promReg := metrics.NewRegistry(reg, version)
	if gate != nil {
		metrics.RegisterAuth(promReg, gate.Invites.Active)
	}
	mux.Handle("/metrics", metrics.Handler(promReg)) // local scrapers only
	return mux
}

// sweepInvites drops expired and revoked invitations from their file every hour.
func sweepInvites(ctx context.Context, invites *auth.Invites, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := invites.Sweep(); err != nil {
			log.Warn("cannot save the invitations file", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// sweepRecordings deletes expired meeting recordings every minute.
func sweepRecordings(ctx context.Context, store *recording.Store, log *slog.Logger) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		for _, slug := range store.Sweep() {
			log.Info("recording expired", "room", slug)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// sweepMeetings drops meeting summaries older than meetings.MaxAge every minute.
func sweepMeetings(ctx context.Context, store *meetings.Store, log *slog.Logger) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if err := store.Sweep(); err != nil {
			log.Warn("cannot save the meetings file", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
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
