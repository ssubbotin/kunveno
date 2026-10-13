package signal

import (
	"context"
	"encoding/json"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/ssubbotin/kunveno/internal/metrics"
	"github.com/ssubbotin/kunveno/internal/quality"
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
	id       string
	sig      sfu.Signaler
	m        *fakeMedia
	mu       sync.Mutex
	tracks   map[sfu.Kind]*webrtc.TrackLocalStaticRTP
	subs     map[string]int // "from/kind" -> subscribe count minus unsubscribe count
	tcpOnly  bool
	sample   quality.Sample
	sampleOK bool
	closed   bool
	offers   int
}

func newFakeMedia() *fakeMedia { return &fakeMedia{peers: map[string]*fakePeer{}} }

func (m *fakeMedia) NewPeer(id string, sig sfu.Signaler, h sfu.Handlers) (MediaPeer, error) {
	p := &fakePeer{id: id, sig: sig, m: m, tcpOnly: h.TCPOnly, tracks: map[sfu.Kind]*webrtc.TrackLocalStaticRTP{}, subs: map[string]int{}}
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
func (p *fakePeer) HandleAnswer(string) error { return nil }

func (p *fakePeer) Sample() (quality.Sample, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sample, p.sampleOK
}

func (p *fakePeer) setSample(s quality.Sample) {
	p.mu.Lock()
	p.sample, p.sampleOK = s, true
	p.mu.Unlock()
}
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
	if k != sfu.KindAudio {
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
	h.qualityEvery = 20 * time.Millisecond
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
	RetryAfter   int               `json:"retry_after"`
	From         string            `json:"from"`
	Emoji        string            `json:"emoji"`
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
	if _, err := e.h.reg.Create("Retro", "432100"); err != nil {
		t.Fatal(err)
	}
	c := dial(t, e)
	send(t, c, `{"type":"join","room":"retro","pin":"432100","name":"Ann"}`)
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

func TestSignalingMetrics(t *testing.T) {
	val := func(c prometheus.Collector) float64 { return testutil.ToFloat64(c) }
	okBefore := val(metrics.Joins.WithLabelValues("metrics", "ok"))
	badPinBefore := val(metrics.Joins.WithLabelValues("metrics", CodeBadPIN))
	muteBefore := val(metrics.Messages.WithLabelValues("mute"))
	badBefore := val(metrics.BadMessages)
	leavesBefore := val(metrics.Leaves.WithLabelValues("metrics"))

	// A room of its own: sessions of earlier tests leave "ops" asynchronously and would be counted here.
	e := newEnv(t, 15)
	e.h.reg.Add(room.New("metrics", "Metrics", "1234", 15))
	c := dial(t, e)
	send(t, c, `{"type":"join","room":"metrics","pin":"0000","name":"Ann"}`)
	expect(t, c, "error")
	ca := dial(t, e)
	send(t, ca, `{"type":"join","room":"metrics","pin":"1234","name":"Ann"}`)
	expect(t, ca, "joined")
	send(t, ca, `{"type":"mute","muted":true}`)
	for !expectMuted(t, ca) {
	}
	send(t, ca, `{"type":"dance"}`)
	expectClosed(t, ca)
	deadline := time.Now().Add(3 * time.Second)
	for val(metrics.Leaves.WithLabelValues("metrics")) == leavesBefore && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	for name, got := range map[string]float64{
		"joins ok":      val(metrics.Joins.WithLabelValues("metrics", "ok")) - okBefore,
		"joins bad_pin": val(metrics.Joins.WithLabelValues("metrics", CodeBadPIN)) - badPinBefore,
		"mute messages": val(metrics.Messages.WithLabelValues("mute")) - muteBefore,
		"bad messages":  val(metrics.BadMessages) - badBefore,
		"leaves":        val(metrics.Leaves.WithLabelValues("metrics")) - leavesBefore,
	} {
		if got != 1 {
			t.Errorf("%s: delta %v, want 1", name, got)
		}
	}
}

func expectMuted(t *testing.T, c *websocket.Conn) bool {
	m := expect(t, c, "participants")
	return len(m.Participants) == 1 && m.Participants[0].Muted
}

func TestJoinRequestingTCPMakesATCPOnlyPeer(t *testing.T) {
	e := newEnv(t, 15)
	c := dial(t, e)
	send(t, c, `{"type":"join","room":"ops","pin":"1234","name":"Ann","media":"tcp"}`)
	m := expect(t, c, "joined")
	if !e.media.peer(m.Self.ID).tcpOnly {
		t.Fatal("peer not TCP-only")
	}
	_, id := join(t, e, "Bob")
	if e.media.peer(id).tcpOnly {
		t.Fatal("default peer is TCP-only")
	}
}

func TestQualityGradeReachesTheRoom(t *testing.T) {
	rttBefore := histogramCount(t, metrics.RTTSeconds)
	e := newEnv(t, 15)
	ca, ida := join(t, e, "Ann")
	e.media.peer(ida).setSample(quality.Sample{UpLoss: 0.1, RTT: 80 * time.Millisecond})
	for {
		m := expect(t, ca, "participants")
		if len(m.Participants) == 1 && m.Participants[0].Quality == quality.Poor {
			break
		}
	}
	if histogramCount(t, metrics.RTTSeconds) <= rttBefore {
		t.Error("rtt not observed")
	}
}

func histogramCount(t *testing.T, c prometheus.Collector) uint64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 1)
	c.Collect(ch)
	var m dto.Metric
	if err := (<-ch).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram().GetSampleCount()
}

func TestPinGuessingIsBlocked(t *testing.T) {
	failsBefore := testutil.ToFloat64(metrics.PINFailures.WithLabelValues("join"))
	e := newEnv(t, 15)
	attacker := http.Header{"X-Forwarded-For": {"10.0.0.66"}}
	for i := 0; i < 5; i++ {
		c := dialWith(t, e, attacker)
		send(t, c, `{"type":"join","room":"ops","pin":"0000","name":"Eve"}`)
		if m := expect(t, c, "error"); m.Code != CodeBadPIN {
			t.Fatalf("attempt %d: code %q", i, m.Code)
		}
	}
	c := dialWith(t, e, attacker)
	send(t, c, `{"type":"join","room":"ops","pin":"1234","name":"Eve"}`)
	m := expect(t, c, "error")
	if m.Code != CodeTooManyAttempts || m.RetryAfter < 1 {
		t.Fatalf("blocked attempt: code %q retry %d", m.Code, m.RetryAfter)
	}
	other := dialWith(t, e, http.Header{"X-Forwarded-For": {"10.0.0.7"}})
	send(t, other, `{"type":"join","room":"ops","pin":"1234","name":"Ann"}`)
	expect(t, other, "joined")
	if d := testutil.ToFloat64(metrics.PINFailures.WithLabelValues("join")) - failsBefore; d != 5 {
		t.Errorf("pin failures counted %v, want 5", d)
	}
}

func TestCameraFlagReachesTheRoom(t *testing.T) {
	e := newEnv(t, 15)
	ca, ida := join(t, e, "Ann")
	send(t, ca, `{"type":"camera","on":true}`)
	for {
		m := expect(t, ca, "participants")
		if len(m.Participants) == 1 && m.Participants[0].ID == ida && m.Participants[0].Camera {
			break
		}
	}
}

func TestCamerasFollowTheReceivePreference(t *testing.T) {
	e := newEnv(t, 15)
	_, ida := join(t, e, "Ann")
	e.media.peer(ida).setTrack(t, sfu.KindCamera)
	e.h.onPublisher(e.media.peer(ida), sfu.KindCamera)

	cb, idb := join(t, e, "Bob") // late joiner gets the camera that is already on
	if n := e.media.peer(idb).subCount(ida, sfu.KindCamera); n != 1 {
		t.Fatalf("late joiner camera subscriptions = %d, want 1", n)
	}
	send(t, cb, `{"type":"video","receive":false}`)
	waitFor(t, func() bool { return e.media.peer(idb).subCount(ida, sfu.KindCamera) == 0 })

	_, idc := join(t, e, "Cid")
	e.media.peer(idc).setTrack(t, sfu.KindCamera)
	e.h.onPublisher(e.media.peer(idc), sfu.KindCamera) // a new camera while Bob declines video
	if n := e.media.peer(idb).subCount(idc, sfu.KindCamera); n != 0 {
		t.Fatalf("Bob got a new camera while declining video: %d", n)
	}
	send(t, cb, `{"type":"video","receive":true}`)
	waitFor(t, func() bool {
		return e.media.peer(idb).subCount(ida, sfu.KindCamera) == 1 && e.media.peer(idb).subCount(idc, sfu.KindCamera) == 1
	})
}

// A join is counted before the client hears "joined"; otherwise the count lags behind what the
// client (and the next test) already saw.
func TestJoinCountedBeforeJoinedIsSent(t *testing.T) {
	e := newEnv(t, 60)
	ok := metrics.Joins.WithLabelValues("ops", "ok")
	for i := 0; i < 50; i++ {
		before := testutil.ToFloat64(ok)
		join(t, e, "Ann")
		if d := testutil.ToFloat64(ok) - before; d != 1 {
			t.Fatalf("join %d: counter moved by %v when joined arrived, want 1", i, d)
		}
	}
}

func TestPlayerJoinsOnlyItsRoomAndStaysWhenEchoGoesOff(t *testing.T) {
	e := newEnv(t, 15)
	bot := dial(t, e)
	send(t, bot, `{"type":"join","room":"ops","name":"Видео","role":"player"}`)
	if m := expect(t, bot, "error"); m.Code != CodeBadRoom {
		t.Fatalf("player in a room without one: %q", m.Code)
	}

	r, _ := e.h.reg.Get("ops")
	r.SetPlayer(true)
	player := dial(t, e)
	send(t, player, `{"type":"join","room":"ops","name":"Видео","role":"player"}`)
	expect(t, player, "joined")

	ca, _ := join(t, e, "Ann")
	send(t, ca, `{"type":"echo","on":true}`)
	echo := dial(t, e)
	send(t, echo, `{"type":"join","room":"ops","name":"Echo","role":"echo"}`)
	expect(t, echo, "joined")
	send(t, ca, `{"type":"echo","on":false}`)
	expectClosed(t, echo)
	for {
		m := expect(t, ca, "participants")
		if !m.Echo && len(m.Participants) == 2 {
			if m.Participants[0].BotKind != "player" {
				t.Fatalf("first participant = %+v, want the player", m.Participants[0])
			}
			break
		}
	}
}

// A page that ends up with two connections (a client bug, a racing reconnect) keeps one
// participant: a join with the same client id replaces the earlier session.
func TestSameClientReplacesItsEarlierSession(t *testing.T) {
	e := newEnv(t, 15)
	first := dial(t, e)
	send(t, first, `{"type":"join","room":"ops","pin":"1234","name":"Вероника","client":"page-abc123"}`)
	expect(t, first, "joined")
	second := dial(t, e)
	send(t, second, `{"type":"join","room":"ops","pin":"1234","name":"Вероника","client":"page-abc123"}`)
	expect(t, second, "joined")
	expectClosed(t, first)
	waitFor(t, func() bool { return e.h.count("ops") == 1 })
	for {
		m := expect(t, second, "participants")
		if len(m.Participants) == 1 {
			break
		}
	}
	// Another page with its own id is a different participant.
	other := dial(t, e)
	send(t, other, `{"type":"join","room":"ops","pin":"1234","name":"Вероника","client":"page-zzz999"}`)
	expect(t, other, "joined")
	waitFor(t, func() bool { return e.h.count("ops") == 2 })
}

func TestReactionReachesEveryoneAndIsRateLimited(t *testing.T) {
	e := newEnv(t, 15)
	ca, ida := join(t, e, "Ann")
	cb, _ := join(t, e, "Bob")
	send(t, ca, `{"type":"react","emoji":"❤️"}`)
	for _, c := range []*websocket.Conn{ca, cb} {
		if m := expect(t, c, "reaction"); m.From != ida || m.Emoji != "❤️" {
			t.Fatalf("reaction = %+v", m)
		}
	}
	// A burst after the limit has passed: only its first gets through, then one more later.
	time.Sleep(800 * time.Millisecond)
	send(t, ca, `{"type":"react","emoji":"👍"}`)
	send(t, ca, `{"type":"react","emoji":"🎉"}`)
	if m := expect(t, cb, "reaction"); m.Emoji != "👍" {
		t.Fatalf("first of the burst = %q", m.Emoji)
	}
	time.Sleep(800 * time.Millisecond)
	send(t, ca, `{"type":"react","emoji":"👏"}`)
	if m := expect(t, cb, "reaction"); m.Emoji != "👏" {
		t.Fatalf("after the limit = %q, want 👏 (🎉 must have been dropped)", m.Emoji)
	}
}

// The reaction set comes from the configuration; anything outside it (say, from a page opened
// before the set changed) is ignored without dropping the connection.
func TestReactionsOutsideTheConfiguredSetAreIgnored(t *testing.T) {
	e := newEnv(t, 15)
	e.h.SetReactions([]string{"🚀"})
	ca, _ := join(t, e, "Ann")
	cb, _ := join(t, e, "Bob")
	send(t, ca, `{"type":"react","emoji":"👍"}`)
	time.Sleep(800 * time.Millisecond)
	send(t, ca, `{"type":"react","emoji":"🚀"}`)
	if m := expect(t, cb, "reaction"); m.Emoji != "🚀" {
		t.Fatalf("reaction = %q, want 🚀 only", m.Emoji)
	}
}

// A live camera joins with its own role, so the client can label it apart from a looped file;
// it needs a room with a player configured, like the player.
func TestCameraRoleNeedsAPlayerRoom(t *testing.T) {
	e := newEnv(t, 15)
	c := dial(t, e)
	send(t, c, `{"type":"join","room":"ops","name":"Двор","role":"camera"}`)
	if m := expect(t, c, "error"); m.Code != CodeBadRoom {
		t.Fatalf("camera in a room without a player: %q", m.Code)
	}
	r, _ := e.h.reg.Get("ops")
	r.SetPlayer(true)
	cam := dial(t, e)
	send(t, cam, `{"type":"join","room":"ops","name":"Двор","role":"camera"}`)
	expect(t, cam, "joined")
	ca, _ := join(t, e, "Ann")
	for {
		m := expect(t, ca, "participants")
		if len(m.Participants) == 2 {
			if m.Participants[0].BotKind != "camera" {
				t.Fatalf("camera participant = %+v", m.Participants[0])
			}
			break
		}
	}
}
