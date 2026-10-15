package sfu

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestPionLogsTagPeerAndCollapseRepeats(t *testing.T) {
	var buf bytes.Buffer
	f := newPionLogs(slog.New(slog.NewJSONHandler(&buf, nil)).With("peer", "p1"))
	now := time.Unix(0, 0)
	f.now = func() time.Time { return now }
	l := f.NewLogger("mux")

	l.Errorf("mux: failed to read from packetio.Buffer %s", "short buffer")
	for range 9 {
		now = now.Add(100 * time.Millisecond)
		l.Errorf("mux: failed to read from packetio.Buffer %s", "short buffer")
	}
	l.Warn("not logged")
	f.NewLogger("ice").Error("other scope")
	now = now.Add(pionRepeatEvery)
	l.Errorf("mux: failed to read from packetio.Buffer %s", "short buffer")

	var got []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		got = append(got, m)
	}
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(got), buf.String())
	}
	for _, m := range got {
		if m["peer"] != "p1" || m["level"] != "ERROR" {
			t.Errorf("line %v: want peer p1 at ERROR", m)
		}
	}
	if got[0]["msg"] != "mux: failed to read from packetio.Buffer short buffer" || got[0]["scope"] != "mux" || got[0]["repeated"] != nil {
		t.Errorf("first line %v", got[0])
	}
	if got[1]["scope"] != "ice" {
		t.Errorf("second line %v: want scope ice", got[1])
	}
	if got[2]["repeated"] != float64(9) {
		t.Errorf("third line %v: want repeated 9", got[2])
	}
}
