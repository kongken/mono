package testkit

import (
	"sync"

	platformv1 "github.com/kongken/mono/pkg/proto/platform/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

// The role probe service exists only in tests: it gives the interceptors a method
// that declares required_role without depending on any product's proto.
const (
	// ProbeRole is the role AdminProcedure requires.
	ProbeRole = "probe-admin"
	// AdminProcedure declares required_role = ProbeRole.
	AdminProcedure = "/testkit.v1.RoleProbe/Admin"
	// OpenProcedure declares nothing: signed-in users only.
	OpenProcedure = "/testkit.v1.RoleProbe/Open"
)

var registerProbe sync.Once

// RegisterRoleProbe registers testkit.v1.RoleProbe in the global proto registry.
func RegisterRoleProbe() {
	registerProbe.Do(func() {
		adminOpts := &descriptorpb.MethodOptions{}
		proto.SetExtension(adminOpts, platformv1.E_RequiredRole, ProbeRole)
		empty := "." + string((&emptypb.Empty{}).ProtoReflect().Descriptor().FullName())
		fdp := &descriptorpb.FileDescriptorProto{
			Name:       proto.String("testkit/v1/role_probe.proto"),
			Package:    proto.String("testkit.v1"),
			Syntax:     proto.String("proto3"),
			Dependency: []string{"google/protobuf/empty.proto", "platform/v1/options.proto"},
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name: proto.String("RoleProbe"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{Name: proto.String("Admin"), InputType: proto.String(empty), OutputType: proto.String(empty), Options: adminOpts},
					{Name: proto.String("Open"), InputType: proto.String(empty), OutputType: proto.String(empty)},
				},
			}},
		}
		fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
		if err != nil {
			panic(err)
		}
		if err := protoregistry.GlobalFiles.RegisterFile(fd); err != nil {
			panic(err)
		}
		var _ protoreflect.FileDescriptor = fd
	})
}
