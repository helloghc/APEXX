package media

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
)

// Edits are non-destructive changes to an image: they are always applied to
// the untouched original, in this order: upright (EXIF), rotate, flip, crop,
// then light and colour. Crop is in fractions of the rotated image, so the
// same edit fits any resolution.
type Edits struct {
	Rotate     int   `json:"rotate,omitempty"` // 0, 90, 180, 270 (clockwise)
	FlipH      bool  `json:"flipH,omitempty"`
	FlipV      bool  `json:"flipV,omitempty"`
	Crop       *Crop `json:"crop,omitempty"`
	Brightness int   `json:"brightness,omitempty"` // -100..100
	Contrast   int   `json:"contrast,omitempty"`   // -100..100
	Saturation int   `json:"saturation,omitempty"` // -100..100 (-100 = black and white)
}

// Crop is a rectangle in fractions (0..1) of the rotated image.
type Crop struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// ErrBadEdit is an edit outside its limits.
var ErrBadEdit = errors.New("media: invalid edit")

// Empty reports whether the edits change nothing.
func (e Edits) Empty() bool {
	return e.Rotate == 0 && !e.FlipH && !e.FlipV && e.Crop == nil && e.Brightness == 0 && e.Contrast == 0 && e.Saturation == 0
}

// Validate checks the limits.
func (e Edits) Validate() error {
	switch e.Rotate {
	case 0, 90, 180, 270:
	default:
		return ErrBadEdit
	}
	for _, v := range []int{e.Brightness, e.Contrast, e.Saturation} {
		if v < -100 || v > 100 {
			return ErrBadEdit
		}
	}
	if c := e.Crop; c != nil {
		if c.X < 0 || c.Y < 0 || c.W <= 0 || c.H <= 0 || c.X+c.W > 1.0001 || c.Y+c.H > 1.0001 {
			return ErrBadEdit
		}
	}
	return nil
}

// Edit decodes the original, applies the edits and encodes the result:
// PNG when it has transparency, else a high-quality JPEG (renditions are
// made from it). Width and height are the result's.
func Edit(original []byte, e Edits) (data []byte, contentType string, w, h int, err error) {
	if err := e.Validate(); err != nil {
		return nil, "", 0, 0, err
	}
	ct, _, err := Sniff(original)
	if err != nil {
		return nil, "", 0, 0, err
	}
	img, err := decode(original, ct)
	if err != nil {
		return nil, "", 0, 0, err
	}
	if ct == "image/jpeg" {
		img = orient(img, exifOrientation(original))
	}
	// Rotation and flips map onto the EXIF orientations.
	o := map[int]int{0: 1, 90: 6, 180: 3, 270: 8}[e.Rotate]
	img = orient(img, o)
	switch {
	case e.FlipH && e.FlipV:
		img = orient(img, 3)
	case e.FlipH:
		img = orient(img, 2)
	case e.FlipV:
		img = orient(img, 4)
	}
	b := img.Bounds()
	if c := e.Crop; c != nil {
		x0, y0 := b.Min.X+int(math.Round(c.X*float64(b.Dx()))), b.Min.Y+int(math.Round(c.Y*float64(b.Dy())))
		x1, y1 := min(b.Max.X, x0+max(1, int(math.Round(c.W*float64(b.Dx()))))), min(b.Max.Y, y0+max(1, int(math.Round(c.H*float64(b.Dy())))))
		r := image.Rect(x0, y0, x1, y1)
		if r.Dx() < 16 || r.Dy() < 16 {
			return nil, "", 0, 0, ErrBadEdit // too small to be useful
		}
		img = copyRect(img, r)
		b = img.Bounds()
	}
	alpha := hasAlpha(img)
	if e.Brightness != 0 || e.Contrast != 0 || e.Saturation != 0 {
		img = adjust(img, e)
	}
	var buf bytes.Buffer
	if alpha {
		err, ct = png.Encode(&buf, img), "image/png"
	} else {
		err, ct = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}), "image/jpeg"
	}
	if err != nil {
		return nil, "", 0, 0, err
	}
	return buf.Bytes(), ct, b.Dx(), b.Dy(), nil
}

func copyRect(img image.Image, r image.Rectangle) image.Image {
	dst := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			dst.Set(x, y, img.At(r.Min.X+x, r.Min.Y+y))
		}
	}
	return dst
}

// adjust changes brightness, contrast and saturation with a lookup table per
// channel and a luminance mix, so a 12 MP photo takes a fraction of a second.
func adjust(img image.Image, e Edits) image.Image {
	b := img.Bounds()
	bright := float64(e.Brightness) / 100 * 255 * 0.5
	contrast := 1 + float64(e.Contrast)/100
	if e.Contrast > 0 {
		contrast = 1 + float64(e.Contrast)/100*1.5
	}
	sat := 1 + float64(e.Saturation)/100
	var lut [256]uint8
	for i := range lut {
		v := (float64(i)-127.5)*contrast + 127.5 + bright
		lut[i] = uint8(math.Max(0, math.Min(255, math.Round(v))))
	}
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			r, g, bl := float64(lut[c.R]), float64(lut[c.G]), float64(lut[c.B])
			if sat != 1 {
				l := 0.299*r + 0.587*g + 0.114*bl
				r, g, bl = l+(r-l)*sat, l+(g-l)*sat, l+(bl-l)*sat
			}
			dst.SetNRGBA(x, y, color.NRGBA{R: clamp8(r), G: clamp8(g), B: clamp8(bl), A: c.A})
		}
	}
	return dst
}

func clamp8(v float64) uint8 { return uint8(math.Max(0, math.Min(255, math.Round(v)))) }
