package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ssubbotin/kunveno/internal/config"
)

// A stack without player rooms never transcodes: an unusable cache must not stop the player, which
// then sits idle as before. With a file player it does.
func TestCacheIsCheckedOnlyForFilePlayers(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	ro := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(ro, "player-cache")
	if err := checkCache(nil, cache); err != nil {
		t.Fatalf("no player rooms: %v", err)
	}
	feed := []config.Room{{Slug: "cam", Player: &config.Player{URL: "rtsp://camera"}}}
	if err := checkCache(feed, cache); err != nil {
		t.Fatalf("only an RTSP feed (no cache): %v", err)
	}
	file := []config.Room{{Slug: "tv", Player: &config.Player{File: "film.mkv"}}}
	if err := checkCache(file, cache); err == nil {
		t.Fatal("a file player with an unwritable cache passed")
	}
}
