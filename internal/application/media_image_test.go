package application

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestPhotoNormalizationDropsAppendedMetadataAndBoundsThumbnail(t *testing.T) {
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 600, 300))); err != nil {
		t.Fatal(err)
	}
	data := append(source.Bytes(), []byte("private GPS metadata")...)
	full, thumb, w, h, err := processMedia(data, "image/png")
	if err != nil || w != 600 || h != 300 {
		t.Fatalf("%d %d %v", w, h, err)
	}
	if bytes.Contains(full, []byte("private GPS")) || bytes.Contains(thumb, []byte("private GPS")) {
		t.Fatal("source metadata copied")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil || format != "jpeg" || cfg.Width != 480 || cfg.Height != 240 {
		t.Fatalf("thumbnail %v %s %v", cfg, format, err)
	}
	if _, _, _, _, err := processMedia(source.Bytes(), "image/jpeg"); err == nil {
		t.Fatal("MIME spoof accepted")
	}
	if _, _, _, _, err := processMedia([]byte("<script>bad</script>"), "image/png"); err == nil {
		t.Fatal("HTML accepted as photo")
	}
	if _, _, _, _, err := processMedia([]byte("not PDF"), "application/pdf"); err == nil {
		t.Fatal("bad PDF accepted")
	}
	if _, err := boundedMedia(bytes.NewReader(make([]byte, mediaMaxBytes+1))); err == nil {
		t.Fatal("oversize accepted")
	}
}
