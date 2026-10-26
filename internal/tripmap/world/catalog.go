package world

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// catalogJSON is a byte-identical copy of packages/geo-data/data/world-catalog.json
// (the generated world country/region catalog). The test in catalog_test.go and
// check:maps keep the two in step; `npm run prepare:world` writes both. Only the
// country codes are read here: the backend never needs names or geometry.
//
//go:embed catalog.json
var catalogJSON []byte

// DerivedCountry is the code the world view derives from the China footprints. It
// is in the catalog (its shape comes from the national map) but never a stored value.
const DerivedCountry = "CN"

type catalogFile struct {
	Version   string `json:"version"`
	Countries []struct {
		Code string `json:"code"`
	} `json:"countries"`
}

type catalog struct {
	version string
	codes   map[string]struct{}
}

var loadCatalog = sync.OnceValues(func() (*catalog, error) {
	var file catalogFile
	if err := json.Unmarshal(catalogJSON, &file); err != nil {
		return nil, fmt.Errorf("world catalog: %w", err)
	}
	c := &catalog{version: file.Version, codes: make(map[string]struct{}, len(file.Countries))}
	for _, entry := range file.Countries {
		if _, dup := c.codes[entry.Code]; dup || entry.Code == "" {
			return nil, fmt.Errorf("world catalog: unusable code %q", entry.Code)
		}
		c.codes[entry.Code] = struct{}{}
	}
	if len(c.codes) == 0 {
		return nil, fmt.Errorf("world catalog: no countries")
	}
	return c, nil
})

// Catalog reports which country/region codes the world document may use.
type Catalog struct{ c *catalog }

// DefaultCatalog is the embedded catalog; it fails only if the embedded file is broken.
func DefaultCatalog() (Catalog, error) {
	c, err := loadCatalog()
	if err != nil {
		return Catalog{}, err
	}
	return Catalog{c: c}, nil
}

// Has reports whether code is in the catalog. CN is (as a derived entry; Validate
// refuses it as a stored value); HK, MO and TW are not.
func (c Catalog) Has(code string) bool {
	_, ok := c.c.codes[code]
	return ok
}

// Len is the number of catalog entries.
func (c Catalog) Len() int { return len(c.c.codes) }

// Version is the catalog's content digest as written by the generator.
func (c Catalog) Version() string { return c.c.version }

// NewCatalog builds a catalog from codes; used by tests.
func NewCatalog(codes ...string) Catalog {
	c := &catalog{codes: make(map[string]struct{}, len(codes))}
	for _, code := range codes {
		c.codes[code] = struct{}{}
	}
	return Catalog{c: c}
}
