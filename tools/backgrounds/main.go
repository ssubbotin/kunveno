// Command backgrounds draws kunveno's built-in virtual backgrounds: calm 1920x1080 gradients with
// soft shapes, so the release needs no third-party images.
//
//	go run ./tools/backgrounds -out deploy/kunveno/backgrounds
package main

import (
	"flag"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"math"
	"os"
	"path/filepath"
)

type palette struct {
	name        string
	top, bottom color.RGBA
	shape       color.RGBA
}

func main() {
	out := flag.String("out", "deploy/kunveno/backgrounds", "output directory")
	flag.Parse()
	for _, p := range []palette{
		{"blue", color.RGBA{0x1f, 0x3b, 0x57, 255}, color.RGBA{0x6f, 0x93, 0xb4, 255}, color.RGBA{0xff, 0xff, 0xff, 22}},
		{"warm", color.RGBA{0x8a, 0x6a, 0x4f, 255}, color.RGBA{0xe8, 0xd5, 0xbd, 255}, color.RGBA{0xff, 0xf6, 0xe8, 28}},
		{"green", color.RGBA{0x2e, 0x4a, 0x3b, 255}, color.RGBA{0x9c, 0xb8, 0x96, 255}, color.RGBA{0xf2, 0xff, 0xf0, 24}},
		{"slate", color.RGBA{0x16, 0x20, 0x2b, 255}, color.RGBA{0x3d, 0x4f, 0x63, 255}, color.RGBA{0xc8, 0x45, 0x8f, 20}},
	} {
		if err := write(filepath.Join(*out, p.name+".jpg"), draw(p)); err != nil {
			log.Fatal(err)
		}
	}
}

func mix(a, b uint8, t float64) uint8 { return uint8(float64(a)*(1-t) + float64(b)*t + 0.5) }

func draw(p palette) *image.RGBA {
	const w, h = 1920, 1080
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Three large soft circles, placed off-centre where a person's head and shoulders are not.
	circles := []struct{ x, y, r float64 }{{0.12, 0.25, 420}, {0.88, 0.7, 520}, {0.7, 0.12, 260}}
	for y := 0; y < h; y++ {
		t := float64(y) / (h - 1)
		for x := 0; x < w; x++ {
			c := color.RGBA{mix(p.top.R, p.bottom.R, t), mix(p.top.G, p.bottom.G, t), mix(p.top.B, p.bottom.B, t), 255}
			for _, s := range circles {
				d := math.Hypot(float64(x)-s.x*w, float64(y)-s.y*h)
				if a := 1 - smooth(s.r-60, s.r, d); a > 0 {
					k := a * float64(p.shape.A) / 255
					c.R, c.G, c.B = mix(c.R, p.shape.R, k), mix(c.G, p.shape.G, k), mix(c.B, p.shape.B, k)
				}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func smooth(e0, e1, x float64) float64 {
	t := math.Max(0, math.Min(1, (x-e0)/(e1-e0)))
	return t * t * (3 - 2*t)
}

func write(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, img, &jpeg.Options{Quality: 85})
}
