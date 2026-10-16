// Package backgrounds lists and serves the virtual background presets: image files in one
// directory, read on each request, so files an administrator adds appear without a restart.
package backgrounds

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg" // also registers the decoder
	_ "image/png"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	MaxBytes  = 5 << 20
	MaxWidth  = 3840
	MaxHeight = 2160
	MaxItems  = 50
	PreviewW  = 320
	PreviewH  = 180
	// MaxDecodes is how many previews are decoded at once; a full decode of a 4K image holds
	// tens of megabytes.
	MaxDecodes = 2
)

var types = map[string]string{".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp"}

// Item is one preset as the client sees it.
type Item struct {
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// making is a preview being made; requests for the same file wait for it.
type making struct {
	wg   sync.WaitGroup
	jpeg []byte
	err  error
}

type cached struct {
	size int64
	mod  time.Time
	jpeg []byte
}

// Dir is the presets directory.
type Dir struct {
	path string
	log  *slog.Logger

	mu       sync.Mutex
	previews map[string]cached
	making   map[string]*making
	slots    chan struct{}   // one per decode under way, at most MaxDecodes
	warned   map[string]bool // files already reported as skipped

	// Test hooks: decoding runs before each full decode, joined when a request waits for a
	// decode of the same file already under way.
	decoding func()
	joined   func()
}

func New(path string, log *slog.Logger) *Dir {
	return &Dir{path: path, log: log, previews: map[string]cached{}, making: map[string]*making{},
		slots: make(chan struct{}, MaxDecodes), warned: map[string]bool{}}
}

// List returns the usable images, sorted by name, at most MaxItems.
func (d *Dir) List() []Item {
	entries, err := os.ReadDir(d.path)
	if err != nil {
		return []Item{}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	items := []Item{}
	for _, e := range entries {
		if len(items) == MaxItems {
			break
		}
		if it, ok := d.check(e); ok {
			items = append(items, it)
		}
	}
	return items
}

// check accepts a regular file of a known type, size and dimensions; anything else is skipped and
// reported once.
func (d *Dir) check(e os.DirEntry) (Item, bool) {
	name := e.Name()
	if _, ok := types[strings.ToLower(filepath.Ext(name))]; !ok || !e.Type().IsRegular() {
		return Item{}, false
	}
	info, err := e.Info()
	if err != nil || info.Size() == 0 || info.Size() > MaxBytes {
		d.skip(name, "size")
		return Item{}, false
	}
	f, err := os.Open(filepath.Join(d.path, name))
	if err != nil {
		d.skip(name, "unreadable: the server runs as user 10001, so the file must be readable by others (chmod 644)")
		return Item{}, false
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width == 0 || cfg.Height == 0 || cfg.Width > MaxWidth || cfg.Height > MaxHeight {
		d.skip(name, "not an image within the limits")
		return Item{}, false
	}
	return Item{Name: name, Width: cfg.Width, Height: cfg.Height}, true
}

func (d *Dir) skip(name, why string) {
	d.warnOnce(name, "background skipped", why)
}

// warnOnce logs one warning per key.
func (d *Dir) warnOnce(key, msg, why string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.warned[key] {
		d.warned[key] = true
		d.log.Warn(msg, "file", key, "reason", why)
	}
}

// find returns the listed item with this name, so only listed files can be read.
func (d *Dir) find(name string) (Item, bool) {
	for _, it := range d.List() {
		if it.Name == name {
			return it, true
		}
	}
	return Item{}, false
}

// preview returns a PreviewW x PreviewH JPEG crop (cover), cached until the file changes.
// Requests for a file whose preview is being made wait for that one decode, and at most
// MaxDecodes files are decoded at once.
func (d *Dir) preview(name string) ([]byte, error) {
	p := filepath.Join(d.path, name)
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if c, ok := d.previews[name]; ok && c.size == info.Size() && c.mod.Equal(info.ModTime()) {
		d.mu.Unlock()
		return c.jpeg, nil
	}
	if c, ok := d.making[name]; ok {
		d.mu.Unlock()
		if d.joined != nil {
			d.joined()
		}
		c.wg.Wait()
		return c.jpeg, c.err
	}
	c := &making{}
	c.wg.Add(1)
	d.making[name] = c
	d.mu.Unlock()

	c.jpeg, c.err = d.render(p)
	d.mu.Lock()
	delete(d.making, name)
	if c.err == nil {
		d.previews[name] = cached{size: info.Size(), mod: info.ModTime(), jpeg: c.jpeg}
	}
	d.mu.Unlock()
	c.wg.Done()
	return c.jpeg, c.err
}

// render decodes the file and scales its centred crop, holding one of the decode slots.
func (d *Dir) render(p string) ([]byte, error) {
	d.slots <- struct{}{}
	defer func() { <-d.slots }()
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if d.decoding != nil {
		d.decoding()
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	// Cover: the largest centred part of the source with the preview's proportions.
	cw, ch := b.Dx(), b.Dx()*PreviewH/PreviewW
	if ch > b.Dy() {
		cw, ch = b.Dy()*PreviewW/PreviewH, b.Dy()
	}
	crop := image.Rect(b.Min.X+(b.Dx()-cw)/2, b.Min.Y+(b.Dy()-ch)/2, 0, 0)
	crop.Max = crop.Min.Add(image.Pt(cw, ch))
	dst := image.NewRGBA(image.Rect(0, 0, PreviewW, PreviewH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Handler serves GET /api/backgrounds, /api/backgrounds/{name} and /api/backgrounds/{name}/preview.
func (d *Dir) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/api/backgrounds")
		if rest == "" || rest == "/" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(d.List())
			return
		}
		parts := strings.Split(strings.TrimPrefix(rest, "/"), "/")
		if len(parts) > 2 || (len(parts) == 2 && parts[1] != "preview") {
			http.NotFound(w, r)
			return
		}
		it, ok := d.find(parts[0])
		if !ok {
			http.NotFound(w, r)
			return
		}
		if len(parts) == 2 {
			b, err := d.preview(it.Name)
			if err != nil {
				d.warnOnce(it.Name+"/preview", "background preview failed", err.Error())
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "public, max-age=86400")
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(b)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("Content-Type", types[strings.ToLower(filepath.Ext(it.Name))])
		http.ServeFile(w, r, filepath.Join(d.path, it.Name))
	})
}
