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
