package roomstore

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ssubbotin/kunveno/internal/room"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "rooms.json")
	specs := []room.Spec{{Slug: "retro", Name: "Retro", PIN: "4321", MaxParticipants: 15}}
	if err := Save(path, specs); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, specs) {
		t.Fatalf("got %+v", got)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600 (the file holds PINs)", st.Mode().Perm())
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestSaveEmptyWritesEmptyList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rooms.json")
	if err := Save(path, nil); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}
