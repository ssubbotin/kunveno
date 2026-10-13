// Package signal implements the WebSocket signaling protocol described in the spec.
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
	RolePlayer = "player"
	MediaTCP   = "tcp"
	MaxSDPLen  = 256 * 1024
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
	On      *bool  `json:"on,omitempty"`      // echo, camera
	Receive *bool  `json:"receive,omitempty"` // video: whether to receive cameras
	Emoji   string `json:"emoji,omitempty"`   // react: one of Reactions
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
		case m.Role != "" && m.Role != RoleEcho && m.Role != RolePlayer:
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
	case "echo", "camera":
		if m.On == nil {
			return m, fmt.Errorf("%w: %s.on", ErrBadMessage, m.Type)
		}
	case "video":
		if m.Receive == nil {
			return m, fmt.Errorf("%w: video.receive", ErrBadMessage)
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
	// BotKind: "echo" or "player" for helpers.
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

func Participants(list []ParticipantInfo, sharer string, echo bool) any {
	if list == nil {
		list = []ParticipantInfo{}
	}
	return participantsMsg{Type: "participants", Participants: list, Sharer: sharer, Echo: echo}
}
