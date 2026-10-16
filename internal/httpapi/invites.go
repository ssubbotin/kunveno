package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ssubbotin/kunveno/internal/auth"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"github.com/ssubbotin/kunveno/internal/room"
)

// inviteChoices are the lifetimes, in hours, an invitation can have; never above the configured longest.
var inviteChoices = []int{1, 24, 168}

type inviteRequest struct {
	PIN   string `json:"pin"`
	Hours int    `json:"hours"`
}

type inviteItem struct {
	ID        string `json:"id"`
	CreatedBy string `json:"created_by"`
	Expires   string `json:"expires"` // RFC 3339
}

// Invites serves the guest invitations of a room; every request carries the room's PIN:
//
//	POST   /api/rooms/{slug}/invites       {"pin","hours"} 201 {"id","link","expires"}
//	POST   /api/rooms/{slug}/invites/list  {"pin"} 200 [{"id","created_by","expires"}], usable ones, newest first
//	DELETE /api/rooms/{slug}/invites/{id}  {"pin"} 204, then revoked(id) so that its guests leave at once
//
// The PIN checks are those of the notes download, in the same order: the method (405), the
// per-address limit (429), the body (400), the room (404) and its PIN (403, counted toward the limit).
// hours is 1, 24 or 168 and at most max (400). The link is publicURL/join/{slug}#invite=<token>; the
// token is shown once and never logged. The creator is the signed-in person (auth.IdentityFrom);
// log may be nil.
func Invites(reg *room.Registry, inv *auth.Invites, pins *ratelimit.Limiter, max time.Duration, publicURL string, revoked func(id string), log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/rooms/"), "/")
		if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] != "invites" || (len(parts) == 3 && parts[2] == "") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		slug := parts[0]
		var want string
		switch {
		case len(parts) == 2:
			want = http.MethodPost // create
		case parts[2] == "list" && r.Method != http.MethodDelete:
			want = http.MethodPost
		default:
			want = http.MethodDelete // revoke parts[2]
		}
		if r.Method != want {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ip, ok := pinAllowed(w, r, pins)
		if !ok {
			return
		}
		var req inviteRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body"})
			return
		}
		if !pinMatches(w, reg, slug, req.PIN, ip, pins, "invites") {
			return
		}
		who, _ := auth.IdentityFrom(r.Context())
		switch {
		case len(parts) == 2:
			createInvite(w, inv, slug, req.Hours, max, publicURL, who, log)
		case r.Method == http.MethodPost:
			listInvites(w, inv, slug)
		default:
			revokeInvite(w, inv, slug, parts[2], revoked, who, log)
		}
	})
}

func createInvite(w http.ResponseWriter, inv *auth.Invites, slug string, hours int, max time.Duration, publicURL string, who auth.Identity, log *slog.Logger) {
	ttl := time.Duration(hours) * time.Hour
	valid := false
	for _, h := range inviteChoices {
		valid = valid || h == hours
	}
	if !valid || ttl > max {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hours must be 1, 24 or 168, and no more than the longest allowed"})
		return
	}
	token, made, err := inv.Create(slug, auth.DisplayName(who), ttl)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot save the invitation"})
		log.Error("invitation not saved", "room", slug, "err", err)
		return
	}
	log.Info("invitation created", "room", slug, "id", made.ID, "by", who.Login, "name", made.CreatedBy, "hours", hours)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]string{"id": made.ID, "link": publicURL + "/join/" + slug + "#invite=" + token,
		"expires": made.Expires.Format(time.RFC3339)})
}

func listInvites(w http.ResponseWriter, inv *auth.Invites, slug string) {
	all := inv.List(slug)
	items := make([]inviteItem, 0, len(all))
	for _, i := range all {
		items = append(items, inviteItem{ID: i.ID, CreatedBy: i.CreatedBy, Expires: i.Expires.Format(time.RFC3339)})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, items)
}

func revokeInvite(w http.ResponseWriter, inv *auth.Invites, slug, id string, revoked func(string), who auth.Identity, log *slog.Logger) {
	_, err := inv.Revoke(slug, id)
	switch {
	case errors.Is(err, auth.ErrBadInvite):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such invitation"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot save the invitation"})
		log.Error("invitation revocation not saved", "room", slug, "id", id, "err", err)
		return
	}
	if revoked != nil {
		revoked(id)
	}
	log.Info("invitation revoked", "room", slug, "id", id, "by", who.Login, "name", auth.DisplayName(who))
	w.WriteHeader(http.StatusNoContent)
}
