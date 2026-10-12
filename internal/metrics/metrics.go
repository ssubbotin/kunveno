// Package metrics defines the Prometheus metrics of kunveno-server. Counters are package
// variables incremented where events happen; room state is read from the registry at scrape
// time, so it never drifts from the truth.
package metrics

import (
	"net"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ssubbotin/kunveno/internal/room"
)

var (
	Joins = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kunveno_joins_total", Help: "Join attempts by room and result (ok or the error code).",
	}, []string{"room", "result"})
	Leaves = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kunveno_leaves_total", Help: "Participants that left, by room.",
	}, []string{"room"})
	Sessions = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kunveno_ws_sessions", Help: "Open signaling WebSocket connections.",
	})
	SessionSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "kunveno_session_duration_seconds", Help: "Time participants spent in a room.",
		Buckets: []float64{10, 30, 60, 300, 900, 1800, 3600, 7200, 14400},
	})
	Messages = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kunveno_signal_messages_total", Help: "Valid client messages by type.",
	}, []string{"type"})
	BadMessages = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kunveno_signal_bad_messages_total", Help: "Client messages rejected as bad_message.",
	})
	MediaFailed = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kunveno_media_failed_total", Help: "Sessions closed because the peer connection failed.",
	})
	RTPPackets = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kunveno_rtp_received_packets_total", Help: "RTP packets received from publishers, by kind (audio or screen).",
	}, []string{"kind"})
	RTPBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kunveno_rtp_received_bytes_total", Help: "RTP payload bytes received from publishers, by kind.",
	}, []string{"kind"})
	PeerStates = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kunveno_peer_state_changes_total", Help: "Peer connection state transitions, by new state.",
	}, []string{"state"})
	RTPSentPackets = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kunveno_rtp_sent_packets_total", Help: "RTP packets sent to participants, by media (audio or video: screen and cameras together); one per subscriber.",
	}, []string{"kind"})
	RTPSentBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kunveno_rtp_sent_bytes_total", Help: "RTP payload bytes sent to participants, by media (audio or video).",
	}, []string{"kind"})
	ActiveConnections = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kunveno_media_connections", Help: "Media connections up right now, by transport (udp or tcp).",
	}, []string{"transport"})
	ConnectSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "kunveno_connect_seconds", Help: "Time from join to an established media connection, by transport.",
		Buckets: []float64{0.25, 0.5, 1, 2, 5, 10, 20, 45},
	}, []string{"transport"})
	RTTSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "kunveno_rtt_seconds", Help: "Round trip between server and participants, sampled every few seconds.",
		Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.2, 0.4, 0.8},
	})
	LossRatio = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "kunveno_packet_loss_ratio", Help: "Audio packet loss, sampled every few seconds, by direction (up: participant to server, down: server to participant).",
		Buckets: []float64{0.001, 0.005, 0.01, 0.02, 0.05, 0.1, 0.2},
	}, []string{"direction"})
	JitterSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "kunveno_jitter_seconds", Help: "Jitter of participants' audio as received by the server, sampled every few seconds.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1},
	})
	Transports = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kunveno_media_connections_total", Help: "Established media connections by transport (udp or tcp).",
	}, []string{"transport"})
	Offers = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kunveno_offers_total", Help: "SDP offers sent to participants.",
	})
	NegotiationErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kunveno_negotiation_errors_total", Help: "Failed offer or answer handling.",
	})
	KeyframeRequests = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kunveno_keyframe_requests_total", Help: "PLIs sent to screen sharers.",
	})
	PINFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kunveno_pin_failures_total", Help: "Wrong room PINs, by where they were tried (join or delete).",
	}, []string{"source"})
	PINBlocks = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kunveno_pin_blocks_total", Help: "Addresses blocked for a minute after too many wrong PINs.",
	})
	RoomsCreated = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kunveno_rooms_created_total", Help: "Rooms created from the lobby.",
	})
	RoomsDeleted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kunveno_rooms_deleted_total", Help: "Rooms deleted from the lobby.",
	})
)

// NewRegistry gathers every kunveno metric, the room state, and Go runtime and process metrics.
func NewRegistry(rooms *room.Registry, version string) *prometheus.Registry {
	r := prometheus.NewRegistry()
	build := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kunveno_build_info", Help: "Always 1; the version label names the running build.",
		ConstLabels: prometheus.Labels{"version": version},
	})
	build.Set(1)
	r.MustRegister(
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		build, roomCollector{rooms},
		Joins, Leaves, Sessions, SessionSeconds, Messages, BadMessages, MediaFailed,
		RTTSeconds, LossRatio, JitterSeconds,
		RTPPackets, RTPBytes, RTPSentPackets, RTPSentBytes, ActiveConnections, ConnectSeconds,
		PeerStates, Transports, Offers, NegotiationErrors, KeyframeRequests,
		PINFailures, PINBlocks, RoomsCreated, RoomsDeleted,
	)
	return r
}

// Handler serves /metrics to local scrapers only (Prometheus runs on the same host); requests
// that came through nginx carry X-Forwarded-For and are refused.
func Handler(r *prometheus.Registry) http.Handler {
	h := promhttp.HandlerFor(r, promhttp.HandlerOpts{})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !local(req) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, req)
	})
}

func local(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var (
	roomsDesc        = prometheus.NewDesc("kunveno_rooms", "Rooms known to the server.", nil, nil)
	participantsDesc = prometheus.NewDesc("kunveno_room_participants", "Participants in the room by kind (person or bot).", []string{"room", "kind"}, nil)
	capacityDesc     = prometheus.NewDesc("kunveno_room_capacity", "Maximum participants of the room.", []string{"room"}, nil)
	sharingDesc      = prometheus.NewDesc("kunveno_room_sharing", "1 while someone shares a screen in the room.", []string{"room"}, nil)
	qualityDesc      = prometheus.NewDesc("kunveno_participants_by_quality", "People in calls by connection grade (good, fair, poor, unknown).", []string{"grade"}, nil)
	echoDesc         = prometheus.NewDesc("kunveno_room_echo", "1 when the room asked for the echo helper.", []string{"room"}, nil)
)

// roomCollector reports the current state of every room at scrape time.
type roomCollector struct{ reg *room.Registry }

func (c roomCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{roomsDesc, participantsDesc, capacityDesc, sharingDesc, echoDesc, qualityDesc} {
		ch <- d
	}
}

func (c roomCollector) Collect(ch chan<- prometheus.Metric) {
	rooms := c.reg.All()
	ch <- prometheus.MustNewConstMetric(roomsDesc, prometheus.GaugeValue, float64(len(rooms)))
	grades := map[string]float64{"good": 0, "fair": 0, "poor": 0, "unknown": 0}
	defer func() {
		for g, n := range grades {
			ch <- prometheus.MustNewConstMetric(qualityDesc, prometheus.GaugeValue, n, g)
		}
	}()
	for _, r := range rooms {
		snap := r.Snapshot()
		var people, bots float64
		for _, p := range snap.Participants {
			if p.Bot {
				bots++
				continue
			}
			people++
			if p.Quality == "" {
				grades["unknown"]++
			} else {
				grades[p.Quality]++
			}
		}
		slug := r.Slug()
		ch <- prometheus.MustNewConstMetric(participantsDesc, prometheus.GaugeValue, people, slug, "person")
		ch <- prometheus.MustNewConstMetric(participantsDesc, prometheus.GaugeValue, bots, slug, "bot")
		ch <- prometheus.MustNewConstMetric(capacityDesc, prometheus.GaugeValue, float64(r.Capacity()), slug)
		ch <- prometheus.MustNewConstMetric(sharingDesc, prometheus.GaugeValue, b2f(snap.Sharer != ""), slug)
		ch <- prometheus.MustNewConstMetric(echoDesc, prometheus.GaugeValue, b2f(snap.Echo), slug)
	}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
