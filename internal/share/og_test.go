package share

import (
	"bytes"
	"image/png"
	"os"
	"strings"
	"testing"
)

func sampleContent() *Content {
	return &Content{Version: ContentVersion, PublishedOn: "2026-10-14", Provinces: []string{"110000", "330000", "350000", "440000", "510000", "530000", "650000"},
		Stats: Stats{ExploredProvinces: 7, TotalProvinces: 34, Cities: 27, TotalCities: 391, Trips: 31}}
}

func TestRenderProducesA1200x630PNG(t *testing.T) {
	r, err := DefaultRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render(sampleContent())
	if err != nil {
		t.Fatal(err)
	}
	if dump := os.Getenv("TRIPMAP_DUMP_OG"); dump != "" {
		if err := os.WriteFile(dump, out, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 1200 || b.Dy() != 630 {
		t.Fatalf("size = %dx%d, want 1200x630", b.Dx(), b.Dy())
	}
	if len(out) > 600<<10 {
		t.Fatalf("preview is %d bytes; social crawlers reject large images", len(out))
	}
}

func TestFontCoversEveryRenderedCharacter(t *testing.T) {
	r, err := DefaultRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range FixedTexts() {
		if c, ok := r.HasGlyphs(text); !ok {
			t.Fatalf("font subset has no glyph for %q in %q", c, text)
		}
	}
	if c, ok := r.HasGlyphs(strings.Repeat("0123456789%-/ .", 2)); !ok {
		t.Fatalf("missing digit glyph %q", c)
	}
	if _, ok := r.HasGlyphs("猫"); ok {
		t.Fatal("the subset must not pretend to cover arbitrary text")
	}
}
