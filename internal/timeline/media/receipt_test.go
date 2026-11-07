package media

import (
	"github.com/kongken/mono/internal/timeline/ent"
	"testing"
)

func TestValidDeleteReceiptRejectsNonMediaReceipt(t *testing.T) {
	ref := "med_abcdefghijklmnopqrstuvwxyz"
	base := func(target, op string) *ent.TimelineMutation {
		return &ent.TimelineMutation{RequestHash: "h", TargetKind: target, Operation: op, MediaRef: &ref, ResultState: "deleting", SnapshotVersion: 1, ResultSnapshot: map[string]any{"media_ref": ref, "entry_ref": "ent_abcdefghijklmnopqrstuvwxyz"}}
	}
	if validDeleteReceipt(base("entry", "delete"), "h", ref) {
		t.Fatal("entry receipt accepted as media delete replay")
	}
	if validDeleteReceipt(base("media", "delete"), "h", ref) {
		t.Fatal("wrong operation accepted as media delete replay")
	}
	if !validDeleteReceipt(base("media", "media_delete"), "h", ref) {
		t.Fatal("valid media receipt rejected")
	}
}
