package room

import (
	"errors"
	"testing"
)

func TestRegistryCreate(t *testing.T) {
	g := NewRegistry()
	g.Add(New("ops", "Operations", "1234", 15))

	r, err := g.Create("  Daily Standup  ", "432100")
	if err != nil {
		t.Fatal(err)
	}
	if r.Slug() != "daily-standup" || r.Name() != "Daily Standup" || !r.Created() || r.Capacity() != DefaultCapacity {
		t.Fatalf("got slug=%q name=%q created=%v cap=%d", r.Slug(), r.Name(), r.Created(), r.Capacity())
	}
	if got, ok := g.Get("daily-standup"); !ok || got != r {
		t.Fatal("created room not found")
	}
	if r2, _ := g.Create("Daily standup", "432100"); r2.Slug() != "daily-standup-2" {
		t.Fatalf("duplicate slug = %q", r2.Slug())
	}
	if r3, _ := g.Create("Планёрка", "432100"); r3.Slug() != "room" {
		t.Fatalf("non-latin slug = %q", r3.Slug())
	}
	if r4, _ := g.Create("OPS", "432100"); r4.Slug() != "ops-2" {
		t.Fatalf("slug clashing with a configured room = %q", r4.Slug())
	}
	for _, c := range []struct{ name, pin string }{{"", "432100"}, {"x", "12"}, {"x", "12345"}, {"x", "12345678901234567"}, {string(make([]byte, 49)), "432100"}} {
		if _, err := g.Create(c.name, c.pin); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%q, %q) err = %v, want ErrInvalid", c.name, c.pin, err)
		}
	}
	if n := len(g.CreatedSpecs()); n != 4 {
		t.Fatalf("created specs = %d, want 4", n)
	}
}

func TestRegistryDelete(t *testing.T) {
	g := NewRegistry()
	g.Add(New("ops", "Operations", "1234", 15))
	r, _ := g.Create("Retro", "432100")

	if err := g.Delete("ops", "1234"); !errors.Is(err, ErrFixed) {
		t.Fatalf("delete configured room: %v", err)
	}
	if err := g.Delete("nope", "1234"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete unknown: %v", err)
	}
	if err := g.Delete("retro", "0000"); !errors.Is(err, ErrBadPIN) {
		t.Fatalf("delete with wrong pin: %v", err)
	}
	_ = r.Join("a", "Ann", "432100")
	if err := g.Delete("retro", "432100"); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("delete non-empty: %v", err)
	}
	r.Leave("a")
	if err := g.Delete("retro", "432100"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := g.Get("retro"); ok {
		t.Fatal("room still listed")
	}
	if n := len(g.All()); n != 1 {
		t.Fatalf("All() = %d rooms, want 1", n)
	}
}

func TestRegistryRestoresCreated(t *testing.T) {
	g := NewRegistry()
	g.Restore(Spec{Slug: "retro", Name: "Retro", PIN: "432100", MaxParticipants: 15})
	r, ok := g.Get("retro")
	if !ok || !r.Created() {
		t.Fatal("restored room missing or not marked created")
	}
}

func TestRegistryRecordingRooms(t *testing.T) {
	g := NewRegistry()
	a, b := New("a", "A", "1234", 15), New("b", "B", "1234", 15)
	g.Add(a)
	g.Add(b)
	b.SetRecording(true)
	if got := g.RecordingRooms(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("got %v", got)
	}
	b.SetRecording(false)
	if got := g.RecordingRooms(); got == nil || len(got) != 0 {
		t.Fatalf("got %#v, want an empty list", got)
	}
}

func TestRegistryFacilitatorRooms(t *testing.T) {
	g := NewRegistry()
	a, b := New("a", "A", "1234", 15), New("b", "B", "1234", 15)
	g.Add(a)
	g.Add(b)
	b.SetFacilitator(true)
	if got := g.FacilitatorRooms(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("got %v", got)
	}
	b.SetFacilitator(false)
	if got := g.FacilitatorRooms(); got == nil || len(got) != 0 {
		t.Fatalf("got %#v, want an empty list", got)
	}
}
