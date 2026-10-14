package botclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/pion/webrtc/v4"
)

// A minimal signaling server: expects a helper join, says joined, sends one real offer and
// checks the answer.
func TestServeJoinsAndAnswers(t *testing.T) {
	gotJoin := make(chan map[string]string, 1)
	gotAnswer := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		var join map[string]string
		if wsjson.Read(ctx, c, &join) != nil {
			return
		}
		gotJoin <- join
		_ = wsjson.Write(ctx, c, map[string]any{"type": "joined", "self": map[string]any{"id": "b", "name": join["name"], "muted": false}, "room": map[string]string{"slug": join["room"], "name": "R"}})
		server, _ := webrtc.NewPeerConnection(webrtc.Configuration{})
		defer server.Close()
		_, _ = server.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
		offer, _ := server.CreateOffer(nil)
		_ = server.SetLocalDescription(offer)
		_ = wsjson.Write(ctx, c, map[string]string{"type": "offer", "sdp": offer.SDP})
		for { // other messages (the camera flag) may come first
			var m map[string]any
			if wsjson.Read(ctx, c, &m) != nil {
				return
			}
			if m["type"] == "answer" {
				gotAnswer <- m["sdp"].(string)
				break
			}
		}
		<-ctx.Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), false)
	if err != nil {
		t.Fatal(err)
	}
	pc, _ := webrtc.NewPeerConnection(webrtc.Configuration{})
	defer pc.Close()
	joined := make(chan struct{})
	go func() {
		_ = Serve(ctx, conn, pc, "tv", "Видео", "player", func(send func(any) error) {
			close(joined)
			_ = send(map[string]any{"type": "camera", "on": true})
		})
	}()

	select {
	case j := <-gotJoin:
		if j["type"] != "join" || j["room"] != "tv" || j["role"] != "player" || j["name"] != "Видео" {
			t.Fatalf("join = %v", j)
		}
	case <-ctx.Done():
		t.Fatal("no join")
	}
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatal("onJoined not called")
	}
	select {
	case sdp := <-gotAnswer:
		if !strings.HasPrefix(sdp, "v=0") {
			t.Fatalf("answer = %q", sdp)
		}
	case <-ctx.Done():
		t.Fatal("no answer")
	}
}

// ServeWith hands the caller every message it does not handle itself, for example the notes
// bot's stop.
func TestServeWithPassesOtherMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		var join map[string]string
		if wsjson.Read(ctx, c, &join) != nil {
			return
		}
		_ = wsjson.Write(ctx, c, map[string]any{"type": "joined", "self": map[string]any{"id": "n", "name": join["name"], "muted": false}, "room": map[string]string{"slug": join["room"], "name": "R"}})
		_ = wsjson.Write(ctx, c, map[string]any{"type": "participants", "participants": []any{}, "sharer": ""})
		_ = wsjson.Write(ctx, c, map[string]string{"type": "stop"})
		<-ctx.Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), false)
	if err != nil {
		t.Fatal(err)
	}
	pc, _ := webrtc.NewPeerConnection(webrtc.Configuration{})
	defer pc.Close()
	got := make(chan string, 4)
	joined := make(chan struct{})
	go func() {
		_ = ServeWith(ctx, conn, pc, "ops", "Заметки", "notes", Options{
			OnJoined:  func(func(any) error) { close(joined) },
			OnMessage: func(typ string, data []byte) { got <- typ + " " + string(data) },
		})
	}()
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatal("OnJoined not called")
	}
	for _, want := range []string{"participants", "stop"} {
		select {
		case m := <-got:
			if !strings.HasPrefix(m, want+" {") {
				t.Fatalf("message = %q, want %s with its frame", m, want)
			}
		case <-ctx.Done():
			t.Fatalf("no %s", want)
		}
	}
}
