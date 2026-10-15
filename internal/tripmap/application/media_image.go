package application

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"io"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const mediaMaxBytes int64 = 10 * 1024 * 1024
const mediaPerCity = 20

// Bound concurrent decoding and the dimensions before allocating image pixels.
var mediaProcessors = make(chan struct{}, 2)

func mediaType(mime string) bool {
	return mime == "image/jpeg" || mime == "image/png" || mime == "image/webp" || mime == "application/pdf"
}

func boundedMedia(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, mediaMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > mediaMaxBytes {
		return nil, fmt.Errorf("file exceeds 10 MB")
	}
	return b, nil
}

// Canonical photos are newly encoded JPEGs: metadata and any appended content
// are not copied. PDF attachments are downloaded, never rendered by the app.
func processMedia(data []byte, mime string) (full, thumb []byte, width, height int, err error) {
	if mime == "application/pdf" {
		if !bytes.HasPrefix(data, []byte("%PDF-")) || !bytes.Contains(data[max(0, len(data)-1024):], []byte("%%EOF")) {
			return nil, nil, 0, 0, fmt.Errorf("invalid PDF")
		}
		return data, nil, 0, 0, nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || "image/"+format != mime || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 8000 || cfg.Height > 8000 || int64(cfg.Width)*int64(cfg.Height) > 24_000_000 {
		return nil, nil, 0, 0, fmt.Errorf("invalid image type or dimensions (maximum 24 MP, 8000 px per side)")
	}
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, 0, 0, fmt.Errorf("corrupt image")
	}
	// Flatten transparency onto white, then discard all source metadata.
	flat := image.NewRGBA(image.Rect(0, 0, cfg.Width, cfg.Height))
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), im, im.Bounds().Min, draw.Over)
	var encoded bytes.Buffer
	if err = jpeg.Encode(&encoded, flat, &jpeg.Options{Quality: 90}); err != nil {
		return nil, nil, 0, 0, err
	}
	if int64(encoded.Len()) > mediaMaxBytes {
		return nil, nil, 0, 0, fmt.Errorf("normalized image exceeds 10 MB")
	}
	w, h := cfg.Width, cfg.Height
	if w > 480 || h > 480 {
		scale := min(480.0/float64(w), 480.0/float64(h))
		w = max(1, int(float64(w)*scale))
		h = max(1, int(float64(h)*scale))
	}
	preview := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.ApproxBiLinear.Scale(preview, preview.Bounds(), flat, flat.Bounds(), draw.Src, nil)
	var small bytes.Buffer
	if err = jpeg.Encode(&small, preview, &jpeg.Options{Quality: 80}); err != nil {
		return nil, nil, 0, 0, err
	}
	return encoded.Bytes(), small.Bytes(), cfg.Width, cfg.Height, nil
}
