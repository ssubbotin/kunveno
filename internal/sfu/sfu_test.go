package sfu

import (
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/ssubbotin/kunveno/internal/metrics"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// chanSignaler delivers server offers to the test's browser stand-in.
type chanSignaler struct{ offers chan string }

func (c *chanSignaler) SendOffer(sdp string) error { c.offers <- sdp; return nil }

func newBrowser(t *testing.T) *webrtc.PeerConnection {
	t.Helper()
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		t.Fatal(err)
	}
	se := webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithSettingEngine(se)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	return pc
}

// answerLoop plays the browser: every server offer is answered with a full SDP.
func answerLoop(t *testing.T, pc *webrtc.PeerConnection, offers <-chan string, peer *Peer, done <-chan struct{}) {
	t.Helper()
	for {
		select {
		case <-done:
			return
		case sdp := <-offers:
			if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err != nil {
				t.Error(err)
				return
			}
			answer, err := pc.CreateAnswer(nil)
			if err != nil {
				t.Error(err)
				return
			}
			gathered := webrtc.GatheringCompletePromise(pc)
			if err := pc.SetLocalDescription(answer); err != nil {
				t.Error(err)
				return
			}
			<-gathered
			if err := peer.HandleAnswer(pc.LocalDescription().SDP); err != nil {
				t.Error(err)
				return
			}
		}
	}
}

func TestAudioFlowsBetweenTwoPeers(t *testing.T) {
	eng, err := NewEngine(EngineConfig{UDPPort: 0, AdvertiseIP: "127.0.0.1", IncludeLoopback: true}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	packetsBefore := testutil.ToFloat64(metrics.RTPPackets.WithLabelValues("audio"))
	offersBefore := testutil.ToFloat64(metrics.Offers)
	sigA, sigB := &chanSignaler{offers: make(chan string, 8)}, &chanSignaler{offers: make(chan string, 8)}
	var peerA, peerB *Peer
	var once sync.Once
	published := make(chan struct{})
	h := Handlers{OnPublisher: func(src Source, kind Kind) {
		if src.ID() == "a" && kind == KindAudio {
			if err := peerB.Subscribe(peerA, KindAudio); err != nil {
				t.Error(err)
			}
			once.Do(func() { close(published) })
		}
	}}
	peerA, err = eng.NewPeer("a", sigA, h)
	if err != nil {
		t.Fatal(err)
	}
	peerB, err = eng.NewPeer("b", sigB, h)
	if err != nil {
		t.Fatal(err)
	}
	defer peerA.Close()
	defer peerB.Close()

	browserA, browserB := newBrowser(t), newBrowser(t)
	mic, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "mic", "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := browserA.AddTrack(mic); err != nil {
		t.Fatal(err)
	}
	gotTrack := make(chan *webrtc.TrackRemote, 1)
	browserB.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) { gotTrack <- tr })

	done := make(chan struct{})
	defer close(done)
	go answerLoop(t, browserA, sigA.offers, peerA, done)
	go answerLoop(t, browserB, sigB.offers, peerB, done)
	peerA.Negotiate()
	peerB.Negotiate()

	// Browser A speaks: one dummy Opus frame every 20 ms.
	go func() {
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				_ = mic.WriteSample(media.Sample{Data: []byte{0xfc, 0xff, 0xfe}, Duration: 20 * time.Millisecond})
			}
		}
	}()

	select {
	case <-published:
	case <-time.After(10 * time.Second):
		t.Fatal("peer A audio never reached the server")
	}
	select {
	case tr := <-gotTrack:
		if tr.StreamID() != "a" || tr.ID() != "audio" {
			t.Fatalf("track named %s/%s, want a/audio", tr.StreamID(), tr.ID())
		}
		if _, _, err := tr.ReadRTP(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("browser B never received A's audio")
	}
	if got := testutil.ToFloat64(metrics.RTPPackets.WithLabelValues("audio")) - packetsBefore; got < 1 {
		t.Errorf("rtp audio packets counted = %v", got)
	}
	if got := testutil.ToFloat64(metrics.Offers) - offersBefore; got < 2 {
		t.Errorf("offers counted = %v, want at least one per peer", got)
	}
}

func TestNegotiateCoalesces(t *testing.T) {
	eng, err := NewEngine(EngineConfig{UDPPort: 0, AdvertiseIP: "127.0.0.1", IncludeLoopback: true}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	sig := &chanSignaler{offers: make(chan string, 8)}
	p, err := eng.NewPeer("x", sig, Handlers{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	browser := newBrowser(t)

	p.Negotiate()
	p.Negotiate()
	p.Negotiate()
	first := <-sig.offers
	select {
	case <-sig.offers:
		t.Fatal("second offer sent before the first answer")
	case <-time.After(300 * time.Millisecond):
	}
	_ = browser.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: first})
	answer, _ := browser.CreateAnswer(nil)
	gathered := webrtc.GatheringCompletePromise(browser)
	_ = browser.SetLocalDescription(answer)
	<-gathered
	if err := p.HandleAnswer(browser.LocalDescription().SDP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sig.offers: // exactly one follow-up offer for the coalesced calls
	case <-time.After(3 * time.Second):
		t.Fatal("no follow-up offer after answer although Negotiate was called while busy")
	}
	select {
	case <-sig.offers:
		t.Fatal("more than one follow-up offer")
	case <-time.After(300 * time.Millisecond):
	}
}

type staticSource struct {
	id     string
	tracks map[Kind]*webrtc.TrackLocalStaticRTP
}

func (s staticSource) ID() string                               { return s.id }
func (s staticSource) Track(k Kind) *webrtc.TrackLocalStaticRTP { return s.tracks[k] }
func (s staticSource) RequestKeyframe(Kind)                     {}

// The two receive slots (mid 0 audio, mid 1 video) carry the participant's own media; a
// forwarded track placed there reaches a browser whose slot is sendonly and is never played.
func TestSubscribeNeverUsesTheReceiveSlots(t *testing.T) {
	eng, err := NewEngine(EngineConfig{UDPPort: 0, AdvertiseIP: "127.0.0.1", IncludeLoopback: true}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	p, err := eng.NewPeer("x", &chanSignaler{offers: make(chan string, 8)}, Handlers{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	audio, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "y")
	video, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, "screen", "y")
	src := staticSource{id: "y", tracks: map[Kind]*webrtc.TrackLocalStaticRTP{KindAudio: audio, KindScreen: video}}
	if err := p.Subscribe(src, KindAudio); err != nil {
		t.Fatal(err)
	}
	if err := p.Subscribe(src, KindScreen); err != nil {
		t.Fatal(err)
	}
	trs := p.pc.GetTransceivers()
	if len(trs) != 4 {
		t.Fatalf("transceivers = %d, want 2 receive slots + 2 subscriptions", len(trs))
	}
	for i, tr := range trs[:2] {
		if tr.Sender() != nil && tr.Sender().Track() != nil {
			t.Errorf("receive slot %d carries forwarded track %q", i, tr.Sender().Track().ID())
		}
		if tr.Direction() != webrtc.RTPTransceiverDirectionRecvonly {
			t.Errorf("receive slot %d direction = %s", i, tr.Direction())
		}
	}
}
