package admin

import (
	"testing"

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
	internal := metrov1.File_metro_v1_admin_proto
	browser := bffmetrov1.File_bff_metro_v1_admin_proto

	inMsgs, brMsgs := internal.Messages(), browser.Messages()
	if inMsgs.Len() != brMsgs.Len() {
		t.Fatalf("metro.v1 has %d admin messages, bff.metro.v1 has %d", inMsgs.Len(), brMsgs.Len())
	}
	for i := range inMsgs.Len() {
		in, br := inMsgs.Get(i), brMsgs.ByName(inMsgs.Get(i).Name())
		if br == nil {
			t.Errorf("bff.metro.v1 has no message %s", in.Name())
			continue
		}
		if in.Fields().Len() != br.Fields().Len() {
			t.Errorf("%s: %d fields vs %d", in.Name(), in.Fields().Len(), br.Fields().Len())
			continue
		}
		for j := range in.Fields().Len() {
			a, b := in.Fields().Get(j), br.Fields().ByNumber(in.Fields().Get(j).Number())
			if b == nil {
				t.Errorf("%s.%s: field number %d missing on the browser side", in.Name(), a.Name(), a.Number())
				continue
			}
			if a.Name() != b.Name() || a.Kind() != b.Kind() || a.Cardinality() != b.Cardinality() ||
				a.HasOptionalKeyword() != b.HasOptionalKeyword() || a.IsMap() != b.IsMap() {
				t.Errorf("%s.%s differs from the browser definition", in.Name(), a.Name())
			}
			if (a.Message() == nil) != (b.Message() == nil) || (a.Message() != nil && a.Message().Name() != b.Message().Name()) {
				t.Errorf("%s.%s: message type differs", in.Name(), a.Name())
			}
			if (a.Enum() == nil) != (b.Enum() == nil) {
				t.Errorf("%s.%s: enum differs", in.Name(), a.Name())
			}
		}
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

	inSvc, brSvc := internal.Services().ByName("AdminService"), browser.Services().ByName("AdminService")
	if inSvc.Methods().Len() != brSvc.Methods().Len() {
		t.Fatalf("%d internal methods vs %d browser methods", inSvc.Methods().Len(), brSvc.Methods().Len())
	}
	for i := range inSvc.Methods().Len() {
		a := inSvc.Methods().Get(i)
		b := brSvc.Methods().ByName(a.Name())
		if b == nil {
			t.Errorf("browser AdminService has no %s", a.Name())
			continue
		}
		if a.Input().Name() != b.Input().Name() || a.Output().Name() != b.Output().Name() {
			t.Errorf("%s: request or response type differs", a.Name())
		}
		// Every method demands the role, on both sides.
		for side, m := range map[string]protoreflect.MethodDescriptor{"metro.v1": a, "bff.metro.v1": b} {
			opts, _ := m.Options().(*descriptorpb.MethodOptions)
			if role, _ := proto.GetExtension(opts, platformv1.E_RequiredRole).(string); role != "metro-admin" {
				t.Errorf("%s %s requires role %q, want metro-admin", side, a.Name(), role)
			}
		}
	}
}
