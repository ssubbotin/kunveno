package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ssubbotin/kunveno/internal/room"
)

func TestRoomCollectorReadsLiveState(t *testing.T) {
	reg := room.NewRegistry()
	r := room.New("testing", "Testing", "1234", 15)
	reg.Add(r)
	_ = r.Join("a", "Ann", "1234")
	_ = r.Join("b", "Bob", "1234")
	r.SetEcho(true)
	_ = r.JoinBot("e", "Echo")
	_ = r.StartShare("b")
	reg.Add(room.New("empty", "Empty", "1234", 30))

	want := `
# HELP kunveno_room_capacity Maximum participants of the room.
# TYPE kunveno_room_capacity gauge
kunveno_room_capacity{room="empty"} 30
kunveno_room_capacity{room="testing"} 15
# HELP kunveno_room_echo 1 when the room asked for the echo helper.
# TYPE kunveno_room_echo gauge
kunveno_room_echo{room="empty"} 0
kunveno_room_echo{room="testing"} 1
# HELP kunveno_room_participants Participants in the room by kind (person or bot).
# TYPE kunveno_room_participants gauge
kunveno_room_participants{kind="bot",room="empty"} 0
kunveno_room_participants{kind="bot",room="testing"} 1
kunveno_room_participants{kind="person",room="empty"} 0
kunveno_room_participants{kind="person",room="testing"} 2
# HELP kunveno_room_sharing 1 while someone shares a screen in the room.
# TYPE kunveno_room_sharing gauge
kunveno_room_sharing{room="empty"} 0
kunveno_room_sharing{room="testing"} 1
# HELP kunveno_rooms Rooms known to the server.
# TYPE kunveno_rooms gauge
kunveno_rooms 2
`
	if err := testutil.CollectAndCompare(roomCollector{reg}, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
}

func TestHandlerServesLocalOnly(t *testing.T) {
	h := Handler(NewRegistry(room.NewRegistry(), "test"))

	req := httptest.NewRequest("GET", "/metrics", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `kunveno_build_info{version="test"} 1`) || !strings.Contains(body, "go_goroutines") {
		t.Fatalf("local: %d\n%s", rec.Code, body)
	}

	req = httptest.NewRequest("GET", "/metrics", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "10.8.0.5")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("proxied: %d, want 403", rec.Code)
	}
}
