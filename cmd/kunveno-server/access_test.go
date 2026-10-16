package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritableAcceptsAnOwnDirectory(t *testing.T) {
	if err := writable(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

// A data directory the server cannot write (Docker created it as root) is named with the fix.
func TestWritableNamesTheFix(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	err := writable(dir)
	if err == nil {
		t.Fatal("a read-only directory passed")
	}
	if want := "chown -R " + uidgid(); !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), dir) {
		t.Fatalf("error %q does not name %q and the directory", err, want)
	}
}

func TestUnreadableConfigNamesTheFix(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads anywhere")
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("rooms: []\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	_, err := loadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "chmod 640") {
		t.Fatalf("error %v does not name the fix", err)
	}
}
