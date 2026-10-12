// Package signal implements the WebSocket signaling protocol described in the spec.
package signal

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
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
)

const (
	MaxNameLen = 32
	RoleEcho   = "echo"
	MediaTCP   = "tcp"
	MaxSDPLen  = 256 * 1024
)

var slugRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

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
	On        *bool                    `json:"on,omitempty"`
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
		case m.Media != "" && m.Media != MediaTCP:
			return m, fmt.Errorf("%w: join.media", ErrBadMessage)
		case m.Role != "" && m.Role != RoleEcho:
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
	case "echo":
		if m.On == nil {
			return m, fmt.Errorf("%w: echo.on", ErrBadMessage)
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
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
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

func Offer(sdp string) any { return offerMsg{Type: "offer", SDP: sdp} }

func Participants(list []ParticipantInfo, sharer string, echo bool) any {
	if list == nil {
		list = []ParticipantInfo{}
	}
	return participantsMsg{Type: "participants", Participants: list, Sharer: sharer, Echo: echo}
}
