package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if cfg.Media.ReconnectGrace != DefaultReconnectGrace || DefaultReconnectGrace != 15 {
		t.Errorf("reconnect_grace default = %d, want 15", cfg.Media.ReconnectGrace)
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

func TestNotes(t *testing.T) {
	base := "media: {advertise_ip: 10.8.0.1}\nrooms: [{slug: a, name: A, pin: \"1234\"}]\n"
	cfg, err := Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	if n := cfg.Notes; n.Enabled || n.Live || n.KeepFor != 24*time.Hour || n.Dir != DefaultNotesDir {
		t.Fatalf("defaults: enabled=%v live=%v keep=%v dir=%q", n.Enabled, n.Live, n.KeepFor, n.Dir)
	}
	cfg, err = Parse([]byte("notes: {enabled: true, keep: 90m, live: true, dir: /srv/notes}\n" + base))
	if err != nil {
		t.Fatal(err)
	}
	if n := cfg.Notes; !n.Enabled || !n.Live || n.KeepFor != 90*time.Minute || n.Dir != "/srv/notes" {
		t.Fatalf("set: enabled=%v live=%v keep=%v dir=%q", n.Enabled, n.Live, n.KeepFor, n.Dir)
	}
	for _, bad := range []string{"2d12h", "1w", "0d", "1.5h", `""`} {
		if _, err := Parse([]byte("notes: {keep: " + bad + "}\n" + base)); err == nil || !strings.Contains(err.Error(), "notes.keep") {
			t.Errorf("keep %s: err = %v, want a notes.keep error", bad, err)
		}
	}
	if _, err := Parse([]byte("notes: {colour: red}\n" + base)); err == nil {
		t.Error("unknown key under notes accepted")
	}
}

func TestRecordingDefaults(t *testing.T) {
	cfg, err := Parse([]byte("media:\n  advertise_ip: 10.0.0.1\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Recording
	if r.Enabled || *r.Keep != "1d" || r.KeepFor != 24*time.Hour || r.Dir != "/var/lib/kunveno/recordings" ||
		r.MaxSize != "8GB" || r.MaxBytes != 8<<30 {
		t.Fatalf("defaults = %+v", r)
	}
}

func TestRecordingSettings(t *testing.T) {
	cfg, err := Parse([]byte("media:\n  advertise_ip: 10.0.0.1\nrecording:\n  enabled: true\n  keep: 12h\n  dir: /r\n  max_size: 500MB\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Recording
	if !r.Enabled || r.KeepFor != 12*time.Hour || r.Dir != "/r" || r.MaxBytes != 500<<20 {
		t.Fatalf("set = %+v", r)
	}
	for _, bad := range []string{"keep: 1w", "keep: \"\"", "max_size: 0GB", "max_size: 8gb", "max_size: 1.5GB", "max_size: 9999999GB"} {
		if _, err := Parse([]byte("media:\n  advertise_ip: 10.0.0.1\nrecording:\n  " + bad + "\n")); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestReconnectGraceRange(t *testing.T) {
	parse := func(v string) (*Config, error) {
		return Parse([]byte("media: {advertise_ip: 10.8.0.1, reconnect_grace: " + v + "}\nrooms: [{slug: a, name: A, pin: \"1234\"}]\n"))
	}
	for _, v := range []string{"3", "30", "120"} {
		if cfg, err := parse(v); err != nil || fmt.Sprint(cfg.Media.ReconnectGrace) != v {
			t.Errorf("%s: err = %v", v, err)
		}
	}
	for _, v := range []string{"1", "2", "121", "-5"} {
		if _, err := parse(v); err == nil || !strings.Contains(err.Error(), "media.reconnect_grace") {
			t.Errorf("%s: err = %v, want media.reconnect_grace", v, err)
		}
	}
}

func TestLoadNamesTheFixForAnUnreadableFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads anywhere")
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("rooms: []\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "sudo chmod 640 config.yaml") {
		t.Fatalf("error %v does not name the fix", err)
	}
}

func TestAuthOffByDefault(t *testing.T) {
	cfg, err := Parse([]byte("media:\n  advertise_ip: 10.0.0.1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.Enabled || cfg.Auth.SessionTTL != 12*time.Hour || cfg.Auth.InviteMaxTTL != 168*time.Hour {
		t.Fatalf("auth defaults %+v", cfg.Auth)
	}
}

const authOn = `media:
  advertise_ip: 10.0.0.1
auth:
  enabled: true
  public_url: https://meet.example.com
  providers:
    - type: yandex
      name: Yandex 360
      client_id: cid
      allow: ["*@example.com"]
`

func TestAuthSecretFromTheEnvironment(t *testing.T) {
	env := map[string]string{"KUNVENO_AUTH_YANDEX_SECRET": "sec"}
	cfg, err := parse([]byte(authOn), func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := cfg.Auth.Providers[0]; p.Secret != "sec" || p.Type != "yandex" {
		t.Fatalf("provider %+v", p)
	}
}

func TestAuthValidation(t *testing.T) {
	env := func(k string) string {
		if k == "KUNVENO_AUTH_YANDEX_SECRET" {
			return "sec"
		}
		return ""
	}
	for name, c := range map[string]struct {
		yaml string
		env  func(string) string
		want string
	}{
		"no providers":     {strings.Replace(authOn, "  providers:\n    - type: yandex\n      name: Yandex 360\n      client_id: cid\n      allow: [\"*@example.com\"]\n", "", 1), env, "auth.providers"},
		"http public_url":  {strings.Replace(authOn, "https://meet", "http://meet", 1), env, "auth.public_url"},
		"no public_url":    {strings.Replace(authOn, "  public_url: https://meet.example.com\n", "", 1), env, "auth.public_url"},
		"no secret":        {authOn, func(string) string { return "" }, "KUNVENO_AUTH_YANDEX_SECRET"},
		"empty allow":      {strings.Replace(authOn, `["*@example.com"]`, "[]", 1), env, "allow"},
		"unknown provider": {strings.Replace(authOn, "type: yandex", "type: myspace", 1), env, "auth.providers[0].type"},
		"bad session":      {authOn + "  session: forever\n", env, "auth.session"},
		"no client id":     {strings.Replace(authOn, "      client_id: cid\n", "", 1), env, "client_id"},
	} {
		if _, err := parse([]byte(c.yaml), c.env, nil); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, c.want)
		}
	}
}
