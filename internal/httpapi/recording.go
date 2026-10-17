package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"github.com/ssubbotin/kunveno/internal/recording"
	"github.com/ssubbotin/kunveno/internal/room"
)

// RecordingRooms tells the recording bot which rooms want a recording: GET /internal/recording,
// a JSON list of slugs, empty while recording.enabled is off; loopback only (bot contract).
func RecordingRooms(reg *room.Registry, enabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !local(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !enabled {
			writeJSON(w, http.StatusOK, []string{})
			return
		}
		writeJSON(w, http.StatusOK, reg.RecordingRooms())
	})
}

// RecordingUpload takes a finished part of a recording from the bot: PUT /internal/recording/{slug},
// loopback only, body video/mp4, X-Kunveno-Meeting the meeting start and X-Kunveno-Part the part's
// start (RFC 3339; without it the part is the meeting's start, as a recorder of the previous release
// sends). 204 stored (then stored(slug) tells the room), 400 no or bad meeting or part, 404 recording
// off or no such room, 409 a meeting the room neither runs nor lists (known false), 413 larger than
// max. A stored part is replaced by an upload of the same part only.
func RecordingUpload(reg *room.Registry, store *recording.Store, max int64, stored func(slug string), known func(slug string, meeting time.Time) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !local(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		slug := r.PathValue("slug")
		if _, ok := reg.Get(slug); store == nil || !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no recording"})
			return
		}
		if r.ContentLength > max {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": recording.ErrTooLarge.Error()})
			return
		}
		meeting, err := time.Parse(time.RFC3339Nano, r.Header.Get("X-Kunveno-Meeting"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "X-Kunveno-Meeting must be RFC 3339"})
			return
		}
		part := meeting
		if h := r.Header.Get("X-Kunveno-Part"); h != "" {
			if part, err = time.Parse(time.RFC3339Nano, h); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "X-Kunveno-Part must be RFC 3339"})
				return
			}
		}
		if known != nil && !known(slug, meeting) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "no such meeting in this room"})
			return
		}
		_, err = store.Put(slug, meeting, part, r.Body, max)
		switch {
		case errors.Is(err, recording.ErrTooLarge):
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error()})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		default:
			if stored != nil {
				stored(slug)
			}
			w.WriteHeader(http.StatusNoContent)
		}
	})
}

// Links are short-lived download links: a token bound to one part of one meeting's recording as it
// was when the link was made, so a link never serves a later or replaced file.
type Links struct {
	ttl time.Duration
	now func() time.Time

	mu sync.Mutex
	m  map[string]link
}

type link struct {
	slug          string
	meeting, part int64     // Unix ms
	ended         time.Time // the part's Meta.Ended at link time
	expires       time.Time
}

func NewLinks(ttl time.Duration) *Links {
	return &Links{ttl: ttl, now: time.Now, m: map[string]link{}}
}

func (l *Links) add(slug string, m recording.Meta) (string, time.Time) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for t, v := range l.m { // drop expired links
		if !now.Before(v.expires) {
			delete(l.m, t)
		}
	}
	exp := now.Add(l.ttl)
	l.m[token] = link{slug: slug, meeting: m.Meeting.UnixMilli(), part: m.Part.UnixMilli(), ended: m.Ended, expires: exp}
	return token, exp
}

func (l *Links) get(slug, token string) (link, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.m[token]
	if !ok || v.slug != slug || !l.now().Before(v.expires) {
		return link{}, false
	}
	return v, true
}

// RecordingLink serves POST /api/rooms/{slug}/recording with {"pin"}: after the PIN check (the
// shared per-address limit) a download link valid for the Links' ttl: {"url","expires"}. 404 when
// recording is off, the room or its recording does not exist.
func RecordingLink(reg *room.Registry, store *recording.Store, pins *ratelimit.Limiter, links *Links) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug, _, ok := pinAccess(w, r, http.MethodPost, "/recording", "recording", reg, store != nil, pins)
		if !ok {
			return
		}
		f, m, err := store.Open(slug)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no recording"})
			return
		}
		_ = f.Close()
		token, exp := links.add(slug, m)
		writeJSON(w, http.StatusOK, map[string]string{"url": "/api/rooms/" + slug + "/recording/" + token, "expires": exp.Format(time.RFC3339)})
	})
}

// RecordingFile serves GET /api/rooms/{slug}/recording/{token}: the file as an attachment with
// Range support; 404 for an unknown or expired link or a recording replaced or deleted since.
func RecordingFile(store *recording.Store, links *Links, loc *time.Location) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		l, ok := links.get(slug, r.PathValue("token"))
		if !ok || store == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no recording"})
			return
		}
		f, m, err := store.OpenPart(slug, l.meeting, l.part)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no recording"})
			return
		}
		defer f.Close()
		if !m.Ended.Equal(l.ended) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no recording"})
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Disposition", `attachment; filename="`+recording.FileName(slug, m.Part, loc)+`"`)
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, "", m.Ended, f)
	})
}

// RecordingDelete serves DELETE /api/rooms/{slug}/recording with {"pin"}: 204 deleted, 404 none,
// 409 while the room is recording, 403 wrong PIN (counted).
func RecordingDelete(reg *room.Registry, store *recording.Store, pins *ratelimit.Limiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug, _, ok := pinAccess(w, r, http.MethodDelete, "/recording", "recording", reg, store != nil, pins)
		if !ok {
			return
		}
		err := store.Delete(slug)
		switch {
		case errors.Is(err, recording.ErrRecording):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "the room is recording"})
		case errors.Is(err, recording.ErrNoRecording):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no recording"})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
}
