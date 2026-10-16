package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ssubbotin/kunveno/internal/metrics"
	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"github.com/ssubbotin/kunveno/internal/room"
)

// NotesRooms tells the notes bot (kunveno-notes add-on) which rooms want notes: GET /internal/notes
// returns a JSON list of slugs, empty while notes.enabled is off. Only loopback requests that did
// not pass through nginx are served. Part of the bot contract (see package signal).
func NotesRooms(reg *room.Registry, enabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !local(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !enabled {
			writeJSON(w, http.StatusOK, []string{})
			return
		}
		writeJSON(w, http.StatusOK, reg.NotesRooms())
	})
}

type notesRequest struct {
	PIN  string `json:"pin"`
	Lang string `json:"lang"` // ru, en or es: the language of the file's fixed labels
}

// NotesDownload serves POST /api/rooms/{slug}/notes with {"pin","lang"}: the notes of the room's
// last meeting as Markdown, times in loc. A wrong PIN is 403 and counts toward the same per-address
// limit as joins (pins, may be nil); 404 when the room or its notes do not exist, have expired, are
// still being written, or notes are off (enabled false).
func NotesDownload(reg *room.Registry, store *notes.Store, enabled bool, pins *ratelimit.Limiter, loc *time.Location) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug, req, ok := pinAccess(w, r, http.MethodPost, "/notes", "notes", reg, enabled && store != nil, pins)
		if !ok {
			return
		}
		t, err := store.Load(slug)
		switch {
		case errors.Is(err, notes.ErrNoNotes):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no notes"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+notes.FileName(slug, t.Start, loc)+`"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(notes.Markdown(t, req.Lang, loc))
	})
}

// NotesDelete serves DELETE /api/rooms/{slug}/notes with {"pin"}: it deletes the notes of the
// room's last meeting, 204. The same checks as NotesDownload come first (404 while notes are off,
// for an unknown room, or for notes that do not exist or have expired; 403 for a wrong PIN, counted
// in pins); 409 while the room's notes are still being recorded.
func NotesDelete(reg *room.Registry, store *notes.Store, enabled bool, pins *ratelimit.Limiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug, _, ok := pinAccess(w, r, http.MethodDelete, "/notes", "notes", reg, enabled && store != nil, pins)
		if !ok {
			return
		}
		err := store.DeleteEnded(slug)
		switch {
		case errors.Is(err, notes.ErrRecording):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "notes are being recorded"})
		case errors.Is(err, notes.ErrNoNotes):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no notes"})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
}

// pinAccess runs the checks shared by the PIN-protected requests of notes and recording (suffix is
// the path after the slug, what is the word in the 404 body and the PIN metric label), in order: the
// method (405), the feature on (404 before anything else), the path, the per-address PIN limit
// (429), the body (400), the room (404) and its PIN (403, counted toward the limit). It writes the
// answer and reports false when a check fails.
func pinAccess(w http.ResponseWriter, r *http.Request, method, suffix, what string, reg *room.Registry, enabled bool, pins *ratelimit.Limiter) (string, notesRequest, bool) {
	var req notesRequest
	if r.Method != method {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return "", req, false
	}
	if !enabled {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no " + what})
		return "", req, false
	}
	slug, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/api/rooms/"), suffix)
	if !ok || slug == "" || strings.Contains(slug, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return "", req, false
	}
	ip, ok := pinAllowed(w, r, pins)
	if !ok {
		return "", req, false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body"})
		return "", req, false
	}
	if !pinMatches(w, reg, slug, req.PIN, ip, pins, what) {
		return "", req, false
	}
	return slug, req, true
}

// pinAllowed is the per-address PIN limit (429 with Retry-After while the address is blocked). It
// returns the client address for pinMatches.
func pinAllowed(w http.ResponseWriter, r *http.Request, pins *ratelimit.Limiter) (string, bool) {
	ip := ratelimit.ClientIP(r)
	if pins != nil {
		if ok, wait := pins.Allow(ip); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many wrong PINs; try again later"})
			return ip, false
		}
	}
	return ip, true
}

// pinMatches checks that the room exists (404) and that pin is its PIN (403, counted toward the
// limit under the metric label what).
func pinMatches(w http.ResponseWriter, reg *room.Registry, slug, pin, ip string, pins *ratelimit.Limiter, what string) bool {
	rm, found := reg.Get(slug)
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": room.ErrNotFound.Error()})
		return false
	}
	if !rm.CheckPIN(pin) {
		metrics.PINFailures.WithLabelValues(what).Inc()
		if pins != nil && pins.Fail(ip) {
			metrics.PINBlocks.Inc()
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"error": room.ErrBadPIN.Error()})
		return false
	}
	if pins != nil {
		pins.Success(ip)
	}
	return true
}
