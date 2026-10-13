package config

import (
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
		{"no rooms", "media: {advertise_ip: 10.8.0.1}\nrooms: []\n", "at least one room"},
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
		if _, err := Parse([]byte(yaml)); err == nil || !strings.Contains(err.Error(), "player.file") {
			t.Errorf("%s: err = %v, want player.file", name, err)
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
