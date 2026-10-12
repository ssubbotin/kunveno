package sfu

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

func TestKindForMid(t *testing.T) {
	for mid, want := range map[string]Kind{"0": KindAudio, "1": KindScreen, "2": KindCamera} {
		if got, ok := kindForMid(mid); !ok || got != want {
			t.Errorf("mid %s: %v %v, want %v", mid, got, ok, want)
		}
	}
	if _, ok := kindForMid("7"); ok {
		t.Error("unknown mid accepted")
	}
	if KindCamera.String() != "camera" {
		t.Errorf("KindCamera = %q", KindCamera.String())
	}
}

func TestPeerHasThreeReceiveSlots(t *testing.T) {
	eng, err := NewEngine(EngineConfig{UDPPort: 0, AdvertiseIP: "127.0.0.1", IncludeLoopback: true}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	p, err := eng.NewPeer("x", &chanSignaler{offers: make(chan string, 1)}, Handlers{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	trs := p.pc.GetTransceivers()
	if len(trs) != 3 || trs[0].Kind() != webrtc.RTPCodecTypeAudio || trs[1].Kind() != webrtc.RTPCodecTypeVideo || trs[2].Kind() != webrtc.RTPCodecTypeVideo {
		t.Fatalf("transceivers: %d", len(trs))
	}
}

// A camera sent on the third slot is published as a camera and reaches another participant
// with the stream id "<publisher>/camera".
func TestCameraFlowsBetweenTwoPeers(t *testing.T) {
	eng, err := NewEngine(EngineConfig{UDPPort: 0, AdvertiseIP: "127.0.0.1", IncludeLoopback: true}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	sigA, sigB := &chanSignaler{offers: make(chan string, 8)}, &chanSignaler{offers: make(chan string, 8)}
	var peerA, peerB *Peer
	var once sync.Once
	published := make(chan struct{})
	peerA, _ = eng.NewPeer("a", sigA, Handlers{OnPublisher: func(src Source, k Kind) {
		if k == KindCamera {
			once.Do(func() { close(published) })
			_ = peerB.Subscribe(src, KindCamera)
		}
	}})
	peerB, _ = eng.NewPeer("b", sigB, Handlers{})
	defer peerA.Close()
	defer peerB.Close()

	browserA, browserB := newBrowser(t), newBrowser(t)
	screen, _ := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "screen", "a")
	cam, _ := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "camera", "a")
	gotTrack := make(chan *webrtc.TrackRemote, 1)
	browserB.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) { gotTrack <- tr })

	// Browser A fills its slots in order: mid 1 (screen, silent here), mid 2 (camera).
	go func() {
		first := true
		for sdp := range sigA.offers {
			_ = browserA.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp})
			if first {
				_, _ = browserA.AddTrack(screen)
				_, _ = browserA.AddTrack(cam)
				first = false
			}
			answer, _ := browserA.CreateAnswer(nil)
			g := webrtc.GatheringCompletePromise(browserA)
			_ = browserA.SetLocalDescription(answer)
			<-g
			_ = peerA.HandleAnswer(browserA.LocalDescription().SDP)
		}
	}()
	done := make(chan struct{})
	defer close(done)
	go answerLoop(t, browserB, sigB.offers, peerB, done)
	peerA.Negotiate()
	peerB.Negotiate()
	go func() {
		tick := time.NewTicker(33 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				_ = cam.WriteSample(media.Sample{Data: []byte{0x10, 0x02, 0x00, 0x9d, 0x01, 0x2a}, Duration: 33 * time.Millisecond})
				_ = screen.WriteSample(media.Sample{Data: []byte{0x10, 0x02, 0x00, 0x9d, 0x01, 0x2a}, Duration: 33 * time.Millisecond})
			}
		}
	}()
	select {
	case <-published:
	case <-time.After(10 * time.Second):
		t.Fatal("camera never published")
	}
	// Both slots carry VP8; only the slot tells them apart.
	deadline := time.Now().Add(5 * time.Second)
	for peerA.Track(KindScreen) == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if peerA.Track(KindScreen) == nil || peerA.Track(KindCamera) == nil || peerA.Track(KindScreen) == peerA.Track(KindCamera) {
		t.Error("screen (mid 1) and camera (mid 2) were not published as two separate tracks")
	}
	select {
	case tr := <-gotTrack:
		if tr.StreamID() != "a/camera" || tr.Kind() != webrtc.RTPCodecTypeVideo {
			t.Fatalf("track %s kind %s, want stream a/camera video", tr.StreamID(), tr.Kind())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("browser B never received A's camera")
	}
}
