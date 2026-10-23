// Package relay carries a browser-facing call to the internal metro service.
//
// The browser contracts (bff.metro.v1) and the internal ones (metro.v1) are
// separate files and separate Go packages, but for administration and rider data
// they are deliberately identical in shape, so a client sees exactly the error
// details and revisions metro produces. Instead of hundreds of hand-written field
// copies, each call converts through the protobuf wire format. Compatible fails
// the build (via each adapter's contract test) when the two definitions drift
// apart, so a field added to one side cannot be silently lost.
package relay

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/upstream"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Service is the upstream name used in errors and logs.
const Service = "metro"

// Convert copies src into dst through the wire format. The messages are
// structurally identical, so nothing is lost.
func Convert(src, dst proto.Message) error {
	b, err := proto.Marshal(src)
	if err != nil {
		return err
	}
	return proto.Unmarshal(b, dst)
}

// Call forwards one call: browser request -> internal request, the call,
// internal response -> browser response.
func Call(ctx context.Context, browserReq, internalReq proto.Message,
	call func(context.Context) (proto.Message, error), browserRes proto.Message) error {
	if err := Convert(browserReq, internalReq); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("malformed request"))
	}
	res, err := call(ctx)
	if err != nil {
		return MapError(err)
	}
	if err = Convert(res, browserRes); err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return nil
}

// MapError turns a metro status into a Connect error. Domain failures keep their
// message and typed details (field errors, the current revision, what still
// references a record) so a client can act on them; everything else goes through
// the shared mapping that hides internals.
func MapError(err error) error {
	st := status.Convert(err)
	switch st.Code() {
	case codes.InvalidArgument, codes.AlreadyExists, codes.FailedPrecondition, codes.NotFound, codes.PermissionDenied:
	default:
		return upstream.ToConnect(Service, err)
	}
	out := connect.NewError(connect.Code(st.Code()), errors.New(st.Message()))
	for _, d := range st.Details() {
		var src proto.Message
		switch m := d.(type) {
		case protoadapt.MessageV1:
			src = protoadapt.MessageV2Of(m)
		case proto.Message:
			src = m
		default:
			continue
		}
		var dst proto.Message
		switch src.(type) {
		case *metrov1.ValidationDetail:
			dst = &bffmetrov1.ValidationDetail{}
		case *metrov1.RevisionConflictDetail:
			dst = &bffmetrov1.RevisionConflictDetail{}
		case *metrov1.ReferencedDetail:
			dst = &bffmetrov1.ReferencedDetail{}
		default:
			continue
		}
		if Convert(src, dst) != nil {
			continue
		}
		if detail, derr := connect.NewErrorDetail(dst); derr == nil {
			out.AddDetail(detail)
		}
	}
	return out
}

// Compatible lists the ways the internal and browser definitions of one service
// differ: messages, fields (number, name, type, cardinality), enums and methods.
// An empty result means the wire conversion is lossless.
func Compatible(internal, browser protoreflect.FileDescriptor, service string) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	inMsgs, brMsgs := internal.Messages(), browser.Messages()
	if inMsgs.Len() != brMsgs.Len() {
		add("metro.v1 has %d messages, bff.metro.v1 has %d", inMsgs.Len(), brMsgs.Len())
	}
	for i := range inMsgs.Len() {
		in := inMsgs.Get(i)
		br := brMsgs.ByName(in.Name())
		if br == nil {
			add("bff.metro.v1 has no message %s", in.Name())
			continue
		}
		if in.Fields().Len() != br.Fields().Len() {
			add("%s: %d fields vs %d", in.Name(), in.Fields().Len(), br.Fields().Len())
			continue
		}
		for j := range in.Fields().Len() {
			a := in.Fields().Get(j)
			b := br.Fields().ByNumber(a.Number())
			if b == nil {
				add("%s.%s: field number %d missing on the browser side", in.Name(), a.Name(), a.Number())
				continue
			}
			if a.Name() != b.Name() || a.Kind() != b.Kind() || a.Cardinality() != b.Cardinality() ||
				a.HasOptionalKeyword() != b.HasOptionalKeyword() || a.IsMap() != b.IsMap() {
				add("%s.%s differs from the browser definition", in.Name(), a.Name())
			}
			if (a.Message() == nil) != (b.Message() == nil) || (a.Message() != nil && a.Message().Name() != b.Message().Name()) {
				add("%s.%s: message type differs", in.Name(), a.Name())
			}
			if (a.Enum() == nil) != (b.Enum() == nil) {
				add("%s.%s: enum differs", in.Name(), a.Name())
			}
		}
	}
	inSvc, brSvc := internal.Services().ByName(protoreflect.Name(service)), browser.Services().ByName(protoreflect.Name(service))
	if inSvc == nil || brSvc == nil {
		return append(problems, "service "+service+" missing on one side")
	}
	if inSvc.Methods().Len() != brSvc.Methods().Len() {
		add("%d internal methods vs %d browser methods", inSvc.Methods().Len(), brSvc.Methods().Len())
	}
	for i := range inSvc.Methods().Len() {
		a := inSvc.Methods().Get(i)
		b := brSvc.Methods().ByName(a.Name())
		if b == nil {
			add("browser %s has no %s", service, a.Name())
			continue
		}
		if a.Input().Name() != b.Input().Name() || a.Output().Name() != b.Output().Name() {
			add("%s: request or response type differs", a.Name())
		}
	}
	return problems
}
