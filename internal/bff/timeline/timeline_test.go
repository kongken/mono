package timeline

import (
	"testing"

	v1 "github.com/kongken/mono/pkg/proto/timeline/v1"
)

func TestOpaqueEntryAndReceiptMapping(t *testing.T) {
	internal := &v1.Entry{Id: "internal-uuid", EntryRef: "ent_0123456789abcdef0123456789", Title: "Dinner", Revision: 2}
	mapped := out(internal)
	if mapped.Id != "" || mapped.EntryRef != internal.EntryRef {
		t.Fatalf("BFF request/response mapping changed fields: %#v", mapped)
	}
	got := receipt(&v1.MutationReceipt{MutationId: "retry:01", Operation: "update", ResultRevision: 3, ResultState: "active", Replayed: true})
	if got == nil || got.MutationId != "retry:01" || !got.Replayed || got.ResultRevision != 3 {
		t.Fatalf("receipt mapping failed: %#v", got)
	}
	if receipt(nil) != nil {
		t.Fatal("nil receipt should remain nil")
	}
}
