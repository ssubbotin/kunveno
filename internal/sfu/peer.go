package sfu

import (
	"errors"
	"fmt"
	"github.com/ssubbotin/kunveno/internal/metrics"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// ErrNoTrack is returned when subscribing to a source that has not published yet.
var ErrNoTrack = errors.New("source has no track of that kind")

// Signaler delivers server offers to one client.
type Signaler interface {
	SendOffer(sdp string) error
}

// Source is a publisher another peer can subscribe to.
type Source interface {
	ID() string
	Track(Kind) *webrtc.TrackLocalStaticRTP
	RequestKeyframe(Kind)
}

// Handlers are callbacks into the coordinator (the signal package).
type Handlers struct {
	OnPublisher func(Source, Kind) // first RTP of that kind arrived; the track is ready
	OnFailed    func(Source)       // the connection failed or closed unexpectedly
	// TCPOnly strips UDP candidates from this peer's offers, for a participant whose UDP path
	// failed: the browser then has only the ICE-TCP candidate to connect to.
	TCPOnly bool
}

// Peer is one participant's connection to the SFU.
type Peer struct {
	id  string
	pc  *webrtc.PeerConnection
	sig Signaler
	h   Handlers
	log *slog.Logger

	mu          sync.Mutex
	negotiating bool
	dirty       bool
	closed      bool
	out         [2]*webrtc.TrackLocalStaticRTP
	outSSRC     [2]uint32
	subs        map[string]*webrtc.RTPSender
	pendingPLI  []Source
}

func (e *Engine) NewPeer(id string, sig Signaler, h Handlers) (*Peer, error) {
	pc, err := e.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	p := &Peer{id: id, pc: pc, sig: sig, h: h, log: e.log.With("peer", id), subs: map[string]*webrtc.RTPSender{}}
	for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeAudio, webrtc.RTPCodecTypeVideo} {
		if _, err := pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			_ = pc.Close()
			return nil, err
		}
	}
	pc.OnTrack(p.onTrack)
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		p.log.Info("connection state", "state", s.String())
		metrics.PeerStates.WithLabelValues(s.String()).Inc()
		if s == webrtc.PeerConnectionStateConnected {
			if pair := p.selectedPair(); pair != nil {
				transport := pair.Remote.Protocol.String()
				p.log.Info("media path", "transport", transport,
					"remote", fmt.Sprintf("%s:%d", pair.Remote.Address, pair.Remote.Port), "remote_type", pair.Remote.Typ.String())
				metrics.Transports.WithLabelValues(transport).Inc()
			}
		}
		if s == webrtc.PeerConnectionStateFailed {
			if p.h.OnFailed != nil {
				p.h.OnFailed(p)
			}
		}
	})
	return p, nil
}

func (p *Peer) ID() string { return p.id }

func (p *Peer) Track(kind Kind) *webrtc.TrackLocalStaticRTP {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out[kind]
}

// RequestKeyframe asks this peer's browser for a keyframe on its published track.
func (p *Peer) RequestKeyframe(kind Kind) {
	p.mu.Lock()
	ssrc := p.outSSRC[kind]
	p.mu.Unlock()
	if ssrc == 0 {
		return
	}
	if err := p.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: ssrc}}); err != nil {
		p.log.Debug("pli", "err", err)
		return
	}
	metrics.KeyframeRequests.Inc()
}

func (p *Peer) onTrack(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
	kind := KindAudio
	if remote.Kind() == webrtc.RTPCodecTypeVideo {
		kind = KindScreen
	}
	local, err := webrtc.NewTrackLocalStaticRTP(remote.Codec().RTPCodecCapability, kind.String(), p.id)
	if err != nil {
		p.log.Error("new local track", "err", err)
		return
	}
	p.mu.Lock()
	p.out[kind] = local
	p.outSSRC[kind] = uint32(remote.SSRC())
	p.mu.Unlock()
	p.log.Info("publishing", "kind", kind.String(), "ssrc", remote.SSRC())
	if p.h.OnPublisher != nil {
		p.h.OnPublisher(p, kind)
	}
	packets := metrics.RTPPackets.WithLabelValues(kind.String())
	bytes := metrics.RTPBytes.WithLabelValues(kind.String())
	for {
		pkt, _, err := remote.ReadRTP()
		if err != nil {
			return
		}
		packets.Inc()
		bytes.Add(float64(len(pkt.Payload)))
		if err := local.WriteRTP(pkt); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			p.log.Debug("forward", "err", err)
		}
	}
}

func subKey(from Source, kind Kind) string { return from.ID() + "/" + kind.String() }

// Subscribe attaches from's track of the given kind to this peer and renegotiates.
func (p *Peer) Subscribe(from Source, kind Kind) error {
	track := from.Track(kind)
	if track == nil {
		return ErrNoTrack
	}
	key := subKey(from, kind)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	if _, ok := p.subs[key]; ok {
		p.mu.Unlock()
		return nil
	}
	// A fresh sendonly transceiver: AddTrack would reuse a free receive slot (mid 0 or 1),
	// which the browser keeps sendonly for its own media and never plays.
	tr, err := p.pc.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	if err != nil {
		p.mu.Unlock()
		return err
	}
	sender := tr.Sender()
	p.subs[key] = sender
	if kind == KindScreen {
		p.pendingPLI = append(p.pendingPLI, from)
	}
	p.mu.Unlock()
	go p.readRTCP(sender, from, kind)
	p.Negotiate()
	return nil
}

// Unsubscribe detaches from's track; a no-op when not subscribed.
func (p *Peer) Unsubscribe(from Source, kind Kind) error {
	key := subKey(from, kind)
	p.mu.Lock()
	sender, ok := p.subs[key]
	delete(p.subs, key)
	closed := p.closed
	p.mu.Unlock()
	if !ok || closed {
		return nil
	}
	if err := p.pc.RemoveTrack(sender); err != nil {
		return err
	}
	p.Negotiate()
	return nil
}

func (p *Peer) readRTCP(sender *webrtc.RTPSender, from Source, kind Kind) {
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		if kind != KindScreen {
			continue
		}
		for _, pkt := range pkts {
			if _, ok := pkt.(*rtcp.PictureLossIndication); ok {
				from.RequestKeyframe(kind)
			}
		}
	}
}

// Negotiate sends an offer now, or marks the peer dirty when one is outstanding.
func (p *Peer) Negotiate() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	if p.negotiating {
		p.dirty = true
		p.mu.Unlock()
		return
	}
	p.negotiating = true
	p.mu.Unlock()
	go p.sendOffer()
}

func (p *Peer) sendOffer() {
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		p.fail(err)
		return
	}
	gathered := webrtc.GatheringCompletePromise(p.pc)
	if err := p.pc.SetLocalDescription(offer); err != nil {
		p.fail(err)
		return
	}
	<-gathered
	sdp := p.pc.LocalDescription().SDP
	if p.h.TCPOnly {
		sdp = withoutUDPCandidates(sdp)
	}
	if err := p.sig.SendOffer(sdp); err != nil {
		p.fail(err)
		return
	}
	metrics.Offers.Inc()
}

// HandleAnswer applies the client's answer to the outstanding offer.
func (p *Peer) HandleAnswer(sdp string) error {
	err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp})
	p.mu.Lock()
	p.negotiating = false
	dirty := p.dirty
	p.dirty = false
	pending := p.pendingPLI
	p.pendingPLI = nil
	p.mu.Unlock()
	if err != nil {
		return err
	}
	for _, src := range pending {
		src.RequestKeyframe(KindScreen)
	}
	if dirty {
		p.Negotiate()
	}
	return nil
}

func (p *Peer) AddICECandidate(c webrtc.ICECandidateInit) error { return p.pc.AddICECandidate(c) }

func (p *Peer) fail(err error) {
	p.log.Error("negotiation failed", "err", err)
	metrics.NegotiationErrors.Inc()
	p.mu.Lock()
	p.negotiating = false
	p.mu.Unlock()
	if p.h.OnFailed != nil {
		p.h.OnFailed(p)
	}
}

func (p *Peer) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	return p.pc.Close()
}

func (p *Peer) selectedPair() *webrtc.ICECandidatePair {
	for _, tr := range p.pc.GetTransceivers() {
		if r := tr.Receiver(); r != nil && r.Transport() != nil {
			if pair, err := r.Transport().ICETransport().GetSelectedCandidatePair(); err == nil && pair != nil {
				return pair
			}
		}
	}
	return nil
}

// Transport names the protocol of the selected ICE candidate pair ("udp" or "tcp"), or "" before
// the connection is up.
func (p *Peer) Transport() string {
	if pair := p.selectedPair(); pair != nil {
		return pair.Remote.Protocol.String()
	}
	return ""
}

// withoutUDPCandidates drops a=candidate lines whose transport is UDP.
func withoutUDPCandidates(sdp string) string {
	lines := strings.Split(sdp, "\r\n")
	out := lines[:0]
	for _, l := range lines {
		if strings.HasPrefix(l, "a=candidate:") {
			if f := strings.Fields(l); len(f) > 2 && strings.EqualFold(f[2], "udp") {
				continue
			}
		}
		out = append(out, l)
	}
	return strings.Join(out, "\r\n")
}
