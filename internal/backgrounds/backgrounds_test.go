package backgrounds

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeJPEG(t *testing.T, dir, name string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestListFiltersAndSorts(t *testing.T) {
	dir := t.TempDir()
	writeJPEG(t, dir, "b-office.jpg", 64, 36)
	writeJPEG(t, dir, "a-sea.jpeg", 32, 18)
	writeJPEG(t, dir, "huge.jpg", MaxWidth+1, 10)                     // too wide
	os.WriteFile(filepath.Join(dir, "empty.png"), nil, 0o644)         // not an image
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644) // wrong type
	os.Mkdir(filepath.Join(dir, "sub.jpg"), 0o755)                    // a directory
	var pb bytes.Buffer
	png.Encode(&pb, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	os.WriteFile(filepath.Join(dir, "renamed.jpg"), pb.Bytes(), 0o644) // a PNG named .jpg is still an image
	got := New(dir, quiet()).List()
	want := []Item{{"a-sea.jpeg", 32, 18}, {"b-office.jpg", 64, 36}, {"renamed.jpg", 8, 8}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v", got)
		}
	}
}

func TestListLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxItems+5; i++ {
		writeJPEG(t, dir, fmt.Sprintf("%03d.jpg", i), 4, 4)
	}
	if n := len(New(dir, quiet()).List()); n != MaxItems {
		t.Fatalf("listed %d", n)
	}
}

func TestMissingDirectoryIsEmpty(t *testing.T) {
	if got := New(filepath.Join(t.TempDir(), "none"), quiet()).List(); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestServing(t *testing.T) {
	dir := t.TempDir()
	writeJPEG(t, dir, "office.jpg", 640, 360)
	h := New(dir, quiet()).Handler()
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	if rec := get("/api/backgrounds"); rec.Code != 200 || !json.Valid(rec.Body.Bytes()) || !bytes.Contains(rec.Body.Bytes(), []byte(`"office.jpg"`)) {
		t.Fatalf("list %d %s", rec.Code, rec.Body)
	}
	if rec := get("/api/backgrounds/office.jpg"); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Header().Get("Cache-Control") == "" {
		t.Fatalf("image %d %v", rec.Code, rec.Header())
	}
	for _, p := range []string{"/api/backgrounds/missing.jpg", "/api/backgrounds/..%2Fsecret", "/api/backgrounds/../secret", "/api/backgrounds/office.jpg/other"} {
		if rec := get(p); rec.Code != 404 {
			t.Fatalf("%s: %d", p, rec.Code)
		}
	}
	rec := get("/api/backgrounds/office.jpg/preview")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("preview %d", rec.Code)
	}
	cfg, err := jpeg.DecodeConfig(rec.Body)
	if err != nil || cfg.Width != PreviewW || cfg.Height != PreviewH {
		t.Fatalf("preview %v %v", cfg, err)
	}
}

func TestPreviewFollowsChanges(t *testing.T) {
	dir := t.TempDir()
	writeJPEG(t, dir, "x.jpg", 64, 36)
	d := New(dir, quiet())
	first, err := d.preview("x.jpg")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := d.preview("x.jpg")
	if &first[0] != &again[0] {
		t.Fatal("not cached")
	}
	time.Sleep(10 * time.Millisecond)
	writeJPEG(t, dir, "x.jpg", 128, 72)
	os.Chtimes(filepath.Join(dir, "x.jpg"), time.Now().Add(time.Second), time.Now().Add(time.Second))
	changed, _ := d.preview("x.jpg")
	if &changed[0] == &first[0] {
		t.Fatal("stale preview")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	rec := httptest.NewRecorder()
	New(t.TempDir(), quiet()).Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/backgrounds", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("%d", rec.Code)
	}
}

func TestBrokenPreviewIsNotCached(t *testing.T) {
	dir := t.TempDir()
	writeJPEG(t, dir, "full.jpg", 640, 360)
	data, err := os.ReadFile(filepath.Join(dir, "full.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	// Cut the file just after its header: DecodeConfig still works, Decode does not.
	n := 1
	for ; n < len(data); n++ {
		if _, err := jpeg.DecodeConfig(bytes.NewReader(data[:n])); err == nil {
			break
		}
	}
	data = data[:n]
	if _, err := jpeg.Decode(bytes.NewReader(data)); err == nil {
		t.Fatal("truncated file decodes")
	}
	if err := os.WriteFile(filepath.Join(dir, "cut.jpg"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h := New(dir, slog.New(slog.NewTextHandler(&logs, nil))).Handler()
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/backgrounds/cut.jpg/preview", nil))
		if rec.Code != 404 || rec.Header().Get("Cache-Control") != "" {
			t.Fatalf("%d %v", rec.Code, rec.Header())
		}
	}
	if n := bytes.Count(logs.Bytes(), []byte("background preview failed")); n != 1 {
		t.Fatalf("logged %d times: %s", n, logs.String())
	}
}
