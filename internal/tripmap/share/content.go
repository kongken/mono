// Package share builds and renders the public share page (F6): the immutable
// content snapshot, its statistics, the preview image and the HTML metadata.
//
// Everything a visitor can receive is defined by the types in this file. They
// are an explicit allow-list: nothing is ever marshalled from account, trip or
// media records, so identifiers such as user, trip, media IDs, object keys and
// file names cannot reach a public response.
package share

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"time"

	"go.orx.me/apps/trip-map/internal/tripmap/regions"
)

const (
	// ContentVersion is the schema of Content.
	ContentVersion = 1
	// MaxPhotos is the most photos one share can show.
	MaxPhotos = 12
	// MaxPayloadBytes bounds the gzip-compressed stored snapshot.
	MaxPayloadBytes = 4 << 20
	maxRawBytes     = 64 << 20
)

var (
	// ErrTooLarge means the snapshot does not fit MaxPayloadBytes.
	ErrTooLarge = errors.New("share content too large")
	// ErrPhotoUnavailable means a chosen photo cannot be shown (deleted, not a
	// ready JPEG, or its city is not part of the share).
	ErrPhotoUnavailable = errors.New("share photo unavailable")
)

// Settings are the owner's choices that decide what is published.
type Settings struct {
	Cities   bool
	Dates    bool
	Notes    bool
	Photos   bool
	PhotoIDs []string
}

// Validate checks field dependencies and photo selection shape. It does not
// look at the database.
func (s Settings) Validate() error {
	if (s.Dates || s.Notes || s.Photos) && !s.Cities {
		return errors.New("dates, notes and photos require city names")
	}
	if !s.Photos && len(s.PhotoIDs) > 0 {
		return errors.New("photo_ids requires show_photos")
	}
	if len(s.PhotoIDs) > MaxPhotos {
		return fmt.Errorf("at most %d photos can be shared", MaxPhotos)
	}
	seen := map[string]bool{}
	for _, id := range s.PhotoIDs {
		if id == "" || len(id) > 64 || seen[id] {
			return errors.New("photo_ids must be unique, non-empty media IDs")
		}
		seen[id] = true
	}
	return nil
}

// Adds reports whether next publishes anything old did not: a city list, a
// field or a photo. Such a change needs a fresh read of the footprints; the
// rest only removes content from an existing snapshot.
func (s Settings) Adds(next Settings) bool {
	if next.Cities && !s.Cities || next.Dates && !s.Dates || next.Notes && !s.Notes || next.Photos && !s.Photos {
		return true
	}
	for _, id := range next.PhotoIDs {
		if !slices.Contains(s.PhotoIDs, id) {
			return true
		}
	}
	return false
}

// Fields tells the page which optional parts were published.
type Fields struct {
	Cities bool `json:"cities"`
	Dates  bool `json:"dates"`
	Notes  bool `json:"notes"`
	Photos bool `json:"photos"`
}

// ProvinceStat is one province's city progress. Only provinces with at least
// one visited city appear.
type ProvinceStat struct {
	Code   string `json:"code"`
	Cities int    `json:"cities"`
	Total  int    `json:"total"`
}

// Stats are aggregates only, counted like the app's statistics page:
// municipalities and the two special administrative regions count one city
// nationwide, a province marked on its own counts as explored without cities.
type Stats struct {
	ExploredProvinces int            `json:"explored_provinces"`
	TotalProvinces    int            `json:"total_provinces"`
	Cities            int            `json:"cities"`
	TotalCities       int            `json:"total_cities"`
	Trips             int            `json:"trips"`
	ByProvince        []ProvinceStat `json:"by_province"`
}

// Photo is a shown photo. Handle is random per publication and only the server
// knows which media it stands for.
type Photo struct {
	Handle string `json:"id"`
	Width  int    `json:"w"`
	Height int    `json:"h"`
}

// Trip is one visit. Without dates and notes it is an empty object: the page
// still shows how many times the city was visited.
type Trip struct {
	Date   string  `json:"date,omitempty"`
	Note   string  `json:"note,omitempty"`
	Photos []Photo `json:"photos,omitempty"`
}

// City appears only when city names are published.
type City struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	Province string  `json:"province"`
	Color    string  `json:"color"`
	Trips    []Trip  `json:"trips"`
	Photos   []Photo `json:"photos,omitempty"`
}

// Content is the complete public document, also served verbatim by
// /api/share/<token>/data.
type Content struct {
	Version int `json:"v"`
	// PublishedOn is the UTC calendar day of publication.
	PublishedOn string   `json:"published_on"`
	Fields      Fields   `json:"fields"`
	Stats       Stats    `json:"stats"`
	Provinces   []string `json:"provinces"`
	Cities      []City   `json:"cities,omitempty"`
}

// Payload is what is stored: the public content plus the server-only map from
// photo handle to media ID.
type Payload struct {
	Content Content           `json:"content"`
	Photos  map[string]string `json:"photos,omitempty"`
}

// SourceTrip, SourceCity and Source describe the owner's footprints. They are
// plain values so the builder needs neither a database nor the ORM types.
type SourceTrip struct{ ID, Date, Note string }
type SourceCity struct {
	Code, Name, Province, Color string
	Trips                       []SourceTrip
}
type SourcePhoto struct {
	Region, TripID string
	Width, Height  int
}
type Source struct {
	Cities []SourceCity
	// Marks are provinces marked on their own.
	Marks []string
	// Photos holds the owner's shareable photos by media ID; the caller only
	// includes ready JPEGs of the owner.
	Photos map[string]SourcePhoto
}

// districtDestinations count one city in nationwide statistics.
var districtDestinations = map[string]bool{"110000": true, "120000": true, "310000": true, "500000": true, "810000": true, "820000": true}

// IsDistrict mirrors isDistrictDestination in src/lib/statistics.ts.
func IsDistrict(province string) bool { return districtDestinations[province] }

// ComputeStats follows src/lib/statistics.ts; scripts/fixtures/share-stats.json
// drives both implementations.
func ComputeStats(idx *regions.Index, src Source) (Stats, []string) {
	explored := map[string]bool{}
	for _, m := range src.Marks {
		explored[m] = true
	}
	cities := map[string]bool{}
	perProvince := map[string]int{}
	trips := 0
	for _, c := range src.Cities {
		province := regions.CanonicalProvince(c.Code, c.Province)
		explored[province] = true
		perProvince[province]++
		trips += len(c.Trips)
		if IsDistrict(province) {
			cities[province] = true
		} else {
			cities[c.Code] = true
		}
	}
	st := Stats{ExploredProvinces: len(explored), TotalProvinces: idx.Provinces(), Cities: len(cities), Trips: trips, ByProvince: []ProvinceStat{}}
	for _, code := range idx.ProvinceCodes() {
		if IsDistrict(code) {
			st.TotalCities++
		} else {
			st.TotalCities += idx.ProvinceCities(code)
		}
		if n := perProvince[code]; n > 0 {
			st.ByProvince = append(st.ByProvince, ProvinceStat{Code: code, Cities: n, Total: idx.ProvinceCities(code)})
		}
	}
	codes := make([]string, 0, len(explored))
	for code := range explored {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return st, codes
}

// orderTrips is the display order of a city's trips: chronological with the
// undated last when dates are published. Without dates the order is by trip ID,
// which is arbitrary, so the sequence cannot reveal when the trips happened.
func orderTrips(in []SourceTrip, dates bool) []SourceTrip {
	trips := slices.Clone(in)
	sort.SliceStable(trips, func(i, j int) bool {
		if !dates {
			return trips[i].ID < trips[j].ID
		}
		a, b := trips[i].Date, trips[j].Date
		if (a == "") != (b == "") {
			return b == ""
		}
		if a != b {
			return a < b
		}
		return trips[i].ID < trips[j].ID
	})
	return trips
}

const handleAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// NewHandle returns an unguessable photo handle: "p" and eight random characters.
func NewHandle() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system random source failing is not recoverable
	}
	out := make([]byte, 0, 9)
	out = append(out, 'p')
	for _, v := range b {
		out = append(out, handleAlphabet[int(v)%len(handleAlphabet)])
	}
	return string(out)
}

// Build makes the public payload for the settings from the source records.
// newHandle supplies photo handles (NewHandle in production).
func Build(idx *regions.Index, src Source, set Settings, now time.Time, newHandle func() string) (*Payload, error) {
	if err := set.Validate(); err != nil {
		return nil, err
	}
	stats, provinces := ComputeStats(idx, src)
	p := &Payload{Content: Content{
		Version: ContentVersion, PublishedOn: now.UTC().Format("2006-01-02"),
		Fields: Fields{Cities: set.Cities, Dates: set.Cities && set.Dates, Notes: set.Cities && set.Notes, Photos: set.Cities && set.Photos},
		Stats:  stats, Provinces: provinces,
	}}
	if p.Content.Provinces == nil {
		p.Content.Provinces = []string{}
	}
	if !set.Cities {
		if len(set.PhotoIDs) > 0 {
			return nil, ErrPhotoUnavailable
		}
		return p, nil
	}
	byCity := map[string]*City{}
	cities := slices.Clone(src.Cities)
	sort.Slice(cities, func(i, j int) bool { return cities[i].Code < cities[j].Code })
	p.Content.Cities = make([]City, 0, len(cities))
	for _, c := range cities {
		out := City{Code: c.Code, Name: c.Name, Province: regions.CanonicalProvince(c.Code, c.Province), Color: c.Color, Trips: []Trip{}}
		trips := orderTrips(c.Trips, set.Dates)
		for _, t := range trips {
			trip := Trip{}
			if set.Dates {
				trip.Date = t.Date
			}
			if set.Notes {
				trip.Note = t.Note
			}
			out.Trips = append(out.Trips, trip)
		}
		p.Content.Cities = append(p.Content.Cities, out)
	}
	for i := range p.Content.Cities {
		byCity[p.Content.Cities[i].Code] = &p.Content.Cities[i]
	}
	if set.Photos && len(set.PhotoIDs) > 0 {
		p.Photos = map[string]string{}
		tripIndex := map[string]map[string]int{}
		for _, c := range cities {
			ids := orderTrips(c.Trips, set.Dates)
			m := map[string]int{}
			for i, t := range ids {
				m[t.ID] = i
			}
			tripIndex[c.Code] = m
		}
		for _, id := range set.PhotoIDs {
			ph, ok := src.Photos[id]
			city := byCity[ph.Region]
			if !ok || city == nil {
				return nil, ErrPhotoUnavailable
			}
			handle := newHandle()
			for p.Photos[handle] != "" {
				handle = newHandle()
			}
			p.Photos[handle] = id
			photo := Photo{Handle: handle, Width: ph.Width, Height: ph.Height}
			if i, linked := tripIndex[ph.Region][ph.TripID]; linked && ph.TripID != "" {
				city.Trips[i].Photos = append(city.Trips[i].Photos, photo)
			} else {
				city.Photos = append(city.Photos, photo)
			}
		}
	}
	return p, nil
}

// Narrow removes from an existing payload whatever next no longer publishes,
// without reading any footprints. It never adds: Adds must be false.
func Narrow(old *Payload, next Settings) (*Payload, error) {
	if err := next.Validate(); err != nil {
		return nil, err
	}
	out := &Payload{Content: old.Content}
	out.Content.Stats.ByProvince = slices.Clone(old.Content.Stats.ByProvince)
	out.Content.Provinces = slices.Clone(old.Content.Provinces)
	out.Content.Fields = Fields{
		Cities: old.Content.Fields.Cities && next.Cities,
		Dates:  old.Content.Fields.Dates && next.Dates && next.Cities,
		Notes:  old.Content.Fields.Notes && next.Notes && next.Cities,
		Photos: old.Content.Fields.Photos && next.Photos && next.Cities,
	}
	out.Content.Cities = nil
	if !out.Content.Fields.Cities {
		return out, nil
	}
	keep := map[string]bool{}
	for handle, id := range old.Photos {
		if out.Content.Fields.Photos && slices.Contains(next.PhotoIDs, id) {
			keep[handle] = true
		}
	}
	filter := func(in []Photo) []Photo {
		var res []Photo
		for _, ph := range in {
			if keep[ph.Handle] {
				res = append(res, ph)
			}
		}
		return res
	}
	out.Content.Cities = make([]City, 0, len(old.Content.Cities))
	for _, c := range old.Content.Cities {
		nc := City{Code: c.Code, Name: c.Name, Province: c.Province, Color: c.Color, Trips: make([]Trip, 0, len(c.Trips)), Photos: filter(c.Photos)}
		for _, t := range c.Trips {
			nt := Trip{Photos: filter(t.Photos)}
			if out.Content.Fields.Dates {
				nt.Date = t.Date
			}
			if out.Content.Fields.Notes {
				nt.Note = t.Note
			}
			nc.Trips = append(nc.Trips, nt)
		}
		out.Content.Cities = append(out.Content.Cities, nc)
	}
	for handle, id := range old.Photos {
		if keep[handle] {
			if out.Photos == nil {
				out.Photos = map[string]string{}
			}
			out.Photos[handle] = id
		}
	}
	return out, nil
}

// Encode serializes and compresses a payload for storage.
func Encode(p *Payload) ([]byte, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err = zw.Write(raw); err != nil {
		return nil, err
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > MaxPayloadBytes {
		return nil, ErrTooLarge
	}
	return buf.Bytes(), nil
}

// Decode reads a stored payload.
func Decode(data []byte) (*Payload, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(zr, maxRawBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxRawBytes {
		return nil, errors.New("share payload too large")
	}
	p := &Payload{}
	if err = json.Unmarshal(raw, p); err != nil {
		return nil, err
	}
	if p.Content.Version != ContentVersion {
		return nil, fmt.Errorf("unsupported share content version %d", p.Content.Version)
	}
	return p, nil
}

// PublicJSON is the exact response body of the public data route.
func (c *Content) PublicJSON() ([]byte, error) { return json.Marshal(c) }

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// ValidToken reports whether s has the shape of a share token.
func ValidToken(s string) bool { return tokenPattern.MatchString(s) }

// NewToken returns 32 random bytes, base64url without padding (43 characters).
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SameContent reports whether two payloads publish the same thing: equal
// content once the per-publication photo handles are replaced by the photos
// they stand for. The publication day is not compared.
func SameContent(a, b *Payload) bool {
	norm := func(p *Payload) []byte {
		raw, err := json.Marshal(p.Content)
		if err != nil {
			return nil
		}
		var c Content
		if err = json.Unmarshal(raw, &c); err != nil {
			return nil
		}
		c.PublishedOn = ""
		swap := func(in []Photo) {
			for i := range in {
				in[i].Handle = p.Photos[in[i].Handle]
			}
		}
		for i := range c.Cities {
			swap(c.Cities[i].Photos)
			for j := range c.Cities[i].Trips {
				swap(c.Cities[i].Trips[j].Photos)
			}
		}
		out, _ := json.Marshal(c)
		return out
	}
	x, y := norm(a), norm(b)
	return x != nil && bytes.Equal(x, y)
}
