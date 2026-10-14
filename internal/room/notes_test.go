package room

import (
	"testing"
	"time"
)

func TestNotesFlagResetsWithoutPeople(t *testing.T) {
	r := New("ops", "Ops", "1234", 5)
	_ = r.Join("a", "Ann", "1234")
	r.SetNotes(true)
	if !r.Notes() || !r.Snapshot().Notes {
		t.Fatal("notes flag not set or not in the snapshot")
	}
	if err := r.JoinBot("n", "Заметки", BotNotes); err != nil {
		t.Fatal(err)
	}
	r.Leave("a")
	if r.Notes() {
		t.Fatal("notes stay on in a room without people")
	}
	if p := r.Snapshot().Participants[0]; !p.Bot || p.BotKind != BotNotes {
		t.Fatalf("bot = %+v", p)
	}
}

func TestMeetingBoundaries(t *testing.T) {
	r := New("ops", "Ops", "1234", 5)
	clock := time.Date(2026, 10, 14, 9, 37, 0, 0, time.UTC)
	r.now = func() time.Time { return clock }
	if start, active := r.Meeting(); active || !start.IsZero() {
		t.Fatal("a meeting in a room nobody entered")
	}
	_ = r.JoinBot("p", "Видео", BotPlayer) // a helper alone starts nothing
	if _, active := r.Meeting(); active {
		t.Fatal("a bot started a meeting")
	}
	_ = r.Join("a", "Ann", "1234")
	first, active := r.Meeting()
	if !active || !first.Equal(clock) {
		t.Fatalf("meeting = %v, %v", first, active)
	}
	clock = clock.Add(time.Minute)
	_ = r.Join("b", "Bob", "1234")
	if m, _ := r.Meeting(); !m.Equal(first) {
		t.Fatal("a second person started a new meeting")
	}
	r.Leave("a")
	r.Leave("b")
	if m, active := r.Meeting(); active || !m.Equal(first) {
		t.Fatalf("after the last person left: %v, %v; want the ended meeting's start", m, active)
	}
	clock = clock.Add(time.Hour)
	_ = r.Join("c", "Cid", "1234")
	if m, active := r.Meeting(); !active || !m.Equal(clock) {
		t.Fatalf("new meeting = %v, %v", m, active)
	}
}

func TestNotesRooms(t *testing.T) {
	g := NewRegistry()
	a := New("a", "A", "1234", 5)
	b := New("b", "B", "1234", 5)
	g.Add(a)
	g.Add(b)
	b.SetNotes(true)
	if got := g.NotesRooms(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("NotesRooms = %v", got)
	}
}
