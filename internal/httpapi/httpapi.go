// Package httpapi serves the small JSON API used by the lobby.
package httpapi

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ssubbotin/kunveno/internal/auth"
	"github.com/ssubbotin/kunveno/internal/lobby"
	"github.com/ssubbotin/kunveno/internal/metrics"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"github.com/ssubbotin/kunveno/internal/room"
)

type roomItem struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Participants int    `json:"participants"`
	Capacity     int    `json:"capacity"`
	Created      bool   `json:"created"` // made through the API; deletable with its PIN
	// NotesUntil (RFC 3339) says until when the notes of the room's last meeting can be downloaded.
	NotesUntil string `json:"notes_until,omitempty"`
	// RecordingUntil (RFC 3339) says until when the room's stored recording can be downloaded.
	RecordingUntil string `json:"recording_until,omitempty"`
	// LastMeeting describes the meeting whose notes or recording are kept: no names, no PIN.
	LastMeeting *meetingItem `json:"last_meeting,omitempty"`
}

type meetingItem struct {
	Start  string `json:"start"` // RFC 3339
	End    string `json:"end"`   // RFC 3339
	People int    `json:"people"`
}

// RoomsConfig is what the room API works with.
type RoomsConfig struct {
	Registry *room.Registry
	Persist  func() error       // saves the created rooms after every change; may be nil
	PINs     *ratelimit.Limiter // wrong PINs per client address, shared with joining; may be nil
	Kept     lobby.Kept         // the notes, recordings and meeting summaries the rooms keep
	// Deleted runs after a room was deleted, before the rooms are saved: with sign-in on it ends
	// the room's invitations. It may be nil; an error fails the request with 500.
	Deleted func(slug string) error
}

// Rooms serves the room list and its changes. PINs never leave the server.
//
//	GET    /api/rooms          list with live participant counts, notes_until, recording_until, last_meeting
//	POST   /api/rooms          {"name","pin"} creates a room, 201 {"slug","name"}
//	DELETE /api/rooms/{slug}   {"pin"} deletes an empty created room, its notes, its recording, its meetings and its invitations, 204
//
// notes_until and recording_until are listed for the enabled features only. A room lists
// last_meeting when it keeps notes or a recording (of the enabled features) of a meeting with a
// stored summary. Deleting a room deletes all it keeps, whatever is enabled.
func Rooms(cfg RoomsConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/rooms"), "/")
		switch {
		case r.Method == http.MethodGet && slug == "":
			list(w, cfg)
		case r.Method == http.MethodPost && slug == "":
			create(w, r, cfg)
		case r.Method == http.MethodDelete && slug != "":
			remove(w, r, cfg, slug)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func list(w http.ResponseWriter, cfg RoomsConfig) {
	rooms := cfg.Registry.All()
	items := make([]roomItem, 0, len(rooms))
	for _, rm := range rooms {
		item := roomItem{Slug: rm.Slug(), Name: rm.Name(), Participants: rm.Count(), Capacity: rm.Capacity(), Created: rm.Created()}
		kept := cfg.Kept.Of(rm.Slug())
		if !kept.NotesUntil.IsZero() {
			item.NotesUntil = kept.NotesUntil.Format(time.RFC3339)
		}
		if !kept.RecordingUntil.IsZero() {
			item.RecordingUntil = kept.RecordingUntil.Format(time.RFC3339)
		}
		if m := kept.Meeting; m != nil {
			item.LastMeeting = &meetingItem{Start: m.Start.Format(time.RFC3339), End: m.End.Format(time.RFC3339), People: m.People}
		}
		items = append(items, item)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, items)
}

type roomRequest struct {
	Name string `json:"name"`
	PIN  string `json:"pin"`
}

func decode(w http.ResponseWriter, r *http.Request) (roomRequest, bool) {
	var req roomRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body"})
		return req, false
	}
	return req, true
}

func create(w http.ResponseWriter, r *http.Request, cfg RoomsConfig) {
	req, ok := decode(w, r)
	if !ok {
		return
	}
	rm, err := cfg.Registry.Create(req.Name, req.PIN)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !save(w, cfg.Persist) {
		return
	}
	metrics.RoomsCreated.Inc()
	writeJSON(w, http.StatusCreated, map[string]string{"slug": rm.Slug(), "name": rm.Name()})
}

func remove(w http.ResponseWriter, r *http.Request, cfg RoomsConfig, slug string) {
	pins := cfg.PINs
	ip := ratelimit.ClientIP(r)
	if pins != nil {
		if ok, wait := pins.Allow(ip); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many wrong PINs; try again later"})
			return
		}
	}
	req, ok := decode(w, r)
	if !ok {
		return
	}
	err := cfg.Registry.Delete(slug, req.PIN)
	if errors.Is(err, room.ErrBadPIN) {
		metrics.PINFailures.WithLabelValues("delete").Inc()
		if pins != nil && pins.Fail(ip) {
			metrics.PINBlocks.Inc()
		}
	} else if err == nil && pins != nil {
		pins.Success(ip)
	}
	switch {
	case errors.Is(err, room.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, room.ErrFixed), errors.Is(err, room.ErrBadPIN):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, room.ErrNotEmpty):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	default:
		metrics.RoomsDeleted.Inc()
		cfg.Kept.Forget(slug)
		if cfg.Deleted != nil {
			if err := cfg.Deleted(slug); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		if save(w, cfg.Persist) {
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

func save(w http.ResponseWriter, persist func() error) bool {
	if persist == nil {
		return true
	}
	if err := persist(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot save rooms: " + err.Error()})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Health answers 200 "ok" for systemd and nginx checks.
func Health() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
}

// EchoRooms tells the local echo helper which rooms want it: GET /internal/echo returns a JSON
// list of slugs. Only loopback requests that did not pass through nginx are served.
func EchoRooms(reg *room.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !local(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		writeJSON(w, http.StatusOK, reg.EchoRooms())
	})
}

// local mirrors the check in internal/signal: loopback peer and no proxy header.
func local(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// SignInInfo is the sign-in block of /api/info: whether people sign in, and the providers' buttons.
type SignInInfo struct {
	Enabled   bool                `json:"enabled"`
	Providers []auth.ProviderInfo `json:"providers"`
}

// Info serves GET /api/info: installation details the client shows: the title, the reactions
// participants can send, whether the noise filter is on by default, whether meeting notes are
// offered (notes.enabled), whether phrases are shown live (notes.live) and whether meeting
// recording is offered (recording.enabled), how many seconds a client waits on a disconnected
// media connection before restarting (media.reconnect_grace) and, as "auth", whether sign-in is on
// and with which providers (signIn; nil is off).
func Info(title string, reactions []string, noiseSuppression, notesEnabled, notesLive, recordingEnabled bool, reconnectGrace int, signIn *SignInInfo) http.Handler {
	in := SignInInfo{Providers: []auth.ProviderInfo{}}
	if signIn != nil && signIn.Enabled {
		in.Enabled = true
		in.Providers = append(in.Providers, signIn.Providers...)
	}
	body := map[string]any{"title": title, "reactions": reactions, "noise_suppression": noiseSuppression,
		"notes": notesEnabled, "notes_live": notesEnabled && notesLive, "recording": recordingEnabled, "reconnect_grace": reconnectGrace,
		"auth": in}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, body)
	})
}
