package signal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/ssubbotin/kunveno/internal/config"
	"github.com/ssubbotin/kunveno/internal/metrics"
	"github.com/ssubbotin/kunveno/internal/quality"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/pion/webrtc/v4"

	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/room"
	"github.com/ssubbotin/kunveno/internal/sfu"
)

// MediaPeer is what the handler needs from one participant's media connection.
type MediaPeer interface {
	sfu.Source
	Negotiate()
	HandleAnswer(sdp string) error
	AddICECandidate(c webrtc.ICECandidateInit) error
	Subscribe(from sfu.Source, kind sfu.Kind) error
	Unsubscribe(from sfu.Source, kind sfu.Kind) error
	Close() error
	Sample() (quality.Sample, bool)
}

// Media creates media peers; the sfu.Engine is wrapped to satisfy it in main.
type Media interface {
	NewPeer(id string, sig sfu.Signaler, h sfu.Handlers) (MediaPeer, error)
}

const (
	joinTimeout  = 10 * time.Second
	writeTimeout = 5 * time.Second
	readLimit    = 256 * 1024
)

// Handler serves /ws.
type Handler struct {
	reg     *room.Registry
	media   Media
	origins []string
	log     *slog.Logger

	qualityEvery time.Duration      // how often connection quality is sampled
	pins         *ratelimit.Limiter // wrong-PIN limit per client address
	reactions    map[string]bool    // emoji participants may send (ui.reactions)
	notes        *notes.Store       // meeting notes (notes.enabled); nil when off
	notesLive    bool               // send each phrase to the room as it is recognised
	stopGrace    time.Duration      // how long a stopped notes bot may take to leave
	notesMu      sync.Mutex         // orders every room's notes state and the store's writes

	mu   sync.Mutex
	live map[string]*liveRoom // slug -> members
}

type liveRoom struct {
	room    *room.Room
	members map[string]*session
	notes   notesRoom // guarded by Handler.notesMu
}

type session struct {
	id, name, slug string
	bot            bool        // a helper: the echo or a player
	botKind        string      // RoleEcho, RolePlayer, RoleCamera or RoleNotes
	noVideo        bool        // declined other participants' cameras; guarded by Handler.mu
	stopping       bool        // notes bot told to stop; guarded by Handler.mu
	stopTimer      *time.Timer // closes a stopped notes bot that does not leave; guarded by Handler.mu
	meeting        time.Time   // notes bot: start of the meeting it joined to take notes of
	client         string      // the page's id from join, if it sent one
	lastReaction   time.Time   // for the reaction rate limit; only the session's read loop uses it
	joined         time.Time
	conn           *websocket.Conn
	peer           MediaPeer
	cancel         context.CancelFunc
	log            *slog.Logger
}

func New(reg *room.Registry, media Media, origins []string, log *slog.Logger) *Handler {
	return &Handler{reg: reg, media: media, origins: origins, log: log, live: map[string]*liveRoom{}, qualityEvery: 3 * time.Second,
		stopGrace: NotesStopGrace, pins: ratelimit.New(5, 10*time.Minute, time.Minute), reactions: set(config.DefaultReactions)}
}

// lookup returns the live state of a registered room, creating it on first use. Rooms can be
// created and deleted at runtime; a re-created slug gets fresh state.
func (h *Handler) lookup(slug string) (*liveRoom, bool) {
	r, ok := h.reg.Get(slug)
	h.mu.Lock()
	defer h.mu.Unlock()
	lr, have := h.live[slug]
	if !ok {
		// A deleted room keeps serving the sessions still inside it.
		return lr, have && len(lr.members) > 0
	}
	if !have || lr.room != r {
		lr = &liveRoom{room: r, members: map[string]*session{}}
		h.live[slug] = lr
	}
	return lr, true
}

// SendOffer implements sfu.Signaler for one session.
func (s *session) SendOffer(sdp string) error { return s.write(Offer(sdp)) }

func (s *session) write(v any) error {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return wsjson.Write(ctx, s.conn, v)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.origins})
	if err != nil {
		h.log.Warn("ws accept", "err", err)
		return
	}
	conn.SetReadLimit(readLimit)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	metrics.Sessions.Inc()
	defer metrics.Sessions.Dec()
	h.serve(ctx, cancel, conn, local(r), ratelimit.ClientIP(r))
}

// local reports a connection made on loopback without passing through nginx, which always
// sets X-Forwarded-For. Only such connections may join as the echo helper.
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

func readClient(ctx context.Context, conn *websocket.Conn) (ClientMessage, error) {
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return ClientMessage{}, err
	}
	if typ != websocket.MessageText {
		return ClientMessage{}, ErrBadMessage
	}
	return ParseClient(data)
}

func writeErrorAndClose(conn *websocket.Conn, code, msg string) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	_ = wsjson.Write(ctx, conn, ErrorMsg(code, msg))
	_ = conn.Close(websocket.StatusPolicyViolation, code)
}

func (h *Handler) serve(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, trusted bool, ip string) {
	joinCtx, joinCancel := context.WithTimeout(ctx, joinTimeout)
	first, err := readClient(joinCtx, conn)
	joinCancel()
	if err != nil || first.Type != "join" {
		metrics.BadMessages.Inc()
		writeErrorAndClose(conn, CodeBadMessage, "first message must be join")
		return
	}
	if first.Role != "" && !trusted {
		h.log.Warn("helper role from a non-local connection", "role", first.Role)
		writeErrorAndClose(conn, CodeBadMessage, "helper roles are local only")
		return
	}
	if ok, wait := h.pins.Allow(ip); !ok && first.Role == "" {
		metrics.Joins.WithLabelValues(h.roomLabel(first.Room), CodeTooManyAttempts).Inc()
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		_ = wsjson.Write(ctx, conn, RetryLater(wait))
		cancel()
		_ = conn.Close(websocket.StatusPolicyViolation, CodeTooManyAttempts)
		return
	}
	s, code, msg := h.join(conn, cancel, first)
	if code != "" { // successful joins are counted inside join, before "joined" goes out
		metrics.Joins.WithLabelValues(h.roomLabel(first.Room), code).Inc()
	}
	switch code {
	case CodeBadPIN:
		metrics.PINFailures.WithLabelValues("join").Inc()
		h.log.Info("wrong pin", "room", h.roomLabel(first.Room), "ip", ip)
		if h.pins.Fail(ip) {
			metrics.PINBlocks.Inc()
			h.log.Warn("too many wrong pins; address blocked for a minute", "ip", ip)
		}
	case "":
		h.pins.Success(ip)
	}
	if s == nil {
		writeErrorAndClose(conn, code, msg)
		return
	}
	defer h.leave(s)
	go h.watchQuality(ctx, s)
	for {
		m, err := readClient(ctx, conn)
		if err != nil {
			if !errors.Is(err, ErrBadMessage) {
				return // socket closed or context cancelled
			}
			s.log.Warn("bad message", "err", err)
			metrics.BadMessages.Inc()
			writeErrorAndClose(conn, CodeBadMessage, err.Error())
			return
		}
		metrics.Messages.WithLabelValues(m.Type).Inc()
		switch m.Type {
		case "answer":
			if err := s.peer.HandleAnswer(m.SDP); err != nil {
				s.log.Warn("answer", "err", err)
			}
		case "candidate":
			if m.Candidate.Candidate == "" {
				continue // end of candidates; ICE lite needs nothing
			}
			if err := s.peer.AddICECandidate(*m.Candidate); err != nil {
				s.log.Debug("candidate", "err", err)
			}
		case "mute":
			lr, _ := h.lookup(s.slug)
			if lr.room.SetMuted(s.id, *m.Muted) {
				h.broadcast(lr)
			}
		case "share":
			h.handleShare(s, m.Action)
		case "camera":
			lr, _ := h.lookup(s.slug)
			if lr.room.SetCamera(s.id, *m.On) {
				h.broadcast(lr)
			}
		case "react":
			h.react(s, m.Emoji)
		case "video":
			if s.botKind == RoleNotes {
				continue // the notes bot hears audio only
			}
			h.setReceiveVideo(s, *m.Receive)
		case "echo":
			if s.bot {
				continue
			}
			lr, _ := h.lookup(s.slug)
			lr.room.SetEcho(*m.On)
			h.dropBotsIfUnwanted(lr)
			h.broadcast(lr)
		case "notes":
			if s.bot || h.notes == nil {
				continue // helpers cannot switch notes; with notes.enabled off the switch does not exist
			}
			h.switchNotes(s, *m.On)
		case "phrase":
			if s.botKind != RoleNotes {
				continue
			}
			h.phrase(s, m)
		case "leave":
			h.leave(s) // at once: closing waits for the other side's answer
			_ = conn.Close(websocket.StatusNormalClosure, "bye")
			return
		case "join":
			writeErrorAndClose(conn, CodeBadMessage, "already joined")
			return
		}
	}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// join validates the request, admits the participant and starts media. On
// failure it returns a nil session plus the error code and message.
func (h *Handler) join(conn *websocket.Conn, cancel context.CancelFunc, m ClientMessage) (*session, string, string) {
	lr, ok := h.lookup(m.Room)
	if !ok {
		return nil, CodeBadRoom, "no such room"
	}
	bot := m.Role != ""
	switch {
	case m.Role == RoleEcho && !lr.room.Echo():
		return nil, CodeBadRoom, "echo not requested in this room"
	case (m.Role == RolePlayer || m.Role == RoleCamera) && !lr.room.HasPlayer():
		return nil, CodeBadRoom, "no player configured for this room"
	case m.Role == RoleNotes && (h.notes == nil || !lr.room.Notes()):
		return nil, CodeBadRoom, "notes not requested in this room"
	}
	h.replaceClient(lr, m.Client)
	id := newID()
	var err error
	if bot {
		err = lr.room.JoinBot(id, m.Name, m.Role)
	} else {
		err = lr.room.Join(id, m.Name, m.PIN)
	}
	switch {
	case errors.Is(err, room.ErrBadPIN):
		return nil, CodeBadPIN, "wrong pin"
	case errors.Is(err, room.ErrFull):
		return nil, CodeRoomFull, "room is full"
	case err != nil:
		return nil, CodeBadMessage, err.Error()
	}
	s := &session{id: id, name: m.Name, slug: m.Room, bot: bot, botKind: m.Role, client: m.Client, joined: time.Now(), conn: conn, cancel: cancel, log: h.log.With("room", m.Room, "participant", id)}
	if m.Role == RoleNotes {
		s.meeting, _ = lr.room.Meeting()
	}
	peer, err := h.media.NewPeer(id, s, sfu.Handlers{OnPublisher: h.onPublisher, OnFailed: h.onFailed, TCPOnly: m.Media == MediaTCP})
	if err != nil {
		lr.room.Leave(id)
		s.log.Error("new peer", "err", err)
		return nil, CodeMediaFailed, "media setup failed"
	}
	s.peer = peer
	h.mu.Lock()
	lr.members[id] = s
	others := lr.membersExcept(id)
	h.mu.Unlock()
	// Subscribe before announcing the join, so the first offer already carries every existing track.
	sharer := lr.room.Sharer()
	for _, o := range others {
		if receives(s, o, sfu.KindAudio) {
			if err := peer.Subscribe(o.peer, sfu.KindAudio); err != nil && !errors.Is(err, sfu.ErrNoTrack) {
				s.log.Warn("subscribe audio", "from", o.id, "err", err)
			}
		}
		if o.id == sharer && receives(s, o, sfu.KindScreen) {
			if err := peer.Subscribe(o.peer, sfu.KindScreen); err != nil && !errors.Is(err, sfu.ErrNoTrack) {
				s.log.Warn("subscribe screen", "from", o.id, "err", err)
			}
		}
		if receives(s, o, sfu.KindCamera) {
			if err := peer.Subscribe(o.peer, sfu.KindCamera); err != nil && !errors.Is(err, sfu.ErrNoTrack) {
				s.log.Warn("subscribe camera", "from", o.id, "err", err)
			}
		}
	}
	metrics.Joins.WithLabelValues(lr.room.Slug(), "ok").Inc()
	if err := s.write(Joined(ParticipantInfo{ID: id, Name: m.Name}, lr.room.Slug(), lr.room.Name())); err != nil {
		h.leave(s)
		return nil, CodeMediaFailed, "write failed"
	}
	s.log.Info("joined", "name", m.Name)
	h.broadcast(lr)
	peer.Negotiate()
	if s.botKind == RoleNotes && !lr.room.Notes() {
		h.stopNotes(lr) // switched off while this bot was joining, before it was a member to stop
	}
	return s, "", ""
}

func (lr *liveRoom) membersExcept(id string) []*session {
	out := make([]*session, 0, len(lr.members))
	for mid, s := range lr.members {
		if mid != id {
			out = append(out, s)
		}
	}
	return out
}

func (h *Handler) leave(s *session) {
	lr, _ := h.lookup(s.slug)
	wasNotes := lr.room.Notes()
	if !s.bot {
		// Before leaving the members, so a phrase of this person is never without a name.
		h.rememberSpeaker(lr, s, wasNotes)
	}
	h.mu.Lock()
	if _, ok := lr.members[s.id]; !ok {
		h.mu.Unlock()
		return
	}
	delete(lr.members, s.id)
	others := lr.membersExcept(s.id)
	h.mu.Unlock()
	lr.room.Leave(s.id)
	for _, o := range others {
		_ = o.peer.Unsubscribe(s.peer, sfu.KindAudio)
		_ = o.peer.Unsubscribe(s.peer, sfu.KindScreen)
		_ = o.peer.Unsubscribe(s.peer, sfu.KindCamera)
	}
	_ = s.peer.Close()
	s.log.Info("left")
	metrics.Leaves.WithLabelValues(s.slug).Inc()
	metrics.SessionSeconds.Observe(time.Since(s.joined).Seconds())
	h.dropBotsIfUnwanted(lr)
	switch {
	case s.botKind == RoleNotes:
		h.notesBotLeft(lr, s)
	case wasNotes && !lr.room.Notes():
		h.stopNotes(lr) // the last person left
	}
	h.broadcast(lr)
}

// dropBotsIfUnwanted disconnects the echo helper once its room no longer wants it.
func (h *Handler) dropBotsIfUnwanted(lr *liveRoom) {
	if lr.room.Echo() {
		return
	}
	h.mu.Lock()
	var bots []*session
	for _, o := range lr.members {
		if o.botKind == RoleEcho { // a room's player stays
			bots = append(bots, o)
		}
	}
	h.mu.Unlock()
	for _, b := range bots {
		_ = b.conn.Close(websocket.StatusNormalClosure, "echo off")
		b.cancel()
	}
}

func (h *Handler) broadcast(lr *liveRoom) {
	snap := lr.room.Snapshot()
	list := make([]ParticipantInfo, len(snap.Participants))
	for i, p := range snap.Participants {
		list[i] = ParticipantInfo{ID: p.ID, Name: p.Name, Muted: p.Muted, Bot: p.Bot, BotKind: p.BotKind, Quality: p.Quality, Camera: p.Camera}
	}
	msg := Participants(list, snap.Sharer, snap.Echo, snap.Notes)
	h.mu.Lock()
	members := lr.membersExcept("")
	h.mu.Unlock()
	for _, s := range members {
		if err := s.write(msg); err != nil {
			s.log.Warn("broadcast write", "err", err)
			s.cancel()
		}
	}
}

func (h *Handler) handleShare(s *session, action string) {
	lr, _ := h.lookup(s.slug)
	switch action {
	case "start":
		if err := lr.room.StartShare(s.id); err != nil {
			_ = s.write(ErrorMsg(CodeShareBusy, "someone else is sharing"))
			return
		}
		h.broadcast(lr)
		if s.peer.Track(sfu.KindScreen) != nil {
			h.subscribeAll(lr, s, sfu.KindScreen)
		}
	case "stop":
		if lr.room.StopShare(s.id) {
			h.mu.Lock()
			others := lr.membersExcept(s.id)
			h.mu.Unlock()
			for _, o := range others {
				_ = o.peer.Unsubscribe(s.peer, sfu.KindScreen)
			}
			h.broadcast(lr)
		}
	}
}

func (h *Handler) subscribeAll(lr *liveRoom, from *session, kind sfu.Kind) {
	h.mu.Lock()
	others := lr.membersExcept(from.id)
	h.mu.Unlock()
	for _, o := range others {
		if !receives(o, from, kind) {
			continue
		}
		if kind == sfu.KindCamera && h.declinesVideo(o) {
			continue
		}
		if err := o.peer.Subscribe(from.peer, kind); err != nil {
			o.log.Warn("subscribe", "from", from.id, "kind", kind.String(), "err", err)
		}
	}
}

func (h *Handler) findSession(id string) (*liveRoom, *session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, lr := range h.live {
		if s, ok := lr.members[id]; ok {
			return lr, s
		}
	}
	return nil, nil
}

// onPublisher runs when the first RTP of a kind arrives from a participant.
func (h *Handler) onPublisher(src sfu.Source, kind sfu.Kind) {
	lr, s := h.findSession(src.ID())
	if s == nil {
		return
	}
	if kind == sfu.KindScreen && lr.room.Sharer() != s.id {
		s.log.Warn("screen RTP without the share lock; ignored")
		return
	}
	h.subscribeAll(lr, s, kind)
}

func (h *Handler) onFailed(src sfu.Source) {
	_, s := h.findSession(src.ID())
	if s == nil {
		return
	}
	s.log.Warn("media failed; closing session")
	metrics.MediaFailed.Inc()
	_ = s.write(ErrorMsg(CodeMediaFailed, "media connection failed"))
	_ = s.conn.Close(websocket.StatusInternalError, CodeMediaFailed)
	s.cancel()
}

// count is used by tests and the HTTP API.
func (h *Handler) count(slug string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if lr, ok := h.live[slug]; ok {
		return len(lr.members)
	}
	return 0
}

// roomLabel keeps metric labels bounded: slugs of unknown rooms collapse into one value.
func (h *Handler) roomLabel(slug string) string {
	if _, ok := h.reg.Get(slug); ok {
		return slug
	}
	return "unknown"
}

// watchQuality samples the session's connection, publishes grade changes to the room and
// records the raw numbers in metrics.
func (h *Handler) watchQuality(ctx context.Context, s *session) {
	t := time.NewTicker(h.qualityEvery)
	defer t.Stop()
	var tr quality.Tracker
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		sample, ok := s.peer.Sample()
		if !ok {
			continue
		}
		if !s.bot {
			metrics.LossRatio.WithLabelValues("up").Observe(sample.UpLoss)
			metrics.LossRatio.WithLabelValues("down").Observe(sample.DownLoss)
			if sample.HasVideo {
				metrics.LossRatio.WithLabelValues("down_video").Observe(sample.VideoLoss)
			}
			metrics.JitterSeconds.Observe(sample.Jitter.Seconds())
			if sample.RTT > 0 {
				metrics.RTTSeconds.Observe(sample.RTT.Seconds())
			}
		}
		grade, changed := tr.Update(sample)
		if !changed {
			continue
		}
		lr, ok := h.lookup(s.slug)
		if ok && lr.room.SetQuality(s.id, grade) {
			s.log.Info("connection quality", "grade", grade, "up_loss", sample.UpLoss, "down_loss", sample.DownLoss,
				"rtt", sample.RTT.String(), "jitter", sample.Jitter.String())
			h.broadcast(lr)
		}
	}
}

// PINLimiter is the wrong-PIN limiter, for sharing with the room API.
func (h *Handler) PINLimiter() *ratelimit.Limiter { return h.pins }

func (h *Handler) declinesVideo(s *session) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return s.noVideo
}

// setReceiveVideo drops or restores the session's subscriptions to everyone's camera, for
// participants on a weak link who keep audio and the shared screen only.
func (h *Handler) setReceiveVideo(s *session, receive bool) {
	lr, _ := h.lookup(s.slug)
	lr.room.SetDeclinesVideo(s.id, !receive)
	h.mu.Lock()
	s.noVideo = !receive
	others := lr.membersExcept(s.id)
	h.mu.Unlock()
	for _, o := range others {
		var err error
		if receive {
			err = s.peer.Subscribe(o.peer, sfu.KindCamera)
		} else {
			err = s.peer.Unsubscribe(o.peer, sfu.KindCamera)
		}
		if err != nil && !errors.Is(err, sfu.ErrNoTrack) {
			s.log.Warn("camera subscription", "from", o.id, "receive", receive, "err", err)
		}
	}
	s.log.Info("receive video", "on", receive)
}

// replaceClient removes the earlier session of the same page from the room, if there is one:
// the page reconnected while its old connection had not been closed yet.
func (h *Handler) replaceClient(lr *liveRoom, client string) {
	if client == "" {
		return
	}
	h.mu.Lock()
	var old *session
	for _, o := range lr.members {
		if o.client == client {
			old = o
			break
		}
	}
	h.mu.Unlock()
	if old == nil {
		return
	}
	old.log.Info("replaced by a new session of the same page")
	h.leave(old) // at once, so it neither counts toward capacity nor appears in the snapshot
	// Close waits for the other side's answer; the new join must not wait for it.
	go func() { _ = old.conn.Close(websocket.StatusNormalClosure, "replaced") }()
	old.cancel()
}

// reactionGap is the least time between two reactions of one participant; faster ones are dropped.
const reactionGap = 700 * time.Millisecond

// react shows a participant's emoji to everyone in the room, the sender included.
func (h *Handler) react(s *session, emoji string) {
	if s.bot || !h.reactions[emoji] || time.Since(s.lastReaction) < reactionGap {
		return // a helper, an emoji outside the configured set, or too fast
	}
	s.lastReaction = time.Now()
	metrics.Reactions.WithLabelValues(emoji).Inc()
	lr, _ := h.lookup(s.slug)
	h.mu.Lock()
	members := lr.membersExcept("")
	h.mu.Unlock()
	msg := Reaction(s.id, emoji)
	for _, o := range members {
		if err := o.write(msg); err != nil {
			o.log.Warn("reaction write", "err", err)
		}
	}
}

// SetReactions sets the emoji participants may send, from ui.reactions. Call before serving.
func (h *Handler) SetReactions(list []string) { h.reactions = set(list) }

func set(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, e := range list {
		m[e] = true
	}
	return m
}
