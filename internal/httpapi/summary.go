package httpapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"github.com/ssubbotin/kunveno/internal/room"
)

// maxSummary is the largest summary the add-on may upload.
const maxSummary = 256 << 10

const summaryPrefix = "/internal/notes/summaries"

// SummaryInternal serves the summary service of the kunveno-notes add-on (part of the bot contract,
// see package signal), to loopback requests that did not pass through nginx only:
//
//	GET  /internal/notes/summaries                      [{"slug","end"}]: ready notes waiting for a summary; [] while summaries are off
//	GET  /internal/notes/summaries/{slug}?end=<ms>      {"room","start","end","notes"}: the notes as Markdown
//	PUT  /internal/notes/summaries/{slug}?end=<ms>      the summary as Markdown (UTF-8, not blank, at most 256 KiB), 204
//	POST /internal/notes/summaries/{slug}/failed?end=<ms>  the add-on gave up on these notes, 204
//
// end is the end of the notes in Unix milliseconds: 409 when the room's ready notes did not end
// then (they were reopened, replaced, deleted or expired). 404 for everything but the list while
// summaries are off.
func SummaryInternal(store *notes.Store, enabled bool, loc *time.Location) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !local(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		rest := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, summaryPrefix), "/")
		if rest == "" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			type item struct {
				Slug string `json:"slug"`
				End  int64  `json:"end"`
			}
			items := []item{}
			if enabled && store != nil {
				for _, p := range store.PendingSummaries() {
					items = append(items, item{Slug: p.Slug, End: p.End})
				}
			}
			writeJSON(w, http.StatusOK, items)
			return
		}
		if !enabled || store == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no summaries"})
			return
		}
		slug, failed := strings.CutSuffix(rest, "/failed")
		if slug == "" || strings.Contains(slug, "/") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		end, err := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad end"})
			return
		}
		switch {
		case failed && r.Method == http.MethodPost:
			summaryResult(w, store.FailSummary(slug, end))
		case !failed && r.Method == http.MethodGet:
			summaryNotes(w, store, slug, end, loc)
		case !failed && r.Method == http.MethodPut:
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSummary))
			if err != nil || !utf8.Valid(body) || len(bytes.TrimSpace(body)) == 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad summary"})
				return
			}
			summaryResult(w, store.PutSummary(slug, end, body))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

// summaryNotes answers the notes that ended at end, or 409.
func summaryNotes(w http.ResponseWriter, store *notes.Store, slug string, end int64, loc *time.Location) {
	if got, ok := store.EndedAt(slug); !ok || got != end {
		summaryResult(w, notes.ErrStale)
		return
	}
	t, err := store.Load(slug)
	if errors.Is(err, notes.ErrNoNotes) || errors.Is(err, notes.ErrBadSlug) {
		summaryResult(w, notes.ErrStale)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"room": t.Room, "start": t.Start.In(loc).Format(time.RFC3339), "end": end,
		"notes": string(notes.Markdown(t, "ru", loc))})
}

func summaryResult(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notes.ErrStale):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "stale"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// SummaryDownload serves POST /api/rooms/{slug}/summary with {"pin","lang"}: the summary of the
// room's last meeting as Markdown, with the same checks as NotesDownload; 404 when there is none
// (yet) or summaries are off (enabled false).
func SummaryDownload(reg *room.Registry, store *notes.Store, enabled bool, pins *ratelimit.Limiter, loc *time.Location) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug, req, ok := pinAccess(w, r, http.MethodPost, "/summary", "summary", reg, enabled && store != nil, pins)
		if !ok {
			return
		}
		sm, err := store.LoadSummary(slug)
		switch {
		case errors.Is(err, notes.ErrNoSummary):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no summary"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+notes.SummaryFileName(slug, sm.Start, loc)+`"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(notes.SummaryMarkdown(sm, req.Lang, loc))
	})
}
