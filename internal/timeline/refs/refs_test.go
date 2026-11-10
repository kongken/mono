package refs

import (
	"regexp"
	"testing"
)

// The CHECK constraints in migrations/timeline/20261107180000_record_refs_mutations.sql
// are the authority on what a column will accept. A generator that drifts from them
// fails every insert, and the drift is invisible until something writes to a real
// database, so the two are asserted together here.
func TestGeneratedRefsSatisfyTheColumnChecks(t *testing.T) {
	checks := []struct {
		prefix string
		column *regexp.Regexp
	}{
		{"ent_", regexp.MustCompile(`^ent_[0-9a-hjkmnp-tv-z]{26}$`)}, // timeline_entries_public_ref_format
		{"med_", regexp.MustCompile(`^med_[0-9a-hjkmnp-tv-z]{26}$`)}, // timeline_media_objects_public_ref_format
	}
	for _, check := range checks {
		seen := make(map[string]bool, 200)
		for i := 0; i < 200; i++ {
			ref := New(check.prefix)
			if !check.column.MatchString(ref) {
				t.Fatalf("%q would be rejected by the column check %s", ref, check.column)
			}
			if !Valid(ref, check.prefix) {
				t.Fatalf("generated ref %q is not accepted by Valid", ref)
			}
			if seen[ref] {
				t.Fatalf("generated ref %q twice", ref)
			}
			seen[ref] = true
		}
	}
}

func TestValidRefusesWhatTheColumnWould(t *testing.T) {
	for _, ref := range []string{
		"",
		"med_",
		"med_0123456789abcdefghjkmnpqr",   // 25 characters
		"med_0123456789abcdefghjkmnpqrst", // 27 characters
		"MED_0123456789abcdefghjkmnpqrs",  // uppercase prefix
		"med_0123456789ABCDEFGHJKMNPQRS",  // uppercase body
		"med_0123456789abcdefghjkmnpqri",  // i is not in the alphabet
		"med_0123456789abcdefghjkmnpqro",  // o is not in the alphabet
		"med_0123456789abcdefghjkmnpqru",  // u is not in the alphabet
		"ent_0123456789abcdefghjkmnpqrs",  // right shape, wrong prefix
	} {
		if Valid(ref, "med_") {
			t.Fatalf("%q must be refused", ref)
		}
	}
	if !Valid("med_0123456789abcdefghjkmnpqrs", "med_") {
		t.Fatal("a well-formed ref must be accepted")
	}
}
