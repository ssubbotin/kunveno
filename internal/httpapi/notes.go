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
// still being written, or notes are off (store nil).
func NotesDownload(reg *room.Registry, store *notes.Store, pins *ratelimit.Limiter, loc *time.Location) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if store == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no notes"})
			return
		}
		slug, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/api/rooms/"), "/notes")
		if !ok || slug == "" || strings.Contains(slug, "/") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		ip := ratelimit.ClientIP(r)
		if pins != nil {
			if ok, wait := pins.Allow(ip); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many wrong PINs; try again later"})
				return
			}
		}
		var req notesRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body"})
			return
		}
		rm, found := reg.Get(slug)
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": room.ErrNotFound.Error()})
			return
		}
		if !rm.CheckPIN(req.PIN) {
			metrics.PINFailures.WithLabelValues("notes").Inc()
			if pins != nil && pins.Fail(ip) {
				metrics.PINBlocks.Inc()
			}
			writeJSON(w, http.StatusForbidden, map[string]string{"error": room.ErrBadPIN.Error()})
			return
		}
		if pins != nil {
			pins.Success(ip)
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
