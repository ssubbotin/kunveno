// Package config loads and validates the kunveno-server configuration file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Room is one configured meeting room.
type Room struct {
	Slug            string `yaml:"slug"`
	Name            string `yaml:"name"`
	PIN             string `yaml:"pin"`
	MaxParticipants int    `yaml:"max_participants"`
}

// Config is the whole configuration file.
type Config struct {
	HTTP struct {
		Listen         string   `yaml:"listen"`
		AllowedOrigins []string `yaml:"allowed_origins"`
	} `yaml:"http"`
	Media struct {
		UDPPort      int    `yaml:"udp_port"`
		TCPPort      int    `yaml:"tcp_port"` // ICE-TCP fallback; -1 disables
		AdvertiseIP  string `yaml:"advertise_ip"`
		ShareMaxKbps int    `yaml:"share_max_kbps"`
	} `yaml:"media"`
	UI struct {
		// Title names the service in the lobby and the browser tab, e.g. a company's name.
		Title string `yaml:"title"`
	} `yaml:"ui"`
	Rooms []Room `yaml:"rooms"`
	// RoomsFile stores rooms created through the API (JSON, mode 0600: it holds PINs).
	RoomsFile string `yaml:"rooms_file"`
}

const (
	DefaultListen          = "127.0.0.1:8080"
	DefaultTitle           = "kunveno"
	MaxTitleLen            = 80
	DefaultRoomsFile       = "/var/lib/kunveno/rooms.json"
	DefaultUDPPort         = 7000
	DefaultShareMaxKbps    = 2500
	DefaultMaxParticipants = 15
	MaxParticipantsLimit   = 50
)

var slugRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// Load reads and validates the file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes YAML, applies defaults and validates.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytesReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	c.UI.Title = strings.TrimSpace(c.UI.Title)
	if c.UI.Title == "" {
		c.UI.Title = DefaultTitle
	}
	if c.RoomsFile == "" {
		c.RoomsFile = DefaultRoomsFile
	}
	if c.HTTP.Listen == "" {
		c.HTTP.Listen = DefaultListen
	}
	if c.Media.UDPPort == 0 {
		c.Media.UDPPort = DefaultUDPPort
	}
	if c.Media.TCPPort == 0 {
		c.Media.TCPPort = DefaultUDPPort
	}
	if c.Media.ShareMaxKbps == 0 {
		c.Media.ShareMaxKbps = DefaultShareMaxKbps
	}
	for i := range c.Rooms {
		if c.Rooms[i].MaxParticipants == 0 {
			c.Rooms[i].MaxParticipants = DefaultMaxParticipants
		}
	}
}

// Validate checks every field; the error names the offending field.
func (c *Config) Validate() error {
	var errs []error
	if c.Media.UDPPort < 1 || c.Media.UDPPort > 65535 {
		errs = append(errs, fmt.Errorf("media.udp_port %d out of range", c.Media.UDPPort))
	}
	if n := utf8.RuneCountInString(c.UI.Title); n > MaxTitleLen {
		errs = append(errs, fmt.Errorf("ui.title is %d characters, at most %d", n, MaxTitleLen))
	}
	if p := c.Media.TCPPort; p != -1 && (p < 1 || p > 65535) {
		errs = append(errs, fmt.Errorf("media.tcp_port %d out of range (or -1 to disable)", p))
	}
	if ip := net.ParseIP(c.Media.AdvertiseIP); ip == nil || ip.To4() == nil {
		errs = append(errs, fmt.Errorf("media.advertise_ip %q is not an IPv4 address", c.Media.AdvertiseIP))
	}
	if c.Media.ShareMaxKbps < 200 || c.Media.ShareMaxKbps > 20000 {
		errs = append(errs, fmt.Errorf("media.share_max_kbps %d out of range 200..20000", c.Media.ShareMaxKbps))
	}
	if len(c.Rooms) == 0 {
		errs = append(errs, errors.New("rooms: at least one room is required"))
	}
	seen := map[string]bool{}
	for i, r := range c.Rooms {
		if !slugRe.MatchString(r.Slug) {
			errs = append(errs, fmt.Errorf("rooms[%d].slug %q must match %s", i, r.Slug, slugRe))
		}
		if seen[r.Slug] {
			errs = append(errs, fmt.Errorf("rooms[%d].slug %q is a duplicate", i, r.Slug))
		}
		seen[r.Slug] = true
		if r.Name == "" {
			errs = append(errs, fmt.Errorf("rooms[%d].name is empty", i))
		}
		if n := len(r.PIN); n < 4 || n > 16 {
			errs = append(errs, fmt.Errorf("rooms[%d].pin must be 4..16 characters", i))
		}
		if r.MaxParticipants < 2 || r.MaxParticipants > MaxParticipantsLimit {
			errs = append(errs, fmt.Errorf("rooms[%d].max_participants %d out of range 2..%d", i, r.MaxParticipants, MaxParticipantsLimit))
		}
	}
	return errors.Join(errs...)
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
