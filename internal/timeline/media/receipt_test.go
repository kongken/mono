package media

import (
	"github.com/kongken/mono/internal/timeline/ent"
	"testing"
)

func TestValidDeleteReceiptStrictSnapshotRefs(t *testing.T) {
	mediaRef := "med_0123456789abcdefghjkmnpqrs"
	entryRef := "ent_0123456789abcdefghjkmnpqrs"
	base := func(snapshot map[string]any) *ent.TimelineMutation {
		return &ent.TimelineMutation{RequestHash: "h", TargetKind: "media", Operation: "media_delete", MediaRef: &mediaRef, EntryRef: &entryRef, ResultState: "deleting", SnapshotVersion: 1, ResultSnapshot: snapshot}
	}
	valid := map[string]any{"media_ref": mediaRef, "entry_ref": entryRef}
	if !validDeleteReceipt(base(valid), "h", mediaRef) {
		t.Fatal("valid receipt rejected")
	}
	cases := []map[string]any{
		{"media_ref": "med_0123456789abcdefghjkmnpqrr", "entry_ref": entryRef},
		{"media_ref": mediaRef, "entry_ref": "ent_0123456789abcdefghjkmnpqrr"},
		{"media_ref": 123, "entry_ref": entryRef},
		{"media_ref": "", "entry_ref": entryRef},
		{"media_ref": mediaRef, "entry_ref": nil},
		// The columns only accept lowercase: a receipt naming a reference the
		// database could never hold is not this mutation's receipt.
		{"media_ref": "MED_0123456789ABCDEFGHJKMNPQRS", "entry_ref": entryRef},
	}
	for i, snapshot := range cases {
		if validDeleteReceipt(base(snapshot), "h", mediaRef) {
			t.Fatalf("case %d accepted", i)
		}
	}
}
