// Package botclient is the signaling side of the server-side helpers (echo, player, and the bots
// of the kunveno-notes and kunveno-recorder add-ons): join a room in a helper role and answer the
// server's offers on a pion peer connection, and (Rooms) keep a helper in every room the server
// lists on a control endpoint. It is public so that helpers built in other modules can use it;
// keep its API stable.
package botclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/pion/webrtc/v4"
)

type serverMsg struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	SDP     string `json:"sdp"`
}

// Dial opens the signaling WebSocket.
func Dial(ctx context.Context, url string, insecure bool) (*websocket.Conn, error) {
	opts := &websocket.DialOptions{}
	if insecure {
		opts.HTTPClient = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	}
	conn, _, err := websocket.Dial(ctx, url, opts)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(256 * 1024)
	return conn, nil
}

// Serve joins room as a helper (role "echo", "player", "camera", "notes" or "recorder") and answers every
// offer on pc until the connection or ctx ends. onJoined (may be nil) runs once the server has
// admitted us and gets a function for sending further messages.
func Serve(ctx context.Context, conn *websocket.Conn, pc *webrtc.PeerConnection, room, name, role string, onJoined func(send func(any) error)) error {
	return ServeWith(ctx, conn, pc, room, name, role, Options{OnJoined: onJoined})
}

// Options are the optional callbacks of ServeWith.
type Options struct {
	// OnJoined runs once the server has admitted us, with a function for sending further messages.
	// The function is safe for concurrent use.
	OnJoined func(send func(any) error)
	// OnJoinedFrame, when set, gets the raw joined frame before OnJoined runs; the recorder
	// reads the meeting start from it.
	OnJoinedFrame func(data []byte)
	// OnMessage gets every server message other than joined, error and offer: its type and the
	// raw frame. It runs on the read loop and must not block.
	OnMessage func(typ string, data []byte)
}

// ServeWith is Serve with every callback.
func ServeWith(ctx context.Context, conn *websocket.Conn, pc *webrtc.PeerConnection, room, name, role string, o Options) error {
	send := func(v any) error { return wsjson.Write(ctx, conn, v) }
	if err := send(map[string]string{"type": "join", "room": room, "name": name, "role": role}); err != nil {
		return err
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var msg serverMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		switch msg.Type {
		case "joined":
			if o.OnJoinedFrame != nil {
				o.OnJoinedFrame(data)
			}
			if o.OnJoined != nil {
				o.OnJoined(send)
			}
		case "error":
			return fmt.Errorf("server error %s: %s", msg.Code, msg.Message)
		case "offer":
			if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: msg.SDP}); err != nil {
				return err
			}
			answer, err := pc.CreateAnswer(nil)
			if err != nil {
				return err
			}
			gathered := webrtc.GatheringCompletePromise(pc)
			if err := pc.SetLocalDescription(answer); err != nil {
				return err
			}
			<-gathered
			if err := send(map[string]string{"type": "answer", "sdp": pc.LocalDescription().SDP}); err != nil {
				return err
			}
		default:
			if o.OnMessage != nil {
				o.OnMessage(msg.Type, data)
			}
		}
	}
}
