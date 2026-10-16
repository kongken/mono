// Package regions exposes the administrative region index shared with the
// frontend (src/regions.json). IDs identify regions; names are display text.
package regions

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
)

//go:embed regions.json
var raw []byte

type city struct {
	Code string `json:"code"`
	Name string `json:"name"`
}
type province struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Cities []city `json:"cities"`
}

// Index answers membership questions about provinces and cities.
type Index struct {
	raw       []byte
	catalog   *Catalog
	provinces map[string]string
	cities    map[string]cityEntry
	// perProvince counts every city or district record of a province.
	perProvince map[string]int
}
type cityEntry struct{ name, province string }

// Parse builds an Index from a regions.json document.
func Parse(data []byte) (*Index, error) {
	var list []province
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse region index: %w", err)
	}
	idx := &Index{raw: bytes.Clone(data), provinces: map[string]string{}, cities: map[string]cityEntry{}, perProvince: map[string]int{}}
	for _, p := range list {
		if p.Code == "" {
			return nil, fmt.Errorf("region index: province without code")
		}
		idx.provinces[p.Code] = p.Name
		idx.perProvince[p.Code] = len(p.Cities)
		for _, c := range p.Cities {
			if _, dup := idx.cities[c.Code]; dup {
				return nil, fmt.Errorf("region index: duplicate city %s", c.Code)
			}
			idx.cities[c.Code] = cityEntry{name: c.Name, province: p.Code}
		}
	}
	return idx, nil
}

var (
	defaultOnce  sync.Once
	defaultIndex *Index
	defaultErr   error
)

// Default returns the index embedded in the binary.
func Default() (*Index, error) {
	defaultOnce.Do(func() {
		defaultIndex, defaultErr = Parse(raw)
		if defaultErr == nil {
			defaultIndex, defaultErr = defaultIndex.WithCatalog(catalogRaw)
		}
	})
	return defaultIndex, defaultErr
}

// HasProvince reports whether code is a known province-level region ID.
func (i *Index) HasProvince(code string) bool { _, ok := i.provinces[code]; return ok }

// ProvinceName returns the display name of a province-level region ID.
func (i *Index) ProvinceName(code string) (name string, ok bool) {
	name, ok = i.provinces[code]
	return name, ok
}

// City returns the display name and province of a city or district ID.
func (i *Index) City(code string) (name, province string, ok bool) {
	c, ok := i.cities[code]
	return c.name, c.province, ok
}

// Provinces and Cities report index sizes.
func (i *Index) Provinces() int { return len(i.provinces) }
func (i *Index) Cities() int    { return len(i.cities) }

// ProvinceCodes lists every province-level ID in ascending order.
func (i *Index) ProvinceCodes() []string {
	codes := make([]string, 0, len(i.provinces))
	for code := range i.provinces {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	return codes
}

// CityCodes lists canonical city IDs in a province (or all cities for an empty
// province). Search scopes by these IDs, not legacy stored province values.
func (i *Index) CityCodes(province string) []string {
	codes := make([]string, 0, len(i.cities))
	for code, c := range i.cities {
		if province == "" || c.province == province {
			codes = append(codes, code)
		}
	}
	slices.Sort(codes)
	return codes
}

// ProvinceCities is the number of city or district records under a province.
func (i *Index) ProvinceCities(code string) int { return i.perProvince[code] }
