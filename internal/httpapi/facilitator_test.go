package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ssubbotin/kunveno/internal/room"
)

func TestFacilitatorRoomsList(t *testing.T) {
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Ops", "1234", 15))
	reg.Add(room.New("other", "Other", "1234", 15))
	r, _ := reg.Get("ops")
	r.SetFacilitator(true)
	get := func(h http.Handler, remote string, header map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/internal/facilitator", nil)
		req.RemoteAddr = remote
		for k, v := range header {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := get(FacilitatorRooms(reg, true), "127.0.0.1:1", nil); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `["ops"]` {
		t.Fatalf("list %d %s", w.Code, w.Body)
	}
	if w := get(FacilitatorRooms(reg, true), "10.0.0.5:1", nil); w.Code != http.StatusForbidden {
		t.Fatalf("off loopback: %d", w.Code)
	}
	if w := get(FacilitatorRooms(reg, true), "127.0.0.1:1", map[string]string{"X-Forwarded-For": "10.0.0.5"}); w.Code != http.StatusForbidden {
		t.Fatalf("through nginx: %d", w.Code)
	}
	if w := get(FacilitatorRooms(reg, false), "127.0.0.1:1", nil); strings.TrimSpace(w.Body.String()) != `[]` {
		t.Fatalf("disabled list %s", w.Body)
	}
}
