package httpapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ssubbotin/kunveno/internal/documents"
	"github.com/ssubbotin/kunveno/internal/room"
)

// DocumentUpload takes a meeting document from the facilitator bot (bot contract, see package
// signal): PUT /internal/documents/{slug}/{id}?title=<text>, loopback only, body Markdown (UTF-8,
// not blank, at most 512 KiB). id is ^[a-z0-9-]{1,40}$; the same id replaces the document of the
// meeting. meeting reports the room's live meeting while a facilitator is in it; the document
// belongs to that meeting. 200 stored, {"id","rev","updated"} with updated in Unix ms (then
// stored tells the room), 400 a bad id, title or body,
// 403 not loopback, 404 documents off (store nil) or no such room, 409 no live meeting with a
// facilitator or the meeting already has 20 documents, 413 a larger body.
func DocumentUpload(reg *room.Registry, store *documents.Store, meeting func(slug string) (time.Time, bool), stored func(slug string, meeting time.Time)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !local(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		slug, id := r.PathValue("slug"), r.PathValue("id")
		if _, ok := reg.Get(slug); store == nil || !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no documents"})
			return
		}
		if !documents.ValidID(id) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
			return
		}
		title, ok := documents.CleanTitle(r.URL.Query().Get("title"))
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad title"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, documents.MaxBody))
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "document too large"})
			return
		case err != nil || !utf8.Valid(body) || len(bytes.TrimSpace(body)) == 0:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad document"})
			return
		}
		start, ok := meeting(slug)
		if !ok {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "no live meeting with a facilitator in this room"})
			return
		}
		info, err := store.Put(slug, start.UnixMilli(), id, title, body)
		switch {
		case errors.Is(err, documents.ErrTooMany):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "the meeting already has 20 documents"})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		default:
			if stored != nil {
				stored(slug, start)
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": info.ID, "rev": info.Rev, "updated": info.Updated.UnixMilli()})
		}
	})
}

// serveDocument writes one document of a meeting as an attachment; 404 when it is gone.
func serveDocument(w http.ResponseWriter, store *documents.Store, slug string, meeting int64, id string, loc *time.Location) {
	info, body, err := store.Load(slug, meeting, id)
	switch {
	case errors.Is(err, documents.ErrNoDocument):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no document"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	start := time.UnixMilli(meeting)
	name := documents.FileName(slug, start, info.Title, id, loc)
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", attachment(name, documents.FileName(slug, start, "", id, loc)))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// attachment is a Content-Disposition for name: plain when name is ASCII, otherwise fallback
// (ASCII) for old clients plus name in RFC 5987 form.
func attachment(name, fallback string) string {
	ascii := true
	for i := 0; i < len(name); i++ {
		if c := name[i]; c >= 0x80 || c < 0x20 || c == '"' || c == '\\' {
			ascii = false
			break
		}
	}
	if ascii {
		return `attachment; filename="` + name + `"`
	}
	return `attachment; filename="` + fallback + `"; filename*=UTF-8''` + strings.ReplaceAll(url.PathEscape(name), "'", "%27")
}
