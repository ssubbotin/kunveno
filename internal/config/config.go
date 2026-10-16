// Package config loads and validates the kunveno-server configuration file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/ssubbotin/kunveno/internal/access"
	"github.com/ssubbotin/kunveno/internal/notes"
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
		// ReconnectGrace is how many seconds a client lets its media connection stay
		// "disconnected" before it restarts the session; served in /api/info.
		ReconnectGrace int `yaml:"reconnect_grace"`
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
	// Notes configures meeting notes. They need the notes bot of the commercial kunveno-notes
	// add-on; switch them on together with installing it.
	Notes struct {
		// Enabled offers notes in rooms; false hides every notes option.
		Enabled bool `yaml:"enabled"`
		// Keep is how long the last meeting's notes stay after it ends (notes.ParseKeep);
		// DefaultNotesKeep when unset. A pointer, so that an empty value is an error.
		Keep *string `yaml:"keep"`
		// Live sends each phrase to the room as it is recognised, for checking the recognition.
		Live bool   `yaml:"live"`
		Dir  string `yaml:"dir"`
		// KeepFor is Keep parsed; set by Parse and Load.
		KeepFor time.Duration `yaml:"-"`
	} `yaml:"notes"`
	// Recording configures meeting recording. It needs the recording bot of the commercial
	// kunveno-recorder add-on; switch it on together with installing it.
	Recording struct {
		// Enabled offers recording in rooms; false hides every recording option.
		Enabled bool `yaml:"enabled"`
		// Keep is how long the last meeting's recording stays after it ends (notes.ParseKeep
		// format); DefaultRecordingKeep when unset.
		Keep *string `yaml:"keep"`
		Dir  string  `yaml:"dir"`
		// MaxSize is the largest upload accepted from the bot: <n>MB or <n>GB.
		MaxSize string `yaml:"max_size"`
		// KeepFor and MaxBytes are Keep and MaxSize parsed; set by Parse and Load.
		KeepFor  time.Duration `yaml:"-"`
		MaxBytes int64         `yaml:"-"`
	} `yaml:"recording"`
	// Auth is the optional sign-in (see package auth). Off by default: no sign-in, as before.
	Auth struct {
		Enabled bool `yaml:"enabled"`
		// PublicURL is the address people open, https only; callbacks are built from it.
		PublicURL string         `yaml:"public_url"`
		Session   string         `yaml:"session"`    // a Go duration, DefaultSession when unset
		InviteMax string         `yaml:"invite_max"` // the longest guest invitation, DefaultInviteMax
		Providers []AuthProvider `yaml:"providers"`
		// SessionTTL and InviteMaxTTL are Session and InviteMax parsed; set by Parse and Load.
		SessionTTL   time.Duration `yaml:"-"`
		InviteMaxTTL time.Duration `yaml:"-"`
	} `yaml:"auth"`
}

// AuthProvider is one sign-in provider. Its secret comes from KUNVENO_AUTH_<TYPE>_SECRET.
type AuthProvider struct {
	Type     string   `yaml:"type"`
	Name     string   `yaml:"name"` // the button's label; the type when unset
	ClientID string   `yaml:"client_id"`
	Allow    []string `yaml:"allow"` // login patterns, e.g. "*@example.com"
	Secret   string   `yaml:"-"`
}

const (
	DefaultSession   = "12h"
	DefaultInviteMax = "168h"
)

// AuthProviderTypes are the providers this build knows.
var AuthProviderTypes = []string{"yandex"}

// SecretEnv names the environment variable holding a provider type's secret.
func SecretEnv(typ string) string { return "KUNVENO_AUTH_" + strings.ToUpper(typ) + "_SECRET" }

// DataDir is where the rooms file lives; auth.key and invites.json go next to it.
func (c *Config) DataDir() string { return filepath.Dir(c.RoomsFile) }

const (
	DefaultListen           = "127.0.0.1:8080"
	DefaultTitle            = "kunveno"
	MaxTitleLen             = 80
	MaxReactions            = 16
	DefaultRoomsFile        = "/var/lib/kunveno/rooms.json"
	DefaultBackgroundsDir   = "/var/lib/kunveno/backgrounds"
	DefaultNotesKeep        = "1d"
	DefaultNotesDir         = "/var/lib/kunveno/notes"
	DefaultRecordingKeep    = "1d"
	DefaultRecordingDir     = "/var/lib/kunveno/recordings"
	DefaultRecordingMaxSize = "8GB"
	DefaultUDPPort          = 7000
	DefaultShareMaxKbps     = 2500
	DefaultReconnectGrace   = 15
	DefaultMaxParticipants  = 15
	MaxParticipantsLimit    = 50
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
		return nil, readHint(err)
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
	for i := range cfg.Auth.Providers {
		cfg.Auth.Providers[i].Secret = getenv(SecretEnv(cfg.Auth.Providers[i].Type))
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.Notes.KeepFor, _ = notes.ParseKeep(*cfg.Notes.Keep)           // checked by Validate
	cfg.Recording.KeepFor, _ = notes.ParseKeep(*cfg.Recording.Keep)   // checked by Validate
	cfg.Recording.MaxBytes, _ = ParseSize(cfg.Recording.MaxSize)      // checked by Validate
	cfg.Auth.SessionTTL, _ = time.ParseDuration(cfg.Auth.Session)     // checked by Validate
	cfg.Auth.InviteMaxTTL, _ = time.ParseDuration(cfg.Auth.InviteMax) // checked by Validate
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Auth.Session == "" {
		c.Auth.Session = DefaultSession
	}
	if c.Auth.InviteMax == "" {
		c.Auth.InviteMax = DefaultInviteMax
	}
	c.Auth.PublicURL = strings.TrimRight(c.Auth.PublicURL, "/")
	for i := range c.Auth.Providers {
		if strings.TrimSpace(c.Auth.Providers[i].Name) == "" {
			c.Auth.Providers[i].Name = c.Auth.Providers[i].Type
		}
	}
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
	if c.Notes.Keep == nil {
		keep := DefaultNotesKeep
		c.Notes.Keep = &keep
	}
	if c.Notes.Dir == "" {
		c.Notes.Dir = DefaultNotesDir
	}
	if c.Recording.Keep == nil {
		keep := DefaultRecordingKeep
		c.Recording.Keep = &keep
	}
	if c.Recording.Dir == "" {
		c.Recording.Dir = DefaultRecordingDir
	}
	if c.Recording.MaxSize == "" {
		c.Recording.MaxSize = DefaultRecordingMaxSize
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
	if c.Media.ReconnectGrace == 0 {
		c.Media.ReconnectGrace = DefaultReconnectGrace
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
	if c.Media.ReconnectGrace < 3 || c.Media.ReconnectGrace > 120 {
		errs = append(errs, fmt.Errorf("media.reconnect_grace %d out of range 3..120", c.Media.ReconnectGrace))
	}
	if c.Notes.Keep != nil {
		if _, err := notes.ParseKeep(*c.Notes.Keep); err != nil {
			errs = append(errs, fmt.Errorf("notes.keep: %w", err))
		}
	}
	if c.Recording.Keep != nil {
		if _, err := notes.ParseKeep(*c.Recording.Keep); err != nil {
			errs = append(errs, fmt.Errorf("recording.keep: %w", err))
		}
	}
	if _, err := ParseSize(c.Recording.MaxSize); err != nil {
		errs = append(errs, fmt.Errorf("recording.max_size: %w", err))
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
	if d, err := time.ParseDuration(c.Auth.Session); err != nil || d < time.Minute || d > 30*24*time.Hour {
		errs = append(errs, fmt.Errorf("auth.session %q must be a duration between 1m and 720h", c.Auth.Session))
	}
	if d, err := time.ParseDuration(c.Auth.InviteMax); err != nil || d < time.Hour || d > 30*24*time.Hour {
		errs = append(errs, fmt.Errorf("auth.invite_max %q must be a duration between 1h and 720h", c.Auth.InviteMax))
	}
	if c.Auth.Enabled {
		if u, err := url.Parse(c.Auth.PublicURL); err != nil || u.Scheme != "https" || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			errs = append(errs, fmt.Errorf("auth.public_url %q must be https://<host>", c.Auth.PublicURL))
		}
		if len(c.Auth.Providers) == 0 {
			errs = append(errs, errors.New("auth.providers: at least one provider is needed with auth.enabled"))
		}
	}
	seenType := map[string]bool{}
	for i, p := range c.Auth.Providers {
		if !slices.Contains(AuthProviderTypes, p.Type) {
			errs = append(errs, fmt.Errorf("auth.providers[%d].type %q is not one of %v", i, p.Type, AuthProviderTypes))
			continue
		}
		if seenType[p.Type] {
			errs = append(errs, fmt.Errorf("auth.providers[%d].type %q is a duplicate", i, p.Type))
		}
		seenType[p.Type] = true
		if !c.Auth.Enabled {
			continue
		}
		if p.ClientID == "" {
			errs = append(errs, fmt.Errorf("auth.providers[%d].client_id is empty", i))
		}
		if p.Secret == "" {
			errs = append(errs, fmt.Errorf("auth.providers[%d]: %s is not set", i, SecretEnv(p.Type)))
		}
		if len(p.Allow) == 0 {
			errs = append(errs, fmt.Errorf("auth.providers[%d].allow is empty: nobody could sign in", i))
		}
		for j, a := range p.Allow {
			if _, err := path.Match(a, ""); err != nil || a == "" || a == "*" {
				errs = append(errs, fmt.Errorf("auth.providers[%d].allow[%d] %q must be a login pattern such as *@example.com", i, j, a))
			}
		}
	}
	return errors.Join(errs...)
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// readHint names the fix for a config file the service cannot read (it runs as user 10001 and the
// stack mounts config.yaml from the host).
func readHint(err error) error {
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	return fmt.Errorf("%w (this service runs as %s: in the stack directory run sudo chgrp %d config.yaml && sudo chmod 640 config.yaml)",
		err, access.ID(), os.Getgid())
}

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

// At most six digits: 999999GB still fits an int64.
var sizeRe = regexp.MustCompile(`^([1-9][0-9]{0,5})(MB|GB)$`)

// ParseSize reads recording.max_size: a positive whole number and MB or GB ("500MB", "8GB").
func ParseSize(s string) (int64, error) {
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("%q is not a positive whole number followed by MB or GB (500MB, 8GB)", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, err
	}
	if m[2] == "GB" {
		return n << 30, nil
	}
	return n << 20, nil
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
