package regions

import (
	"slices"
	"testing"
)

func TestSearchScopeUsesCanonicalCityIDs(t *testing.T) {
	idx, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.CityCodes("")) != idx.Cities() {
		t.Fatal("search omits destinations")
	}
	for _, code := range []string{"tw-09020", "tw-09007"} {
		if !slices.Contains(idx.CityCodes("350000"), code) || slices.Contains(idx.CityCodes("710000"), code) {
			t.Fatalf("wrong search scope for %s", code)
		}
	}
	if len(idx.CityCodes("unknown")) != 0 {
		t.Fatal("unknown scope must be empty")
	}
}
