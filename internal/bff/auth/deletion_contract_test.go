package auth

import (
	"strings"
	"testing"

	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// No account-deletion call names a user, an account, a workflow or an email: the caller is the
// Cookie, and the only identifiers in a request are the caller's own idempotency key and the
// typed confirmation. A field added here that could point at someone else fails this test, so
// a path that closes another person's account cannot appear unnoticed.
func TestDeletionCallsNameNoUser(t *testing.T) {
	allowed := map[protoreflect.FullName]map[string]bool{
		(&bffv1.DeleteAccountRequest{}).ProtoReflect().Descriptor().FullName():        {"confirmation": true, "mutation_id": true},
		(&bffv1.GetAccountDeletionRequest{}).ProtoReflect().Descriptor().FullName():   {},
		(&bffv1.RetryAccountDeletionRequest{}).ProtoReflect().Descriptor().FullName(): {},
	}
	msgs := map[protoreflect.FullName]protoreflect.MessageDescriptor{
		(&bffv1.DeleteAccountRequest{}).ProtoReflect().Descriptor().FullName():        (&bffv1.DeleteAccountRequest{}).ProtoReflect().Descriptor(),
		(&bffv1.GetAccountDeletionRequest{}).ProtoReflect().Descriptor().FullName():   (&bffv1.GetAccountDeletionRequest{}).ProtoReflect().Descriptor(),
		(&bffv1.RetryAccountDeletionRequest{}).ProtoReflect().Descriptor().FullName(): (&bffv1.RetryAccountDeletionRequest{}).ProtoReflect().Descriptor(),
	}
	for name, md := range msgs {
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			if f := string(fields.Get(i).Name()); !allowed[name][f] {
				t.Errorf("%s has field %q: a deletion call may not carry anything that names an account", name, f)
			}
		}
	}
	// The answer is the caller's own workflow state and carries no identifier of an account either.
	res := (&bffv1.AccountDeletion{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < res.Len(); i++ {
		if n := string(res.Get(i).Name()); strings.Contains(n, "user") || strings.Contains(n, "email") || n == "id" || strings.HasSuffix(n, "_id") && n != "mutation_id" {
			t.Errorf("AccountDeletion exposes %q", n)
		}
	}
	// The three methods take exactly these messages and nothing else is a deletion method.
	svc := bffv1.File_bff_v1_auth_proto.Services().ByName("AuthService")
	var deleting []string
	for i := 0; i < svc.Methods().Len(); i++ {
		m := svc.Methods().Get(i)
		if strings.Contains(strings.ToLower(string(m.Name())), "delet") {
			deleting = append(deleting, string(m.Name()))
		}
	}
	if len(deleting) != 3 || deleting[0] != "DeleteAccount" || deleting[1] != "GetAccountDeletion" || deleting[2] != "RetryAccountDeletion" {
		t.Errorf("unexpected deletion methods on the browser API: %v", deleting)
	}
}
