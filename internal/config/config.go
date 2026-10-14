// Package config loads and validates the kunveno-server configuration file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Room is one configured meeting room.
type Room struct {
	Slug            string `yaml:"slug"`
	Name            string `yaml:"name"`
	PIN             string `yaml:"pin"`
	MaxParticipants int    `yaml:"max_participants"`
	// Player, when set, keeps a looped video playing in the room (see cmd/kunveno-player).
	Player *Player `yaml:"player"`
}

// Player is what a room plays: a media file in a loop, or a live RTSP feed. Exactly one of File
// and URL is set.
type Player struct {
	// File is relative to the player's media directory (~/kunveno/media on the host).
	File string `yaml:"file"`
	// URL is an rtsp:// or rtsps:// feed, credentials included if the camera needs them.
	URL string `yaml:"url"`
	// Name labels the player's tile; defaults to the file name without its extension.
	Name string `yaml:"name"`
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
		// Reactions are the emoji participants can send; DefaultReactions when unset.
		Reactions []string `yaml:"reactions"`
		// NoiseSuppression turns the client's noise filter on for participants who have not
		// chosen themselves.
		NoiseSuppression bool `yaml:"noise_suppression"`
	} `yaml:"ui"`
	Rooms []Room `yaml:"rooms"`
	// RoomsFile stores rooms created through the API (JSON, mode 0600: it holds PINs).
	RoomsFile string `yaml:"rooms_file"`
	// BackgroundsDir holds the virtual background presets (images listed at /api/backgrounds).
	BackgroundsDir string `yaml:"backgrounds_dir"`
}

const (
	DefaultListen          = "127.0.0.1:8080"
	DefaultTitle           = "kunveno"
	MaxTitleLen            = 80
	MaxReactions           = 16
	DefaultRoomsFile       = "/var/lib/kunveno/rooms.json"
	DefaultBackgroundsDir  = "/var/lib/kunveno/backgrounds"
	DefaultUDPPort         = 7000
	DefaultShareMaxKbps    = 2500
	DefaultMaxParticipants = 15
	MaxParticipantsLimit   = 50
)

var slugRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// Environment variables that fill settings the file leaves out, so that the configuration
// template works unchanged in a container.
const (
	EnvListen      = "KUNVENO_LISTEN"       // http.listen
	EnvAdvertiseIP = "KUNVENO_ADVERTISE_IP" // media.advertise_ip
)

// Load reads and validates the file at path. Settings the file leaves out come from the
// environment (EnvListen, EnvAdvertiseIP); media.advertise_ip falls back to the host's primary
// IPv4 address (DetectIP).
func Load(path string) (*Config, error) {
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		// Docker creates a directory in place of a bind-mounted file that does not exist.
		return nil, fmt.Errorf("config: %s is a directory (Docker creates one when the file is missing): remove it and restore config.yaml from the kunveno archive", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(data, os.Getenv, DetectIP)
}

// Parse decodes YAML, applies defaults and validates. It reads no environment.
func Parse(data []byte) (*Config, error) {
	return parse(data, func(string) string { return "" }, nil)
}

func parse(data []byte, getenv func(string) string, detect func() (string, error)) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytesReader(data))
	dec.KnownFields(true)
	// A file with nothing but comments is an empty configuration.
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("config: %w", err)
	}
	if cfg.HTTP.Listen == "" {
		cfg.HTTP.Listen = getenv(EnvListen)
	}
	if cfg.Media.AdvertiseIP == "" {
		cfg.Media.AdvertiseIP = getenv(EnvAdvertiseIP)
	}
	if cfg.Media.AdvertiseIP == "" && detect != nil {
		ip, err := detect()
		if err != nil {
			return nil, err
		}
		cfg.Media.AdvertiseIP = ip
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.UI.Reactions == nil {
		c.UI.Reactions = append([]string(nil), DefaultReactions...)
	}
	c.UI.Title = strings.TrimSpace(c.UI.Title)
	if c.UI.Title == "" {
		c.UI.Title = DefaultTitle
	}
	if c.RoomsFile == "" {
		c.RoomsFile = DefaultRoomsFile
	}
	if c.BackgroundsDir == "" {
		c.BackgroundsDir = DefaultBackgroundsDir
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
		if p := c.Rooms[i].Player; p != nil && strings.TrimSpace(p.Name) == "" {
			if p.URL != "" {
				if u, err := url.Parse(p.URL); err == nil {
					p.Name = u.Hostname()
				}
			} else {
				base := filepath.Base(p.File)
				p.Name = strings.TrimSuffix(base, filepath.Ext(base))
			}
		}
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
	if err := validReactions(c.UI.Reactions); err != nil {
		errs = append(errs, err)
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
		if p := r.Player; p != nil {
			switch {
			case (p.File == "") == (p.URL == ""):
				errs = append(errs, fmt.Errorf("rooms[%d].player needs exactly one of file and url", i))
			case p.File != "" && (filepath.IsAbs(p.File) || !filepath.IsLocal(p.File)):
				errs = append(errs, fmt.Errorf("rooms[%d].player.file %q must be a file name inside the media directory", i, p.File))
			case p.URL != "":
				if u, err := url.Parse(p.URL); err != nil || (u.Scheme != "rtsp" && u.Scheme != "rtsps") || u.Hostname() == "" {
					errs = append(errs, fmt.Errorf("rooms[%d].player.url must be rtsp:// or rtsps:// with a host", i))
				}
			}
		}
		if r.MaxParticipants < 2 || r.MaxParticipants > MaxParticipantsLimit {
			errs = append(errs, fmt.Errorf("rooms[%d].max_participants %d out of range 2..%d", i, r.MaxParticipants, MaxParticipantsLimit))
		}
	}
	return errors.Join(errs...)
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// DetectIP returns the host's primary IPv4 address: the source address of the default route, or,
// on a host without one, the first IPv4 address of an interface that is up (container bridges
// skipped). Dialing UDP only looks up the route; no packet is sent.
func DetectIP() (string, error) {
	if c, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		a, ok := c.LocalAddr().(*net.UDPAddr)
		c.Close()
		if ok && !a.IP.IsLoopback() && !a.IP.IsUnspecified() {
			return a.IP.String(), nil
		}
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || bridgeName(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				return n.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no IPv4 address found: set media.advertise_ip or %s", EnvAdvertiseIP)
}

func bridgeName(name string) bool {
	for _, p := range []string{"docker", "br-", "veth", "virbr", "cni", "flannel"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// DefaultReactions is the reaction set when ui.reactions is not configured.
var DefaultReactions = []string{"👍", "👎", "👏", "😂", "❤️", "🎉", "😮", "🤔", "☕", "👋"}

// validReactions accepts 1..MaxReactions distinct emoji. An entry is short (at most 16 bytes, room
// for modifiers and joiners) and has no letters, digits, spaces or punctuation, so a reaction
// cannot carry text.
func validReactions(list []string) error {
	if len(list) == 0 || len(list) > MaxReactions {
		return fmt.Errorf("ui.reactions must list 1..%d emoji", MaxReactions)
	}
	seen := map[string]bool{}
	for i, e := range list {
		if e == "" || len(e) > 16 {
			return fmt.Errorf("ui.reactions[%d] %q is not a single emoji", i, e)
		}
		for _, r := range e {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || unicode.IsPunct(r) || r < 0x80 {
				return fmt.Errorf("ui.reactions[%d] %q is not a single emoji", i, e)
			}
		}
		if seen[e] {
			return fmt.Errorf("ui.reactions[%d] %q is a duplicate", i, e)
		}
		seen[e] = true
	}
	return nil
}
