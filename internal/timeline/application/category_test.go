package application

import "testing"

func TestDefaultCategorySetIsComplete(t *testing.T) {
	if len(defaultCategoryNames) != 22 {
		t.Fatalf("default categories=%d, want 22", len(defaultCategoryNames))
	}
	seen := map[string]bool{}
	for _, name := range defaultCategoryNames {
		if seen[name] {
			t.Fatalf("duplicate default category %q", name)
		}
		seen[name] = true
	}
	if !seen["其他"] {
		t.Fatal(`the default set must include "其他"`)
	}
}
