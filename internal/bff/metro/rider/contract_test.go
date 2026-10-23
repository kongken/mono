package rider

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

// See admin.TestContractsStayWireCompatible: the relay is only correct while both
// definitions match.
func TestContractsStayWireCompatible(t *testing.T) {
	internal, browser := metrov1.File_metro_v1_rider_proto, bffmetrov1.File_bff_metro_v1_rider_proto
	for _, p := range relay.Compatible(internal, browser, "RiderService") {
		t.Error(p)
	}
	// Only the public profile is anonymous, on both sides; none needs a role.
	for side, file := range map[string]protoreflect.FileDescriptor{"metro.v1": internal, "bff.metro.v1": browser} {
		methods := file.Services().ByName("RiderService").Methods()
		for i := range methods.Len() {
			m := methods.Get(i)
			opts, _ := m.Options().(*descriptorpb.MethodOptions)
			anonymous, _ := proto.GetExtension(opts, platformv1.E_Anonymous).(bool)
			if anonymous != (m.Name() == "GetPublicProfile") {
				t.Errorf("%s %s anonymous=%v", side, m.Name(), anonymous)
			}
			if role, _ := proto.GetExtension(opts, platformv1.E_RequiredRole).(string); role != "" {
				t.Errorf("%s %s requires role %q", side, m.Name(), role)
			}
		}
	}
}
