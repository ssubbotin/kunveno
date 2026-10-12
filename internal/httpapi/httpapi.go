// Package httpapi serves the small JSON API used by the lobby.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/ssubbotin/kunveno/internal/room"
)

type roomItem struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Participants int    `json:"participants"`
	Capacity     int    `json:"capacity"`
}

// Rooms lists configured rooms with live participant counts. PINs never leave the server.
func Rooms(reg *room.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		rooms := reg.All()
		items := make([]roomItem, 0, len(rooms))
		for _, rm := range rooms {
			items = append(items, roomItem{Slug: rm.Slug(), Name: rm.Name(), Participants: rm.Count(), Capacity: rm.Capacity()})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(items)
	})
}

// Health answers 200 "ok" for systemd and nginx checks.
func Health() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
}
