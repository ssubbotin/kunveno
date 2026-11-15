package conference

import (
	"testing"
	"time"
)

func TestBotStateWindow(t *testing.T) {
	var w stateWindow
	t0 := time.Unix(1000, 0)
	for i := 0; i < BotStateRate; i++ {
		if !w.allow(t0.Add(time.Duration(i) * 10 * time.Millisecond)) {
			t.Fatalf("change %d refused", i)
		}
	}
	if w.allow(t0.Add(100 * time.Millisecond)) {
		t.Fatal("a sixth change within the second allowed")
	}
	if w.allow(t0.Add(999 * time.Millisecond)) {
		t.Fatal("allowed before the first change is a second old")
	}
	if !w.allow(t0.Add(time.Second)) {
		t.Fatal("refused once the first change is a second old")
	}
	if w.allow(t0.Add(time.Second + 5*time.Millisecond)) {
		t.Fatal("the second change is not a second old yet")
	}
	if !w.allow(t0.Add(time.Second + 10*time.Millisecond)) {
		t.Fatal("refused once the second change is a second old")
	}
}

func TestBotStateWindowWait(t *testing.T) {
	var w stateWindow
	t0 := time.Unix(1000, 0)
	if d := w.wait(t0); d != 0 {
		t.Fatalf("empty window waits %v", d)
	}
	for i := 0; i < BotStateRate; i++ {
		w.allow(t0.Add(time.Duration(i) * 10 * time.Millisecond))
	}
	if d := w.wait(t0.Add(300 * time.Millisecond)); d != 700*time.Millisecond {
		t.Fatalf("wait %v, want 700ms", d)
	}
	if d := w.wait(t0.Add(2 * time.Second)); d != 0 {
		t.Fatalf("wait %v after the window", d)
	}
}
