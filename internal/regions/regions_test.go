package regions

import (
	"bytes"
	"os"
	"testing"
)

func TestDefaultIndex(t *testing.T) {
	idx, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if idx.Provinces() != 34 || idx.Cities() != 497 {
		t.Fatalf("unexpected index size: %d provinces, %d cities", idx.Provinces(), idx.Cities())
	}
	name, province, ok := idx.City("330100")
	if !ok || name != "杭州市" || province != "330000" {
		t.Fatalf("Hangzhou = %q %q %v", name, province, ok)
	}
	if _, _, ok := idx.City("000000"); ok || idx.HasProvince("000000") || !idx.HasProvince("330000") {
		t.Fatal("membership checks are wrong")
	}
}

// The frontend validates against src/regions.json; both copies must match.
func TestMatchesFrontendIndex(t *testing.T) {
	frontend, err := os.ReadFile("../../src/regions.json")
	if os.IsNotExist(err) {
		t.Skip("src/regions.json is not part of this build context")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(frontend), bytes.TrimSpace(raw)) {
		t.Fatal("internal/regions/regions.json differs from src/regions.json; run npm run download:maps or copy the file")
	}
}
