package application

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kongken/mono/internal/metro/ent"
	"github.com/kongken/mono/internal/metro/ent/line"
	"github.com/kongken/mono/internal/metro/ent/schema"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
)

// The record types below are the catalog's editable fields in one shape. They
// are the metro-catalog/1 import and export format, the input the admin RPCs are
// converted to and the snapshots written to the audit log, so a rule written
// against them holds for every way the catalog changes. References between
// records use natural keys (city slug, rolling stock code).

// CatalogFormat is the value of "format" in an import or export document.
const CatalogFormat = "metro-catalog/1"

type cityDoc struct {
	Slug         string   `json:"slug"`
	Name         string   `json:"name"`
	HeaderImages []string `json:"header_images"`
	MapSVGURL    string   `json:"map_svg_url"`
	SortOrder    int      `json:"sort_order"`
	Region       string   `json:"region,omitempty"`
}

type stockDoc struct {
	Code     string   `json:"code"`
	LengthM  *float64 `json:"length_m,omitempty"`
	WidthM   *float64 `json:"width_m,omitempty"`
	Capacity *int     `json:"capacity,omitempty"`
}

type lineDoc struct {
	Code         string   `json:"code"`
	City         string   `json:"city"`
	Name         string   `json:"name"`
	Number       int      `json:"number"`
	Status       string   `json:"status"`
	RollingStock string   `json:"rolling_stock,omitempty"`
	Cars         int      `json:"cars"`
	CarsAddition int      `json:"cars_addition"`
	Stations     int      `json:"stations"`
	LengthKm     float64  `json:"length_km"`
	Color        string   `json:"color"`
	CommencedOn  string   `json:"commenced_on,omitempty"`
	ExtendedOn   string   `json:"extended_on,omitempty"`
	IsLoop       bool     `json:"is_loop"`
	VehicleType  string   `json:"vehicle_type,omitempty"`
	StationNames []string `json:"station_names,omitempty"`
}

type topicDoc struct {
	Slug              string   `json:"slug"`
	Name              string   `json:"name"`
	RollingStockCodes []string `json:"rolling_stock_codes"`
	Cars              []int    `json:"cars"`
}

// catalogDoc is a whole metro-catalog/1 document.
type catalogDoc struct {
	Format        string     `json:"format"`
	Cities        []cityDoc  `json:"cities"`
	RollingStocks []stockDoc `json:"rolling_stocks"`
	Lines         []lineDoc  `json:"lines"`
	Topics        []topicDoc `json:"topics"`
}

// ---- normalization: defaults that make equal records compare equal ----

func (d *cityDoc) normalize() {
	if d.HeaderImages == nil {
		d.HeaderImages = []string{}
	}
}

func (d *lineDoc) normalize() {
	if d.Status == "" {
		d.Status = string(line.StatusOperating)
	}
}

func (d *topicDoc) normalize() {
	if d.RollingStockCodes == nil {
		d.RollingStockCodes = []string{}
	}
	if d.Cars == nil {
		d.Cars = []int{}
	}
}

// ---- equality ----

func floatPtrEqual(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return math.Abs(*a-*b) < 1e-9
}

func (d cityDoc) equal(o cityDoc) bool {
	return d.Slug == o.Slug && d.Name == o.Name && slices.Equal(d.HeaderImages, o.HeaderImages) &&
		d.MapSVGURL == o.MapSVGURL && d.SortOrder == o.SortOrder && d.Region == o.Region
}

func (d stockDoc) equal(o stockDoc) bool {
	capacityEqual := d.Capacity == o.Capacity || (d.Capacity != nil && o.Capacity != nil && *d.Capacity == *o.Capacity)
	return d.Code == o.Code && floatPtrEqual(d.LengthM, o.LengthM) && floatPtrEqual(d.WidthM, o.WidthM) && capacityEqual
}

func (d lineDoc) equal(o lineDoc) bool {
	return d.Code == o.Code && d.City == o.City && d.Name == o.Name && d.Number == o.Number && d.Status == o.Status &&
		d.RollingStock == o.RollingStock && d.Cars == o.Cars && d.CarsAddition == o.CarsAddition &&
		d.Stations == o.Stations && math.Abs(d.LengthKm-o.LengthKm) < 1e-9 && d.Color == o.Color &&
		d.CommencedOn == o.CommencedOn && d.ExtendedOn == o.ExtendedOn && d.IsLoop == o.IsLoop &&
		d.VehicleType == o.VehicleType && slices.Equal(d.StationNames, o.StationNames)
}

func (d topicDoc) equal(o topicDoc) bool {
	return d.Slug == o.Slug && d.Name == o.Name && slices.Equal(d.RollingStockCodes, o.RollingStockCodes) && slices.Equal(d.Cars, o.Cars)
}

// ---- records from stored rows ----

func cityDocOf(c *ent.City) cityDoc {
	d := cityDoc{Slug: c.Slug, Name: c.Name, HeaderImages: slices.Clone(c.HeaderImages), MapSVGURL: c.MapSvgURL, SortOrder: c.SortOrder, Region: c.Region}
	d.normalize()
	return d
}

func stockDocOf(r *ent.RollingStock) stockDoc {
	return stockDoc{Code: r.Code, LengthM: r.LengthM, WidthM: r.WidthM, Capacity: r.Capacity}
}

// lineDocOf needs the city and rolling stock edges loaded.
func lineDocOf(l *ent.Line) lineDoc {
	d := lineDoc{
		Code: l.Code, Name: l.Name, Number: l.Number, Status: string(l.Status), Cars: l.Cars,
		CarsAddition: l.CarsAddition, Stations: l.Stations, LengthKm: l.LengthKm, Color: l.Color, IsLoop: l.IsLoop,
		VehicleType: l.VehicleType, StationNames: slices.Clone(l.StationNames),
	}
	if l.Edges.City != nil {
		d.City = l.Edges.City.Slug
	}
	if l.Edges.RollingStock != nil {
		d.RollingStock = l.Edges.RollingStock.Code
	}
	if l.CommencedOn != nil {
		d.CommencedOn = l.CommencedOn.Format(dateLayout)
	}
	if l.ExtendedOn != nil {
		d.ExtendedOn = l.ExtendedOn.Format(dateLayout)
	}
	return d
}

func topicDocOf(t *ent.RollingStockTopic) topicDoc {
	d := topicDoc{Slug: t.Slug, Name: t.Name, RollingStockCodes: slices.Clone(t.RollingStockCodes), Cars: slices.Clone(t.Cars)}
	d.normalize()
	return d
}

// ---- records from RPC input ----

func cityFromInput(in *metrov1.CityInput) cityDoc {
	d := cityDoc{Slug: in.GetSlug(), Name: in.GetName(), HeaderImages: slices.Clone(in.GetHeaderImages()),
		MapSVGURL: in.GetMapSvgUrl(), SortOrder: int(in.GetSortOrder()), Region: in.GetRegion()}
	d.normalize()
	return d
}

func stockFromInput(in *metrov1.RollingStockInput) stockDoc {
	d := stockDoc{Code: in.GetCode()}
	if in.LengthM != nil {
		v := in.GetLengthM()
		d.LengthM = &v
	}
	if in.WidthM != nil {
		v := in.GetWidthM()
		d.WidthM = &v
	}
	if in.Capacity != nil {
		v := int(in.GetCapacity())
		d.Capacity = &v
	}
	return d
}

func lineFromInput(in *metrov1.LineInput) lineDoc {
	d := lineDoc{
		Code: in.GetCode(), City: in.GetCitySlug(), Name: in.GetName(), Number: int(in.GetNumber()),
		RollingStock: in.GetRollingStockCode(), Cars: int(in.GetCars()), CarsAddition: int(in.GetCarsAddition()),
		Stations: int(in.GetStations()), LengthKm: in.GetLengthKm(), Color: in.GetColor(),
		CommencedOn: in.GetCommencedOn(), ExtendedOn: in.GetExtendedOn(), IsLoop: in.GetIsLoop(),
		VehicleType: in.GetVehicleType(), StationNames: slices.Clone(in.GetStationNames()),
	}
	switch s := in.GetStatus(); s {
	case metrov1.LineStatus_LINE_STATUS_UNSPECIFIED:
	default:
		if name, ok := lineStatusNames[s]; ok {
			d.Status = string(name)
		} else {
			d.Status = fmt.Sprintf("unknown(%d)", int32(s))
		}
	}
	d.normalize()
	return d
}

func topicFromInput(in *metrov1.RollingStockTopicInput) topicDoc {
	d := topicDoc{Slug: in.GetSlug(), Name: in.GetName(), RollingStockCodes: slices.Clone(in.GetRollingStockCodes())}
	for _, c := range in.GetCars() {
		d.Cars = append(d.Cars, int(c))
	}
	d.normalize()
	return d
}

// ---- RPC messages from records ----

func cityInputOf(d cityDoc) *metrov1.CityInput {
	return &metrov1.CityInput{Slug: d.Slug, Name: d.Name, HeaderImages: slices.Clone(d.HeaderImages),
		MapSvgUrl: d.MapSVGURL, SortOrder: int32(d.SortOrder), Region: d.Region}
}

func stockInputOf(d stockDoc) *metrov1.RollingStockInput {
	out := &metrov1.RollingStockInput{Code: d.Code, LengthM: d.LengthM, WidthM: d.WidthM}
	if d.Capacity != nil {
		v := int32(*d.Capacity)
		out.Capacity = &v
	}
	return out
}

func lineInputOf(d lineDoc) *metrov1.LineInput {
	out := &metrov1.LineInput{
		Code: d.Code, CitySlug: d.City, Name: d.Name, Number: int32(d.Number), RollingStockCode: d.RollingStock,
		Cars: int32(d.Cars), CarsAddition: int32(d.CarsAddition), Stations: int32(d.Stations), LengthKm: d.LengthKm,
		Color: d.Color, CommencedOn: d.CommencedOn, ExtendedOn: d.ExtendedOn, IsLoop: d.IsLoop,
		VehicleType: d.VehicleType, StationNames: slices.Clone(d.StationNames),
	}
	out.Status = statusEnum(line.Status(d.Status))
	return out
}

func topicInputOf(d topicDoc) *metrov1.RollingStockTopicInput {
	out := &metrov1.RollingStockTopicInput{Slug: d.Slug, Name: d.Name, RollingStockCodes: slices.Clone(d.RollingStockCodes)}
	for _, c := range d.Cars {
		out.Cars = append(out.Cars, int32(c))
	}
	return out
}

// ---- validation ----

// maxFieldErrors bounds how many problems one request reports.
const maxFieldErrors = 100

// errList collects field-level problems.
type errList struct {
	errs      []*metrov1.FieldError
	truncated bool
}

func (l *errList) add(field, format string, args ...any) {
	if len(l.errs) >= maxFieldErrors {
		l.truncated = true
		return
	}
	l.errs = append(l.errs, &metrov1.FieldError{Field: field, Reason: fmt.Sprintf(format, args...)})
}

func (l *errList) any() bool { return len(l.errs) > 0 }

var (
	slugPattern  = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	codePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)
	colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

const (
	maxURLLength    = 512
	maxHeaderImages = 8
	maxListItems    = 32
	maxSortOrder    = 1_000_000
)

// cleanText reports whether s is 1 to max characters with no control characters
// and no leading or trailing blanks.
func cleanText(s string, max int) string {
	n := utf8.RuneCountInString(s)
	switch {
	case !utf8.ValidString(s):
		return "must be valid UTF-8"
	case n < 1:
		return "is required"
	case n > max:
		return fmt.Sprintf("must be at most %d characters", max)
	case strings.TrimSpace(s) != s:
		return "must not start or end with blanks"
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "must not contain control characters"
		}
	}
	return ""
}

func (l *errList) text(field, s string, max int) {
	if why := cleanText(s, max); why != "" {
		l.add(field, "%s", why)
	}
}

// httpsURL reports whether s is a plain https address.
func httpsURL(s string) bool {
	if len(s) > maxURLLength {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

// atMostTwoDecimals reports whether v survives storage as numeric(_,2) unchanged.
func atMostTwoDecimals(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v*100-math.Round(v*100)) < 1e-6
}

// parseDate accepts only YYYY-MM-DD.
func parseDate(s string) (time.Time, bool) {
	if len(s) != len(dateLayout) {
		return time.Time{}, false
	}
	t, err := time.Parse(dateLayout, s)
	return t, err == nil && t.Year() >= 1800
}

func (l *errList) intRange(field string, v, lo, hi int) {
	if v < lo || v > hi {
		l.add(field, "must be between %d and %d", lo, hi)
	}
}

func (d cityDoc) validate(p string, l *errList) {
	switch {
	case d.Slug == "":
		l.add(p+"slug", "is required")
	case len(d.Slug) > 32 || !slugPattern.MatchString(d.Slug):
		l.add(p+"slug", "must be lowercase letters, digits and hyphens, at most 32 characters")
	}
	l.text(p+"name", d.Name, 32)
	if !slices.Contains([]string{"", "east", "north", "south", "southwest", "central", "northeast", "northwest"}, d.Region) {
		l.add(p+"region", "must be empty or east, north, south, southwest, central, northeast, northwest")
	}
	if len(d.HeaderImages) > maxHeaderImages {
		l.add(p+"header_images", "at most %d images", maxHeaderImages)
	}
	for i, u := range d.HeaderImages {
		if !httpsURL(u) {
			l.add(fmt.Sprintf("%sheader_images[%d]", p, i), "must be an https address of at most %d characters", maxURLLength)
		}
	}
	if d.MapSVGURL != "" && !httpsURL(d.MapSVGURL) {
		l.add(p+"map_svg_url", "must be empty or an https address of at most %d characters", maxURLLength)
	}
	l.intRange(p+"sort_order", d.SortOrder, -maxSortOrder, maxSortOrder)
}

func (d stockDoc) validate(p string, l *errList) {
	l.text(p+"code", d.Code, 32)
	for _, dim := range []struct {
		field string
		v     *float64
	}{{"length_m", d.LengthM}, {"width_m", d.WidthM}} {
		if dim.v != nil && (!atMostTwoDecimals(*dim.v) || *dim.v <= 0 || *dim.v > 9999.99) {
			l.add(p+dim.field, "must be greater than 0, at most 9999.99, with at most two decimals")
		}
	}
	if d.Capacity != nil && (*d.Capacity < 1 || *d.Capacity > 100000) {
		l.add(p+"capacity", "must be between 1 and 100000")
	}
}

// validate checks everything that does not need the database; references are
// resolved by the caller.
func (d lineDoc) validate(p string, l *errList) {
	switch {
	case d.Code == "":
		l.add(p+"code", "is required")
	case !codePattern.MatchString(d.Code):
		l.add(p+"code", "must match [a-z0-9][a-z0-9-]{1,31}")
	}
	if d.City == "" {
		l.add(p+"city", "is required")
	}
	l.text(p+"name", d.Name, 32)
	l.intRange(p+"number", d.Number, 0, 9999)
	if !slices.Contains(schema.LineStatuses, d.Status) {
		l.add(p+"status", "must be one of %s", strings.Join(schema.LineStatuses, ", "))
	}
	if d.RollingStock != "" {
		l.text(p+"rolling_stock", d.RollingStock, 32)
	}
	l.intRange(p+"cars", d.Cars, 0, 99)
	l.intRange(p+"cars_addition", d.CarsAddition, 0, 99)
	l.intRange(p+"stations", d.Stations, 0, 999)
	if !slices.Contains([]string{"", "a", "b", "l", "rubber", "other"}, d.VehicleType) {
		l.add(p+"vehicle_type", "must be empty or a, b, l, rubber, other")
	}
	if len(d.StationNames) > 999 || (len(d.StationNames) > 0 && len(d.StationNames) != d.Stations) {
		l.add(p+"station_names", "must be empty or contain exactly stations entries, at most 999")
	}
	for i, name := range d.StationNames {
		l.text(fmt.Sprintf("%sstation_names[%d]", p, i), name, 64)
	}
	if !atMostTwoDecimals(d.LengthKm) || d.LengthKm < 0 || d.LengthKm > 9999.99 {
		l.add(p+"length_km", "must be between 0 and 9999.99 with at most two decimals")
	}
	if !colorPattern.MatchString(d.Color) {
		l.add(p+"color", "must be #RRGGBB")
	}
	var commenced, extended time.Time
	var okCommenced, okExtended bool
	if d.CommencedOn != "" {
		if commenced, okCommenced = parseDate(d.CommencedOn); !okCommenced {
			l.add(p+"commenced_on", "must be a date as YYYY-MM-DD")
		}
	}
	if d.ExtendedOn != "" {
		if extended, okExtended = parseDate(d.ExtendedOn); !okExtended {
			l.add(p+"extended_on", "must be a date as YYYY-MM-DD")
		}
	}
	if okCommenced && okExtended && extended.Before(commenced) {
		l.add(p+"extended_on", "must not be before commenced_on")
	}
}

func (d topicDoc) validate(p string, l *errList) {
	switch {
	case d.Slug == "":
		l.add(p+"slug", "is required")
	case len(d.Slug) > 32 || !slugPattern.MatchString(d.Slug):
		l.add(p+"slug", "must be lowercase letters, digits and hyphens, at most 32 characters")
	}
	l.text(p+"name", d.Name, 64)
	if len(d.RollingStockCodes) > maxListItems {
		l.add(p+"rolling_stock_codes", "at most %d codes", maxListItems)
	}
	for i, c := range d.RollingStockCodes {
		if why := cleanText(c, 32); why != "" {
			l.add(fmt.Sprintf("%srolling_stock_codes[%d]", p, i), "%s", why)
		} else if slices.Index(d.RollingStockCodes, c) != i {
			l.add(fmt.Sprintf("%srolling_stock_codes[%d]", p, i), "is listed twice")
		}
	}
	if len(d.Cars) > maxListItems {
		l.add(p+"cars", "at most %d car counts", maxListItems)
	}
	for i, c := range d.Cars {
		f := fmt.Sprintf("%scars[%d]", p, i)
		if c < 1 || c > 99 {
			l.add(f, "must be between 1 and 99")
		} else if slices.Index(d.Cars, c) != i {
			l.add(f, "is listed twice")
		}
	}
	if len(d.RollingStockCodes) == 0 && len(d.Cars) == 0 {
		l.add(p+"cars", "give rolling_stock_codes or cars: a topic with neither would match every line")
	}
}
