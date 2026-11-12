package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestEdits(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			c := color.RGBA{200, 40, 40, 255} // red left half
			if x >= 200 {
				c = color.RGBA{40, 40, 200, 255} // blue right half
			}
			src.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, src, &jpeg.Options{Quality: 95})
	orig := buf.Bytes()
	at := func(data []byte, x, y int) color.RGBA {
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
	}
	// Rotation swaps the sides; 90° clockwise puts the left (red) on top.
	out, ct, w, h, err := Edit(orig, Edits{Rotate: 90})
	if err != nil || ct != "image/jpeg" || w != 200 || h != 400 || at(out, 100, 50).R < 150 {
		t.Fatalf("rotate: %v %s %dx%d %v", err, ct, w, h, at(out, 100, 50))
	}
	// Mirror: red moves to the right.
	out, _, _, _, _ = Edit(orig, Edits{FlipH: true})
	if at(out, 350, 100).R < 150 {
		t.Fatalf("flip: %v", at(out, 350, 100))
	}
	// Crop the right half: only blue remains.
	out, _, w, h, err = Edit(orig, Edits{Crop: &Crop{X: 0.5, Y: 0, W: 0.5, H: 1}})
	if err != nil || w != 200 || h != 200 || at(out, 10, 10).B < 150 {
		t.Fatalf("crop: %v %dx%d %v", err, w, h, at(out, 10, 10))
	}
	// Black and white, brighter.
	out, _, _, _, _ = Edit(orig, Edits{Saturation: -100})
	if c := at(out, 50, 50); absDiff(c.R, c.G) > 6 || absDiff(c.G, c.B) > 6 {
		t.Fatalf("grayscale: %v", c)
	}
	out, _, _, _, _ = Edit(orig, Edits{Brightness: 60})
	if at(out, 50, 50).G <= 60 {
		t.Fatalf("brightness: %v", at(out, 50, 50))
	}
	for _, bad := range []Edits{{Rotate: 45}, {Brightness: 101}, {Crop: &Crop{X: 0.9, Y: 0, W: 0.5, H: 1}}, {Crop: &Crop{X: 0, Y: 0, W: 0.01, H: 0.01}}} {
		if _, _, _, _, err := Edit(orig, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func absDiff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}
