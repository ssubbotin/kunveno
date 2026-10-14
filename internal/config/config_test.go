package config

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const good = `
http:
  listen: 127.0.0.1:8080
media:
  udp_port: 7000
  advertise_ip: 10.8.0.1
rooms:
  - slug: ops
    name: Operations
    pin: "1234"
  - slug: dev
    name: Development
    pin: "5678"
    max_participants: 20
  - slug: all-hands
    name: All hands
    pin: "9999"
`

func TestParseAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Media.ShareMaxKbps != 2500 {
		t.Errorf("share_max_kbps default = %d, want 2500", cfg.Media.ShareMaxKbps)
	}
	if cfg.Rooms[0].MaxParticipants != 15 {
		t.Errorf("ops max = %d, want 15", cfg.Rooms[0].MaxParticipants)
	}
	if cfg.Rooms[1].MaxParticipants != 20 {
		t.Errorf("dev max = %d, want 20", cfg.Rooms[1].MaxParticipants)
	}
}

func TestParseMinimalDefaults(t *testing.T) {
	cfg, err := Parse([]byte("media:\n  advertise_ip: 10.8.0.1\nrooms:\n  - {slug: a, name: A, pin: \"0000\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Title != "kunveno" {
		t.Errorf("ui.title default = %q, want kunveno", cfg.UI.Title)
	}
	if cfg.Media.TCPPort != 7000 {
		t.Errorf("tcp_port default = %d, want 7000", cfg.Media.TCPPort)
	}
	if cfg.RoomsFile != "/var/lib/kunveno/rooms.json" {
		t.Errorf("rooms_file default = %q", cfg.RoomsFile)
	}
	if cfg.HTTP.Listen != "127.0.0.1:8080" || cfg.Media.UDPPort != 7000 {
		t.Errorf("defaults: listen=%q udp=%d", cfg.HTTP.Listen, cfg.Media.UDPPort)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct{ name, yaml, want string }{
		{"bad slug", "media: {advertise_ip: 10.8.0.1}\nrooms: [{slug: Ops!, name: A, pin: \"1234\"}]\n", "slug"},
		{"dup slug", "media: {advertise_ip: 10.8.0.1}\nrooms: [{slug: a, name: A, pin: \"1234\"}, {slug: a, name: B, pin: \"1234\"}]\n", "duplicate"},
		{"short pin", "media: {advertise_ip: 10.8.0.1}\nrooms: [{slug: a, name: A, pin: \"12\"}]\n", "pin"},
		{"bad ip", "media: {advertise_ip: nope}\nrooms: [{slug: a, name: A, pin: \"1234\"}]\n", "advertise_ip"},
		{"max too big", "media: {advertise_ip: 10.8.0.1}\nrooms: [{slug: a, name: A, pin: \"1234\", max_participants: 51}]\n", "max_participants"},
		{"title too long", "ui: {title: \"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\"}\nmedia: {advertise_ip: 10.8.0.1}\nrooms: [{slug: a, name: A, pin: \"1234\"}]\n", "ui.title"},
		{"bad tcp port", "media: {advertise_ip: 10.8.0.1, tcp_port: 70000}\nrooms: [{slug: a, name: A, pin: \"1234\"}]\n", "tcp_port"},
		{"numeric pin stays string", "media: {advertise_ip: 10.8.0.1}\nrooms: [{slug: a, name: A, pin: 0012}]\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := Parse([]byte(c.yaml))
			if c.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if cfg.Rooms[0].PIN != "0012" {
					t.Errorf("pin = %q, want 0012 (leading zeros kept)", cfg.Rooms[0].PIN)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want containing %q", err, c.want)
			}
		})
	}
}

func TestTCPPortCanBeDisabled(t *testing.T) {
	cfg, err := Parse([]byte("media: {advertise_ip: 10.8.0.1, tcp_port: -1}\nrooms: [{slug: a, name: A, pin: \"1234\"}]\n"))
	if err != nil || cfg.Media.TCPPort != -1 {
		t.Fatalf("cfg.Media.TCPPort = %v, err = %v", cfg, err)
	}
}

func TestTitleFromConfig(t *testing.T) {
	cfg, err := Parse([]byte("ui:\n  title: \"  Конференц-связь kunveno. Частный сервер.  \"\nmedia: {advertise_ip: 10.8.0.1}\nrooms: [{slug: a, name: A, pin: \"1234\"}]\n"))
	if err != nil || cfg.UI.Title != "Конференц-связь kunveno. Частный сервер." {
		t.Fatalf("title = %q, err = %v", cfg.UI.Title, err)
	}
}

func TestPlayerRoom(t *testing.T) {
	cfg, err := Parse([]byte("media: {advertise_ip: 10.8.0.1}\nrooms:\n  - {slug: tv, name: TV, pin: \"123456\", player: {file: intro.mp4}}\n  - {slug: a, name: A, pin: \"1234\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rooms[0].Player == nil || cfg.Rooms[0].Player.File != "intro.mp4" || cfg.Rooms[0].Player.Name != "intro" || cfg.Rooms[1].Player != nil {
		t.Fatalf("player = %+v / %+v", cfg.Rooms[0].Player, cfg.Rooms[1].Player)
	}
	for name, yaml := range map[string]string{
		"empty file": "media: {advertise_ip: 10.8.0.1}\nrooms: [{slug: tv, name: TV, pin: \"123456\", player: {file: \"\"}}]\n",
		"escape":     "media: {advertise_ip: 10.8.0.1}\nrooms: [{slug: tv, name: TV, pin: \"123456\", player: {file: ../etc/passwd}}]\n",
	} {
		if _, err := Parse([]byte(yaml)); err == nil || !strings.Contains(err.Error(), "player") {
			t.Errorf("%s: err = %v, want a player error", name, err)
		}
	}
}

func TestReactions(t *testing.T) {
	base := "media: {advertise_ip: 10.8.0.1}\nrooms: [{slug: a, name: A, pin: \"1234\"}]\n"
	cfg, err := Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"👍", "👎", "👏", "😂", "❤️", "🎉", "😮", "🤔", "☕", "👋"}
	if strings.Join(cfg.UI.Reactions, " ") != strings.Join(want, " ") {
		t.Fatalf("default reactions = %v", cfg.UI.Reactions)
	}
	cfg, err = Parse([]byte("ui: {reactions: [\"🚀\", \"✅\"]}\n" + base))
	if err != nil || strings.Join(cfg.UI.Reactions, " ") != "🚀 ✅" {
		t.Fatalf("custom reactions = %v, %v", cfg.UI.Reactions, err)
	}
	for name, list := range map[string]string{
		"text":      `["ok"]`,
		"spaces":    `["👍 👍"]`,
		"empty":     `[""]`,
		"duplicate": `["👍", "👍"]`,
		"too many":  `["😀","😁","😂","😃","😄","😅","😆","😇","😈","😉","😊","😋","😌","😍","😎","😏","😐"]`,
		"too long":  `["👍👍👍👍👍"]`,
	} {
		if _, err := Parse([]byte("ui: {reactions: " + list + "}\n" + base)); err == nil || !strings.Contains(err.Error(), "ui.reactions") {
			t.Errorf("%s: err = %v, want ui.reactions", name, err)
		}
	}
}

func TestPlayerFromRTSP(t *testing.T) {
	room := func(player string) string {
		return "media: {advertise_ip: 10.8.0.1}\nrooms: [{slug: yard, name: Yard, pin: \"123456\", player: " + player + "}]\n"
	}
	cfg, err := Parse([]byte(room(`{url: "rtsp://user:secret@10.10.10.50:554/stream1"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if p := cfg.Rooms[0].Player; p.URL != "rtsp://user:secret@10.10.10.50:554/stream1" || p.File != "" || p.Name != "10.10.10.50" {
		t.Fatalf("player = %+v", p)
	}
	for name, player := range map[string]string{
		"both":      `{file: a.mp4, url: "rtsp://h/s"}`,
		"neither":   `{name: X}`,
		"http":      `{url: "http://h/s"}`,
		"no host":   `{url: "rtsp:///s"}`,
		"not a url": `{url: "::nope"}`,
	} {
		if _, err := Parse([]byte(room(player))); err == nil || !strings.Contains(err.Error(), "player") {
			t.Errorf("%s: err = %v, want a player error", name, err)
		}
	}
}

func TestNoRoomsIsValid(t *testing.T) {
	cfg, err := Parse([]byte("media: {advertise_ip: 10.8.0.1}\n"))
	if err != nil || len(cfg.Rooms) != 0 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestLoadFillsGapsFromEnvironment(t *testing.T) {
	t.Setenv(EnvListen, "127.0.0.1:8090")
	t.Setenv(EnvAdvertiseIP, "10.8.0.7")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("# only comments, like a fresh template\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Listen != "127.0.0.1:8090" || cfg.Media.AdvertiseIP != "10.8.0.7" || cfg.Media.UDPPort != DefaultUDPPort ||
		cfg.UI.Title != DefaultTitle || len(cfg.Rooms) != 0 {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestLoadNeedsTheFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "config.yaml")); err == nil {
		t.Fatal("a missing file must fail")
	}
	// What Docker leaves when the bind-mounted file is missing.
	path := filepath.Join(dir, "mounted.yaml")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("err = %v, want the directory explained", err)
	}
}

func TestFileWinsOverEnvironment(t *testing.T) {
	t.Setenv(EnvListen, "127.0.0.1:8090")
	t.Setenv(EnvAdvertiseIP, "10.8.0.7")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("http: {listen: \"127.0.0.1:9000\"}\nmedia: {advertise_ip: 10.8.0.1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil || cfg.HTTP.Listen != "127.0.0.1:9000" || cfg.Media.AdvertiseIP != "10.8.0.1" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestAdvertiseIPDetectedLast(t *testing.T) {
	noEnv := func(string) string { return "" }
	cfg, err := parse(nil, noEnv, func() (string, error) { return "10.9.0.1", nil })
	if err != nil || cfg.Media.AdvertiseIP != "10.9.0.1" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	if _, err := parse(nil, noEnv, func() (string, error) { return "", errors.New("no address") }); err == nil {
		t.Fatal("a failed detection must fail the load")
	}
	called := false
	if _, err := parse([]byte("media: {advertise_ip: 10.8.0.1}\n"), noEnv, func() (string, error) { called = true; return "", nil }); err != nil || called {
		t.Fatalf("detection must not run when the file sets the address (err=%v)", err)
	}
}

func TestDetectIP(t *testing.T) {
	ip, err := DetectIP()
	if err != nil {
		t.Skipf("no IPv4 address on this machine: %v", err)
	}
	if p := net.ParseIP(ip); p == nil || p.To4() == nil || p.IsLoopback() {
		t.Fatalf("DetectIP = %q", ip)
	}
}

// The template users copy to config.yaml must load unchanged.
func TestTemplateLoadsAsIs(t *testing.T) {
	data, err := os.ReadFile("../../deploy/kunveno/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parse(data, func(string) string { return "" }, func() (string, error) { return "10.9.0.1", nil })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Listen != DefaultListen || cfg.Media.AdvertiseIP != "10.9.0.1" || len(cfg.Rooms) == 0 {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestNoiseSuppression(t *testing.T) {
	base := "media: {advertise_ip: 10.8.0.1}\nrooms: [{slug: a, name: A, pin: \"1234\"}]\n"
	cfg, err := Parse([]byte(base))
	if err != nil || cfg.UI.NoiseSuppression {
		t.Fatalf("default = %v, %v", cfg.UI.NoiseSuppression, err)
	}
	cfg, err = Parse([]byte("ui: {noise_suppression: true}\n" + base))
	if err != nil || !cfg.UI.NoiseSuppression {
		t.Fatalf("set = %v, %v", cfg.UI.NoiseSuppression, err)
	}
}

func TestBackgroundsDir(t *testing.T) {
	base := "media: {advertise_ip: 10.8.0.1}\nrooms: [{slug: a, name: A, pin: \"1234\"}]\n"
	cfg, err := Parse([]byte(base))
	if err != nil || cfg.BackgroundsDir != DefaultBackgroundsDir {
		t.Fatalf("default = %q, %v", cfg.BackgroundsDir, err)
	}
	cfg, err = Parse([]byte("backgrounds_dir: /srv/bg\n" + base))
	if err != nil || cfg.BackgroundsDir != "/srv/bg" {
		t.Fatalf("set = %q, %v", cfg.BackgroundsDir, err)
	}
}
