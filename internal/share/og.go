package share

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// Preview image size, the 1.91:1 ratio social platforms expect.
const (
	ImageWidth  = 1200
	ImageHeight = 630
)

//go:embed assets/geometry.json
var geometryJSON []byte

//go:embed assets/NotoSansSC-Regular.subset.otf
var regularFont []byte

//go:embed assets/NotoSansSC-Bold.subset.otf
var boldFont []byte

// Fixed texts of the preview. Every character they and the numbers use must
// exist in the embedded font subset; TestFontCoversEveryRenderedCharacter checks it.
const (
	TextBrand    = "山海足迹 / SHANHAI TRAVEL"
	TextTitle    = "我的旅行足迹"
	TextSlogan   = "世界很大，慢慢留下足迹。"
	TextProvince = "已探索省份"
	TextCities   = "全国打卡城市"
	TextRate     = "打卡率 "
	TextTrips    = "旅行次数"
	TextInset    = "南海诸岛"
	TextRecorded = "记录于 "
)

// FixedTexts lists every constant string the renderer draws.
func FixedTexts() []string {
	return []string{TextBrand, TextTitle, TextSlogan, TextProvince, TextCities, TextRate, TextTrips, TextInset, TextRecorded, " /·%.-0123456789"}
}

type geometryProvince struct {
	Code  string         `json:"code"`
	Color string         `json:"color"`
	Rings [][][2]float64 `json:"rings"`
}
type geometryFile struct {
	Version   int                `json:"version"`
	Provinces []geometryProvince `json:"provinces"`
	Inset     struct {
		X, Y, Width, Height float64
		Rings               [][][2]float64 `json:"rings"`
	} `json:"inset"`
}

// Renderer draws the share preview. Build it once with NewRenderer.
type Renderer struct {
	geo     geometryFile
	regular *opentype.Font
	bold    *opentype.Font
	// bounds of the national map and its inset in geometry units.
	minX, minY, maxX, maxY float64
}

var (
	rendererOnce sync.Once
	renderer     *Renderer
	rendererErr  error
)

// DefaultRenderer returns the renderer over the embedded geometry and fonts.
func DefaultRenderer() (*Renderer, error) {
	rendererOnce.Do(func() { renderer, rendererErr = NewRenderer() })
	return renderer, rendererErr
}

// NewRenderer parses the embedded assets.
func NewRenderer() (*Renderer, error) {
	r := &Renderer{}
	if err := json.Unmarshal(geometryJSON, &r.geo); err != nil {
		return nil, fmt.Errorf("share geometry: %w", err)
	}
	if r.geo.Version != 1 || len(r.geo.Provinces) == 0 {
		return nil, errors.New("share geometry: unsupported file")
	}
	var err error
	if r.regular, err = opentype.Parse(regularFont); err != nil {
		return nil, fmt.Errorf("share font: %w", err)
	}
	if r.bold, err = opentype.Parse(boldFont); err != nil {
		return nil, fmt.Errorf("share font: %w", err)
	}
	r.minX, r.minY, r.maxX, r.maxY = math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	grow := func(x, y float64) {
		r.minX, r.maxX = math.Min(r.minX, x), math.Max(r.maxX, x)
		r.minY, r.maxY = math.Min(r.minY, y), math.Max(r.maxY, y)
	}
	// The app clips the national map to its frame: far southern islands lie beyond
	// the bottom of the inset and are cropped, so they must not enlarge the camera.
	bottom := r.geo.Inset.Y + r.geo.Inset.Height
	for _, p := range r.geo.Provinces {
		for _, ring := range p.Rings {
			for _, pt := range ring {
				if pt[1] <= bottom {
					grow(pt[0], pt[1])
				}
			}
		}
	}
	grow(r.geo.Inset.X, r.geo.Inset.Y)
	grow(r.geo.Inset.X+r.geo.Inset.Width, r.geo.Inset.Y+r.geo.Inset.Height)
	return r, nil
}

// ProvinceCodes lists the provinces in the geometry file.
func (r *Renderer) ProvinceCodes() []string {
	codes := make([]string, 0, len(r.geo.Provinces))
	for _, p := range r.geo.Provinces {
		codes = append(codes, p.Code)
	}
	return codes
}

// ProvinceColor is the fixed palette color of a province.
func (r *Renderer) ProvinceColor(code string) string {
	for _, p := range r.geo.Provinces {
		if p.Code == code {
			return p.Color
		}
	}
	return ""
}

// HasGlyphs reports the first rune of s the embedded fonts cannot draw.
func (r *Renderer) HasGlyphs(s string) (missing rune, ok bool) {
	var buf sfnt.Buffer
	for _, f := range []*opentype.Font{r.regular, r.bold} {
		for _, c := range s {
			idx, err := f.GlyphIndex(&buf, c)
			if err != nil || idx == 0 {
				return c, false
			}
		}
	}
	return 0, true
}

func (r *Renderer) face(bold bool, size float64) (font.Face, error) {
	src := r.regular
	if bold {
		src = r.bold
	}
	return opentype.NewFace(src, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
}

var (
	colBackground = color.RGBA{0xf6, 0xf7, 0xef, 0xff}
	colCard       = color.RGBA{0xff, 0xfe, 0xf9, 0xff}
	colCardLine   = color.RGBA{0xde, 0xe5, 0xd9, 0xff}
	colEmpty      = color.RGBA{0xe9, 0xee, 0xe9, 0xff}
	colInsetFill  = color.RGBA{0xfa, 0xfb, 0xf8, 0xff}
	colInsetLine  = color.RGBA{0xd6, 0xdd, 0xd7, 0xff}
	colInsetSea   = color.RGBA{0xb6, 0xc5, 0xba, 0xff}
	colBrand      = color.RGBA{0x69, 0x81, 0x6b, 0xff}
	colTitle      = color.RGBA{0x35, 0x59, 0x43, 0xff}
	colMuted      = color.RGBA{0x7b, 0x88, 0x74, 0xff}
	colFooter     = color.RGBA{0x92, 0x9b, 0x89, 0xff}
)

func parseHex(s string) color.RGBA {
	var r, g, b uint8
	if _, err := fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return colEmpty
	}
	return color.RGBA{r, g, b, 0xff}
}

// fillPolys fills the closed polygons with c.
func fillPolys(dst *image.RGBA, polys [][][2]float64, c color.Color) {
	b := dst.Bounds()
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	for _, poly := range polys {
		if len(poly) < 3 {
			continue
		}
		z.MoveTo(float32(poly[0][0]), float32(poly[0][1]))
		for _, p := range poly[1:] {
			z.LineTo(float32(p[0]), float32(p[1]))
		}
		z.ClosePath()
	}
	z.Draw(dst, b, image.NewUniform(c), image.Point{})
}

// strokePolys draws the outline of the polygons, one quad per segment plus a
// square at every vertex so corners stay closed.
func strokePolys(dst *image.RGBA, polys [][][2]float64, width float64, c color.Color, closed bool) {
	b := dst.Bounds()
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	h := width / 2
	quad := func(x0, y0, x1, y1 float64) {
		dx, dy := x1-x0, y1-y0
		l := math.Hypot(dx, dy)
		if l == 0 {
			return
		}
		nx, ny := -dy/l*h, dx/l*h
		z.MoveTo(float32(x0+nx), float32(y0+ny))
		z.LineTo(float32(x1+nx), float32(y1+ny))
		z.LineTo(float32(x1-nx), float32(y1-ny))
		z.LineTo(float32(x0-nx), float32(y0-ny))
		z.ClosePath()
	}
	for _, poly := range polys {
		for i := 1; i < len(poly); i++ {
			quad(poly[i-1][0], poly[i-1][1], poly[i][0], poly[i][1])
		}
		if closed && len(poly) > 2 {
			quad(poly[len(poly)-1][0], poly[len(poly)-1][1], poly[0][0], poly[0][1])
		}
		for _, p := range poly {
			z.MoveTo(float32(p[0]-h), float32(p[1]-h))
			z.LineTo(float32(p[0]+h), float32(p[1]-h))
			z.LineTo(float32(p[0]+h), float32(p[1]+h))
			z.LineTo(float32(p[0]-h), float32(p[1]+h))
			z.ClosePath()
		}
	}
	z.Draw(dst, b, image.NewUniform(c), image.Point{})
}

func roundedRect(x, y, w, h, r float64) [][2]float64 {
	var pts [][2]float64
	arc := func(cx, cy, from float64) {
		for i := 0; i <= 8; i++ {
			a := from + float64(i)*math.Pi/16
			pts = append(pts, [2]float64{cx + r*math.Cos(a), cy + r*math.Sin(a)})
		}
	}
	arc(x+w-r, y+r, -math.Pi/2)
	arc(x+w-r, y+h-r, 0)
	arc(x+r, y+h-r, math.Pi/2)
	arc(x+r, y+r, math.Pi)
	return pts
}

func (r *Renderer) text(dst *image.RGBA, s string, x, baseline float64, size float64, bold bool, c color.Color, spacing float64) error {
	face, err := r.face(bold, size)
	if err != nil {
		return err
	}
	defer func() { _ = face.Close() }()
	d := font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face}
	d.Dot = fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(baseline * 64)}
	if spacing == 0 {
		d.DrawString(s)
		return nil
	}
	for _, ch := range s {
		d.DrawString(string(ch))
		d.Dot.X += fixed.Int26_6(spacing * 64)
	}
	return nil
}

func pct(count, total int) string {
	if total == 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", float64(count)/float64(total)*100)
}

// Render draws the 1200x630 preview of the content. Only province-level
// coloring, aggregate numbers and the publication day are drawn: never city
// names, notes, dates of trips or photos, whatever the share publishes.
func (r *Renderer) Render(c *Content) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, ImageWidth, ImageHeight))
	draw.Draw(img, img.Bounds(), image.NewUniform(colBackground), image.Point{}, draw.Src)
	card := [][][2]float64{roundedRect(24, 24, ImageWidth-48, ImageHeight-48, 28)}
	strokePolys(img, card, 2, colCardLine, true)
	fillPolys(img, [][][2]float64{roundedRect(25, 25, ImageWidth-50, ImageHeight-50, 27)}, colCard)

	// Map, scaled to fit the left part of the card and clipped to the app's frame.
	boxX, boxY, boxW, boxH := 56.0, 70.0, 660.0, 490.0
	bw, bh := r.maxX-r.minX, r.maxY-r.minY
	scale := math.Min(boxW/bw, boxH/bh)
	mapW, mapH := int(math.Ceil(bw*scale)), int(math.Ceil(bh*scale))
	mapImg := image.NewRGBA(image.Rect(0, 0, mapW, mapH))
	ox, oy := -r.minX*scale, -r.minY*scale
	tf := func(rings [][][2]float64) [][][2]float64 {
		out := make([][][2]float64, len(rings))
		for i, ring := range rings {
			pts := make([][2]float64, len(ring))
			for j, p := range ring {
				pts[j] = [2]float64{p[0]*scale + ox, p[1]*scale + oy}
			}
			out[i] = pts
		}
		return out
	}
	explored := map[string]bool{}
	for _, code := range c.Provinces {
		explored[code] = true
	}
	for _, p := range r.geo.Provinces {
		fill := colEmpty
		if explored[p.Code] {
			fill = parseHex(p.Color)
		}
		fillPolys(mapImg, tf(p.Rings), fill)
	}
	for _, p := range r.geo.Provinces {
		strokePolys(mapImg, tf(p.Rings), 1.2, colCard, true)
	}
	in := r.geo.Inset
	box := tf([][][2]float64{{{in.X, in.Y}, {in.X + in.Width, in.Y}, {in.X + in.Width, in.Y + in.Height}, {in.X, in.Y + in.Height}}})
	fillPolys(mapImg, box, colInsetFill)
	strokePolys(mapImg, box, 1, colInsetLine, true)
	shifted := make([][][2]float64, len(in.Rings))
	for i, ring := range in.Rings {
		pts := make([][2]float64, len(ring))
		for j, p := range ring {
			pts[j] = [2]float64{p[0] + in.X, p[1] + in.Y}
		}
		shifted[i] = pts
	}
	strokePolys(mapImg, tf(shifted), 1, colInsetSea, false)
	label := box[0]
	if err := r.text(mapImg, TextInset, label[0][0]+8*scale, label[2][1]-5*scale, 11*math.Max(scale, 1), false, colBrand, 0); err != nil {
		return nil, err
	}

	at := image.Pt(int(boxX+(boxW-float64(mapW))/2), int(boxY+(boxH-float64(mapH))/2))
	draw.Draw(img, mapImg.Bounds().Add(at), mapImg, image.Point{}, draw.Over)

	// Right column: title and aggregate numbers.
	const x = 756.0
	if err := r.text(img, TextBrand, x, 96, 15, false, colBrand, 2.4); err != nil {
		return nil, err
	}
	if err := r.text(img, TextTitle, x, 160, 46, true, colTitle, 0); err != nil {
		return nil, err
	}
	if err := r.text(img, TextSlogan, x, 200, 20, false, colMuted, 0); err != nil {
		return nil, err
	}
	fillPolys(img, [][][2]float64{{{x, 232}, {x + 380, 232}, {x + 380, 233.5}, {x, 233.5}}}, colCardLine)
	st := c.Stats
	metrics := []struct{ value, label string }{
		{fmt.Sprintf("%d / %d", st.ExploredProvinces, st.TotalProvinces), TextProvince},
		{fmt.Sprintf("%d / %d", st.Cities, st.TotalCities), TextCities + " · " + TextRate + pct(st.Cities, st.TotalCities)},
		{fmt.Sprintf("%d", st.Trips), TextTrips},
	}
	for i, m := range metrics {
		y := 296 + float64(i)*98
		if err := r.text(img, m.value, x, y, 50, true, colTitle, 0); err != nil {
			return nil, err
		}
		if err := r.text(img, m.label, x, y+32, 19, false, colMuted, 0); err != nil {
			return nil, err
		}
	}
	if err := r.text(img, TextRecorded+c.PublishedOn, x, 584, 16, false, colFooter, 0); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
