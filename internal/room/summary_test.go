package room

import (
	"testing"
	"time"
)

func TestLastMeetingCountsPeopleOnce(t *testing.T) {
	r := New("ops", "Ops", "1234", 10)
	clock := time.Date(2026, 10, 15, 11, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return clock }
	if _, ok := r.LastMeeting(); ok {
		t.Fatal("a summary before any meeting ended")
	}
	start := clock
	_ = r.Join("a", "Ann", "1234")
	_ = r.JoinBot("n", "Заметки", BotNotes) // a bot never counts
	_ = r.Join("b", "Bob", "1234")
	r.Leave("b")
	_ = r.Join("b2", " Bob ", "1234") // Bob reconnects under the same name
	if _, ok := r.LastMeeting(); ok {
		t.Fatal("a summary while the meeting goes on")
	}
	r.Leave("a")
	clock = clock.Add(27 * time.Minute)
	r.Leave("b2")
	s, ok := r.LastMeeting()
	if !ok || !s.Start.Equal(start) || !s.End.Equal(clock) || s.People != 2 {
		t.Fatalf("summary = %+v, %v; want start %v, end %v, 2 people", s, ok, start, clock)
	}
	r.Leave("n") // the bot leaving later changes nothing
	if s2, _ := r.LastMeeting(); s2 != s {
		t.Fatalf("summary changed when the bot left: %+v", s2)
	}
}

func TestLastMeetingResetsWithANewMeeting(t *testing.T) {
	r := New("ops", "Ops", "1234", 10)
	clock := time.Date(2026, 10, 15, 11, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return clock }
	_ = r.Join("a", "Ann", "1234")
	_ = r.Join("b", "Bob", "1234")
	r.Leave("a")
	r.Leave("b")
	first, _ := r.LastMeeting()
	clock = clock.Add(time.Hour)
	_ = r.Join("c", "Cid", "1234")
	if s, ok := r.LastMeeting(); !ok || s != first {
		t.Fatalf("during the next meeting the last summary = %+v, %v; want %+v", s, ok, first)
	}
	clock = clock.Add(5 * time.Minute)
	r.Leave("c")
	s, ok := r.LastMeeting()
	if !ok || s.People != 1 || !s.Start.Equal(clock.Add(-5*time.Minute)) || !s.End.Equal(clock) {
		t.Fatalf("second summary = %+v, %v", s, ok)
	}
}

func TestLastLeaveReportsItsMeetingEvenIfSomeoneJoinsRightAfter(t *testing.T) {
	r := New("ops", "Ops", "1234", 10)
	clock := time.Date(2026, 10, 15, 11, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return clock }
	start := clock
	_ = r.Join("a", "Ann", "1234")
	_ = r.Join("b", "Bob", "1234")
	_ = r.JoinBot("n", "Заметки", BotNotes)
	if _, _, ended := r.LeaveEnding("a"); ended {
		t.Fatal("ended while Bob is still in the meeting")
	}
	clock = clock.Add(27 * time.Minute)
	_, m, ended := r.LeaveEnding("b")
	end := clock
	clock = clock.Add(time.Second)
	_ = r.Join("c", "Cid", "1234") // the next meeting starts at once
	if !ended || !m.Start.Equal(start) || !m.End.Equal(end) || m.People != 2 {
		t.Fatalf("last leave = %+v, %v; want the 11:00 meeting of 2 people", m, ended)
	}
	if _, _, ended := r.LeaveEnding("n"); ended {
		t.Fatal("a bot leaving ended a meeting")
	}
	if _, _, ended := r.LeaveEnding("zz"); ended {
		t.Fatal("an unknown id ended a meeting")
	}
}
