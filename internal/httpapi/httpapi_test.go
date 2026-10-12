package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/ssubbotin/kunveno/internal/room"
)

func TestRoomsListsWithoutPINs(t *testing.T) {
	reg := room.NewRegistry()
	r := room.New("ops", "Operations", "1234", 15)
	_ = r.Join("a", "Ann", "1234")
	reg.Add(r)
	reg.Add(room.New("dev", "Development", "5678", 20))

	rec := httptest.NewRecorder()
	Rooms(reg).ServeHTTP(rec, httptest.NewRequest("GET", "/api/rooms", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0]["slug"] != "ops" || got[0]["participants"].(float64) != 1 || got[1]["capacity"].(float64) != 20 {
		t.Fatalf("body = %s", rec.Body.String())
	}
	for _, item := range got {
		if _, leaked := item["pin"]; leaked {
			t.Fatal("pin leaked into /api/rooms")
		}
	}
	rec = httptest.NewRecorder()
	Rooms(reg).ServeHTTP(rec, httptest.NewRequest("POST", "/api/rooms", nil))
	if rec.Code != 405 {
		t.Fatalf("POST status %d, want 405", rec.Code)
	}
}
