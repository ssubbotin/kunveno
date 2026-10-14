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

	"github.com/ssubbotin/kunveno/internal/metrics"
	"github.com/ssubbotin/kunveno/internal/notes"
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
}

// Rooms serves the room list and its changes. PINs never leave the server.
//
//	GET    /api/rooms          list with live participant counts and, with notes on, notes_until
//	POST   /api/rooms          {"name","pin"} creates a room, 201 {"slug","name"}
//	DELETE /api/rooms/{slug}   {"pin"} deletes an empty created room and its notes, 204
//
// persist (may be nil) is called after every change to save the created rooms. pins (may be
// nil) limits wrong PINs per client address, shared with joining. store (may be nil) holds the
// meeting notes: it is passed even while notes.enabled is off so that deleting a room always
// deletes its notes; notesEnabled alone decides whether notes_until is listed.
func Rooms(reg *room.Registry, persist func() error, pins *ratelimit.Limiter, store *notes.Store, notesEnabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/rooms"), "/")
		switch {
		case r.Method == http.MethodGet && slug == "":
			list(w, reg, store, notesEnabled)
		case r.Method == http.MethodPost && slug == "":
			create(w, r, reg, persist)
		case r.Method == http.MethodDelete && slug != "":
			remove(w, r, reg, persist, pins, store, slug)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func list(w http.ResponseWriter, reg *room.Registry, store *notes.Store, notesEnabled bool) {
	rooms := reg.All()
	items := make([]roomItem, 0, len(rooms))
	for _, rm := range rooms {
		item := roomItem{Slug: rm.Slug(), Name: rm.Name(), Participants: rm.Count(), Capacity: rm.Capacity(), Created: rm.Created()}
		if notesEnabled && store != nil {
			if until, ok := store.Until(rm.Slug()); ok {
				item.NotesUntil = until.Format(time.RFC3339)
			}
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

func create(w http.ResponseWriter, r *http.Request, reg *room.Registry, persist func() error) {
	req, ok := decode(w, r)
	if !ok {
		return
	}
	rm, err := reg.Create(req.Name, req.PIN)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !save(w, persist) {
		return
	}
	metrics.RoomsCreated.Inc()
	writeJSON(w, http.StatusCreated, map[string]string{"slug": rm.Slug(), "name": rm.Name()})
}

func remove(w http.ResponseWriter, r *http.Request, reg *room.Registry, persist func() error, pins *ratelimit.Limiter, store *notes.Store, slug string) {
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
	err := reg.Delete(slug, req.PIN)
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
		if store != nil {
			// A failure leaves the file to the expiry sweep; the room itself is gone.
			_ = store.Delete(slug)
		}
		if save(w, persist) {
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

// Info serves GET /api/info: installation details the client shows: the title, the reactions
// participants can send, whether the noise filter is on by default, whether meeting notes are
// offered (notes.enabled) and whether phrases are shown live (notes.live).
func Info(title string, reactions []string, noiseSuppression, notesEnabled, notesLive bool) http.Handler {
	body := map[string]any{"title": title, "reactions": reactions, "noise_suppression": noiseSuppression,
		"notes": notesEnabled, "notes_live": notesEnabled && notesLive}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, body)
	})
}
