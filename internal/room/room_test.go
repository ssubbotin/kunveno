package room

import (
	"errors"
	"fmt"
	"testing"
)

func newRoom() *Room { return New("ops", "Operations", "1234", 3) }

func TestJoinChecksPINAndCapacity(t *testing.T) {
	r := newRoom()
	if err := r.Join("a", "Ann", "0000"); !errors.Is(err, ErrBadPIN) {
		t.Fatalf("bad pin: got %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := r.Join(fmt.Sprintf("p%d", i), "P", "1234"); err != nil {
			t.Fatalf("join %d: %v", i, err)
		}
	}
	if err := r.Join("p3", "P", "1234"); !errors.Is(err, ErrFull) {
		t.Fatalf("4th join: got %v, want ErrFull", err)
	}
	if r.Count() != 3 {
		t.Fatalf("count = %d, want 3", r.Count())
	}
}

func TestSnapshotKeepsJoinOrderAndMute(t *testing.T) {
	r := newRoom()
	_ = r.Join("b", "Bob", "1234")
	_ = r.Join("a", "Ann", "1234")
	if !r.SetMuted("a", true) {
		t.Fatal("SetMuted returned false for a known participant")
	}
	if r.SetMuted("zzz", true) {
		t.Fatal("SetMuted returned true for an unknown participant")
	}
	s := r.Snapshot()
	if len(s.Participants) != 2 || s.Participants[0].ID != "b" || s.Participants[1].ID != "a" {
		t.Fatalf("order wrong: %+v", s.Participants)
	}
	if !s.Participants[1].Muted || s.Participants[0].Muted {
		t.Fatalf("mute flags wrong: %+v", s.Participants)
	}
}

func TestShareLock(t *testing.T) {
	r := newRoom()
	_ = r.Join("a", "Ann", "1234")
	_ = r.Join("b", "Bob", "1234")
	if err := r.StartShare("a"); err != nil {
		t.Fatal(err)
	}
	if err := r.StartShare("a"); err != nil {
		t.Fatalf("re-request by the sharer must succeed: %v", err)
	}
	if err := r.StartShare("b"); !errors.Is(err, ErrShareBusy) {
		t.Fatalf("b start: got %v, want ErrShareBusy", err)
	}
	if err := r.StartShare("nobody"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown start: got %v", err)
	}
	if r.StopShare("b") {
		t.Fatal("non-sharer stop must be ignored")
	}
	if r.Sharer() != "a" {
		t.Fatalf("sharer = %q, want a", r.Sharer())
	}
	if !r.StopShare("a") {
		t.Fatal("sharer stop must succeed")
	}
	if r.Sharer() != "" {
		t.Fatal("lock not released")
	}
}

func TestLeaveReleasesShare(t *testing.T) {
	r := newRoom()
	_ = r.Join("a", "Ann", "1234")
	_ = r.Join("b", "Bob", "1234")
	_ = r.StartShare("a")
	if released := r.Leave("b"); released {
		t.Fatal("leaving non-sharer must not release")
	}
	if released := r.Leave("a"); !released {
		t.Fatal("leaving sharer must release")
	}
	if r.Count() != 0 || r.Sharer() != "" {
		t.Fatalf("state after leaves: count=%d sharer=%q", r.Count(), r.Sharer())
	}
	if released := r.Leave("a"); released {
		t.Fatal("double leave must be a no-op")
	}
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	reg.Add(New("a", "A", "1234", 2))
	reg.Add(New("b", "B", "1234", 2))
	if _, ok := reg.Get("zzz"); ok {
		t.Fatal("unknown slug found")
	}
	if r, ok := reg.Get("b"); !ok || r.Name() != "B" {
		t.Fatal("lookup failed")
	}
	if all := reg.All(); len(all) != 2 || all[0].Slug() != "a" {
		t.Fatalf("All order: %v", all)
	}
}

func TestRecordingFlagClearedWhenThePeopleLeave(t *testing.T) {
	r := New("ops", "Ops", "1234", 15)
	if err := r.Join("a", "Ann", "1234"); err != nil {
		t.Fatal(err)
	}
	if err := r.JoinBot("b", "Запись", BotRecorder); err != nil {
		t.Fatal(err)
	}
	r.SetRecording(true)
	if !r.Recording() || !r.Snapshot().Recording {
		t.Fatal("recording not set")
	}
	r.Leave("a")
	if r.Recording() {
		t.Fatal("recording kept with only the bot left")
	}
}

func TestJoinGuestNeedsNoPINButCountsTowardCapacity(t *testing.T) {
	r := New("ops", "Ops", "1234", 2)
	if err := r.JoinGuest("g1", "Гость"); err != nil {
		t.Fatal(err)
	}
	if p := r.Snapshot().Participants[0]; !p.Guest || p.Name != "Гость" {
		t.Fatalf("guest %+v", p)
	}
	_ = r.Join("a", "Ann", "1234")
	if err := r.JoinGuest("g2", "Второй"); !errors.Is(err, ErrFull) {
		t.Fatalf("over capacity: %v", err)
	}
}

func TestFacilitatorFlagClearedWhenThePeopleLeave(t *testing.T) {
	r := New("ops", "Ops", "1234", 15)
	if err := r.Join("a", "Ann", "1234"); err != nil {
		t.Fatal(err)
	}
	if err := r.JoinBot("b", "Ведущий", BotFacilitator); err != nil {
		t.Fatal(err)
	}
	r.SetFacilitator(true)
	if !r.Facilitator() || !r.Snapshot().Facilitator {
		t.Fatal("facilitator not set")
	}
	r.Leave("a")
	if r.Facilitator() {
		t.Fatal("facilitator kept with only the bot left")
	}
}
