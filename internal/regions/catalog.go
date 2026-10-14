package regions

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
)

//go:embed catalog.json
var catalogRaw []byte

// Mapping is an exact historic tuple, not a guess from a display name.
type Mapping struct {
	FromCode     string `json:"fromCode"`
	FromName     string `json:"fromName"`
	FromProvince string `json:"fromProvince"`
	ToCode       string `json:"toCode"`
}

type Catalog struct {
	Version           string    `json:"version"`
	SupportedVersions []string  `json:"supportedVersions"`
	Mappings          []Mapping `json:"mappings"`
}

type CityRef struct{ Code, Name, Province string }

// WithCatalog validates a generated catalog against this exact index. It returns
// a new Index; the existing immutable index and all its callers stay untouched.
func (i *Index) WithCatalog(data []byte) (*Index, error) {
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, i.raw); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	var mappingBytes bytes.Buffer
	if err := json.Compact(&mappingBytes, fields["mappings"]); err != nil {
		return nil, err
	}
	mappings := mappingBytes.Bytes()
	canonical := append([]byte(`{"regions":`), compact.Bytes()...)
	canonical = append(canonical, []byte(`,"mappings":`)...)
	canonical = append(canonical, mappings...)
	canonical = append(canonical, '}')
	if c.Version != fmt.Sprintf("sha256:%x", sha256.Sum256(canonical)) {
		return nil, fmt.Errorf("region catalog digest does not match index")
	}
	seen, aliases := map[string]bool{}, map[string]string{}
	for _, m := range c.Mappings {
		key := m.FromCode + "\x00" + m.FromName + "\x00" + m.FromProvince
		_, _, target := i.City(m.ToCode)
		_, _, reused := i.City(m.FromCode)
		if m.FromCode == "" || m.FromName == "" || m.FromProvince == "" || !target || seen[key] || (reused && m.FromCode != m.ToCode) || (aliases[m.FromCode] != "" && aliases[m.FromCode] != m.ToCode) {
			return nil, fmt.Errorf("invalid or ambiguous region mapping")
		}
		seen[key], aliases[m.FromCode] = true, m.ToCode
	}
	out := *i
	out.catalog = &c
	return &out, nil
}

func (i *Index) Version() string {
	if i.catalog == nil {
		return ""
	}
	return i.catalog.Version
}
func (i *Index) SupportsVersion(version string) bool {
	return version == "" || version == "legacy" || version == i.Version() || (i.catalog != nil && slices.Contains(i.catalog.SupportedVersions, version))
}
func (i *Index) Mappings() []Mapping {
	if i.catalog == nil {
		return nil
	}
	return slices.Clone(i.catalog.Mappings)
}

// ResolveCity accepts only a current tuple or an explicitly reviewed historic
// tuple. An ID collision is checked by the caller against its complete dataset.
func (i *Index) ResolveCity(code, name, province string) (CityRef, error) {
	n, p, ok := i.City(code)
	if ok && name == n && province == p {
		return CityRef{code, n, p}, nil
	}
	if i.catalog != nil {
		for _, m := range i.catalog.Mappings {
			if m.FromCode == code && m.FromName == name && m.FromProvince == province {
				n, p, _ := i.City(m.ToCode)
				return CityRef{m.ToCode, n, p}, nil
			}
		}
	}
	return CityRef{}, fmt.Errorf("unmapped region %q", code)
}
func (i *Index) ResolveCode(code string) (string, bool) {
	if _, _, ok := i.City(code); ok {
		return code, true
	}
	if i.catalog != nil {
		for _, m := range i.catalog.Mappings {
			if m.FromCode == code {
				return m.ToCode, true
			}
		}
	}
	return "", false
}

func (i *Index) JSON() string { return string(i.raw) }
func (i *Index) SupportedVersions() []string {
	if i.catalog == nil {
		return []string{"legacy"}
	}
	return slices.Clone(i.catalog.SupportedVersions)
}

// CanonicalCity projects known historical rows for consumers of the bundled
// index. Unknown rows are kept verbatim so callers can fail closed, never drop them.
func CanonicalCity(code, name, province string) CityRef {
	if index, err := Default(); err == nil {
		if ref, err := index.ResolveCity(code, name, province); err == nil {
			return ref
		}
	}
	return CityRef{code, name, province}
}
func CanonicalCode(code string) string {
	if index, err := Default(); err == nil {
		if canonical, ok := index.ResolveCode(code); ok {
			return canonical
		}
	}
	return code
}

// CurrentVersion is safe for infrastructure status and request version gates.
func CurrentVersion() string {
	i, err := Default()
	if err != nil {
		return ""
	}
	return i.Version()
}
