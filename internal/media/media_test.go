package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"testing"

	"golang.org/x/image/webp"
)

func photo(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// A noisy gradient, like a photograph: lossless WebP cannot beat JPEG here.
			n := uint8((x*7 + y*13 + (x*y)%31) % 255)
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), n, uint8(y * 255 / h), 255})
		}
	}
	return img
}

func encJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestProcessJPEGRenditions(t *testing.T) {
	p, err := Process(encJPEG(t, photo(2000, 1000)))
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != 2000 || p.Height != 1000 || p.ContentType != "image/jpeg" {
		t.Fatalf("original %dx%d %s", p.Width, p.Height, p.ContentType)
	}
	got := map[int]bool{}
	for _, r := range p.Renditions {
		if r.Format == "jpeg" {
			got[r.Width] = true
			cfg, err := jpeg.DecodeConfig(bytes.NewReader(r.Data))
			if err != nil || cfg.Width != r.Width || cfg.Height != r.Height {
				t.Fatalf("rendition %d decodes as %dx%d (%v)", r.Width, cfg.Width, cfg.Height, err)
			}
			if r.Height != r.Width/2 {
				t.Fatalf("aspect ratio lost: %dx%d", r.Width, r.Height)
			}
		}
	}
	for _, w := range Widths {
		if !got[w] {
			t.Fatalf("missing jpeg width %d (have %v)", w, got)
		}
	}
}

func TestProcessNeverUpscales(t *testing.T) {
	p, err := Process(encJPEG(t, photo(500, 400)))
	if err != nil {
		t.Fatal(err)
	}
	maxW := 0
	for _, r := range p.Renditions {
		maxW = max(maxW, r.Width)
	}
	if maxW != 500 {
		t.Fatalf("largest rendition %d, want the original 500", maxW)
	}
}

func TestProcessTransparentPNGKeepsAlphaAndWebP(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 400, 400))
	for y := 100; y < 300; y++ {
		for x := 100; x < 300; x++ {
			img.Set(x, y, color.NRGBA{200, 30, 30, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	p, err := Process(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var sawPNG, sawWebP bool
	for _, r := range p.Renditions {
		switch r.Format {
		case "jpeg":
			t.Fatal("transparent image must not fall back to JPEG")
		case "png":
			sawPNG = true
			d, err := png.Decode(bytes.NewReader(r.Data))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, a := d.At(0, 0).RGBA(); a != 0 {
				t.Fatal("corner should stay transparent")
			}
		case "webp":
			sawWebP = true
			if _, err := webp.Decode(bytes.NewReader(r.Data)); err != nil {
				t.Fatalf("webp rendition does not decode: %v", err)
			}
		}
	}
	if !sawPNG || !sawWebP {
		t.Fatalf("png=%v webp=%v", sawPNG, sawWebP)
	}
}

func TestValidation(t *testing.T) {
	if _, err := Process([]byte("<html>not an image</html>")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("html: %v", err)
	}
	if _, err := Process([]byte("%PDF-1.4 ...")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("pdf: %v", err)
	}
	if _, err := Process(make([]byte, MaxUploadBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("large: %v", err)
	}
	good := encJPEG(t, photo(64, 64))
	if _, err := Process(good[:len(good)/3]); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("truncated: %v", err)
	}
	// A PNG header declaring a huge canvas is rejected before decoding pixels.
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1)))
	bomb := b.Bytes()
	copy(bomb[16:24], []byte{0, 0, 0x40, 0, 0, 0, 0x40, 0}) // 16384 x 16384
	binary.BigEndian.PutUint32(bomb[29:33], crc32.ChecksumIEEE(bomb[12:29]))
	if _, err := Process(bomb); !errors.Is(err, ErrDimensions) {
		t.Fatalf("bomb: %v", err)
	}
}

func TestOrientation(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	o := orient(img, 6) // rotate 90° clockwise
	if o.Bounds().Dx() != 2 || o.Bounds().Dy() != 4 {
		t.Fatalf("bounds %v", o.Bounds())
	}
	if r, _, _, _ := o.At(1, 0).RGBA(); r == 0 {
		t.Fatal("top-left pixel should move to the top-right")
	}
	// Minimal EXIF segment with Orientation = 6.
	exif := []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x22, 'E', 'x', 'i', 'f', 0, 0,
		'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, 6, 0, 0, 0, 0, 0, 0}
	if got := exifOrientation(exif); got != 6 {
		t.Fatalf("orientation %d", got)
	}
}

func TestLocalStorageIsolation(t *testing.T) {
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := l.Put(ctx, "store-a", "m_abc/640.jpg", []byte("A"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	o, err := l.Open(ctx, "store-a", "m_abc/640.jpg")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(o)
	_ = o.Close()
	if string(b) != "A" {
		t.Fatalf("read %q", b)
	}
	if _, err := l.Open(ctx, "store-b", "m_abc/640.jpg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other tenant: %v", err)
	}
	for _, bad := range [][2]string{{"store-a", "../store-b/x.jpg"}, {"store-a", "m_abc/../../etc.jpg"}, {"../x", "m_abc/1.jpg"}, {"store-a", "/etc/passwd"}, {"store-a", "m_abc/a.svg"}} {
		if err := l.Put(ctx, bad[0], bad[1], []byte("x"), ""); !errors.Is(err, ErrBadKey) {
			t.Fatalf("%v accepted: %v", bad, err)
		}
	}
	if err := l.DeletePrefix(ctx, "store-a", "m_abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Open(ctx, "store-a", "m_abc/640.jpg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := (GCS{}).Put(ctx, "store-a", "m_abc/1.jpg", nil, ""); !errors.Is(err, ErrNotConfigured) {
		t.Fatal("GCS stub should say it is not configured")
	}
}
