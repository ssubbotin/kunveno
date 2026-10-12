package signal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/pion/webrtc/v4"

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

	mu   sync.Mutex
	live map[string]*liveRoom // slug -> members
}

type liveRoom struct {
	room    *room.Room
	members map[string]*session
}

type session struct {
	id, name, slug string
	conn           *websocket.Conn
	peer           MediaPeer
	cancel         context.CancelFunc
	log            *slog.Logger
}

func New(reg *room.Registry, media Media, origins []string, log *slog.Logger) *Handler {
	return &Handler{reg: reg, media: media, origins: origins, log: log, live: map[string]*liveRoom{}}
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
	h.serve(ctx, cancel, conn)
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

func (h *Handler) serve(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn) {
	joinCtx, joinCancel := context.WithTimeout(ctx, joinTimeout)
	first, err := readClient(joinCtx, conn)
	joinCancel()
	if err != nil || first.Type != "join" {
		writeErrorAndClose(conn, CodeBadMessage, "first message must be join")
		return
	}
	s, code, msg := h.join(conn, cancel, first)
	if s == nil {
		writeErrorAndClose(conn, code, msg)
		return
	}
	defer h.leave(s)
	for {
		m, err := readClient(ctx, conn)
		if err != nil {
			if !errors.Is(err, ErrBadMessage) {
				return // socket closed or context cancelled
			}
			s.log.Warn("bad message", "err", err)
			writeErrorAndClose(conn, CodeBadMessage, err.Error())
			return
		}
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
		case "leave":
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
	id := newID()
	switch err := lr.room.Join(id, m.Name, m.PIN); {
	case errors.Is(err, room.ErrBadPIN):
		return nil, CodeBadPIN, "wrong pin"
	case errors.Is(err, room.ErrFull):
		return nil, CodeRoomFull, "room is full"
	case err != nil:
		return nil, CodeBadMessage, err.Error()
	}
	s := &session{id: id, name: m.Name, slug: m.Room, conn: conn, cancel: cancel, log: h.log.With("room", m.Room, "participant", id)}
	peer, err := h.media.NewPeer(id, s, sfu.Handlers{OnPublisher: h.onPublisher, OnFailed: h.onFailed})
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
		if err := peer.Subscribe(o.peer, sfu.KindAudio); err != nil && !errors.Is(err, sfu.ErrNoTrack) {
			s.log.Warn("subscribe audio", "from", o.id, "err", err)
		}
		if o.id == sharer {
			if err := peer.Subscribe(o.peer, sfu.KindScreen); err != nil && !errors.Is(err, sfu.ErrNoTrack) {
				s.log.Warn("subscribe screen", "from", o.id, "err", err)
			}
		}
	}
	if err := s.write(Joined(ParticipantInfo{ID: id, Name: m.Name}, lr.room.Slug(), lr.room.Name())); err != nil {
		h.leave(s)
		return nil, CodeMediaFailed, "write failed"
	}
	s.log.Info("joined", "name", m.Name)
	h.broadcast(lr)
	peer.Negotiate()
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
	}
	_ = s.peer.Close()
	s.log.Info("left")
	h.broadcast(lr)
}

func (h *Handler) broadcast(lr *liveRoom) {
	snap := lr.room.Snapshot()
	list := make([]ParticipantInfo, len(snap.Participants))
	for i, p := range snap.Participants {
		list[i] = ParticipantInfo{ID: p.ID, Name: p.Name, Muted: p.Muted}
	}
	msg := Participants(list, snap.Sharer)
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
