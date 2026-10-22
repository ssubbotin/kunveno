// Package signal implements the WebSocket signaling protocol described in the spec.
//
// Sign-in (auth.enabled). With it on, a person's join needs the session cookie and the room's PIN;
// the name comes from the account, not from the join. Without a session the join is refused with
// auth_required. A guest joins with {"type":"join","room":slug,"invite":<token from the link>,
// "name":...} and no PIN; an unknown, expired or revoked invitation, or one for another room, is
// refused with bad_invite, which counts toward the same per-address limit as bad_pin. Guests are
// marked "guest":true in the participants list and cannot switch the echo, notes or recording. A
// revoked invitation disconnects its guests. Every reconnect is a fresh join, so an expired (not
// revoked) invitation and a session expired at most one lifetime ago still let back the guests and
// employees admitted to the room's current meeting, until a minute after it ends; anyone else gets
// bad_invite or auth_required. With sign-in off a join with "invite" is a bad_message. Helpers (a
// role, over loopback) join as before either way.
//
// The notes bot contract. The meeting-notes bot (the commercial kunveno-notes add-on, built in
// another repository) depends on this exact protocol; treat it as a stable interface and change
// it only together with the bot:
//
//   - GET /internal/notes (loopback only, never proxied by nginx) returns a JSON list of the slugs
//     of rooms that want notes; the bot polls it every 2 s. It is empty while notes.enabled is off.
//   - The bot joins each listed room over loopback with {"type":"join","room":slug,"name":...,
//     "role":"notes"} (no PIN); the join is refused unless the room wants notes. It sends no media
//     and receives every person's audio track (stream id = participant id), no video and no other
//     helper's audio.
//   - For each recognised phrase it sends {"type":"phrase","speaker":<participant id>,
//     "start":<unix ms>,"end":<unix ms>,"text":...}. A malformed frame (wrong field types, a missing
//     speaker, start or end) is a bad_message: the server closes the socket, as for any other
//     malformed frame. A well-formed phrase whose content is invalid is ignored and the socket stays
//     open. Valid content: text of 1..2000 characters after every whitespace run (newlines and tabs
//     included) is collapsed to a single space and the ends are trimmed, start positive and not
//     after end, end at most 60 s ahead of the server's clock. Phrases of a speaker unknown to the
//     server are dropped; people who left during the meeting stay known.
//   - When the room no longer wants notes the server sends {"type":"stop"}. The bot then finishes
//     its pending phrases, sends them and {"type":"leave"}. A bot still there 10 s after the stop is
//     closed. The notes are ready only after the bot has left (or the grace closed it).
//   - A bot that loses its socket while the room still wants notes rejoins and sends what it
//     recognised meanwhile. A bot is bound to the meeting it joined and cannot write into a later
//     one.
//
// The bot uses pkg/botclient for the join and the offer/answer exchange.
//
// Meeting summaries (notes.summary, with notes.enabled). The summary service of the same add-on
// writes a summary of each meeting's ready notes; it talks to the server over HTTP only, loopback
// only, never proxied by nginx:
//
//   - GET /internal/notes/summaries lists [{"slug","end"}]: ready notes without a summary that the
//     service has not given up on; end is the notes' end in Unix ms and names those notes. Empty
//     while summaries are off.
//   - GET /internal/notes/summaries/{slug}?end=<ms> returns {"room","start","end","notes"}, the
//     notes as Markdown with Russian labels.
//   - PUT /internal/notes/summaries/{slug}?end=<ms> stores the summary (Markdown, UTF-8, not blank,
//     at most 256 KiB): 204, 400 for a bad body.
//   - POST /internal/notes/summaries/{slug}/failed?end=<ms> records that the service gave up: 204.
//     The room then shows the summary as failed until the server restarts.
//   - Any of them answers 409 when the room's ready notes did not end at end: they were reopened,
//     replaced by a new meeting, deleted or expired. The service drops such work silently.
//
// The server keeps the summary in a file of its own next to the notes, for as long as the notes
// would be kept; deleting the notes keeps it, and a new meeting in the room or a deleted room removes
// it. A person may delete the summary alone: while its notes stay they are never listed again and an
// upload for them gets 409.
//
// The recording bot contract (the commercial kunveno-recorder add-on), stable like the notes one:
//
//   - GET /internal/recording (loopback only) returns the slugs of rooms that want a recording;
//     empty while recording.enabled is off. The bot polls it every 2 s.
//   - The bot joins with {"type":"join","room":slug,"name":...,"role":"recorder"} (no PIN). The
//     join is refused unless the room wants a recording, and while another recorder of the room
//     that was not told to stop is there. The joined message carries "meeting": the start of the
//     meeting (RFC 3339), which the upload names. The bot sends no media and receives every
//     person's audio (stream id = participant id), the current sharer's screen and every
//     person's camera (stream id = participant id + "/camera"); nothing from other helpers. A
//     camera switched off stops sending; the participants message says whose camera is on.
//   - When the room no longer wants a recording the server sends {"type":"stop"}. The bot finishes
//     the file, uploads it with PUT /internal/recording/<slug> (loopback only, body video/mp4,
//     header X-Kunveno-Meeting: the meeting start from joined, header X-Kunveno-Part: when this
//     bot's part began, RFC 3339) and sends {"type":"leave"}. A bot still there 60 s after the
//     stop is closed; its upload is still accepted.
//   - Every part of a meeting is kept: a recording switched off and on again gives the meeting a
//     second part. An upload of the same part replaces that part only, so a recorder that recorded
//     nothing (no audio and no screen received, for example stopped right after joining) must not
//     upload. Without X-Kunveno-Part (a recorder of the previous release) the part is the meeting's
//     start, so its uploads replace one another.
//   - Upload answers: 204 stored (the room hears recording_ready), 400 no or bad meeting or part
//     header, 404 recording off or no such room, 409 a meeting the room neither runs now nor
//     lists, 413 larger than recording.max_size.
//
// The facilitator bot contract (the commercial kunveno-facilitator add-on), stable like the others:
//
//   - The switch exists only with facilitator.enabled and only in the rooms listed in
//     facilitator.rooms; there the joined message carries "room":{...,"facilitator":true}. A person
//     (never a guest or a helper) switches it with {"type":"facilitator","on":true|false}; the
//     participants message says whether it is on ("facilitator"). Elsewhere the switch is ignored.
//   - GET /internal/facilitator (loopback only) returns the slugs of rooms that want the
//     facilitator; empty while facilitator.enabled is off. The bot polls it every 2 s.
//   - The bot joins with {"type":"join","room":slug,"name":...,"role":"facilitator"} (no PIN);
//     its name is the one people see and the one its phrases carry in the notes. The join is
//     refused unless the room offers the facilitator and wants it, and while another facilitator
//     of the room that was not told to stop is there. It may send audio (its speech, heard by
//     everyone, the notes bot and the recorder included) and receives every person's audio
//     (stream id = participant id), no video and nothing from other helpers.
//   - When the room no longer wants the facilitator (switched off, or the last person left) the
//     server sends {"type":"stop"}. The bot stops speaking and sends {"type":"leave"}. A bot still
//     there 5 s after the stop is closed.
package signal

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ssubbotin/kunveno/internal/media"
)

// ErrBadMessage wraps every validation failure of a client message.
var ErrBadMessage = errors.New("bad message")

// Error codes sent to clients.
const (
	CodeBadMessage  = "bad_message"
	CodeBadRoom     = "bad_room"
	CodeBadPIN      = "bad_pin"
	CodeRoomFull    = "room_full"
	CodeNotJoined   = "not_joined"
	CodeShareBusy   = "share_busy"
	CodeMediaFailed = "media_failed"
	// CodeTooManyAttempts: too many wrong PINs from this address; retry_after says when to try again.
	CodeTooManyAttempts = "too_many_attempts"
	// CodeAuthRequired: sign-in is on and the join came without a session.
	CodeAuthRequired = "auth_required"
	// CodeBadInvite: the invitation is unknown, for another room, expired or revoked.
	CodeBadInvite = "bad_invite"
)

const (
	MaxNameLen   = 32
	RoleEcho     = "echo"
	RolePlayer   = "player"   // a media file in a loop
	RoleCamera   = "camera"   // a live RTSP feed, through the same player service
	RoleNotes    = "notes"    // the meeting-notes bot (kunveno-notes add-on)
	RoleRecorder = "recorder" // the meeting recording bot (kunveno-recorder add-on)
	// RoleFacilitator is the voice stand-up facilitator (kunveno-facilitator add-on).
	RoleFacilitator = "facilitator"
	MediaTCP        = "tcp"
	MaxSDPLen       = 256 * 1024
)

// Phrase limits of the notes bot contract.
const (
	MaxPhraseLen   = 2000             // characters after normalising whitespace
	MaxPhraseAhead = 60 * time.Second // how far in the future a phrase may end
)

var (
	slugRe   = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	clientRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
)

// RestartReasons are the reasons a page may give for dropping its previous session.
var RestartReasons = map[string]bool{
	"ws_closed": true, "media_disconnected": true, "media_failed": true,
	"connect_timeout": true, "media_error": true, "rtc_error": true,
}

// MaxRestartAfter is the longest session length (seconds) a restart report may claim.
const MaxRestartAfter = 86400

// Restart says why a page dropped its previous session; see ClientMessage.Restart.
type Restart struct {
	Reason    string `json:"reason"`
	After     int    `json:"after"`
	Transport string `json:"transport"`
}

// UnmarshalJSON never fails: a malformed object (wrong types, not an object) becomes an invalid
// Restart, which ParseClient drops, so a bad report cannot refuse the join.
func (r *Restart) UnmarshalJSON(data []byte) error {
	var raw struct {
		Reason    any `json:"reason"`
		After     any `json:"after"`
		Transport any `json:"transport"`
	}
	*r = Restart{After: -1, Transport: "?"}
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	if v, ok := raw.Reason.(string); ok {
		r.Reason = v
	}
	if v, ok := raw.After.(float64); ok && v == float64(int(v)) && v >= 0 && v <= MaxRestartAfter {
		r.After = int(v)
	}
	if v, ok := raw.Transport.(string); ok {
		r.Transport = v
	}
	if raw.Transport == nil {
		r.Transport = ""
	}
	return nil
}

func (r *Restart) valid() bool {
	return r != nil && RestartReasons[r.Reason] && r.After >= 0 && r.After <= MaxRestartAfter &&
		(r.Transport == "" || r.Transport == "udp" || r.Transport == "tcp")
}

// ClientMessage is the union of every client-to-server message.
type ClientMessage struct {
	Type      string           `json:"type"`
	Room      string           `json:"room,omitempty"`
	PIN       string           `json:"pin,omitempty"`
	Name      string           `json:"name,omitempty"`
	SDP       string           `json:"sdp,omitempty"`
	Candidate *media.Candidate `json:"candidate,omitempty"`
	Muted     *bool            `json:"muted,omitempty"`
	Action    string           `json:"action,omitempty"`
	Role      string           `json:"role,omitempty"`  // "echo" for the local echo helper
	Media     string           `json:"media,omitempty"` // "tcp" after a failed UDP path
	// Invite is a guest's invitation token from the link (sign-in on); it replaces the PIN.
	Invite string `json:"invite,omitempty"`
	// Restart is sent by a page that rejoins after its own connection dropped (never on a plain
	// first join or after the person left; also after a failed first attempt): {"restart":{"reason":"media_disconnected","after":27,
	// "transport":"udp"}}. reason is one of RestartReasons (ws_closed: the signaling socket closed;
	// media_disconnected, media_failed: the peer connection stayed disconnected past the grace or
	// failed; connect_timeout: it never connected; media_error: the server sent media_failed;
	// rtc_error: the peer connection could not be created), after is how many seconds the dropped
	// session lived (0..86400) and transport is its media path ("", "udp" or "tcp"). The server logs
	// it and counts it; an invalid object is dropped without refusing the join (ParseClient sets
	// Restart to nil).
	Restart *Restart `json:"restart,omitempty"`
	// Client identifies the page: a join with the same client id replaces that page's earlier
	// session, so a page never shows up twice.
	Client  string `json:"client,omitempty"`
	On      *bool  `json:"on,omitempty"`      // echo, camera, notes, recording, facilitator
	Receive *bool  `json:"receive,omitempty"` // video: whether to receive cameras
	Emoji   string `json:"emoji,omitempty"`   // react: one of Reactions

	// phrase, from the notes bot only
	Speaker string `json:"speaker,omitempty"` // the speaking participant's id
	Start   *int64 `json:"start,omitempty"`   // unix ms
	End     *int64 `json:"end,omitempty"`     // unix ms
	Text    string `json:"text,omitempty"`
}

// ParseClient decodes and validates one client frame.
func ParseClient(data []byte) (ClientMessage, error) {
	var m ClientMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("%w: %v", ErrBadMessage, err)
	}
	switch m.Type {
	case "join":
		m.Name = strings.TrimSpace(m.Name)
		if !m.Restart.valid() {
			m.Restart = nil
		}
		switch {
		case !slugRe.MatchString(m.Room):
			return m, fmt.Errorf("%w: join.room", ErrBadMessage)
		case m.Client != "" && !clientRe.MatchString(m.Client):
			return m, fmt.Errorf("%w: join.client", ErrBadMessage)
		case m.Media != "" && m.Media != MediaTCP:
			return m, fmt.Errorf("%w: join.media", ErrBadMessage)
		case m.Role != "" && m.Role != RoleEcho && m.Role != RolePlayer && m.Role != RoleCamera && m.Role != RoleNotes && m.Role != RoleRecorder && m.Role != RoleFacilitator:
			return m, fmt.Errorf("%w: join.role", ErrBadMessage)
		case m.Invite != "" && (m.Role != "" || m.PIN != "" || len(m.Invite) > 64):
			return m, fmt.Errorf("%w: join.invite", ErrBadMessage)
		case m.Role == "" && m.Invite == "" && (len(m.PIN) < 1 || len(m.PIN) > 16):
			return m, fmt.Errorf("%w: join.pin", ErrBadMessage)
		case m.Name == "" || utf8.RuneCountInString(m.Name) > MaxNameLen:
			return m, fmt.Errorf("%w: join.name", ErrBadMessage)
		}
	case "answer":
		if m.SDP == "" || len(m.SDP) > MaxSDPLen {
			return m, fmt.Errorf("%w: answer.sdp", ErrBadMessage)
		}
	case "candidate":
		// An empty candidate string is the end-of-candidates marker (Firefox sends it); it is valid.
		if m.Candidate == nil {
			return m, fmt.Errorf("%w: candidate", ErrBadMessage)
		}
	case "echo", "camera", "notes", "recording", "facilitator":
		if m.On == nil {
			return m, fmt.Errorf("%w: %s.on", ErrBadMessage, m.Type)
		}
	case "video":
		if m.Receive == nil {
			return m, fmt.Errorf("%w: video.receive", ErrBadMessage)
		}
	case "phrase":
		// The shape only; the content is checked by validPhrase, and a bad phrase is ignored.
		if m.Speaker == "" || len(m.Speaker) > 64 || m.Start == nil || m.End == nil {
			return m, fmt.Errorf("%w: phrase", ErrBadMessage)
		}
	case "react":
		// The configured set is checked by the handler; here only the shape.
		if m.Emoji == "" || len(m.Emoji) > 16 {
			return m, fmt.Errorf("%w: react.emoji", ErrBadMessage)
		}
	case "mute":
		if m.Muted == nil {
			return m, fmt.Errorf("%w: mute.muted", ErrBadMessage)
		}
	case "share":
		if m.Action != "start" && m.Action != "stop" {
			return m, fmt.Errorf("%w: share.action", ErrBadMessage)
		}
	case "leave":
	default:
		return m, fmt.Errorf("%w: type %q", ErrBadMessage, m.Type)
	}
	return m, nil
}

// ParticipantInfo is the wire form of room.Participant.
type ParticipantInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Muted bool   `json:"muted"`
	Bot   bool   `json:"bot,omitempty"`
	// BotKind: "echo", "player", "camera", "notes", "recorder" or "facilitator" for helpers.
	BotKind string `json:"bot_kind,omitempty"`
	// Quality is the connection grade: "good", "fair" or "poor"; absent until measured.
	Quality string `json:"quality,omitempty"`
	Camera  bool   `json:"camera,omitempty"`
	Guest   bool   `json:"guest,omitempty"` // joined by an invitation link
}

type roomInfo struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	// Facilitator: the facilitator switch is offered in this room (facilitator.rooms).
	Facilitator bool `json:"facilitator,omitempty"`
}

type joinedMsg struct {
	Type string          `json:"type"`
	Self ParticipantInfo `json:"self"`
	Room roomInfo        `json:"room"`
	// Meeting is set for a recorder only: the start of the meeting, RFC 3339 with fractional seconds (RFC3339Nano).
	Meeting string `json:"meeting,omitempty"`
}

type errorMsg struct {
	Type       string `json:"type"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	RetryAfter int    `json:"retry_after,omitempty"` // seconds, for too_many_attempts
}

type offerMsg struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

type participantsMsg struct {
	Type         string            `json:"type"`
	Participants []ParticipantInfo `json:"participants"`
	Sharer       string            `json:"sharer"`
	Echo         bool              `json:"echo"`
	Notes        bool              `json:"notes"`       // the room takes meeting notes
	Recording    bool              `json:"recording"`   // the room's meeting is being recorded
	Facilitator  bool              `json:"facilitator"` // the room wants the facilitator
}

func Joined(self ParticipantInfo, slug, name string) any {
	return joinedMsg{Type: "joined", Self: self, Room: roomInfo{Slug: slug, Name: name}}
}

// JoinedWithFacilitator is joined in a room that offers the facilitator switch.
func JoinedWithFacilitator(self ParticipantInfo, slug, name string) any {
	return joinedMsg{Type: "joined", Self: self, Room: roomInfo{Slug: slug, Name: name, Facilitator: true}}
}

// JoinedRecorder is joined for a recorder: it also carries the start of the meeting.
func JoinedRecorder(self ParticipantInfo, slug, name string, meeting time.Time) any {
	return joinedMsg{Type: "joined", Self: self, Room: roomInfo{Slug: slug, Name: name}, Meeting: meeting.UTC().Format(time.RFC3339Nano)}
}

func ErrorMsg(code, message string) any {
	return errorMsg{Type: "error", Code: code, Message: message}
}

// RetryLater is the too_many_attempts error with the wait in whole seconds (at least 1).
func RetryLater(wait time.Duration) any {
	secs := int((wait + time.Second - 1) / time.Second)
	return errorMsg{Type: "error", Code: CodeTooManyAttempts, Message: "too many wrong PINs; try again later", RetryAfter: max(secs, 1)}
}

func Offer(sdp string) any { return offerMsg{Type: "offer", SDP: sdp} }

type reactionMsg struct {
	Type  string `json:"type"`
	From  string `json:"from"`
	Emoji string `json:"emoji"`
}

// Reaction tells the room that a participant sent an emoji; it is not part of the room state.
func Reaction(from, emoji string) any { return reactionMsg{Type: "reaction", From: from, Emoji: emoji} }

func Participants(list []ParticipantInfo, sharer string, echo, notes, recording, facilitator bool) any {
	if list == nil {
		list = []ParticipantInfo{}
	}
	return participantsMsg{Type: "participants", Participants: list, Sharer: sharer, Echo: echo, Notes: notes, Recording: recording, Facilitator: facilitator}
}

// validPhrase checks a phrase's content: text of 1..MaxPhraseLen characters once every whitespace
// run is collapsed to one space (so no newline reaches the Markdown), a positive start not after
// the end, and an end at most MaxPhraseAhead after now.
func validPhrase(m ClientMessage, now time.Time) (text string, start, end time.Time, ok bool) {
	text = strings.Join(strings.Fields(m.Text), " ")
	if n := utf8.RuneCountInString(text); n < 1 || n > MaxPhraseLen || m.Start == nil || m.End == nil || *m.Start <= 0 || *m.Start > *m.End {
		return "", time.Time{}, time.Time{}, false
	}
	start, end = time.UnixMilli(*m.Start), time.UnixMilli(*m.End)
	if end.After(now.Add(MaxPhraseAhead)) {
		return "", time.Time{}, time.Time{}, false
	}
	return text, start, end, true
}

type typeOnlyMsg struct {
	Type string `json:"type"`
}

// Stop asks the notes bot to send its last phrases and leave, the recorder to upload its file and
// leave, and the facilitator to stop speaking and leave.
func Stop() any { return typeOnlyMsg{Type: "stop"} }

// NotesReady tells the people in a room that its notes can be downloaded.
func NotesReady() any { return typeOnlyMsg{Type: "notes_ready"} }

// RecordingReady tells the people in a room that its recording can be downloaded.
func RecordingReady() any { return typeOnlyMsg{Type: "recording_ready"} }

type livePhraseMsg struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Start int64  `json:"start"` // unix ms
	Text  string `json:"text"`
}

// LivePhrase shows a recognised phrase in the room (notes.live).
func LivePhrase(name string, start int64, text string) any {
	return livePhraseMsg{Type: "phrase", Name: name, Start: start, Text: text}
}
