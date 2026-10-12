// Command loadgen joins a kunveno room with N synthetic participants that send
// dummy Opus frames and count what they receive. It exercises the server the way
// browsers do: WebSocket join, answer every offer, publish on mid 0.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type serverMsg struct {
	Type string `json:"type"`
	Code string `json:"code"`
	SDP  string `json:"sdp"`
}

var (
	tracksReceived  atomic.Int64
	packetsReceived atomic.Int64
	joined          atomic.Int64
)

func main() {
	url := flag.String("url", "ws://127.0.0.1:8080/ws", "signaling URL")
	room := flag.String("room", "ops", "room slug")
	pin := flag.String("pin", "1234", "room pin")
	n := flag.Int("n", 14, "number of synthetic participants")
	duration := flag.Duration("duration", 2*time.Minute, "how long to stay")
	insecure := flag.Bool("insecure", false, "skip TLS verification")
	payload := flag.Int("payload", 3, "Opus payload bytes per packet (3 is a silence frame; 80+ looks like speech)")
	echoOn := flag.Bool("echo", false, "participant 0 switches the room's echo on")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()
	for i := 0; i < *n; i++ {
		go func(i int) {
			if err := participant(ctx, *url, *room, *pin, fmt.Sprintf("load-%02d", i), *insecure, *payload, *echoOn && i == 0); err != nil {
				log.Printf("participant %d: %v", i, err)
			}
		}(i)
		time.Sleep(200 * time.Millisecond)
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("done: joined=%d tracks=%d packets=%d", joined.Load(), tracksReceived.Load(), packetsReceived.Load())
			return
		case <-tick.C:
			log.Printf("joined=%d tracks=%d packets=%d", joined.Load(), tracksReceived.Load(), packetsReceived.Load())
		}
	}
}

func participant(ctx context.Context, url, room, pin, name string, insecure bool, payload int, echoOn bool) error {
	opts := &websocket.DialOptions{}
	if insecure {
		opts.HTTPClient = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	}
	conn, _, err := websocket.Dial(ctx, url, opts)
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(256 * 1024)

	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return err
	}
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(m)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	defer pc.Close()
	mic, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "mic", name)
	if err != nil {
		return err
	}
	if _, err := pc.AddTrack(mic); err != nil {
		return err
	}
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		tracksReceived.Add(1)
		for {
			if _, _, err := tr.ReadRTP(); err != nil {
				return
			}
			packetsReceived.Add(1)
		}
	})
	frame := make([]byte, max(payload, 3))
	copy(frame, []byte{0xfc, 0xff, 0xfe})
	go func() {
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = mic.WriteSample(media.Sample{Data: frame, Duration: 20 * time.Millisecond})
			}
		}
	}()

	if err := wsjson.Write(ctx, conn, map[string]string{"type": "join", "room": room, "pin": pin, "name": name}); err != nil {
		return err
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		var msg serverMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		switch msg.Type {
		case "joined":
			joined.Add(1)
			if echoOn {
				if err := wsjson.Write(ctx, conn, map[string]any{"type": "echo", "on": true}); err != nil {
					return err
				}
			}
		case "error":
			return fmt.Errorf("server error %s", msg.Code)
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
			if err := wsjson.Write(ctx, conn, map[string]string{"type": "answer", "sdp": pc.LocalDescription().SDP}); err != nil {
				return err
			}
		}
	}
}
