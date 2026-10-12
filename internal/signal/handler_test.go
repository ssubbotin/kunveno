package signal

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/ssubbotin/kunveno/internal/room"
	"github.com/ssubbotin/kunveno/internal/sfu"
)

// fakeMedia records subscriptions instead of moving RTP.
type fakeMedia struct {
	mu    sync.Mutex
	peers map[string]*fakePeer
}

type fakePeer struct {
	id     string
	sig    sfu.Signaler
	m      *fakeMedia
	mu     sync.Mutex
	tracks map[sfu.Kind]*webrtc.TrackLocalStaticRTP
	subs   map[string]int // "from/kind" -> subscribe count minus unsubscribe count
	closed bool
	offers int
}

func newFakeMedia() *fakeMedia { return &fakeMedia{peers: map[string]*fakePeer{}} }

func (m *fakeMedia) NewPeer(id string, sig sfu.Signaler, _ sfu.Handlers) (MediaPeer, error) {
	p := &fakePeer{id: id, sig: sig, m: m, tracks: map[sfu.Kind]*webrtc.TrackLocalStaticRTP{}, subs: map[string]int{}}
	m.mu.Lock()
	m.peers[id] = p
	m.mu.Unlock()
	return p, nil
}

func (m *fakeMedia) peer(id string) *fakePeer {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.peers[id]
}

func (p *fakePeer) ID() string { return p.id }
func (p *fakePeer) Track(k sfu.Kind) *webrtc.TrackLocalStaticRTP {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tracks[k]
}
func (p *fakePeer) RequestKeyframe(sfu.Kind) {}
func (p *fakePeer) Negotiate() {
	p.mu.Lock()
	p.offers++
	p.mu.Unlock()
	_ = p.sig.SendOffer("v=0 fake offer")
}
func (p *fakePeer) HandleAnswer(string) error                     { return nil }
func (p *fakePeer) AddICECandidate(webrtc.ICECandidateInit) error { return nil }
func (p *fakePeer) Subscribe(from sfu.Source, k sfu.Kind) error {
	if from.Track(k) == nil {
		return sfu.ErrNoTrack
	}
	p.mu.Lock()
	p.subs[from.ID()+"/"+k.String()]++
	p.mu.Unlock()
	return nil
}
func (p *fakePeer) Unsubscribe(from sfu.Source, k sfu.Kind) error {
	p.mu.Lock()
	p.subs[from.ID()+"/"+k.String()]--
	p.mu.Unlock()
	return nil
}
func (p *fakePeer) Close() error { p.mu.Lock(); p.closed = true; p.mu.Unlock(); return nil }

func (p *fakePeer) setTrack(t *testing.T, k sfu.Kind) {
	t.Helper()
	mime := webrtc.MimeTypeOpus
	if k == sfu.KindScreen {
		mime = webrtc.MimeTypeVP8
	}
	tr, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: mime, ClockRate: 90000}, k.String(), p.id)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.tracks[k] = tr
	p.mu.Unlock()
}

func (p *fakePeer) subCount(fromID string, k sfu.Kind) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.subs[fromID+"/"+k.String()]
}

type env struct {
	srv   *httptest.Server
	media *fakeMedia
	h     *Handler
}

func newEnv(t *testing.T, capacity int) *env {
	t.Helper()
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Operations", "1234", capacity))
	media := newFakeMedia()
	tw := &testWriter{t: t}
	t.Cleanup(tw.stop)
	h := New(reg, media, nil, slog.New(slog.NewTextHandler(tw, nil)))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &env{srv: srv, media: media, h: h}
}

// testWriter routes server logs to t.Log until the test ends; session goroutines
// on hijacked connections can outlive the test and must not call t.Log after it.
type testWriter struct {
	mu   sync.Mutex
	t    *testing.T
	done bool
}

func (w *testWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.done {
		w.t.Log(strings.TrimSpace(string(b)))
	}
	return len(b), nil
}

func (w *testWriter) stop() { w.mu.Lock(); w.done = true; w.mu.Unlock() }

func dial(t *testing.T, e *env) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func send(t *testing.T, c *websocket.Conn, raw string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte(raw)); err != nil {
		t.Fatal(err)
	}
}

type anyMsg struct {
	Type         string            `json:"type"`
	Code         string            `json:"code"`
	Sharer       string            `json:"sharer"`
	Participants []ParticipantInfo `json:"participants"`
	Self         ParticipantInfo   `json:"self"`
	Echo         bool              `json:"echo"`
}

// expect reads frames until one of the wanted type arrives.
func expect(t *testing.T, c *websocket.Conn, typ string) anyMsg {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		_, data, err := c.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("waiting for %q: %v", typ, err)
		}
		var m anyMsg
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		if m.Type == typ {
			return m
		}
	}
	t.Fatalf("no %q message within deadline", typ)
	return anyMsg{}
}

func expectClosed(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
	}
}

func join(t *testing.T, e *env, name string) (*websocket.Conn, string) {
	t.Helper()
	c := dial(t, e)
	send(t, c, `{"type":"join","room":"ops","pin":"1234","name":"`+name+`"}`)
	m := expect(t, c, "joined")
	return c, m.Self.ID
}

func TestFirstFrameMustBeJoin(t *testing.T) {
	e := newEnv(t, 15)
	c := dial(t, e)
	send(t, c, `{"type":"mute","muted":true}`)
	if m := expect(t, c, "error"); m.Code != CodeBadMessage {
		t.Fatalf("code = %q", m.Code)
	}
	expectClosed(t, c)
	c2 := dial(t, e)
	send(t, c2, `not json at all`)
	if m := expect(t, c2, "error"); m.Code != CodeBadMessage {
		t.Fatalf("code = %q", m.Code)
	}
	expectClosed(t, c2)
	if n := e.h.count("ops"); n != 0 {
		t.Fatalf("room count after rejected frames = %d", n)
	}
}

func TestJoinErrors(t *testing.T) {
	e := newEnv(t, 2)
	c := dial(t, e)
	send(t, c, `{"type":"join","room":"nope","pin":"1234","name":"Ann"}`)
	if m := expect(t, c, "error"); m.Code != CodeBadRoom {
		t.Fatalf("code = %q", m.Code)
	}
	expectClosed(t, c)
	c = dial(t, e)
	send(t, c, `{"type":"join","room":"ops","pin":"0000","name":"Ann"}`)
	if m := expect(t, c, "error"); m.Code != CodeBadPIN {
		t.Fatalf("code = %q", m.Code)
	}
	expectClosed(t, c)
	join(t, e, "A")
	join(t, e, "B")
	c = dial(t, e)
	send(t, c, `{"type":"join","room":"ops","pin":"1234","name":"C"}`)
	if m := expect(t, c, "error"); m.Code != CodeRoomFull {
		t.Fatalf("code = %q", m.Code)
	}
	expectClosed(t, c)
	if n := e.h.count("ops"); n != 2 {
		t.Fatalf("room count = %d, want 2", n)
	}
}

func TestJoinSendsSnapshotAndOffer(t *testing.T) {
	e := newEnv(t, 15)
	c, id := join(t, e, "Ann")
	m := expect(t, c, "participants")
	if len(m.Participants) != 1 || m.Participants[0].ID != id || m.Participants[0].Name != "Ann" {
		t.Fatalf("snapshot = %+v", m)
	}
	expect(t, c, "offer")
}

func TestMuteBroadcasts(t *testing.T) {
	e := newEnv(t, 15)
	ca, ida := join(t, e, "Ann")
	cb, _ := join(t, e, "Bob")
	send(t, ca, `{"type":"mute","muted":true}`)
	for _, c := range []*websocket.Conn{ca, cb} {
		for {
			m := expect(t, c, "participants")
			if len(m.Participants) == 2 && m.Participants[0].ID == ida && m.Participants[0].Muted {
				break
			}
		}
	}
}

func TestLateJoinerSubscribesToExistingPublishers(t *testing.T) {
	e := newEnv(t, 15)
	_, ida := join(t, e, "Ann")
	e.media.peer(ida).setTrack(t, sfu.KindAudio)
	e.h.onPublisher(e.media.peer(ida), sfu.KindAudio) // simulate first RTP from Ann
	_, idb := join(t, e, "Bob")
	if n := e.media.peer(idb).subCount(ida, sfu.KindAudio); n != 1 {
		t.Fatalf("Bob subscribed to Ann audio %d times, want 1", n)
	}
	if n := e.media.peer(ida).subCount(idb, sfu.KindAudio); n != 0 {
		t.Fatalf("Ann subscribed to Bob although Bob has no track yet: %d", n)
	}
}

func TestShareLockLifecycle(t *testing.T) {
	e := newEnv(t, 15)
	ca, ida := join(t, e, "Ann")
	cb, idb := join(t, e, "Bob")
	send(t, ca, `{"type":"share","action":"start"}`)
	for {
		if m := expect(t, cb, "participants"); m.Sharer == ida {
			break
		}
	}
	send(t, cb, `{"type":"share","action":"start"}`)
	if m := expect(t, cb, "error"); m.Code != CodeShareBusy {
		t.Fatalf("code = %q", m.Code)
	}
	// Ann's screen RTP arrives: Bob gets subscribed.
	e.media.peer(ida).setTrack(t, sfu.KindScreen)
	e.h.onPublisher(e.media.peer(ida), sfu.KindScreen)
	if n := e.media.peer(idb).subCount(ida, sfu.KindScreen); n != 1 {
		t.Fatalf("Bob screen subs = %d, want 1", n)
	}
	// Ann stops and starts again: resubscribed from the existing track (Review Focus 5).
	send(t, ca, `{"type":"share","action":"stop"}`)
	for {
		if m := expect(t, cb, "participants"); m.Sharer == "" {
			break
		}
	}
	if n := e.media.peer(idb).subCount(ida, sfu.KindScreen); n != 0 {
		t.Fatalf("Bob screen subs after stop = %d, want 0", n)
	}
	send(t, ca, `{"type":"share","action":"start"}`)
	for {
		if m := expect(t, cb, "participants"); m.Sharer == ida {
			break
		}
	}
	waitFor(t, func() bool { return e.media.peer(idb).subCount(ida, sfu.KindScreen) == 1 })
	// Ann's socket dies without a stop (Review Focus 2).
	_ = ca.CloseNow()
	for {
		m := expect(t, cb, "participants")
		if m.Sharer == "" && len(m.Participants) == 1 {
			break
		}
	}
	waitFor(t, func() bool { return e.media.peer(idb).subCount(ida, sfu.KindScreen) == 0 })
	waitFor(t, func() bool { p := e.media.peer(ida); p.mu.Lock(); defer p.mu.Unlock(); return p.closed })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// Firefox signals end-of-candidates with an empty candidate string; the session must survive it.
func TestEndOfCandidatesKeepsSession(t *testing.T) {
	e := newEnv(t, 15)
	c, id := join(t, e, "Ann")
	send(t, c, `{"type":"candidate","candidate":{"candidate":"","sdpMid":"0","sdpMLineIndex":0}}`)
	send(t, c, `{"type":"mute","muted":true}`)
	for {
		m := expect(t, c, "participants")
		if len(m.Participants) == 1 && m.Participants[0].ID == id && m.Participants[0].Muted {
			return
		}
	}
}

// Rooms created through the API after the handler started must accept joins.
func TestJoinRoomCreatedAtRuntime(t *testing.T) {
	e := newEnv(t, 15)
	if _, err := e.h.reg.Create("Retro", "4321"); err != nil {
		t.Fatal(err)
	}
	c := dial(t, e)
	send(t, c, `{"type":"join","room":"retro","pin":"4321","name":"Ann"}`)
	expect(t, c, "joined")
}

func dialWith(t *testing.T, e *env, header http.Header) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func TestEchoToggle(t *testing.T) {
	e := newEnv(t, 15)
	ca, _ := join(t, e, "Ann")
	send(t, ca, `{"type":"echo","on":true}`)
	for !expect(t, ca, "participants").Echo {
	}
	if got := e.h.reg.EchoRooms(); len(got) != 1 || got[0] != "ops" {
		t.Fatalf("EchoRooms = %v", got)
	}

	bot := dial(t, e) // loopback, no proxy header: trusted
	send(t, bot, `{"type":"join","room":"ops","name":"Echo","role":"echo"}`)
	expect(t, bot, "joined")
	for {
		m := expect(t, ca, "participants")
		if len(m.Participants) == 2 && m.Participants[1].Bot {
			break
		}
	}

	send(t, ca, `{"type":"echo","on":false}`)
	expectClosed(t, bot)
	for {
		m := expect(t, ca, "participants")
		if !m.Echo && len(m.Participants) == 1 {
			break
		}
	}
}

func TestEchoRoleRejectedThroughProxy(t *testing.T) {
	e := newEnv(t, 15)
	c := dialWith(t, e, http.Header{"X-Forwarded-For": {"10.8.0.5"}})
	send(t, c, `{"type":"join","room":"ops","name":"Echo","role":"echo"}`)
	if m := expect(t, c, "error"); m.Code != CodeBadMessage {
		t.Fatalf("code = %q", m.Code)
	}
	if n := e.h.count("ops"); n != 0 {
		t.Fatalf("room count = %d", n)
	}
}

func TestLastPersonLeavingDropsEcho(t *testing.T) {
	e := newEnv(t, 15)
	ca, _ := join(t, e, "Ann")
	send(t, ca, `{"type":"echo","on":true}`)
	for !expect(t, ca, "participants").Echo {
	}
	bot := dial(t, e)
	send(t, bot, `{"type":"join","room":"ops","name":"Echo","role":"echo"}`)
	expect(t, bot, "joined")
	_ = ca.Close(websocket.StatusNormalClosure, "")
	expectClosed(t, bot)
}

func TestEchoBotNeedsARequest(t *testing.T) {
	e := newEnv(t, 15)
	join(t, e, "Ann")
	bot := dial(t, e)
	send(t, bot, `{"type":"join","room":"ops","name":"Echo","role":"echo"}`)
	if m := expect(t, bot, "error"); m.Code != CodeBadRoom {
		t.Fatalf("code = %q", m.Code)
	}
}
