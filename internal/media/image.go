package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"

	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// Upload limits.
const (
	MaxUploadBytes = 10 << 20 // 10 MiB per image
	MaxPixels      = 25_000_000
	MaxSide        = 12_000
	JPEGQuality    = 82
)

// Widths are the rendition widths generated for srcset. Renditions are never
// upscaled: a 500 px original gets 160, 320 and its own 500.
var Widths = []int{160, 320, 640, 1024, 1600}

// Validation errors. Each maps to a stable API code.
var (
	ErrTooLarge    = errors.New("media: file too large")
	ErrUnsupported = errors.New("media: unsupported image type")
	ErrCorrupt     = errors.New("media: image cannot be decoded")
	ErrDimensions  = errors.New("media: image dimensions out of range")
)

// Rendition is one encoded size of an image.
type Rendition struct {
	Width, Height int
	Format        string // jpeg | png | webp
	Ext           string // jpg | png | webp
	ContentType   string
	Data          []byte
}

// Processed is the result of the image pipeline.
type Processed struct {
	ContentType   string // of the original
	Ext           string // extension for the stored original
	Width, Height int    // after applying the EXIF orientation
	Renditions    []Rendition
}

// Sniff validates size and type from the file content (never from the file
// name or the declared Content-Type) and returns the detected type.
func Sniff(data []byte) (contentType, ext string, err error) {
	if len(data) == 0 {
		return "", "", ErrCorrupt
	}
	if len(data) > MaxUploadBytes {
		return "", "", ErrTooLarge
	}
	switch ct := http.DetectContentType(data); ct {
	case "image/jpeg":
		return ct, "jpg", nil
	case "image/png":
		return ct, "png", nil
	case "image/gif":
		return ct, "gif", nil
	case "image/webp":
		return ct, "webp", nil
	}
	return "", "", ErrUnsupported
}

func decode(data []byte, contentType string) (image.Image, error) {
	r := bytes.NewReader(data)
	var cfg image.Config
	var err error
	switch contentType {
	case "image/jpeg":
		cfg, err = jpeg.DecodeConfig(r)
	case "image/png":
		cfg, err = png.DecodeConfig(r)
	case "image/gif":
		cfg, err = gif.DecodeConfig(r)
	case "image/webp":
		cfg, err = webp.DecodeConfig(r)
	default:
		return nil, ErrUnsupported
	}
	if err != nil {
		return nil, ErrCorrupt
	}
	// Check the declared size before allocating pixels (decompression bombs).
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxSide || cfg.Height > MaxSide || cfg.Width*cfg.Height > MaxPixels {
		return nil, ErrDimensions
	}
	r.Reset(data)
	var img image.Image
	switch contentType {
	case "image/jpeg":
		img, err = jpeg.Decode(r)
	case "image/png":
		img, err = png.Decode(r)
	case "image/gif":
		img, err = gif.Decode(r) // first frame
	case "image/webp":
		img, err = webp.Decode(r)
	}
	if err != nil {
		return nil, ErrCorrupt
	}
	return img, nil
}

// Process validates data and produces every rendition. For each width it
// encodes a fallback (JPEG, or PNG when the image has transparency) and a
// WebP. The pure-Go WebP encoder is lossless, which beats JPEG on graphics,
// logos and flat colours but not on photographs, so a WebP rendition is kept
// only when it is smaller than its fallback (always for transparent images,
// where it replaces a much larger PNG).
func Process(data []byte) (*Processed, error) {
	ct, ext, err := Sniff(data)
	if err != nil {
		return nil, err
	}
	img, err := decode(data, ct)
	if err != nil {
		return nil, err
	}
	if ct == "image/jpeg" {
		img = orient(img, exifOrientation(data))
	}
	b := img.Bounds()
	out := &Processed{ContentType: ct, Ext: ext, Width: b.Dx(), Height: b.Dy()}
	alpha := hasAlpha(img)
	var widths []int
	for _, w := range Widths {
		if w < b.Dx() {
			widths = append(widths, w)
		}
	}
	widths = append(widths, min(b.Dx(), Widths[len(Widths)-1]))
	tryWebP := true
	for _, w := range widths {
		h := max(1, int(float64(b.Dy())*float64(w)/float64(b.Dx())+0.5))
		var dst draw.Image
		if alpha {
			dst = image.NewNRGBA(image.Rect(0, 0, w, h))
		} else {
			dst = image.NewRGBA(image.Rect(0, 0, w, h))
			draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		}
		if w == b.Dx() {
			draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
		} else {
			draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		}
		var fb bytes.Buffer
		fallback := Rendition{Width: w, Height: h, Format: "jpeg", Ext: "jpg", ContentType: "image/jpeg"}
		if alpha {
			fallback.Format, fallback.Ext, fallback.ContentType = "png", "png", "image/png"
			err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&fb, dst)
		} else {
			err = jpeg.Encode(&fb, dst, &jpeg.Options{Quality: JPEGQuality})
		}
		if err != nil {
			return nil, fmt.Errorf("encode %s %d: %w", fallback.Format, w, err)
		}
		fallback.Data = fb.Bytes()
		out.Renditions = append(out.Renditions, fallback)
		if !tryWebP {
			continue
		}
		var wb bytes.Buffer
		if err := nativewebp.Encode(&wb, dst, nil); err == nil && wb.Len() < fb.Len() {
			out.Renditions = append(out.Renditions, Rendition{Width: w, Height: h, Format: "webp", Ext: "webp", ContentType: "image/webp", Data: wb.Bytes()})
		} else if !alpha {
			// A photograph: lossless WebP will lose at every size, so stop paying for it.
			tryWebP = false
		}
	}
	return out, nil
}

func hasAlpha(img image.Image) bool {
	switch m := img.(type) {
	case *image.YCbCr, *image.Gray, *image.CMYK:
		return false
	case interface{ Opaque() bool }:
		return !m.Opaque()
	}
	return false
}

// exifOrientation reads the Orientation tag (1..8) of a JPEG, or 1.
func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		if marker == 0xDA || marker == 0xD9 {
			return 1
		}
		size := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if size < 2 || i+2+size > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:8]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off : off+2]))
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:e+2]) == 0x0112 {
			if v := int(bo.Uint16(t[e+8 : e+10])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orient returns img rotated/flipped so it displays upright.
func orient(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
