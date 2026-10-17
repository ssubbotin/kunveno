package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/ssubbotin/kunveno/internal/lobby"
	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"github.com/ssubbotin/kunveno/internal/recording"
	"github.com/ssubbotin/kunveno/internal/room"
)

// MeetingsConfig is what the meeting history routes work with.
type MeetingsConfig struct {
	Registry *room.Registry
	Kept     lobby.Kept // the stores and which features are on
	PINs     *ratelimit.Limiter
	Links    *Links // recording download links
	Loc      *time.Location
}

type partItem struct {
	Part  int64  `json:"part"`  // the part's start in Unix ms: its name in the routes
	Start string `json:"start"` // the same, RFC 3339 for display
	Until string `json:"until"`
	Size  int64  `json:"size"`
}

type historyItem struct {
	Meeting      int64      `json:"meeting"` // the meeting's start in Unix ms: its name in the routes
	Start        string     `json:"start"`   // the same, RFC 3339 for display
	End          string     `json:"end,omitempty"`
	People       int        `json:"people,omitempty"`
	NotesUntil   string     `json:"notes_until,omitempty"`
	Summary      string     `json:"summary,omitempty"`
	SummaryUntil string     `json:"summary_until,omitempty"`
	Parts        []partItem `json:"parts"`
}

// RegisterMeetings adds the meeting history routes to mux, each wrapped by wrap (the sign-in gate):
//
//	GET    /api/rooms/{slug}/meetings                                the room's meetings, newest first; no names, no PIN
//	POST   /api/rooms/{slug}/meetings/{meeting}/notes                {"pin","lang"}: the meeting's notes as Markdown
//	DELETE /api/rooms/{slug}/meetings/{meeting}/notes                {"pin"}: 204
//	POST   /api/rooms/{slug}/meetings/{meeting}/summary              {"pin","lang"}: the meeting's summary as Markdown
//	DELETE /api/rooms/{slug}/meetings/{meeting}/summary              {"pin"}: 204
//	POST   /api/rooms/{slug}/meetings/{meeting}/recording/{part}     {"pin"}: a download link {"url","expires"}
//	DELETE /api/rooms/{slug}/meetings/{meeting}/recording/{part}     {"pin"}: 204
//
// {meeting} and {part} are starts in Unix milliseconds. The item routes take the PIN with the same
// per-address limit as the other PIN routes; 404 for an item that is not there or a feature that
// is off.
func RegisterMeetings(mux *http.ServeMux, c MeetingsConfig, wrap func(http.Handler) http.Handler) {
	if wrap == nil {
		wrap = func(h http.Handler) http.Handler { return h }
	}
	k := c.Kept
	notesOn := k.NotesEnabled && k.Notes != nil
	summaryOn := notesOn && k.SummaryEnabled
	recordingOn := k.RecordingEnabled && k.Recordings != nil
	handle := func(pattern string, f http.HandlerFunc) { mux.Handle(pattern, wrap(f)) }

	handle("GET /api/rooms/{slug}/meetings", func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if _, ok := c.Registry.Get(slug); !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": room.ErrNotFound.Error()})
			return
		}
		items := []historyItem{}
		for _, m := range k.History(slug) {
			it := historyItem{Meeting: m.Start.UnixMilli(), Start: m.Start.In(c.Loc).Format(time.RFC3339), People: m.People, Summary: string(m.Summary), Parts: []partItem{}}
			if !m.End.IsZero() {
				it.End = m.End.In(c.Loc).Format(time.RFC3339)
			}
			if !m.NotesUntil.IsZero() {
				it.NotesUntil = m.NotesUntil.In(c.Loc).Format(time.RFC3339)
			}
			if !m.SummaryUntil.IsZero() {
				it.SummaryUntil = m.SummaryUntil.In(c.Loc).Format(time.RFC3339)
			}
			for _, p := range m.Parts {
				it.Parts = append(it.Parts, partItem{Part: p.Start.UnixMilli(), Start: p.Start.In(c.Loc).Format(time.RFC3339), Until: p.Until.In(c.Loc).Format(time.RFC3339), Size: p.Size})
			}
			items = append(items, it)
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, items)
	})

	// item runs the common checks of the item routes and returns the room, the meeting and the body.
	item := func(w http.ResponseWriter, r *http.Request, what string, on bool) (string, int64, notesRequest, bool) {
		if !on {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no " + what})
			return "", 0, notesRequest{}, false
		}
		meeting, err := strconv.ParseInt(r.PathValue("meeting"), 10, 64)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return "", 0, notesRequest{}, false
		}
		slug := r.PathValue("slug")
		req, ok := pinChecked(w, r, slug, what, c.Registry, c.PINs)
		return slug, meeting, req, ok
	}
	gone := func(w http.ResponseWriter, what string) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no " + what})
	}

	handle("POST /api/rooms/{slug}/meetings/{meeting}/notes", func(w http.ResponseWriter, r *http.Request) {
		slug, meeting, req, ok := item(w, r, "notes", notesOn)
		if !ok {
			return
		}
		t, err := k.Notes.Load(slug, meeting)
		if err != nil {
			gone(w, "notes")
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+notes.FileName(slug, t.Start, c.Loc)+`"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(notes.Markdown(t, req.Lang, c.Loc))
	})
	handle("DELETE /api/rooms/{slug}/meetings/{meeting}/notes", func(w http.ResponseWriter, r *http.Request) {
		slug, meeting, _, ok := item(w, r, "notes", notesOn)
		if !ok {
			return
		}
		switch err := k.Notes.DeleteEnded(slug, meeting); {
		case errors.Is(err, notes.ErrRecording):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "notes are being recorded"})
		case errors.Is(err, notes.ErrNoNotes), errors.Is(err, notes.ErrBadSlug):
			gone(w, "notes")
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	handle("POST /api/rooms/{slug}/meetings/{meeting}/summary", func(w http.ResponseWriter, r *http.Request) {
		slug, meeting, req, ok := item(w, r, "summary", summaryOn)
		if !ok {
			return
		}
		sm, err := k.Notes.LoadSummary(slug, meeting)
		if err != nil {
			gone(w, "summary")
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+notes.SummaryFileName(slug, sm.Start, c.Loc)+`"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(notes.SummaryMarkdown(sm, req.Lang, c.Loc))
	})
	handle("DELETE /api/rooms/{slug}/meetings/{meeting}/summary", func(w http.ResponseWriter, r *http.Request) {
		slug, meeting, _, ok := item(w, r, "summary", summaryOn)
		if !ok {
			return
		}
		switch err := k.Notes.DeleteSummary(slug, meeting); {
		case errors.Is(err, notes.ErrNoSummary):
			gone(w, "summary")
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	part := func(r *http.Request) (int64, bool) {
		p, err := strconv.ParseInt(r.PathValue("part"), 10, 64)
		return p, err == nil
	}
	handle("POST /api/rooms/{slug}/meetings/{meeting}/recording/{part}", func(w http.ResponseWriter, r *http.Request) {
		p, pok := part(r)
		slug, meeting, _, ok := item(w, r, "recording", recordingOn && pok)
		if !ok {
			return
		}
		f, m, err := k.Recordings.OpenPart(slug, meeting, p)
		if err != nil {
			gone(w, "recording")
			return
		}
		_ = f.Close()
		token, exp := c.Links.add(slug, m)
		writeJSON(w, http.StatusOK, map[string]string{"url": "/api/rooms/" + slug + "/recording/" + token, "expires": exp.Format(time.RFC3339)})
	})
	handle("DELETE /api/rooms/{slug}/meetings/{meeting}/recording/{part}", func(w http.ResponseWriter, r *http.Request) {
		p, pok := part(r)
		slug, meeting, _, ok := item(w, r, "recording", recordingOn && pok)
		if !ok {
			return
		}
		switch err := k.Recordings.DeletePart(slug, meeting, p); {
		case errors.Is(err, recording.ErrNoRecording):
			gone(w, "recording")
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
}
