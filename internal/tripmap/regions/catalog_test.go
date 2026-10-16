package regions_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/kongken/mono/internal/tripmap/regions"
)

func boundCatalog(t *testing.T, idx *regions.Index, mappings []regions.Mapping) []byte {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(idx.JSON())); err != nil {
		t.Fatal(err)
	}
	rules, err := json.Marshal(mappings)
	if err != nil {
		t.Fatal(err)
	}
	canonical := append([]byte(`{"regions":`), compact.Bytes()...)
	canonical = append(canonical, []byte(`,"mappings":`)...)
	canonical = append(canonical, rules...)
	canonical = append(canonical, '}')
	version := fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
	raw, err := json.Marshal(regions.Catalog{Version: version, SupportedVersions: []string{"legacy", idx.Version(), version}, Mappings: mappings})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBundledCatalogBindingAndHistoricalTuples(t *testing.T) {
	idx, err := regions.Default()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(idx.Version(), "sha256:") || len(idx.Version()) != 71 || idx.Provinces() != 34 || idx.Cities() != 497 {
		t.Fatal("invalid current catalog")
	}
	for _, code := range []string{"tw-09020", "tw-09007"} {
		name, _, _ := idx.City(code)
		ref, err := idx.ResolveCity(code, name, "710000")
		if err != nil || ref.Code != code || ref.Province != "350000" {
			t.Fatalf("legacy tuple %s: %v %v", code, ref, err)
		}
		if _, err := idx.ResolveCity(code, "not the historical name", "710000"); err == nil {
			t.Fatal("guessed historical name")
		}
	}
	if !idx.SupportsVersion("") || !idx.SupportsVersion("legacy") || !idx.SupportsVersion(idx.Version()) || idx.SupportsVersion("future") {
		t.Fatal("unsafe version handling")
	}
	copy := idx.Mappings()
	copy[0].FromName = "mutated"
	if idx.Mappings()[0].FromName == "mutated" {
		t.Fatal("mutable catalog rules escaped")
	}
	versions := idx.SupportedVersions()
	versions[0] = "mutated"
	if idx.SupportedVersions()[0] == "mutated" {
		t.Fatal("mutable versions escaped")
	}
}

func TestCatalogSupportsReviewedRetiredIDs(t *testing.T) {
	idx, err := regions.Default()
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Mapping regions.Mapping                       `json:"mapping"`
		Current struct{ Code, Name, Province string } `json:"current"`
	}
	raw, err := os.ReadFile("../../../scripts/fixtures/region-upgrades.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	mappings := append(idx.Mappings(), fixture.Mapping)
	custom, err := idx.WithCatalog(boundCatalog(t, idx, mappings))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := custom.ResolveCity(fixture.Mapping.FromCode, fixture.Mapping.FromName, fixture.Mapping.FromProvince)
	if err != nil || ref.Code != fixture.Current.Code || ref.Name != fixture.Current.Name || ref.Province != fixture.Current.Province {
		t.Fatalf("retired ID = %v %v", ref, err)
	}
	if code, ok := custom.ResolveCode(fixture.Mapping.FromCode); !ok || code != fixture.Current.Code {
		t.Fatal("ID-only reference was not mapped")
	}
	if _, ok := idx.ResolveCode(fixture.Mapping.FromCode); ok {
		t.Fatal("test-only IDs leaked into production")
	}
}

func TestCatalogRejectsStaleAmbiguousAndReusedIDs(t *testing.T) {
	idx, err := regions.Default()
	if err != nil {
		t.Fatal(err)
	}
	old := regions.Mapping{FromCode: "old-hangzhou", FromName: "旧杭州", FromProvince: "330000", ToCode: "330100"}
	cases := map[string][]regions.Mapping{
		"duplicate":         {old, old},
		"unknown target":    {{FromCode: "old", FromName: "旧", FromProvince: "330000", ToCode: "missing"}},
		"reused current ID": {{FromCode: "110101", FromName: "旧", FromProvince: "110000", ToCode: "330100"}},
		"ambiguous alias":   {old, {FromCode: old.FromCode, FromName: "another name", FromProvince: "330000", ToCode: "330200"}},
		"unflattened chain": {{FromCode: "old-a", FromName: "旧", FromProvince: "330000", ToCode: "old-b"}},
	}
	for name, mappings := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := idx.WithCatalog(boundCatalog(t, idx, mappings)); err == nil {
				t.Fatal("accepted unsafe mapping")
			}
		})
	}
	var catalog regions.Catalog
	if err := json.Unmarshal(boundCatalog(t, idx, idx.Mappings()), &catalog); err != nil {
		t.Fatal(err)
	}
	catalog.Version = "sha256:stale"
	raw, _ := json.Marshal(catalog)
	if _, err := idx.WithCatalog(raw); err == nil {
		t.Fatal("accepted wrong digest")
	}
	changed, err := regions.Parse([]byte(strings.Replace(idx.JSON(), "杭州市", "新杭州市", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := changed.WithCatalog(boundCatalog(t, idx, idx.Mappings())); err == nil {
		t.Fatal("accepted catalog after a name-only index change")
	}
}
