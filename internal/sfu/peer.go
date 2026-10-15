package sfu

import (
	"errors"
	"fmt"
	"github.com/ssubbotin/kunveno/internal/metrics"
	"github.com/ssubbotin/kunveno/internal/quality"
	"io"
	"log/slog"
	"math/bits"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"

	"github.com/ssubbotin/kunveno/internal/media"
)

// source is what a peer subscribes to: another participant's Peer, or a test's stand-in. The
// coordinator hands it over as a media.Source.
type source interface {
	media.Source
	Track(media.Kind) *webrtc.TrackLocalStaticRTP
	RequestKeyframe(media.Kind)
}

// Peer is one participant's connection to the SFU.
type Peer struct {
	created     time.Time
	upTransport string // transport counted in ActiveConnections while connected; guarded by mu
	prevLost    int64  // cumulative uplink counters at the previous Sample; guarded by mu
	prevRecv    int64
	audioLoss   lossWindow    // downlink loss of the audio we send; guarded by mu
	videoLoss   lossWindow    // downlink loss of the screen and cameras we send; guarded by mu
	rtt         time.Duration // latest round trip from its receiver reports; guarded by mu
	id          string
	pc          *webrtc.PeerConnection
	sig         media.Signaler
	h           media.Handlers
	log         *slog.Logger

	mu          sync.Mutex
	negotiating bool
	dirty       bool
	closed      bool
	out         [numKinds]*webrtc.TrackLocalStaticRTP
	outSSRC     [numKinds]uint32
	subs        map[string]*webrtc.RTPSender
	pendingPLI  []pendingKeyframe
}

func (e *Engine) NewPeer(id string, sig media.Signaler, h media.Handlers) (*Peer, error) {
	// One API per peer: pion takes its loggers from the API, and this way they carry the peer id.
	// Each PeerConnection copies the media engine and builds its own interceptors anyway.
	se := e.se
	se.LoggerFactory = newPionLogs(e.log.With("peer", id))
	api := webrtc.NewAPI(webrtc.WithMediaEngine(e.media), webrtc.WithInterceptorRegistry(e.reg), webrtc.WithSettingEngine(se))
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	p := &Peer{created: time.Now(), id: id, pc: pc, sig: sig, h: h, log: e.log.With("peer", id), subs: map[string]*webrtc.RTPSender{}}
	// Receive slots in mid order: audio, screen, camera (see kindForMid).
	for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeAudio, webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeVideo} {
		if _, err := pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			_ = pc.Close()
			return nil, err
		}
	}
	pc.OnTrack(p.onTrack)
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		p.log.Info("connection state", "state", s.String())
		metrics.PeerStates.WithLabelValues(s.String()).Inc()
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed {
			p.mu.Lock()
			up := p.upTransport
			p.upTransport = ""
			p.mu.Unlock()
			if up != "" {
				metrics.ActiveConnections.WithLabelValues(up).Dec()
			}
		}
		if s == webrtc.PeerConnectionStateConnected {
			if pair := p.selectedPair(); pair != nil {
				transport := pair.Remote.Protocol.String()
				p.mu.Lock()
				first := p.upTransport == ""
				if first {
					p.upTransport = transport
				}
				p.mu.Unlock()
				if first {
					metrics.ActiveConnections.WithLabelValues(transport).Inc()
					metrics.ConnectSeconds.WithLabelValues(transport).Observe(time.Since(p.created).Seconds())
				}
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

// Publishes reports whether this peer's track of kind has arrived.
func (p *Peer) Publishes(kind media.Kind) bool { return p.Track(kind) != nil }

func (p *Peer) Track(kind media.Kind) *webrtc.TrackLocalStaticRTP {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out[kind]
}

// RequestKeyframe asks this peer's browser for a keyframe on its published track.
func (p *Peer) RequestKeyframe(kind media.Kind) {
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
	metrics.KeyframeRequests.WithLabelValues(kind.String()).Inc()
}

func (p *Peer) onTrack(remote *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
	kind, ok := p.kindOf(receiver)
	if !ok {
		p.log.Warn("track on an unknown slot; ignored", "ssrc", remote.SSRC())
		return
	}
	// The camera gets its own stream id so browsers can tell it from the screen.
	stream := p.id
	if kind == media.KindCamera {
		stream = p.id + "/camera"
	}
	local, err := webrtc.NewTrackLocalStaticRTP(remote.Codec().RTPCodecCapability, kind.String(), stream)
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

func subKey(from media.Source, kind media.Kind) string { return from.ID() + "/" + kind.String() }

// errNotPublisher is returned when subscribing to a source this engine did not create.
var errNotPublisher = errors.New("source is not a peer of this engine")

// Subscribe attaches from's track of the given kind to this peer and renegotiates.
func (p *Peer) Subscribe(src media.Source, kind media.Kind) error {
	from, ok := src.(source)
	if !ok {
		return errNotPublisher
	}
	track := from.Track(kind)
	if track == nil {
		return media.ErrNoTrack
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
	if kind != media.KindAudio {
		p.pendingPLI = append(p.pendingPLI, pendingKeyframe{from, kind})
	}
	p.mu.Unlock()
	go p.readRTCP(sender, from, kind)
	p.Negotiate()
	return nil
}

// Unsubscribe detaches from's track; a no-op when not subscribed.
func (p *Peer) Unsubscribe(from media.Source, kind media.Kind) error {
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

func (p *Peer) readRTCP(sender *webrtc.RTPSender, from source, kind media.Kind) {
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		for _, pkt := range pkts {
			switch v := pkt.(type) {
			case *rtcp.PictureLossIndication:
				metrics.ViewerPLIs.WithLabelValues(kind.String()).Inc()
				if kind != media.KindAudio {
					from.RequestKeyframe(kind)
				}
			case *rtcp.TransportLayerNack:
				metrics.NACKedPackets.WithLabelValues(kind.String()).Add(float64(nackedPackets(v)))
			case *rtcp.ReceiverReport:
				p.noteReport(kind, v)
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
	for _, k := range pending {
		k.src.RequestKeyframe(k.kind)
	}
	if dirty {
		p.Negotiate()
	}
	return nil
}

// AddICECandidate adds a candidate the client sent.
func (p *Peer) AddICECandidate(c media.Candidate) error {
	return p.pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: c.Candidate, SDPMid: c.SDPMid, SDPMLineIndex: c.SDPMLineIndex, UsernameFragment: c.UsernameFragment})
}

func (p *Peer) fail(err error) {
	p.mu.Lock()
	p.negotiating = false
	p.mu.Unlock()
	if p.pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
		// The participant is leaving and closed its connection while others were leaving too;
		// the session ends through its socket. Not a failure.
		p.log.Debug("negotiation on a closed connection skipped", "err", err)
		return
	}
	p.log.Error("negotiation failed", "err", err)
	metrics.NegotiationErrors.Inc()
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

// Sample measures this participant's connection from the peer connection statistics. Uplink
// loss covers the time since the previous call. ok is false until there is anything to measure.
func (p *Peer) Sample() (s quality.Sample, ok bool) {
	var lost, received int64
	inbound := func(v webrtc.InboundRTPStreamStats) {
		if v.Kind != "audio" {
			return
		}
		ok = true
		lost += int64(v.PacketsLost)
		received += int64(v.PacketsReceived)
		s.Jitter = max(s.Jitter, seconds(v.Jitter))
	}
	// pion v4.2 does not report remote-inbound stats; kept in case a later version does.
	remote := func(v webrtc.RemoteInboundRTPStreamStats) {
		if v.Kind != "audio" {
			return
		}
		ok = true
		s.DownLoss = max(s.DownLoss, v.FractionLost)
		s.RTT = max(s.RTT, seconds(v.RoundTripTime))
	}
	for _, st := range p.pc.GetStats() {
		switch v := st.(type) {
		case webrtc.InboundRTPStreamStats:
			inbound(v)
		case *webrtc.InboundRTPStreamStats:
			inbound(*v)
		case webrtc.RemoteInboundRTPStreamStats:
			remote(v)
		case *webrtc.RemoteInboundRTPStreamStats:
			remote(*v)
		}
	}
	p.mu.Lock()
	dLost, dRecv := lost-p.prevLost, received-p.prevRecv
	p.prevLost, p.prevRecv = lost, received
	if p.rtt > 0 {
		ok = true
		s.RTT = max(s.RTT, p.rtt)
	}
	p.mu.Unlock()
	s.DownLoss = max(s.DownLoss, p.takeDownLoss())
	s.VideoLoss, s.HasVideo = p.takeVideoLoss()
	if dLost > 0 && dLost+dRecv > 0 {
		s.UpLoss = float64(dLost) / float64(dLost+dRecv)
	}
	return s, ok
}

func seconds(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// noteReceiverReport records what the participant says about the audio we send it.
func (p *Peer) noteReceiverReport(rr *rtcp.ReceiverReport) { p.noteReport(media.KindAudio, rr) }

// noteReport records what the participant says about a stream we send it: the round trip from
// audio reports, and loss for audio and video in separate windows.
func (p *Peer) noteReport(kind media.Kind, rr *rtcp.ReceiverReport) {
	now := compactNTP(time.Now())
	p.mu.Lock()
	defer p.mu.Unlock()
	w := &p.videoLoss
	if kind == media.KindAudio {
		w = &p.audioLoss
	}
	for _, r := range rr.Reports {
		if kind == media.KindAudio {
			if rtt := rttFromReport(now, r.LastSenderReport, r.Delay); rtt > 0 {
				p.rtt = rtt
			}
		}
		w.note(r)
	}
}

// takeDownLoss returns the audio downlink loss ratio since the previous call and starts a new window.
func (p *Peer) takeDownLoss() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.audioLoss.take()
}

// takeVideoLoss is takeDownLoss for the video we send; ok is false when the participant got no
// video in this window, so idle viewers do not dilute the loss figures.
func (p *Peer) takeVideoLoss() (float64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	had := p.videoLoss.expected > 0
	return p.videoLoss.take(), had
}

// lossWindow accumulates loss from the cumulative counters of receiver reports.
type lossWindow struct {
	last           map[uint32][2]uint32 // per SSRC: highest sequence and total lost of the last report
	expected, lost int64
}

func (w *lossWindow) note(r rtcp.ReceptionReport) {
	if w.last == nil {
		w.last = map[uint32][2]uint32{}
	}
	if prev, seen := w.last[r.SSRC]; seen && r.LastSequenceNumber > prev[0] {
		w.expected += int64(r.LastSequenceNumber - prev[0])
		if r.TotalLost > prev[1] {
			w.lost += int64(r.TotalLost - prev[1])
		}
	}
	w.last[r.SSRC] = [2]uint32{r.LastSequenceNumber, r.TotalLost}
}

func (w *lossWindow) take() float64 {
	exp, lost := w.expected, w.lost
	w.expected, w.lost = 0, 0
	if exp <= 0 || lost <= 0 {
		return 0
	}
	return min(1, float64(lost)/float64(exp))
}

// nackedPackets counts the packets a NACK asks to resend: each pair names one packet and a
// bitmask of up to 16 more.
func nackedPackets(n *rtcp.TransportLayerNack) int {
	total := 0
	for _, pair := range n.Nacks {
		total += 1 + bits.OnesCount16(uint16(pair.LostPackets))
	}
	return total
}

// rttFromReport applies RFC 3550 6.4.1: round trip = arrival - LSR - DLSR, all in compact NTP
// (1/65536 s). Zero when no sender report was echoed yet or the result is not positive.
func rttFromReport(nowCompact, lsr, dlsr uint32) time.Duration {
	if lsr == 0 {
		return 0
	}
	d := int64(nowCompact) - int64(lsr) - int64(dlsr)
	if d <= 0 {
		return 0
	}
	return time.Duration(d * int64(time.Second) / 65536)
}

// compactNTP is the middle 32 bits of the NTP timestamp of t.
func compactNTP(t time.Time) uint32 {
	const ntpEpochOffset = 2208988800
	secs := uint64(t.Unix()) + ntpEpochOffset
	frac := uint64(t.Nanosecond()) << 32 / 1e9
	return uint32((secs<<32 | frac) >> 16)
}

// pendingKeyframe is a video subscription waiting for the answer that activates it.
type pendingKeyframe struct {
	src  source
	kind media.Kind
}

// kindOf finds which fixed slot a remote track arrived on.
func (p *Peer) kindOf(r *webrtc.RTPReceiver) (media.Kind, bool) {
	for _, tr := range p.pc.GetTransceivers() {
		if tr.Receiver() == r {
			return kindForMid(tr.Mid())
		}
	}
	return 0, false
}
