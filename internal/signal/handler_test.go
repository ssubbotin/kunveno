package signal

import (
	"context"
	"encoding/json"
	"log/slog"
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
	h := New(reg, media, nil, slog.New(slog.NewTextHandler(testWriter{t}, nil)))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &env{srv: srv, media: media, h: h}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(b)))
	return len(b), nil
}

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
