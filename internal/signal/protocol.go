// Package signal implements the WebSocket signaling protocol described in the spec.
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
//     closed. A bot that loses its socket while the room still wants notes rejoins and sends what it
//     recognised meanwhile.
//
// The bot uses pkg/botclient for the join and the offer/answer exchange.
package signal

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pion/webrtc/v4"
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
)

const (
	MaxNameLen = 32
	RoleEcho   = "echo"
	RolePlayer = "player" // a media file in a loop
	RoleCamera = "camera" // a live RTSP feed, through the same player service
	RoleNotes  = "notes"  // the meeting-notes bot (kunveno-notes add-on)
	MediaTCP   = "tcp"
	MaxSDPLen  = 256 * 1024
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

// ClientMessage is the union of every client-to-server message.
type ClientMessage struct {
	Type      string                   `json:"type"`
	Room      string                   `json:"room,omitempty"`
	PIN       string                   `json:"pin,omitempty"`
	Name      string                   `json:"name,omitempty"`
	SDP       string                   `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit `json:"candidate,omitempty"`
	Muted     *bool                    `json:"muted,omitempty"`
	Action    string                   `json:"action,omitempty"`
	Role      string                   `json:"role,omitempty"`  // "echo" for the local echo helper
	Media     string                   `json:"media,omitempty"` // "tcp" after a failed UDP path
	// Client identifies the page: a join with the same client id replaces that page's earlier
	// session, so a page never shows up twice.
	Client  string `json:"client,omitempty"`
	On      *bool  `json:"on,omitempty"`      // echo, camera, notes
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
		switch {
		case !slugRe.MatchString(m.Room):
			return m, fmt.Errorf("%w: join.room", ErrBadMessage)
		case m.Client != "" && !clientRe.MatchString(m.Client):
			return m, fmt.Errorf("%w: join.client", ErrBadMessage)
		case m.Media != "" && m.Media != MediaTCP:
			return m, fmt.Errorf("%w: join.media", ErrBadMessage)
		case m.Role != "" && m.Role != RoleEcho && m.Role != RolePlayer && m.Role != RoleCamera && m.Role != RoleNotes:
			return m, fmt.Errorf("%w: join.role", ErrBadMessage)
		case m.Role == "" && (len(m.PIN) < 1 || len(m.PIN) > 16):
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
	case "echo", "camera", "notes":
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
	// BotKind: "echo", "player", "camera" or "notes" for helpers.
	BotKind string `json:"bot_kind,omitempty"`
	// Quality is the connection grade: "good", "fair" or "poor"; absent until measured.
	Quality string `json:"quality,omitempty"`
	Camera  bool   `json:"camera,omitempty"`
}

type roomInfo struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type joinedMsg struct {
	Type string          `json:"type"`
	Self ParticipantInfo `json:"self"`
	Room roomInfo        `json:"room"`
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
	Notes        bool              `json:"notes"` // the room takes meeting notes
}

func Joined(self ParticipantInfo, slug, name string) any {
	return joinedMsg{Type: "joined", Self: self, Room: roomInfo{Slug: slug, Name: name}}
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

func Participants(list []ParticipantInfo, sharer string, echo, notes bool) any {
	if list == nil {
		list = []ParticipantInfo{}
	}
	return participantsMsg{Type: "participants", Participants: list, Sharer: sharer, Echo: echo, Notes: notes}
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

// Stop asks the notes bot to send its last phrases and leave.
func Stop() any { return typeOnlyMsg{Type: "stop"} }

// NotesReady tells the people in a room that its notes can be downloaded.
func NotesReady() any { return typeOnlyMsg{Type: "notes_ready"} }

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
