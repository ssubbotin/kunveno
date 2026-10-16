// Package conference runs the meetings: who is in each live room, which media every member
// receives, the share lock, the helpers (echo, player, notes bot, recorder) and their lifecycles.
// It knows nothing of WebSocket or of the wire format: a member's client is a Conn, and what the
// client is told is an Event. The signal package adapts both to the protocol.
package conference

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ssubbotin/kunveno/internal/config"
	"github.com/ssubbotin/kunveno/internal/media"
	"github.com/ssubbotin/kunveno/internal/meetings"
	"github.com/ssubbotin/kunveno/internal/metrics"
	"github.com/ssubbotin/kunveno/internal/notes"
	"github.com/ssubbotin/kunveno/internal/quality"
	"github.com/ssubbotin/kunveno/internal/recording"
	"github.com/ssubbotin/kunveno/internal/room"
)

// MediaPeer is what the conference needs from one member's media connection.
type MediaPeer interface {
	media.Source
	Negotiate()
	HandleAnswer(sdp string) error
	AddICECandidate(c media.Candidate) error
	Subscribe(from media.Source, kind media.Kind) error
	Unsubscribe(from media.Source, kind media.Kind) error
	Close() error
	Sample() (quality.Sample, bool)
}

// Media creates media peers; the sfu.Engine is wrapped to satisfy it in main.
type Media interface {
	NewPeer(id string, sig media.Signaler, h media.Handlers) (MediaPeer, error)
}

// Conn is one member's client as the conference sees it.
type Conn interface {
	media.Signaler
	Send(e Event) error  // tells the client e; an error means the client is gone
	Close(reason string) // closes the connection with reason, waiting for the answer, then ends the session
	Abort(reason string) // ends the session at once; the close follows in the background
	End()                // ends the session, after a failed write
	Fail()               // tells the client its media failed and ends the session
}

// Errors of Join, besides room.ErrBadPIN and room.ErrFull.
var (
	ErrNoRoom = errors.New("no such room")
	ErrMedia  = errors.New("media setup failed")
	ErrWrite  = errors.New("write failed")
	// ErrRevoked: the guest's invitation was revoked while the guest was joining.
	ErrRevoked = errors.New("invitation revoked")
	// ErrNotAdmitted: a rejoin (JoinRequest.Rejoin) of an invitation or a person not admitted to
	// the room's current meeting.
	ErrNotAdmitted = errors.New("not admitted to this meeting")
)

// DefaultRejoinGrace is Conference.RejoinGrace unless set: longer than the client's reconnect
// backoff (at most 10 s) with room for a slow network.
const DefaultRejoinGrace = time.Minute

// revokedKeep is how long DropInvite remembers a revoked invitation for joins still in progress.
// A join passes its invitation check and reaches the room within moments, so a minute is ample and
// keeps the set small: it holds only the invitations revoked in the last minute.
const revokedKeep = time.Minute

// RefusedError is a helper refused by the room: it was not asked for, or one is already there.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return e.Reason }

// JoinRequest is a validated join.
type JoinRequest struct {
	Room, Name, PIN string
	Role            string // empty for a person; room.BotEcho, BotPlayer, BotCamera, BotNotes or BotRecorder
	Client          string // the page's id, if it sent one
	TCPOnly         bool   // the member's UDP path failed
	Guest           bool   // admitted by an invitation; no PIN
	Invite          string // the invitation's ID, for DropInvite
	Login           string // the signed-in employee's login (sign-in on)
	// Rejoin: the invitation or the session has expired, so the member is let in only when its
	// invitation (Guest) or login was admitted to the room's current meeting.
	Rejoin bool
}

// admitKey names what admitted a person to a meeting: the invitation of a guest or the login of
// an employee; empty with sign-in off.
func (r JoinRequest) admitKey() string {
	switch {
	case r.Guest && r.Invite != "":
		return "invite:" + r.Invite
	case !r.Guest && r.Login != "":
		return "login:" + strings.ToLower(r.Login)
	}
	return ""
}

// Conference is safe for concurrent use.
type Conference struct {
	reg   *room.Registry
	media Media
	log   *slog.Logger

	// QualityEvery is how often connection quality is sampled. Set before serving.
	QualityEvery time.Duration
	// NotesStopGrace is how long a stopped notes bot may take to leave. Set before serving.
	NotesStopGrace time.Duration
	// RecordingStopGrace is how long a stopped recorder may take to upload and leave. Set before serving.
	RecordingStopGrace time.Duration
	// RejoinGrace is how long after a meeting ended its expired invitations and sessions still
	// let their people back, for a blip that dropped everyone at once. Set before serving.
	RejoinGrace time.Duration

	reactions map[string]bool  // emoji participants may send (ui.reactions)
	notes     *notes.Store     // meeting notes (notes.enabled); nil when off
	notesLive bool             // send each phrase to the room as it is recognised
	notesMu   sync.Mutex       // orders every room's notes state and the store's writes
	recMu     sync.Mutex       // orders every room's recording flag and the store's active mark; taken before c.mu, never while holding it
	recording *recording.Store // meeting recording (recording.enabled); nil when off
	meetings  *meetings.Store  // summaries of ended meetings; nil keeps none

	mu      sync.Mutex
	live    map[string]*liveRoom // slug -> members
	revoked map[string]time.Time // invitation ID -> when DropInvite revoked it; see revokedKeep
}

type liveRoom struct {
	room    *room.Room
	members map[string]*Member
	notes   notesRoom // guarded by Conference.notesMu
	// admitted holds the invitations and logins (JoinRequest.admitKey) admitted to the current
	// meeting; ended is when the last person left. Both guarded by Conference.mu.
	admitted map[string]bool
	ended    time.Time
}

// forgetEndedLocked clears the admissions of a meeting that ended more than RejoinGrace ago.
// Called with c.mu held.
func (c *Conference) forgetEndedLocked(lr *liveRoom) {
	if lr.ended.IsZero() {
		return
	}
	if _, active := lr.room.Meeting(); !active && time.Since(lr.ended) > c.RejoinGrace {
		lr.admitted, lr.ended = nil, time.Time{}
	}
}

// Member is one session in a live room.
type Member struct {
	id, name, slug string
	bot            bool        // a helper: the echo, a player, the notes bot or the recorder
	botKind        string      // room.BotEcho, BotPlayer, BotCamera, BotNotes or BotRecorder
	guest          bool        // admitted by an invitation: cannot switch the echo, notes or recording
	invite         string      // the guest's invitation ID, for DropInvite
	noVideo        bool        // declined other participants' cameras; guarded by Conference.mu
	stopping       bool        // notes bot or recorder told to stop; guarded by Conference.mu
	stoppedAtJoin  bool        // notes bot whose notes were switched off while it joined; guarded by Conference.mu
	stopTimer      *time.Timer // closes a stopped notes bot or recorder that does not leave; guarded by Conference.mu
	meeting        time.Time   // notes bot: start of the meeting it joined to take notes of
	client         string      // the page's id from join, if it sent one
	lastReaction   time.Time   // for the reaction rate limit; only the session's read loop uses it
	joined         time.Time
	conn           Conn
	peer           MediaPeer
	log            *slog.Logger
}

// Peer is the member's media connection.
func (m *Member) Peer() MediaPeer { return m.peer }

// Role is the helper kind, empty for a person.
func (m *Member) Role() string { return m.botKind }

// Log is the member's logger, labelled with its room and id.
func (m *Member) Log() *slog.Logger { return m.log }

// New returns a conference over the rooms of reg.
func New(reg *room.Registry, media Media, log *slog.Logger) *Conference {
	return &Conference{reg: reg, media: media, log: log, live: map[string]*liveRoom{}, QualityEvery: 3 * time.Second,
		NotesStopGrace: DefaultNotesStopGrace, RecordingStopGrace: DefaultRecordingStopGrace, RejoinGrace: DefaultRejoinGrace,
		reactions: set(config.DefaultReactions)}
}

// lookup returns the live state of a registered room, creating it on first use. Rooms can be
// created and deleted at runtime; a re-created slug gets fresh state.
func (c *Conference) lookup(slug string) (*liveRoom, bool) {
	r, ok := c.reg.Get(slug)
	c.mu.Lock()
	defer c.mu.Unlock()
	lr, have := c.live[slug]
	if !ok {
		// A deleted room keeps serving the sessions still inside it.
		return lr, have && len(lr.members) > 0
	}
	if !have || lr.room != r {
		lr = &liveRoom{room: r, members: map[string]*Member{}}
		c.live[slug] = lr
	}
	return lr, true
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Join admits a member and starts its media. It tells conn Joined before announcing the member to
// the room. On failure it returns ErrNoRoom, a *RefusedError, room.ErrBadPIN, room.ErrFull,
// ErrMedia, ErrWrite or the room's validation error.
func (c *Conference) Join(conn Conn, req JoinRequest) (*Member, error) {
	lr, ok := c.lookup(req.Room)
	if !ok {
		return nil, ErrNoRoom
	}
	bot := req.Role != ""
	switch {
	case req.Role == room.BotEcho && !lr.room.Echo():
		return nil, &RefusedError{"echo not requested in this room"}
	case (req.Role == room.BotPlayer || req.Role == room.BotCamera) && !lr.room.HasPlayer():
		return nil, &RefusedError{"no player configured for this room"}
	case req.Role == room.BotNotes && (c.notes == nil || !lr.room.Notes()):
		return nil, &RefusedError{"notes not requested in this room"}
	case req.Role == room.BotRecorder && (c.recording == nil || !lr.room.Recording() || c.hasActiveRecorder(lr)):
		return nil, &RefusedError{"recording not requested in this room"}
	}
	if !bot {
		c.mu.Lock()
		c.forgetEndedLocked(lr)
		admitted := lr.admitted[req.admitKey()]
		c.mu.Unlock()
		if req.Rejoin && !admitted {
			return nil, ErrNotAdmitted
		}
	}
	c.replaceClient(lr, req.Client)
	id := newID()
	var err error
	switch {
	case bot:
		err = lr.room.JoinBot(id, req.Name, req.Role)
	case req.Guest:
		err = lr.room.JoinGuest(id, req.Name)
	default:
		err = lr.room.Join(id, req.Name, req.PIN)
	}
	if err != nil {
		return nil, err
	}
	s := &Member{id: id, name: req.Name, slug: req.Room, bot: bot, botKind: req.Role, guest: req.Guest && !bot, invite: req.Invite, client: req.Client, joined: time.Now(), conn: conn, log: c.log.With("room", req.Room, "participant", id)}
	if req.Role == room.BotNotes {
		s.meeting, _ = lr.room.Meeting()
	}
	peer, err := c.media.NewPeer(id, conn, media.Handlers{OnPublisher: c.onPublisher, OnFailed: c.onFailed, TCPOnly: req.TCPOnly})
	if err != nil {
		lr.room.Leave(id)
		s.log.Error("new peer", "err", err)
		return nil, ErrMedia
	}
	s.peer = peer
	c.mu.Lock()
	if req.Guest && c.revokedLocked(req.Invite) {
		// DropInvite ran after this guest's invitation was checked and found nobody to disconnect.
		c.mu.Unlock()
		_ = peer.Close()
		lr.room.Leave(id)
		s.log.Info("invitation revoked during the join; guest refused")
		return nil, ErrRevoked
	}
	lr.members[id] = s
	if key := req.admitKey(); !bot && key != "" {
		if lr.admitted == nil {
			lr.admitted = map[string]bool{}
		}
		lr.admitted[key] = true
	}
	// Whether a notes bot came in after the notes were switched off: a stopNotes that ran before
	// this point missed it. One that runs after finds it among the members and stops it itself.
	notesOffAtJoin := req.Role == room.BotNotes && !lr.room.Notes()
	others := lr.membersExcept(id)
	c.mu.Unlock()
	// Subscribe before announcing the join, so the first offer already carries every existing track.
	sharer := lr.room.Sharer()
	for _, o := range others {
		if receives(s, o, media.KindAudio) {
			if err := peer.Subscribe(o.peer, media.KindAudio); err != nil && !errors.Is(err, media.ErrNoTrack) {
				s.log.Warn("subscribe audio", "from", o.id, "err", err)
			}
		}
		if o.id == sharer && receives(s, o, media.KindScreen) {
			if err := peer.Subscribe(o.peer, media.KindScreen); err != nil && !errors.Is(err, media.ErrNoTrack) {
				s.log.Warn("subscribe screen", "from", o.id, "err", err)
			}
		}
		if receives(s, o, media.KindCamera) {
			if err := peer.Subscribe(o.peer, media.KindCamera); err != nil && !errors.Is(err, media.ErrNoTrack) {
				s.log.Warn("subscribe camera", "from", o.id, "err", err)
			}
		}
	}
	metrics.Joins.WithLabelValues(lr.room.Slug(), "ok").Inc()
	joined := Joined{ID: id, Name: req.Name, Slug: lr.room.Slug(), RoomName: lr.room.Name()}
	if req.Role == room.BotRecorder {
		joined.Recorder = true
		joined.Meeting, _ = lr.room.Meeting()
	}
	if err := conn.Send(joined); err != nil {
		c.Leave(s)
		return nil, ErrWrite
	}
	s.log.Info("joined", "name", req.Name)
	c.broadcast(lr)
	peer.Negotiate()
	if notesOffAtJoin && !lr.room.Notes() {
		c.mu.Lock()
		s.stoppedAtJoin = true // it heard nothing while the notes were on
		c.mu.Unlock()
		c.stopNotes(lr) // switched off while this bot was joining, before it was a member to stop
	}
	if s.botKind == room.BotRecorder && !lr.room.Recording() {
		c.stopRecorders(lr) // switched off while this recorder was joining
	}
	return s, nil
}

func (lr *liveRoom) membersExcept(id string) []*Member {
	out := make([]*Member, 0, len(lr.members))
	for mid, s := range lr.members {
		if mid != id {
			out = append(out, s)
		}
	}
	return out
}

// Leave removes s from its room; a no-op once it has left.
func (c *Conference) Leave(s *Member) {
	lr, _ := c.lookup(s.slug)
	wasNotes := lr.room.Notes()
	wasRecording := lr.room.Recording()
	if !s.bot {
		// Before leaving the members, so a phrase of this person is never without a name.
		c.rememberSpeaker(lr, s, wasNotes)
	}
	c.mu.Lock()
	if _, ok := lr.members[s.id]; !ok {
		c.mu.Unlock()
		return
	}
	delete(lr.members, s.id)
	others := lr.membersExcept(s.id)
	c.mu.Unlock()
	if _, m, ended := lr.room.LeaveEnding(s.id); ended {
		c.mu.Lock()
		lr.ended = time.Now() // its admissions go after RejoinGrace
		c.mu.Unlock()
		c.saveMeeting(lr, s, m)
	}
	for _, o := range others {
		_ = o.peer.Unsubscribe(s.peer, media.KindAudio)
		_ = o.peer.Unsubscribe(s.peer, media.KindScreen)
		_ = o.peer.Unsubscribe(s.peer, media.KindCamera)
	}
	_ = s.peer.Close()
	s.log.Info("left")
	metrics.Leaves.WithLabelValues(s.slug).Inc()
	metrics.SessionSeconds.Observe(time.Since(s.joined).Seconds())
	c.dropBotsIfUnwanted(lr)
	switch {
	case s.botKind == room.BotNotes:
		c.notesBotLeft(lr, s)
	case wasNotes && !lr.room.Notes():
		c.stopNotes(lr) // the last person left
	}
	switch {
	case s.botKind == room.BotRecorder:
		c.recorderLeft(lr, s)
	case wasRecording && !lr.room.Recording():
		c.stopRecorders(lr) // the last person left
	}
	c.broadcast(lr)
}

// SetMeetings keeps the summary of every ended meeting in store. Call before serving.
func (c *Conference) SetMeetings(store *meetings.Store) { c.meetings = store }

// saveMeeting stores the summary of the room's meeting that s's leave has just ended.
func (c *Conference) saveMeeting(lr *liveRoom, s *Member, m room.Summary) {
	if c.meetings == nil {
		return
	}
	if err := c.meetings.Add(lr.room.Slug(), meetings.Summary{Start: m.Start, End: m.End, People: m.People}); err != nil {
		s.log.Error("save meeting summary", "err", err)
	}
}

// dropBotsIfUnwanted disconnects the echo helper once its room no longer wants it.
func (c *Conference) dropBotsIfUnwanted(lr *liveRoom) {
	if lr.room.Echo() {
		return
	}
	c.mu.Lock()
	var bots []*Member
	for _, o := range lr.members {
		if o.botKind == room.BotEcho { // a room's player stays
			bots = append(bots, o)
		}
	}
	c.mu.Unlock()
	for _, b := range bots {
		b.conn.Close("echo off")
	}
}

func (c *Conference) broadcast(lr *liveRoom) {
	msg := Roster{lr.room.Snapshot()}
	c.mu.Lock()
	members := lr.membersExcept("")
	c.mu.Unlock()
	for _, s := range members {
		if err := s.conn.Send(msg); err != nil {
			s.log.Warn("broadcast write", "err", err)
			s.conn.End()
		}
	}
}

// SetMuted records s's microphone state.
func (c *Conference) SetMuted(s *Member, muted bool) {
	lr, _ := c.lookup(s.slug)
	if lr.room.SetMuted(s.id, muted) {
		c.broadcast(lr)
	}
}

// SetCamera records whether s's camera is on.
func (c *Conference) SetCamera(s *Member, on bool) {
	lr, _ := c.lookup(s.slug)
	if lr.room.SetCamera(s.id, on) {
		c.broadcast(lr)
	}
}

// SetEcho asks for the room's echo helper or lets it go. Helpers and guests cannot switch it.
func (c *Conference) SetEcho(s *Member, on bool) {
	if s.bot || s.guest {
		return
	}
	lr, _ := c.lookup(s.slug)
	lr.room.SetEcho(on)
	c.dropBotsIfUnwanted(lr)
	c.broadcast(lr)
}

// DropInvite disconnects the guests who joined with invitation id (it was revoked) and returns
// how many there were. A guest whose join is still in progress is refused by Join with ErrRevoked.
// Call it after the store has revoked the invitation, so no new join can pass the check.
func (c *Conference) DropInvite(id string) int {
	if id == "" {
		return 0
	}
	c.mu.Lock()
	now := time.Now()
	for r, at := range c.revoked {
		if now.Sub(at) > revokedKeep {
			delete(c.revoked, r)
		}
	}
	if c.revoked == nil {
		c.revoked = map[string]time.Time{}
	}
	c.revoked[id] = now // a guest still joining with it is refused when it reaches the room
	var drop []*Member
	for _, lr := range c.live {
		for _, m := range lr.members {
			if m.guest && m.invite == id {
				drop = append(drop, m)
			}
		}
	}
	c.mu.Unlock()
	for _, m := range drop {
		m.log.Info("invitation revoked; guest disconnected")
		m.conn.Abort("invite revoked") // Close would wait for each guest's answer
	}
	return len(drop)
}

// revokedLocked reports whether DropInvite revoked invitation id within revokedKeep. Called with
// c.mu held.
func (c *Conference) revokedLocked(id string) bool {
	at, ok := c.revoked[id]
	return ok && time.Since(at) <= revokedKeep
}

// StartShare gives s the share lock, or tells it ShareBusy.
func (c *Conference) StartShare(s *Member) {
	lr, _ := c.lookup(s.slug)
	if err := lr.room.StartShare(s.id); err != nil {
		_ = s.conn.Send(ShareBusy{})
		return
	}
	c.broadcast(lr)
	if s.peer.Publishes(media.KindScreen) {
		c.subscribeAll(lr, s, media.KindScreen)
	}
}

// StopShare releases s's share lock and drops everyone's subscription to its screen.
func (c *Conference) StopShare(s *Member) {
	lr, _ := c.lookup(s.slug)
	if lr.room.StopShare(s.id) {
		c.mu.Lock()
		others := lr.membersExcept(s.id)
		c.mu.Unlock()
		for _, o := range others {
			_ = o.peer.Unsubscribe(s.peer, media.KindScreen)
		}
		c.broadcast(lr)
	}
}

func (c *Conference) subscribeAll(lr *liveRoom, from *Member, kind media.Kind) {
	c.mu.Lock()
	others := lr.membersExcept(from.id)
	c.mu.Unlock()
	for _, o := range others {
		if !receives(o, from, kind) {
			continue
		}
		if kind == media.KindCamera && c.declinesVideo(o) {
			continue
		}
		if err := o.peer.Subscribe(from.peer, kind); err != nil {
			o.log.Warn("subscribe", "from", from.id, "kind", kind.String(), "err", err)
		}
	}
}

func (c *Conference) findMember(id string) (*liveRoom, *Member) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, lr := range c.live {
		if s, ok := lr.members[id]; ok {
			return lr, s
		}
	}
	return nil, nil
}

// onPublisher runs when the first RTP of a kind arrives from a participant.
func (c *Conference) onPublisher(src media.Source, kind media.Kind) {
	lr, s := c.findMember(src.ID())
	if s == nil {
		return
	}
	if kind == media.KindScreen && lr.room.Sharer() != s.id {
		s.log.Warn("screen RTP without the share lock; ignored")
		return
	}
	c.subscribeAll(lr, s, kind)
}

func (c *Conference) onFailed(src media.Source) {
	_, s := c.findMember(src.ID())
	if s == nil {
		return
	}
	s.log.Warn("media failed; closing session")
	metrics.MediaFailed.Inc()
	s.conn.Fail()
}

// Members reports how many sessions the room has.
func (c *Conference) Members(slug string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lr, ok := c.live[slug]; ok {
		return len(lr.members)
	}
	return 0
}

// RoomLabel keeps metric labels bounded: slugs of unknown rooms collapse into one value.
func (c *Conference) RoomLabel(slug string) string {
	if _, ok := c.reg.Get(slug); ok {
		return slug
	}
	return "unknown"
}

// WatchQuality samples s's connection until ctx ends, publishes grade changes to the room and
// records the raw numbers in metrics.
func (c *Conference) WatchQuality(ctx context.Context, s *Member) {
	t := time.NewTicker(c.QualityEvery)
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
		lr, ok := c.lookup(s.slug)
		if ok && lr.room.SetQuality(s.id, grade) {
			s.log.Info("connection quality", "grade", grade, "up_loss", sample.UpLoss, "down_loss", sample.DownLoss,
				"rtt", sample.RTT.String(), "jitter", sample.Jitter.String())
			c.broadcast(lr)
		}
	}
}

func (c *Conference) declinesVideo(s *Member) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return s.noVideo
}

// ReceiveVideo drops or restores s's subscriptions to everyone's camera, for participants on a
// weak link who keep audio and the shared screen only. The notes bot hears audio only and the
// recorder's media is fixed, so both are ignored.
func (c *Conference) ReceiveVideo(s *Member, receive bool) {
	if s.botKind == room.BotNotes || s.botKind == room.BotRecorder {
		return
	}
	lr, _ := c.lookup(s.slug)
	lr.room.SetDeclinesVideo(s.id, !receive)
	c.mu.Lock()
	s.noVideo = !receive
	others := lr.membersExcept(s.id)
	c.mu.Unlock()
	for _, o := range others {
		var err error
		if receive {
			err = s.peer.Subscribe(o.peer, media.KindCamera)
		} else {
			err = s.peer.Unsubscribe(o.peer, media.KindCamera)
		}
		if err != nil && !errors.Is(err, media.ErrNoTrack) {
			s.log.Warn("camera subscription", "from", o.id, "receive", receive, "err", err)
		}
	}
	s.log.Info("receive video", "on", receive)
}

// replaceClient removes the earlier session of the same page from the room, if there is one:
// the page reconnected while its old connection had not been closed yet.
func (c *Conference) replaceClient(lr *liveRoom, client string) {
	if client == "" {
		return
	}
	c.mu.Lock()
	var old *Member
	for _, o := range lr.members {
		if o.client == client {
			old = o
			break
		}
	}
	c.mu.Unlock()
	if old == nil {
		return
	}
	old.log.Info("replaced by a new session of the same page")
	c.Leave(old) // at once, so it neither counts toward capacity nor appears in the snapshot
	// Close waits for the other side's answer; the new join must not wait for it.
	old.conn.Abort("replaced")
}

// reactionGap is the least time between two reactions of one participant; faster ones are dropped.
const reactionGap = 700 * time.Millisecond

// React shows a participant's emoji to everyone in the room, the sender included.
func (c *Conference) React(s *Member, emoji string) {
	if s.bot || !c.reactions[emoji] || time.Since(s.lastReaction) < reactionGap {
		return // a helper, an emoji outside the configured set, or too fast
	}
	s.lastReaction = time.Now()
	metrics.Reactions.WithLabelValues(emoji).Inc()
	lr, _ := c.lookup(s.slug)
	c.mu.Lock()
	members := lr.membersExcept("")
	c.mu.Unlock()
	msg := Reaction{From: s.id, Emoji: emoji}
	for _, o := range members {
		if err := o.conn.Send(msg); err != nil {
			o.log.Warn("reaction write", "err", err)
		}
	}
}

// SetReactions sets the emoji participants may send, from ui.reactions. Call before serving.
func (c *Conference) SetReactions(list []string) { c.reactions = set(list) }

func set(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, e := range list {
		m[e] = true
	}
	return m
}
