package migrations

import (
	"path/filepath"
	"testing"
)

func TestDiscoverPlanCoversFullTripmapLifecycle(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	plan := DiscoverPlan(root)
	if len(plan.TripmapIncrements) != 8 {
		t.Fatalf("Tripmap increments = %d, want all pre-receipt increments", len(plan.TripmapIncrements))
	}
	phases := plan.phases()
	if len(phases) != 14 {
		t.Fatalf("phase count = %d, want 14", len(phases))
	}
	want := map[int]string{0: "timeline-baseline", 1: "tripmap-baseline", 2: "timeline-refs", 3: "tripmap-increment-00", 10: "tripmap-increment-07", 11: "media-entry-links", 12: "media-entry-link-import", 13: "tripmap-media"}
	for i, name := range want {
		if phases[i].name != name {
			t.Errorf("phase %d = %q, want %q", i, phases[i].name, name)
		}
	}
	for _, phase := range phases {
		if phase.path == "" && phase.name != "media-entry-link-import" {
			t.Errorf("phase %s has empty path", phase.name)
		}
	}
}
