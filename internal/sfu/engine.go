// Package sfu forwards RTP between participants with pion/webrtc. The server is
// the only offerer; clients only answer.
package sfu

import (
	"fmt"
	"log/slog"
	"net"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
)

// Kind is the media a Source publishes.
type Kind int

const (
	KindAudio Kind = iota
	KindScreen
	KindCamera
	numKinds
)

func (k Kind) String() string {
	switch k {
	case KindAudio:
		return "audio"
	case KindCamera:
		return "camera"
	}
	return "screen"
}

// kindForMid maps the participant's fixed send slots to what they carry: the server creates
// them in this order on every connection (mid 0 audio, mid 1 screen, mid 2 camera).
func kindForMid(mid string) (Kind, bool) {
	switch mid {
	case "0":
		return KindAudio, true
	case "1":
		return KindScreen, true
	case "2":
		return KindCamera, true
	}
	return 0, false
}

// EngineConfig selects the UDP port and the single advertised host address.
type EngineConfig struct {
	UDPPort int
	// TCP adds a passive ICE-TCP candidate on TCPPort, for clients whose UDP path to the
	// server is blocked or unreliable. Browsers prefer UDP and fall back to it on their own.
	TCP             bool
	TCPPort         int
	AdvertiseIP     string
	IncludeLoopback bool // tests only
}

// Engine owns the pion settings and the shared UDP socket.
type Engine struct {
	media *webrtc.MediaEngine
	reg   *interceptor.Registry
	se    webrtc.SettingEngine
	conn  *net.UDPConn
	tcp   net.Listener // nil without ICE-TCP
	log   *slog.Logger
}

func NewEngine(cfg EngineConfig, log *slog.Logger) (*Engine, error) {
	m := &webrtc.MediaEngine{}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"},
		PayloadType:        111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	fb := []webrtc.RTCPFeedback{{Type: "goog-remb"}, {Type: "ccm", Parameter: "fir"}, {Type: "nack"}, {Type: "nack", Parameter: "pli"}}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000, RTCPFeedback: fb},
		PayloadType:        96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	reg := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, reg); err != nil {
		return nil, err
	}
	reg.Add(sentCounterFactory{})
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: cfg.UDPPort})
	if err != nil {
		return nil, fmt.Errorf("listen udp %d: %w", cfg.UDPPort, err)
	}
	se := webrtc.SettingEngine{}
	se.SetLite(true)
	se.SetICEUDPMux(webrtc.NewICEUDPMux(nil, conn))
	se.SetNAT1To1IPs([]string{cfg.AdvertiseIP}, webrtc.ICECandidateTypeHost)
	networks := []webrtc.NetworkType{webrtc.NetworkTypeUDP4}
	var tcp net.Listener
	if cfg.TCP {
		tcp, err = listenICETCP(cfg.TCPPort)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("listen tcp %d: %w", cfg.TCPPort, err)
		}
		se.SetICETCPMux(webrtc.NewICETCPMux(nil, tcp, 64))
		networks = append(networks, webrtc.NetworkTypeTCP4)
	}
	se.SetNetworkTypes(networks)
	if cfg.IncludeLoopback {
		se.SetIncludeLoopbackCandidate(true)
	}
	return &Engine{media: m, reg: reg, se: se, conn: conn, tcp: tcp, log: log}, nil
}

// Port is the bound UDP port (useful when 0 was requested).
func (e *Engine) Port() int { return e.conn.LocalAddr().(*net.UDPAddr).Port }

// TCPPort is the ICE-TCP port, or 0 when ICE-TCP is off.
func (e *Engine) TCPPort() int {
	if e.tcp == nil {
		return 0
	}
	return e.tcp.Addr().(*net.TCPAddr).Port
}

func (e *Engine) Close() error {
	if e.tcp != nil {
		_ = e.tcp.Close()
	}
	return e.conn.Close()
}
