package httpapi

import (
	"net/http"

	"github.com/ssubbotin/kunveno/internal/room"
)

// FacilitatorRooms tells the facilitator bot which rooms want it: GET /internal/facilitator, a JSON
// list of slugs, empty while facilitator.enabled is off; loopback only (bot contract).
func FacilitatorRooms(reg *room.Registry, enabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !local(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !enabled {
			writeJSON(w, http.StatusOK, []string{})
			return
		}
		writeJSON(w, http.StatusOK, reg.FacilitatorRooms())
	})
}
