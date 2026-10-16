package signal

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/ssubbotin/kunveno/internal/auth"
	"github.com/ssubbotin/kunveno/internal/conference"
	"github.com/ssubbotin/kunveno/internal/metrics"
	"github.com/ssubbotin/kunveno/internal/ratelimit"
	"github.com/ssubbotin/kunveno/internal/room"
)

const (
	joinTimeout  = 10 * time.Second
	writeTimeout = 5 * time.Second
	readLimit    = 256 * 1024
)

// Handler serves /ws: it validates frames, turns them into conference calls and conference events
// into protocol messages.
type Handler struct {
	conf    *conference.Conference
	origins []string
	log     *slog.Logger
	pins    *ratelimit.Limiter // wrong-PIN limit per client address
	access  Access             // sign-in; nil when off
}

// Access is the sign-in, when it is on (package auth's Gate with its invitations).
type Access interface {
	Session(r *http.Request) (auth.Identity, bool)
	CheckInvite(room, token string) (auth.Invite, error)
}

// SetAccess turns sign-in on: people join with a session (and the room's PIN) or a guest
// invitation. Call before serving; nil keeps sign-in off.
func (h *Handler) SetAccess(a Access) { h.access = a }

// session is one WebSocket client; it is the conference.Conn of its member.
type session struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
}

func New(conf *conference.Conference, origins []string, log *slog.Logger) *Handler {
	return &Handler{conf: conf, origins: origins, log: log, pins: ratelimit.New(5, 10*time.Minute, time.Minute)}
}

// SendOffer implements media.Signaler for one session.
func (s *session) SendOffer(sdp string) error { return s.write(Offer(sdp)) }

// Send implements conference.Conn.
func (s *session) Send(e conference.Event) error { return s.write(wire(e)) }

// Close implements conference.Conn.
func (s *session) Close(reason string) {
	_ = s.conn.Close(websocket.StatusNormalClosure, reason)
	s.cancel()
}

// Abort implements conference.Conn.
func (s *session) Abort(reason string) {
	go func() { _ = s.conn.Close(websocket.StatusNormalClosure, reason) }()
	s.cancel()
}

// End implements conference.Conn.
func (s *session) End() { s.cancel() }

// Fail implements conference.Conn.
func (s *session) Fail() {
	_ = s.write(ErrorMsg(CodeMediaFailed, "media connection failed"))
	_ = s.conn.Close(websocket.StatusInternalError, CodeMediaFailed)
	s.cancel()
}

func (s *session) write(v any) error {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return wsjson.Write(ctx, s.conn, v)
}

// wire is the protocol message of a conference event.
func wire(e conference.Event) any {
	switch e := e.(type) {
	case conference.Joined:
		self := ParticipantInfo{ID: e.ID, Name: e.Name}
		if e.Recorder {
			return JoinedRecorder(self, e.Slug, e.RoomName, e.Meeting)
		}
		return Joined(self, e.Slug, e.RoomName)
	case conference.Roster:
		list := make([]ParticipantInfo, len(e.Participants))
		for i, p := range e.Participants {
			list[i] = ParticipantInfo{ID: p.ID, Name: p.Name, Muted: p.Muted, Bot: p.Bot, BotKind: p.BotKind, Quality: p.Quality, Camera: p.Camera, Guest: p.Guest}
		}
		return Participants(list, e.Sharer, e.Echo, e.Notes, e.Recording)
	case conference.ShareBusy:
		return ErrorMsg(CodeShareBusy, "someone else is sharing")
	case conference.Reaction:
		return Reaction(e.From, e.Emoji)
	case conference.Stop:
		return Stop()
	case conference.NotesReady:
		return NotesReady()
	case conference.RecordingReady:
		return RecordingReady()
	case conference.LivePhrase:
		return LivePhrase(e.Name, e.Start.UnixMilli(), e.Text)
	}
	panic("signal: unknown conference event")
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var who *auth.Identity // the signed-in person, read before the upgrade
	if h.access != nil {
		if id, ok := h.access.Session(r); ok {
			who = &id
		}
	}
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
	h.serve(ctx, cancel, conn, local(r), ratelimit.ClientIP(r), who)
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

func (h *Handler) serve(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, trusted bool, ip string, who *auth.Identity) {
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
		metrics.Joins.WithLabelValues(h.conf.RoomLabel(first.Room), CodeTooManyAttempts).Inc()
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		_ = wsjson.Write(ctx, conn, RetryLater(wait))
		cancel()
		_ = conn.Close(websocket.StatusPolicyViolation, CodeTooManyAttempts)
		return
	}
	s, code, msg := h.join(&session{conn: conn, cancel: cancel}, first, who)
	if code != "" { // successful joins are counted inside Join, before "joined" goes out
		metrics.Joins.WithLabelValues(h.conf.RoomLabel(first.Room), code).Inc()
	}
	switch code {
	case CodeBadPIN:
		metrics.PINFailures.WithLabelValues("join").Inc()
		h.log.Info("wrong pin", "room", h.conf.RoomLabel(first.Room), "ip", ip)
		if h.pins.Fail(ip) {
			metrics.PINBlocks.Inc()
			h.log.Warn("too many wrong pins; address blocked for a minute", "ip", ip)
		}
	case CodeBadInvite:
		metrics.PINFailures.WithLabelValues("invite").Inc()
		h.log.Info("bad invitation", "room", h.conf.RoomLabel(first.Room), "ip", ip) // never the token
		if h.pins.Fail(ip) {
			metrics.PINBlocks.Inc()
			h.log.Warn("too many bad invitations; address blocked for a minute", "ip", ip)
		}
	case "":
		h.pins.Success(ip)
	}
	if s == nil {
		writeErrorAndClose(conn, code, msg)
		return
	}
	defer h.conf.Leave(s)
	go h.conf.WatchQuality(ctx, s)
	for {
		m, err := readClient(ctx, conn)
		if err != nil {
			if !errors.Is(err, ErrBadMessage) {
				return // socket closed or context cancelled
			}
			s.Log().Warn("bad message", "err", err)
			metrics.BadMessages.Inc()
			writeErrorAndClose(conn, CodeBadMessage, err.Error())
			return
		}
		metrics.Messages.WithLabelValues(m.Type).Inc()
		switch m.Type {
		case "answer":
			if err := s.Peer().HandleAnswer(m.SDP); err != nil {
				s.Log().Warn("answer", "err", err)
			}
		case "candidate":
			if m.Candidate.Candidate == "" {
				continue // end of candidates; ICE lite needs nothing
			}
			if err := s.Peer().AddICECandidate(*m.Candidate); err != nil {
				s.Log().Debug("candidate", "err", err)
			}
		case "mute":
			h.conf.SetMuted(s, *m.Muted)
		case "share":
			if m.Action == "start" {
				h.conf.StartShare(s)
			} else {
				h.conf.StopShare(s)
			}
		case "camera":
			h.conf.SetCamera(s, *m.On)
		case "react":
			h.conf.React(s, m.Emoji)
		case "video":
			h.conf.ReceiveVideo(s, *m.Receive)
		case "echo":
			h.conf.SetEcho(s, *m.On)
		case "notes":
			h.conf.SwitchNotes(s, *m.On)
		case "recording":
			h.conf.SwitchRecording(s, *m.On)
		case "phrase":
			if s.Role() != RoleNotes {
				continue
			}
			text, start, end, ok := validPhrase(m, time.Now())
			if !ok {
				s.Log().Warn("phrase refused")
				continue
			}
			h.conf.Phrase(s, m.Speaker, text, start, end)
		case "leave":
			h.conf.Leave(s) // at once: closing waits for the other side's answer
			_ = conn.Close(websocket.StatusNormalClosure, "bye")
			return
		case "join":
			writeErrorAndClose(conn, CodeBadMessage, "already joined")
			return
		}
	}
}

// join hands a validated join to the conference. who is the signed-in person (nil without a
// session); with sign-in on a person needs a session and a guest an invitation. On failure it
// returns a nil member plus the error code and message.
func (h *Handler) join(s *session, m ClientMessage, who *auth.Identity) (*conference.Member, string, string) {
	req := conference.JoinRequest{Room: m.Room, Name: m.Name, PIN: m.PIN, Role: m.Role, Client: m.Client, TCPOnly: m.Media == MediaTCP}
	if m.Role == "" {
		switch {
		case h.access == nil && m.Invite != "":
			return nil, CodeBadMessage, "invitations need sign-in"
		case h.access != nil && m.Invite != "":
			inv, err := h.access.CheckInvite(m.Room, m.Invite)
			if err != nil {
				return nil, CodeBadInvite, "the invitation has expired or was revoked"
			}
			req.Guest, req.Invite = true, inv.ID
		case h.access != nil && who == nil:
			return nil, CodeAuthRequired, "sign in first"
		case h.access != nil:
			if name := auth.DisplayName(*who); name != "" { // an account with neither name nor login keeps the typed one
				req.Name = name
			}
		}
	}
	member, err := h.conf.Join(s, req)
	var refused *conference.RefusedError
	switch {
	case err == nil:
	case errors.Is(err, conference.ErrNoRoom):
		return nil, CodeBadRoom, "no such room"
	case errors.As(err, &refused):
		return nil, CodeBadRoom, refused.Reason
	case errors.Is(err, room.ErrBadPIN):
		return nil, CodeBadPIN, "wrong pin"
	case errors.Is(err, room.ErrFull):
		return nil, CodeRoomFull, "room is full"
	case errors.Is(err, conference.ErrRevoked):
		return nil, CodeBadInvite, "the invitation has expired or was revoked"
	case errors.Is(err, conference.ErrMedia):
		return nil, CodeMediaFailed, "media setup failed"
	case errors.Is(err, conference.ErrWrite):
		return nil, CodeMediaFailed, "write failed"
	default:
		return nil, CodeBadMessage, err.Error()
	}
	if r := m.Restart; r != nil && m.Role == "" {
		member.Log().Info("rejoin", "name", m.Name, "reason", r.Reason, "after", r.After, "transport", r.Transport)
		metrics.ClientRestarts.WithLabelValues(r.Reason).Inc()
	}
	return member, "", ""
}

// PINLimiter is the wrong-PIN limiter, for sharing with the room API.
func (h *Handler) PINLimiter() *ratelimit.Limiter { return h.pins }
