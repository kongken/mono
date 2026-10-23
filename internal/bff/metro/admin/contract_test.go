package admin

import (
	"testing"

	"github.com/kongken/mono/internal/bff/metro/relay"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	platformv1 "github.com/kongken/mono/pkg/proto/platform/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// The adapter converts through the wire format, which is only correct while the
// two AdminService contracts have the same messages with the same field numbers
// and types. This test is the guard: change one side without the other and it fails.
func TestContractsStayWireCompatible(t *testing.T) {
	internal, browser := metrov1.File_metro_v1_admin_proto, bffmetrov1.File_bff_metro_v1_admin_proto
	for _, p := range relay.Compatible(internal, browser, "AdminService") {
		t.Error(p)
	}
	// The shared enum keeps its numbers across the two packages.
	for name, v := range metrov1.LineStatus_value {
		if bffmetrov1.LineStatus_value[name] != v {
			t.Errorf("LineStatus %s: %d vs %d", name, v, bffmetrov1.LineStatus_value[name])
		}
	}
	if len(metrov1.LineStatus_value) != len(bffmetrov1.LineStatus_value) {
		t.Error("LineStatus has a different number of values")
	}
	// Every method demands the role, on both sides.
	for side, file := range map[string]protoreflect.FileDescriptor{"metro.v1": internal, "bff.metro.v1": browser} {
		methods := file.Services().ByName("AdminService").Methods()
		for i := range methods.Len() {
			m := methods.Get(i)
			opts, _ := m.Options().(*descriptorpb.MethodOptions)
			if role, _ := proto.GetExtension(opts, platformv1.E_RequiredRole).(string); role != "metro-admin" {
				t.Errorf("%s %s requires role %q, want metro-admin", side, m.Name(), role)
			}
		}
	}
}
