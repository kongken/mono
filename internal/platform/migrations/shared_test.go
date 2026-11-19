package migrations

import (
	"path/filepath"
	"sort"
	"testing"

	"ariga.io/atlas/sql/migrate"
)

func TestDiscoverPlanCoversFullTripmapLifecycle(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	plan := DiscoverPlan(root)
	if len(plan.TripmapIncrements) != 8 {
		t.Fatalf("Tripmap increments = %d, want all pre-receipt increments", len(plan.TripmapIncrements))
	}
	if len(plan.TimelineIncrements) != 3 {
		t.Fatalf("Timeline increments = %d, want the three post-refs migrations", len(plan.TimelineIncrements))
	}
	phases := plan.phases()
	if len(phases) != 17 {
		t.Fatalf("phase count = %d, want 17", len(phases))
	}
	want := map[int]string{
		0: "timeline-baseline", 1: "tripmap-baseline", 2: "timeline-refs", 3: "tripmap-increment-00", 10: "tripmap-increment-07",
		11: "media-entry-links", 12: "media-entry-link-import", 13: "tripmap-media",
		14: "timeline-20261112000000_media_upload_mutations", 15: "timeline-20261112010000_amount_presence", 16: "timeline-20261112011000_categories",
	}
	for i, name := range want {
		if phases[i].name != name {
			t.Errorf("phase %d = %q, want %q", i, phases[i].name, name)
		}
	}
	for _, phase := range phases {
		if phase.path == "" && phase.name != "media-entry-link-import" {
			t.Errorf("phase %s has empty path", phase.name)
		}
		if phase.path != "" && len(phase.probes) == 0 {
			t.Errorf("phase %s has no schema fingerprint, so an existing database could never adopt it", phase.name)
		}
	}
}

// Every checked-in migration file must be in the shared plan exactly once, and both
// directories must still match their atlas.sum: a file added to a directory but
// not to the plan is the failure the first KON-153 review found (three Timeline
// migrations were missing from `cmd/migrate-shared`).
func TestSharedPlanCoversEveryMigrationFileOnceAndChecksumsMatch(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	plan := DiscoverPlan(root)
	inPlan := map[string]int{}
	for _, phase := range plan.phases() {
		if phase.path != "" {
			inPlan[phase.path]++
		}
	}
	for _, dir := range []string{"timeline", "tripmap"} {
		full := filepath.Join(root, "migrations", dir)
		files, err := SQLFiles(full)
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: %v %v", dir, files, err)
		}
		for _, file := range files {
			if inPlan[file] != 1 {
				t.Errorf("%s is in the shared plan %d times, want exactly once", file, inPlan[file])
			}
			delete(inPlan, file)
		}
		local, err := migrate.NewLocalDir(full)
		if err != nil {
			t.Fatal(err)
		}
		if err = migrate.Validate(local); err != nil {
			t.Errorf("%s: atlas.sum does not match the files: %v", dir, err)
		}
	}
	var extra []string
	for file := range inPlan {
		extra = append(extra, file)
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("plan references files outside the migration directories: %v", extra)
	}
}

// The order is the dependency order: the refs migration (public_ref, receipts)
// precedes the link staging table that has a foreign key onto it, which precedes the
// media migration; the Timeline increments come last and only add to Timeline tables.
func TestSharedPlanOrderFollowsDependencies(t *testing.T) {
	plan := DiscoverPlan(filepath.Join("..", "..", ".."))
	position := map[string]int{}
	for i, phase := range plan.phases() {
		position[phase.name] = i
	}
	order := [][2]string{
		{"timeline-baseline", "timeline-refs"},
		{"tripmap-baseline", "tripmap-increment-00"},
		{"timeline-refs", "media-entry-links"},
		{"media-entry-links", "media-entry-link-import"},
		{"media-entry-link-import", "tripmap-media"},
		{"tripmap-media", "timeline-20261112000000_media_upload_mutations"},
		{"timeline-20261112000000_media_upload_mutations", "timeline-20261112010000_amount_presence"},
		{"timeline-20261112010000_amount_presence", "timeline-20261112011000_categories"},
	}
	for _, pair := range order {
		if position[pair[0]] >= position[pair[1]] {
			t.Errorf("%s must run before %s", pair[0], pair[1])
		}
	}
}
