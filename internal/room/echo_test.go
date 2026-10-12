package room

import (
	"errors"
	"testing"
)

func TestEchoFlagAndBots(t *testing.T) {
	r := New("testing", "Testing", "1234", 3)
	if err := r.Join("a", "Ann", "1234"); err != nil {
		t.Fatal(err)
	}
	r.SetEcho(true)
	if !r.Snapshot().Echo {
		t.Fatal("echo flag not in snapshot")
	}
	if err := r.JoinBot("e", "Echo"); err != nil {
		t.Fatal(err)
	}
	snap := r.Snapshot()
	if len(snap.Participants) != 2 || !snap.Participants[1].Bot || snap.Participants[0].Bot {
		t.Fatalf("participants = %+v", snap.Participants)
	}
	// The last person leaving switches the echo off, even with the bot still inside.
	r.Leave("a")
	if r.Snapshot().Echo {
		t.Fatal("echo stays on in a room without people")
	}
}

func TestBotRespectsCapacity(t *testing.T) {
	r := New("testing", "Testing", "1234", 2)
	_ = r.Join("a", "Ann", "1234")
	_ = r.Join("b", "Bob", "1234")
	if err := r.JoinBot("e", "Echo"); !errors.Is(err, ErrFull) {
		t.Fatalf("err = %v, want ErrFull", err)
	}
}

func TestEchoRooms(t *testing.T) {
	g := NewRegistry()
	a := New("a", "A", "1234", 5)
	b := New("b", "B", "1234", 5)
	g.Add(a)
	g.Add(b)
	b.SetEcho(true)
	if got := g.EchoRooms(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("EchoRooms = %v", got)
	}
}

func TestSetQuality(t *testing.T) {
	r := New("testing", "Testing", "1234", 3)
	_ = r.Join("a", "Ann", "1234")
	if !r.SetQuality("a", "poor") {
		t.Fatal("first grade not reported as a change")
	}
	if r.SetQuality("a", "poor") {
		t.Fatal("same grade reported as a change")
	}
	if r.SetQuality("nobody", "good") {
		t.Fatal("unknown participant changed")
	}
	if got := r.Snapshot().Participants[0].Quality; got != "poor" {
		t.Fatalf("snapshot quality = %q", got)
	}
}
