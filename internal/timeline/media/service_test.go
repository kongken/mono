package media

import (
	"errors"
	"testing"
)

func TestValidateUploadContract(t *testing.T) {
	good := Upload{EntryRef: "ent_0123456789abcdefghjkmnpqrs", Filename: "photo.jpg", ContentType: "image/jpeg", SizeBytes: 10, MutationID: "m1"}
	if err := validate(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Upload{{EntryRef: "x", Filename: "a", ContentType: "image/jpeg", SizeBytes: 1, MutationID: "m"}, {EntryRef: "ent_x", Filename: "a", ContentType: "text/plain", SizeBytes: 1, MutationID: "m"}, {EntryRef: "ent_x", Filename: "a", ContentType: "image/jpeg", SizeBytes: 10*1024*1024 + 1, MutationID: "m"}} {
		if !errors.Is(validate(bad), ErrInvalid) {
			t.Fatalf("validate(%+v) did not reject", bad)
		}
	}
}
